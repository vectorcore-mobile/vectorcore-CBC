// Package integration traces a real CAP alert all the way to an encoded
// SBcAP PDU, using the cell-inventory subsystem's polygon-based selection
// wired into the live cbs.Preparer (internal/cbs.Preparer.SetCellSelector)
// exactly as cmd/cbc/main.go wires it when cell_inventory.enabled is set -
// this is the real CBS/CAP-ingest code path, not a re-derivation of it.
package integration

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/cbs"
	"github.com/vectorcore/cbc/internal/config"
	"github.com/vectorcore/cbc/internal/inventory"
	"github.com/vectorcore/cbc/internal/sbcap"
	"github.com/vectorcore/cbc/internal/storage/sqlite"
)

// seedCell is a temp-radio-database fixture derived from the CAP example's
// polygon geometry. Fields are exported so the optional trace report can
// serialize them directly.
type seedCell struct {
	Name               string  `json:"name"`
	ECI                uint32  `json:"eci"`
	TAC                uint16  `json:"tac"`
	MME                string  `json:"mmeName"`
	Lat                float64 `json:"latitude"`
	Lon                float64 `json:"longitude"`
	GeometryQuality    string  `json:"geometryQuality"`
	CoverageGeoJSON    string  `json:"coverageGeoJSON,omitempty"`
	WantInsideTriangle bool    `json:"insideTriangle"` // sanity precondition, checked before relying on it
}

func macroENB(enbID uint32, localCellID uint8) uint32 { return enbID<<8 | uint32(localCellID) }

