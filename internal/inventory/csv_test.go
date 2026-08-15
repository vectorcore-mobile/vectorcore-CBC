package inventory

import (
	"os"
	"strings"
	"testing"
)

func TestExampleCSVValidates(t *testing.T) {
	r, err := os.Open("../../docs/example-lte-cell-inventory.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := ParseCSV(r)
	if len(got.Errors) != 0 || len(got.Cells) != 3 {
		t.Fatalf("cells=%d errors=%v", len(got.Cells), got.Errors)
	}
	if got.Cells[0].PLMN.MCC != "311" || got.Cells[0].ECI != 1048577 {
		t.Fatal("identity was not preserved")
	}
}
func TestCSVRejectsUnknownHeader(t *testing.T) {
	got := ParseCSV(strings.NewReader("mcc,bogus\n311,x\n"))
	if len(got.Errors) == 0 {
		t.Fatal("accepted unknown header")
	}
}

const header = "mcc,mnc,mnc_length,eci,enb_id,local_cell_id,tac,cell_name,mme_name,latitude,longitude,nominal_radius_m,azimuth_deg,beamwidth_deg,geometry_quality,source,source_record_id,source_version,active,coverage_geojson"

func validRow() []string {
	return []string{"311", "435", "3", "1048577", "4096", "1", "1", "", "", "", "", "", "", "", "unknown", "", "", "", "true", ""}
}

func csvFromRows(rows ...[]string) string {
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	for _, r := range rows {
		sb.WriteString(strings.Join(r, ","))
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestCSVPreservesLeadingZerosInMCCAndMNC(t *testing.T) {
	row := validRow()
	row[0], row[1] = "001", "01"
	row[2] = "2"
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if len(got.Errors) != 0 {
		t.Fatalf("errors=%v", got.Errors)
	}
	if got.Cells[0].PLMN.MCC != "001" || got.Cells[0].PLMN.MNC != "01" {
		t.Fatalf("leading zeros not preserved: %+v", got.Cells[0].PLMN)
	}
}

func TestCSVMissingRequiredHeader(t *testing.T) {
	got := ParseCSV(strings.NewReader("mnc,mnc_length,eci,enb_id,local_cell_id,tac,cell_name,mme_name,latitude,longitude,nominal_radius_m,azimuth_deg,beamwidth_deg,geometry_quality,source,source_record_id,source_version,active,coverage_geojson\n"))
	found := false
	for _, e := range got.Errors {
		if e.Code == "missing_header" && e.Column == "mcc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_header for mcc, got %v", got.Errors)
	}
}

func TestCSVDuplicateHeader(t *testing.T) {
	got := ParseCSV(strings.NewReader(header + ",mcc\n"))
	found := false
	for _, e := range got.Errors {
		if e.Code == "duplicate_header" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate_header, got %v", got.Errors)
	}
}

func TestCSVMalformedQuoting(t *testing.T) {
	got := ParseCSV(strings.NewReader(header + "\n\"unterminated,311,435\n"))
	if len(got.Errors) == 0 || got.Errors[0].Code != "malformed_csv" {
		t.Fatalf("expected malformed_csv, got %v", got.Errors)
	}
}

func TestCSVOptionalFieldsAcceptedEmpty(t *testing.T) {
	got := ParseCSV(strings.NewReader(csvFromRows(validRow())))
	if len(got.Errors) != 0 || len(got.Cells) != 1 {
		t.Fatalf("errors=%v cells=%d", got.Errors, len(got.Cells))
	}
	c := got.Cells[0]
	if c.Latitude != nil || c.Longitude != nil || c.NominalRadiusM != nil || c.AzimuthDeg != nil || c.BeamwidthDeg != nil {
		t.Fatalf("expected nil optional fields, got %+v", c)
	}
}

func TestCSVInvalidLatitudeLongitude(t *testing.T) {
	row := validRow()
	row[9] = "95" // latitude out of range
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if !hasErrorCode(got.Errors, "invalid_latitude") {
		t.Fatalf("expected invalid_latitude, got %v", got.Errors)
	}

	row = validRow()
	row[10] = "-185" // longitude out of range
	got = ParseCSV(strings.NewReader(csvFromRows(row)))
	if !hasErrorCode(got.Errors, "invalid_longitude") {
		t.Fatalf("expected invalid_longitude, got %v", got.Errors)
	}
}

func TestCSVInvalidECI(t *testing.T) {
	row := validRow()
	row[3] = "999999999"
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if !hasErrorCode(got.Errors, "invalid_eci") {
		t.Fatalf("expected invalid_eci, got %v", got.Errors)
	}
}

func TestCSVInvalidTAC(t *testing.T) {
	row := validRow()
	row[6] = "99999999"
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if !hasErrorCode(got.Errors, "invalid_tac") {
		t.Fatalf("expected invalid_tac, got %v", got.Errors)
	}
}

func TestCSVInconsistentIdentity(t *testing.T) {
	row := validRow()
	row[3] = "1048578" // does not equal (enb_id<<8)|local_cell_id = 1048577
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if !hasErrorCode(got.Errors, "eci_identity_mismatch") {
		t.Fatalf("expected eci_identity_mismatch, got %v", got.Errors)
	}
}

func TestCSVDuplicateECGIInOneFile(t *testing.T) {
	row := validRow()
	got := ParseCSV(strings.NewReader(csvFromRows(row, row)))
	if !hasErrorCode(got.Errors, "duplicate_ecgi") {
		t.Fatalf("expected duplicate_ecgi, got %v", got.Errors)
	}
}

func TestCSVMalformedEmbeddedGeoJSON(t *testing.T) {
	row := validRow()
	row[19] = `"{""type"":""Polygon"",""coordinates"":[[[0,0]]]}"`
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if !hasErrorCode(got.Errors, "invalid_geometry") {
		t.Fatalf("expected invalid_geometry, got %v", got.Errors)
	}
}

func TestCSVValidPolygonAndMultiPolygon(t *testing.T) {
	poly := validRow()
	poly[14] = "engineered_polygon"
	poly[19] = `"{""type"":""Polygon"",""coordinates"":[[[-86.31,32.37],[-86.285,32.392],[-86.258,32.374],[-86.31,32.37]]]}"`

	multi := validRow()
	multi[3] = "1048578"
	multi[5] = "2"
	multi[14] = "engineered_polygon"
	multi[19] = `"{""type"":""MultiPolygon"",""coordinates"":[[[[-86.31,32.37],[-86.285,32.392],[-86.258,32.374],[-86.31,32.37]]]]}"`

	got := ParseCSV(strings.NewReader(csvFromRows(poly, multi)))
	if len(got.Errors) != 0 || len(got.Cells) != 2 {
		t.Fatalf("errors=%v cells=%d", got.Errors, len(got.Cells))
	}
}

func TestCSVGeometryQualityRequiresPolygon(t *testing.T) {
	row := validRow()
	row[14] = "engineered_polygon" // no coverage_geojson supplied
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if !hasErrorCode(got.Errors, "geometry_quality_requires_polygon") {
		t.Fatalf("expected geometry_quality_requires_polygon, got %v", got.Errors)
	}
}

func TestCSVInactiveCell(t *testing.T) {
	row := validRow()
	row[18] = "false"
	got := ParseCSV(strings.NewReader(csvFromRows(row)))
	if len(got.Errors) != 0 || len(got.Cells) != 1 || got.Cells[0].Active {
		t.Fatalf("errors=%v cells=%+v", got.Errors, got.Cells)
	}
}

func hasErrorCode(errs []ValidationError, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}
