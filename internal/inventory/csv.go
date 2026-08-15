package inventory

import (
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CSVColumns is the canonical, stable interchange column order. Importers
// accept these headers case-insensitively with surrounding whitespace
// trimmed; exports always emit exactly these names in this order. All
// columns must be present in an imported file (this is what keeps the
// format round-trip-stable); only a subset of their values are required to
// be non-empty - see requiredValueColumns.
var CSVColumns = []string{"mcc", "mnc", "mnc_length", "eci", "enb_id", "local_cell_id", "tac", "cell_name", "mme_name", "latitude", "longitude", "nominal_radius_m", "azimuth_deg", "beamwidth_deg", "geometry_quality", "source", "source_record_id", "source_version", "active", "coverage_geojson"}

// geometryQualities are the supported values for the geometry_quality
// column. engineered_polygon and propagation_model additionally require a
// coverage_geojson value - see cellFrom.
var geometryQualities = map[string]bool{
	"engineered_polygon": true,
	"propagation_model":  true,
	"sector_estimate":    true,
	"point_radius":       true,
	"site_point":         true,
	"unknown":            true,
}

// maxCSVErrors bounds the validation error list for extremely malformed
// files: past this point we stop parsing and return a single terminal error
// instead of an unbounded response payload.
const maxCSVErrors = 1000

type ValidationError struct {
	Row     int    `json:"row,omitempty"`
	Column  string `json:"column,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ParseResult struct {
	Cells  []LTECell
	Rows   int
	SHA256 string
	Errors []ValidationError
}

// fieldError is cellFrom's row-scoped error before the caller attaches a row
// number.
type fieldError struct {
	Column, Code, Message string
}

// ParseCSV validates the complete canonical inventory interchange file before
// any repository mutation. Unknown, missing, or duplicate headers are fatal
// for the whole file; row-level problems are collected per row so operators
// see every issue in one pass, up to maxCSVErrors.
func ParseCSV(r io.Reader) ParseResult {
	h := sha256.New()
	rr := csv.NewReader(io.TeeReader(r, h))
	rr.FieldsPerRecord = -1
	rows, err := rr.ReadAll()
	out := ParseResult{SHA256: fmt.Sprintf("%x", h.Sum(nil))}
	if err != nil {
		out.Errors = []ValidationError{{Row: 1, Code: "malformed_csv", Message: err.Error()}}
		return out
	}
	if len(rows) == 0 {
		out.Errors = []ValidationError{{Row: 1, Code: "empty_file", Message: "CSV is empty"}}
		return out
	}
	idx := map[string]int{}
	for i, v := range rows[0] {
		k := strings.ToLower(strings.TrimSpace(v))
		if _, ok := idx[k]; ok {
			out.Errors = append(out.Errors, ValidationError{Row: 1, Column: k, Code: "duplicate_header", Message: "duplicate header"})
		}
		idx[k] = i
	}
	for _, k := range CSVColumns {
		if _, ok := idx[k]; !ok {
			out.Errors = append(out.Errors, ValidationError{Row: 1, Column: k, Code: "missing_header", Message: "required canonical header missing"})
		}
	}
	for k := range idx {
		found := false
		for _, want := range CSVColumns {
			if k == want {
				found = true
			}
		}
		if !found {
			out.Errors = append(out.Errors, ValidationError{Row: 1, Column: k, Code: "unknown_header", Message: "unknown header"})
		}
	}
	if len(out.Errors) > 0 {
		return out
	}
	seen := map[string]int{}
	for n, row := range rows[1:] {
		if len(out.Errors) >= maxCSVErrors {
			out.Errors = append(out.Errors, ValidationError{Code: "too_many_errors", Message: "validation stopped after 1000 errors; fix the reported issues and re-submit"})
			break
		}
		out.Rows++
		if len(row) != len(rows[0]) {
			out.Errors = append(out.Errors, ValidationError{Row: n + 2, Code: "field_count", Message: "unexpected field count"})
			continue
		}
		get := func(k string) string { return strings.TrimSpace(row[idx[k]]) }
		c, fieldErrs := cellFrom(get)
		if len(fieldErrs) > 0 {
			for _, fe := range fieldErrs {
				out.Errors = append(out.Errors, ValidationError{Row: n + 2, Column: fe.Column, Code: fe.Code, Message: fe.Message})
			}
			continue
		}
		key := c.PLMN.MCC + "-" + c.PLMN.MNC + "-" + strconv.Itoa(c.PLMN.MNCLength) + "-" + strconv.FormatUint(uint64(c.ECI), 10)
		if prev, ok := seen[key]; ok {
			out.Errors = append(out.Errors, ValidationError{Row: n + 2, Column: "eci", Code: "duplicate_ecgi", Message: fmt.Sprintf("ECGI %s-%s-%d also appears on row %d", c.PLMN.MCC, c.PLMN.MNC, c.ECI, prev)})
			continue
		}
		seen[key] = n + 2
		out.Cells = append(out.Cells, c)
	}
	return out
}

// cellFrom validates one CSV row against every rule independently so a
// caller gets the complete set of problems for that row, not just the
// first. The supported eNB identity model is macro eNB:
// ECI == (eNBID << 8) | localCellID; other identity encodings are rejected.
func cellFrom(get func(string) string) (LTECell, []fieldError) {
	var errs []fieldError
	add := func(column, code, message string) { errs = append(errs, fieldError{column, code, message}) }

	c := LTECell{
		CellName:        get("cell_name"),
		MMEName:         get("mme_name"),
		GeometryQuality: get("geometry_quality"),
		Source:          get("source"),
		SourceRecordID:  get("source_record_id"),
		SourceVersion:   get("source_version"),
	}

	mcc, mnc := get("mcc"), get("mnc")
	if len(mcc) != 3 || !digits(mcc) {
		add("mcc", "invalid_mcc", "MCC must be exactly three decimal digits")
	} else {
		c.PLMN.MCC = mcc
	}
	if (len(mnc) != 2 && len(mnc) != 3) || !digits(mnc) {
		add("mnc", "invalid_mnc", "MNC must be two or three decimal digits")
	} else {
		c.PLMN.MNC = mnc
	}
	if mncLen, err := atoi(get("mnc_length"), 2, 3); err != nil {
		add("mnc_length", "invalid_mnc_length", "mnc_length must be 2 or 3")
	} else {
		c.PLMN.MNCLength = mncLen
		if len(mnc) > 0 && mncLen != len(mnc) {
			add("mnc_length", "mnc_length_mismatch", "mnc_length must match the number of digits in mnc")
		}
	}

	eciVal, eciErr := atoi(get("eci"), 0, 268435455)
	if eciErr != nil {
		add("eci", "invalid_eci", "ECI must be in the 28-bit range 0-268435455")
	} else {
		c.ECI = uint32(eciVal)
	}
	enbVal, enbErr := atoi(get("enb_id"), 0, 1048575)
	if enbErr != nil {
		add("enb_id", "invalid_enb_id", "eNB ID must be in range 0-1048575 for the supported macro eNB identity model")
	} else {
		c.ENBID = uint32(enbVal)
	}
	localVal, localErr := atoi(get("local_cell_id"), 0, 255)
	if localErr != nil {
		add("local_cell_id", "invalid_local_cell_id", "local cell ID must be in range 0-255")
	} else {
		c.LocalCellID = uint8(localVal)
	}
	if eciErr == nil && enbErr == nil && localErr == nil && c.ECI != c.ENBID<<8|uint32(c.LocalCellID) {
		add("eci", "eci_identity_mismatch", "ECI must equal (eNB ID << 8) | local cell ID for the supported macro eNB identity model")
	}

	if tacVal, err := atoi(get("tac"), 0, 65535); err != nil {
		add("tac", "invalid_tac", "TAC must be in range 0-65535")
	} else {
		c.TAC = uint16(tacVal)
	}

	if b, err := strconv.ParseBool(get("active")); err != nil {
		add("active", "invalid_active", "active must be a boolean value")
	} else {
		c.Active = b
	}

	if c.GeometryQuality == "" {
		add("geometry_quality", "missing_geometry_quality", "geometry_quality is required")
	} else if !geometryQualities[c.GeometryQuality] {
		add("geometry_quality", "invalid_geometry_quality", "geometry_quality must be one of engineered_polygon, propagation_model, sector_estimate, point_radius, site_point, unknown")
	}

	if v := get("latitude"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err != nil || f < -90 || f > 90 {
			add("latitude", "invalid_latitude", "latitude must be between -90 and 90")
		} else {
			c.Latitude = &f
		}
	}
	if v := get("longitude"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err != nil || f < -180 || f > 180 {
			add("longitude", "invalid_longitude", "longitude must be between -180 and 180")
		} else {
			c.Longitude = &f
		}
	}
	if v := get("nominal_radius_m"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err != nil || f <= 0 {
			add("nominal_radius_m", "invalid_radius", "nominal_radius_m must be a positive number")
		} else {
			c.NominalRadiusM = &f
		}
	}
	if v := get("azimuth_deg"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || f >= 360 {
			add("azimuth_deg", "invalid_azimuth", "azimuth_deg must satisfy 0 <= value < 360")
		} else {
			c.AzimuthDeg = &f
		}
	}
	if v := get("beamwidth_deg"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err != nil || f <= 0 || f > 360 {
			add("beamwidth_deg", "invalid_beamwidth", "beamwidth_deg must satisfy 0 < value <= 360")
		} else {
			c.BeamwidthDeg = &f
		}
	}

	if v := get("coverage_geojson"); v != "" {
		norm, bounds, err := ValidateCoverageGeoJSON(v)
		if err != nil {
			add("coverage_geojson", "invalid_geometry", err.Error())
		} else {
			c.CoverageGeoJSON = norm
			b := bounds
			c.Bounds = &b
		}
	} else if c.GeometryQuality == "engineered_polygon" || c.GeometryQuality == "propagation_model" {
		add("coverage_geojson", "geometry_quality_requires_polygon", "geometry_quality engineered_polygon or propagation_model requires a coverage_geojson value")
	}

	return c, errs
}

func atoi(s string, min, max int) (int, error) {
	v, e := strconv.Atoi(s)
	if e != nil || v < min || v > max {
		return 0, fmt.Errorf("out of range")
	}
	return v, nil
}
func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