// TestCAPPolygonSelectionTracesToSBcAP loads the real example CAP alert
// (polygon only, no geocode), seeds a temp SQLite cell-inventory database
// from its polygon, runs the polygon-based selection preview directly for
// detailed assertions, then prepares a TS 23.041 CBS plan through the real
// cbs.Preparer (with SetCellSelector wired, exactly as cmd/cbc/main.go wires
// it) fed the alert unmodified, encodes a real TS 29.168 SBcAP
// Write-Replace-Warning-Request, and exchanges it with a simulated MME - end
// to end, CAP polygon in, verified SBcAP bytes out, through the live code
// path a real CAP alert would actually take.
//
// "eNB selection" here means what this CBC boundary actually controls: the
// E-UTRAN Cell Global Identifier (ECGI) list in the Warning Area List, which
// is what the MME/eNB use downstream to pick broadcast cells (TS 23.041 /
// TS 29.168). Nothing in this test transmits over a real SCTP association or
// a real MME.
func TestCAPPolygonSelectionTracesToSBcAP(t *testing.T) {
	ctx := context.Background()

	// 1. Parse the real example CAP alert.
	raw, err := os.ReadFile("../../docs/wea_2026-08-02T04-09-05-355Z.xml")
	if err != nil {
		t.Fatal(err)
	}
	alert, err := cap.Parse(raw)
	if err != nil {
		t.Fatalf("CAP parse failed: %v", err)
	}
	if len(alert.Info) == 0 || len(alert.Info[0].Areas) == 0 || len(alert.Info[0].Areas[0].Polygons) == 0 {
		t.Fatal("example CAP alert is missing its polygon")
	}
	polygon := alert.Info[0].Areas[0].Polygons[0]
	areaGeoJSON, err := cap.PolygonToGeoJSON(polygon)
	if err != nil {
		t.Fatalf("CAP polygon conversion failed: %v", err)
	}

	// 2. Build a temp radio database seeded from the polygon's own geometry:
	// its centroid (guaranteed inside any triangle) hosts two in-area cells,
	// one of its bounding-box corners (verified below to fall outside the
	// actual triangle) hosts a candidate that must be excluded, and a cell
	// far away must be pruned before even reaching Go-level geometry
	// comparison.
	insideLat, insideLon := triangleCentroid(t, polygon)
	bboxCornerLat, bboxCornerLon := 32.40, -86.33 // near the polygon's bbox, verified outside the triangle below

	plmn := inventory.PLMN{MCC: "311", MNC: "435", MNCLength: 3}
	const enbID = 5000
	seeds := []seedCell{
		{
			Name: "engineered-polygon-cell", ECI: macroENB(enbID, 1), TAC: 100, MME: "MME-MGM",
			Lat: insideLat, Lon: insideLon, GeometryQuality: "engineered_polygon",
			CoverageGeoJSON:    smallSquareAround(insideLat, insideLon, 0.01),
			WantInsideTriangle: true,
		},
		{
			Name: "point-radius-cell", ECI: macroENB(enbID, 2), TAC: 100, MME: "MME-MGM",
			Lat: insideLat + 0.001, Lon: insideLon + 0.001, GeometryQuality: "point_radius",
			WantInsideTriangle: true,
		},
		{
			Name: "bbox-corner-cell-excluded", ECI: macroENB(enbID, 3), TAC: 100, MME: "MME-MGM",
			Lat: bboxCornerLat, Lon: bboxCornerLon, GeometryQuality: "point_radius",
			WantInsideTriangle: false,
		},
		{
			Name: "far-away-cell-pruned", ECI: macroENB(enbID, 4), TAC: 200, MME: "MME-OTHER",
			Lat: 40.0, Lon: -75.0, GeometryQuality: "point_radius",
			WantInsideTriangle: false,
		},
	}
	verifySeedGeometryAgainstPolygon(t, areaGeoJSON, seeds)

	dbPath := filepath.Join(t.TempDir(), "cell-inventory.db")
	store, err := sqlite.Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	var cells []inventory.LTECell
	for _, s := range seeds {
		lat, lon := s.Lat, s.Lon
		c := inventory.LTECell{
			PLMN: plmn, ECI: s.ECI, ENBID: enbID, LocalCellID: uint8(s.ECI & 0xff), TAC: s.TAC,
			MMEName: s.MME, Latitude: &lat, Longitude: &lon, GeometryQuality: s.GeometryQuality,
			Source: "cap-polygon-trace-test", Active: true,
		}
		if s.CoverageGeoJSON != "" {
			norm, bounds, err := inventory.ValidateCoverageGeoJSON(s.CoverageGeoJSON)
			if err != nil {
				t.Fatal(err)
			}
			c.CoverageGeoJSON = norm
			c.Bounds = &bounds
		}
		cells = append(cells, c)
	}
	if _, err := store.ApplyImport(ctx, inventory.ApplyImportInput{
		ImportID: inventory.NewID("imp"), Mode: inventory.Merge, Cells: cells,
		VersionName: "cap-trace-lab", SourceFilename: "seed", SourceSHA256: "n/a",
	}); err != nil {
		t.Fatal(err)
	}

	// 3. Run the polygon-based selection preview against the CAP polygon.
	invSvc := inventory.NewService(store, inventory.NewGoSpatialMatcher(), 0)
	result, err := invSvc.SelectionPreview(ctx, inventory.SelectionRequest{
		PLMN: plmn, Policy: inventory.PolicyConservativeIntersection, Area: areaGeoJSON,
	})
	if err != nil {
		t.Fatalf("selection preview failed: %v", err)
	}
	if result.CandidateCount != 3 {
		t.Fatalf("expected the far-away cell to be pruned by the SQLite bounding-box query, candidateCount=%d", result.CandidateCount)
	}
	if result.SelectedCount != 2 {
		t.Fatalf("expected exactly the two in-triangle cells to be selected, got %d: %+v", result.SelectedCount, result.Cells)
	}
	gotECIs := map[uint32]string{}
	for _, c := range result.Cells {
		gotECIs[c.ECI] = c.SelectionReason
	}
	if gotECIs[macroENB(enbID, 1)] != inventory.ReasonCoverageIntersection {
		t.Fatalf("expected the engineered-polygon cell to be selected via coverage_intersection, got %+v", gotECIs)
	}
	if gotECIs[macroENB(enbID, 2)] != inventory.ReasonCenterInside {
		t.Fatalf("expected the point-radius cell to be selected via center_inside, got %+v", gotECIs)
	}
	if _, excluded := gotECIs[macroENB(enbID, 3)]; excluded {
		t.Fatal("bbox-corner cell outside the actual triangle must not be selected")
	}

	// 4. Prepare the TS 23.041 CBS plan through the *real* live code path:
	// cbs.Preparer with SetCellSelector wired to invSvc, exactly as
	// cmd/cbc/main.go wires it - fed the original alert unmodified (no
	// geocode injection). This is what proves polygon-only CAP alerts (this
	// example alert has no geocode at all) now resolve to real cells
	// without any test-side bridging.
	const plmnStr = "311-435"
	cbsCfg := config.CBS{DefaultMessageIdentifier: 0x1112}
	preparer := cbs.New(cbsCfg, store)
	preparer.SetCellSelector(plmnStr, invSvc)
	plan, err := preparer.Prepare(ctx, alert)
	if err != nil {
		t.Fatalf("CBS prepare failed: %v", err)
	}
	if len(plan.Messages) != 1 {
		t.Fatalf("expected 1 CBS message, got %d", len(plan.Messages))
	}
	msg := plan.Messages[0]
	if msg.Target.Scope != cbs.CellWide {
		t.Fatalf("expected cell-wide scope, got %d", msg.Target.Scope)
	}
	if len(msg.Target.Cells) != 2 {
		t.Fatalf("expected 2 targeted cells in the CBS plan, got %v", msg.Target.Cells)
	}

	// 5. Encode the real TS 29.168 SBcAP Write-Replace-Warning-Request.
	pdu, err := cbs.SBcAPWriteReplace(msg, 30, 4, plmnStr)
	if err != nil {
		t.Fatalf("SBcAP encode failed: %v", err)
	}
	outcome, procedure, err := sbcap.Header(pdu)
	if err != nil {
		t.Fatalf("SBcAP self-decode failed: %v", err)
	}
	if outcome != sbcap.OutcomeInitiating || procedure != sbcap.ProcedureWriteReplace {
		t.Fatalf("unexpected SBcAP outcome/procedure: %d/%d", outcome, procedure)
	}

	// 7. Exchange it with a simulated MME over an in-memory pipe, proving
	// the encoded PDU round-trips through the real SBcAP wire format.
	cbcSide, mmeSide := net.Pipe()
	defer cbcSide.Close()
	go func() {
		defer mmeSide.Close()
		buf := make([]byte, 4096)
		if _, err := mmeSide.Read(buf); err != nil {
			return
		}
		response, err := sbcap.SuccessResponse(sbcap.ProcedureWriteReplace, msg.MessageIdentifier, msg.SerialNumber, sbcap.CauseMessageAccepted)
		if err == nil {
			_, _ = mmeSide.Write(response)
		}
	}()
	if err := sbcap.ExchangeFor(ctx, cbcSide, pdu, sbcap.ProcedureWriteReplace, msg.MessageIdentifier, msg.SerialNumber); err != nil {
		t.Fatalf("simulated MME exchange failed: %v", err)
	}

	// Optional: write a full trace report for external inspection when
	// asked for one. Off by default so a normal `go test` run has no side
	// effects outside its temp directory.
	if reportPath := os.Getenv("CBC_TRACE_REPORT_PATH"); reportPath != "" {
		writeTraceReport(t, reportPath, alert, polygon, string(areaGeoJSON), dbPath, seeds, result, plan, msg, pdu, plmnStr)
	}
}

// triangleCentroid averages the CAP polygon's three vertices. For a
// (non-degenerate) triangle the centroid is always strictly interior, so
// this needs no runtime containment check to be a safe "inside" fixture.
func triangleCentroid(t *testing.T, polygon string) (lat, lon float64) {
	t.Helper()
	fields := strings.Fields(polygon)
	if len(fields) != 3 {
		t.Fatalf("expected a 3-vertex triangle, got %d vertices", len(fields))
	}
	for _, pair := range fields {
		parts := strings.SplitN(pair, ",", 2)
		la, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lo, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		lat += la
		lon += lo
	}
	return lat / 3, lon / 3
}

func smallSquareAround(lat, lon, delta float64) string {
	b, _ := json.Marshal(struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}{
		Type: "Polygon",
		Coordinates: [][][2]float64{{
			{lon - delta, lat - delta}, {lon - delta, lat + delta},
			{lon + delta, lat + delta}, {lon + delta, lat - delta},
			{lon - delta, lat - delta},
		}},
	})
	return string(b)
}

// verifySeedGeometryAgainstPolygon is a fixture precondition, not part of
// the traced pipeline: it fails loudly if a seed cell's assumed
// inside/outside placement relative to the CAP triangle is wrong, instead
// of silently asserting the wrong thing downstream.
func verifySeedGeometryAgainstPolygon(t *testing.T, areaGeoJSON []byte, seeds []seedCell) {
	t.Helper()
	area, err := inventory.ParseGeometry(areaGeoJSON)
	if err != nil {
		t.Fatalf("CAP polygon did not parse as GeoJSON: %v", err)
	}
	for _, s := range seeds {
		got := area.ContainsPoint(s.Lon, s.Lat)
		if got != s.WantInsideTriangle {
			t.Fatalf("fixture %q: expected inside-triangle=%v, computed=%v (lat=%f lon=%f)", s.Name, s.WantInsideTriangle, got, s.Lat, s.Lon)
		}
	}
}

func writeTraceReport(t *testing.T, path string, alert cap.Alert, polygon, areaGeoJSON, dbPath string, seeds []seedCell, result *inventory.SelectionResult, plan cbs.Plan, msg cbs.Message, pdu []byte, plmnStr string) {
	t.Helper()
	type report struct {
		CAP struct {
			Identifier, Sender, Event, Headline, Severity, Polygon, PolygonGeoJSON string
		}
		Database struct {
			Path  string
			Cells []seedCell
		}
		Selection *inventory.SelectionResult
		CBSPlan   cbs.Plan
		SBcAP     struct {
			PLMN              string
			MessageIdentifier uint16
			SerialNumber      uint16
			PDUHex            string
			PDULengthBytes    int
			DecodedOutcome    int
			DecodedProcedure  int
			MMEExchangeOK     bool
		}
	}
	var r report
	r.CAP.Identifier, r.CAP.Sender, r.CAP.Event = alert.Identifier, alert.Sender, alert.Info[0].Event
	r.CAP.Headline, r.CAP.Severity = alert.Info[0].Headline, alert.Info[0].Severity
	r.CAP.Polygon, r.CAP.PolygonGeoJSON = polygon, areaGeoJSON
	r.Database.Path = dbPath
	r.Database.Cells = seeds
	r.Selection = result
	r.CBSPlan = plan
	outcome, procedure, _ := sbcap.Header(pdu)
	r.SBcAP.PLMN = plmnStr
	r.SBcAP.MessageIdentifier = msg.MessageIdentifier
	r.SBcAP.SerialNumber = msg.SerialNumber
	r.SBcAP.PDUHex = hex.EncodeToString(pdu)
	r.SBcAP.PDULengthBytes = len(pdu)
	r.SBcAP.DecodedOutcome = outcome
	r.SBcAP.DecodedProcedure = procedure
	r.SBcAP.MMEExchangeOK = true
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
}
