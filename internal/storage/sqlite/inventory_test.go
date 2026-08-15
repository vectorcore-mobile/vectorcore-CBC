package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/geocode"
	"github.com/vectorcore/cbc/internal/inventory"
)

func testCell(local uint8, tac uint16) inventory.LTECell {
	return inventory.LTECell{
		PLMN:            inventory.PLMN{MCC: "311", MNC: "435", MNCLength: 3},
		ECI:             uint32(4096<<8) | uint32(local),
		ENBID:           4096,
		LocalCellID:     local,
		TAC:             tac,
		GeometryQuality: "unknown",
		Active:          true,
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "i.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func applyImport(t *testing.T, s *Store, mode inventory.ImportMode, cells []inventory.LTECell) inventory.ApplyResult {
	t.Helper()
	ctx := context.Background()
	id := inventory.NewID("imp")
	if err := s.CreateImport(ctx, inventory.InventoryImport{
		ID: id, Mode: mode, Status: inventory.ImportStatusPending,
		RowsReceived: len(cells), RowsValid: len(cells), CreatedAt: time.Now().UTC(),
		SourceFilename: "test.csv", SourceSHA256: "deadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := s.ApplyImport(ctx, inventory.ApplyImportInput{
		ImportID: id, Mode: mode, Cells: cells, VersionName: "v-" + id,
		SourceFilename: "test.csv", SourceSHA256: "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyImportMergeInsertsAndUpdates(t *testing.T) {
	s := openTestStore(t)
	a, b := testCell(1, 1), testCell(2, 1)
	r := applyImport(t, s, inventory.Merge, []inventory.LTECell{a, b})
	if r.Inserted != 2 || r.Updated != 0 {
		t.Fatalf("first merge result=%+v", r)
	}
	a.MMEName = "MME2"
	r = applyImport(t, s, inventory.Merge, []inventory.LTECell{a})
	if r.Inserted != 0 || r.Updated != 1 {
		t.Fatalf("second merge result=%+v", r)
	}
	cells, total, err := s.ListCells(context.Background(), inventory.CellFilter{})
	if err != nil || total != 2 || len(cells) != 2 {
		t.Fatalf("cells=%d total=%d err=%v", len(cells), total, err)
	}
}

func TestApplyImportReplaceDeactivatesAbsentButLeavesMergeAbsentUntouched(t *testing.T) {
	s := openTestStore(t)
	a, b := testCell(1, 1), testCell(2, 1)
	applyImport(t, s, inventory.Merge, []inventory.LTECell{a, b})

	// merge with only `a` present must leave `b` untouched (still active).
	applyImport(t, s, inventory.Merge, []inventory.LTECell{a})
	cell, err := s.GetCell(context.Background(), 2)
	if err != nil || cell == nil || !cell.Active {
		t.Fatalf("merge deactivated an absent cell: cell=%+v err=%v", cell, err)
	}

	// replace with only `a` present must deactivate `b`.
	r := applyImport(t, s, inventory.Replace, []inventory.LTECell{a})
	if r.Deactivated != 1 {
		t.Fatalf("replace result=%+v", r)
	}
	cell, err = s.GetCell(context.Background(), 2)
	if err != nil || cell == nil || cell.Active {
		t.Fatalf("replace did not deactivate absent cell: cell=%+v err=%v", cell, err)
	}
}

func TestApplyImportRejectsValidateOnly(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.ApplyImport(context.Background(), inventory.ApplyImportInput{Mode: inventory.ValidateOnly, Cells: []inventory.LTECell{testCell(1, 1)}}); err == nil {
		t.Fatal("expected ApplyImport to reject validate-only mode")
	}
}

func TestApplyImportRollsBackOnFailure(t *testing.T) {
	s := openTestStore(t)
	applyImport(t, s, inventory.Merge, []inventory.LTECell{testCell(1, 1)})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ApplyImport(ctx, inventory.ApplyImportInput{
		Mode: inventory.Merge, Cells: []inventory.LTECell{testCell(2, 1)},
		ImportID: "imp-does-not-matter", VersionName: "v", SourceFilename: "f", SourceSHA256: "h",
	}); err == nil {
		t.Fatal("expected cancelled context to fail ApplyImport")
	}
	_, total, err := s.ListCells(context.Background(), inventory.CellFilter{})
	if err != nil || total != 1 {
		t.Fatalf("failed apply must not add rows: total=%d err=%v", total, err)
	}
}

func TestImportAuditRecordAndErrorsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := inventory.NewID("imp")
	now := time.Now().UTC()
	if err := s.CreateImport(ctx, inventory.InventoryImport{
		ID: id, Mode: inventory.ValidateOnly, Status: inventory.ImportStatusValidated,
		RowsReceived: 2, RowsValid: 1, RowsRejected: 1, CreatedAt: now, CompletedAt: &now,
		SourceFilename: "bad.csv", SourceSHA256: "abc123",
	}); err != nil {
		t.Fatal(err)
	}
	errs := []inventory.ValidationError{{Row: 2, Column: "eci", Code: "invalid_eci", Message: "out of range"}}
	if err := s.StoreImportErrors(ctx, id, errs); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetImport(ctx, id)
	if err != nil || got == nil || got.Status != inventory.ImportStatusValidated || got.RowsRejected != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	gotErrs, err := s.ListImportErrors(ctx, id)
	if err != nil || len(gotErrs) != 1 || gotErrs[0].Code != "invalid_eci" {
		t.Fatalf("gotErrs=%+v err=%v", gotErrs, err)
	}
	if unknown, err := s.GetImport(ctx, "does-not-exist"); err != nil || unknown != nil {
		t.Fatalf("unknown import should be nil,nil: %+v %v", unknown, err)
	}
}

func TestListCellsFiltersAndPagination(t *testing.T) {
	s := openTestStore(t)
	cells := []inventory.LTECell{testCell(1, 1), testCell(2, 1), testCell(3, 2)}
	cells[2].MMEName = "MME2"
	applyImport(t, s, inventory.Merge, cells)

	tac := uint16(1)
	list, total, err := s.ListCells(context.Background(), inventory.CellFilter{TAC: &tac})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("tac filter: list=%d total=%d err=%v", len(list), total, err)
	}

	list, total, err = s.ListCells(context.Background(), inventory.CellFilter{MMEName: "MME2"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("mme filter: list=%d total=%d err=%v", len(list), total, err)
	}

	list, total, err = s.ListCells(context.Background(), inventory.CellFilter{Limit: 1, Offset: 1})
	if err != nil || total != 3 || len(list) != 1 {
		t.Fatalf("pagination: list=%d total=%d err=%v", len(list), total, err)
	}
}

func TestExportCellsCanonicalFormatAndGeoJSONRoundTrip(t *testing.T) {
	s := openTestStore(t)
	poly := `{"type":"Polygon","coordinates":[[[-86.31,32.37],[-86.285,32.392],[-86.258,32.374],[-86.31,32.37]]]}`
	c := testCell(1, 1)
	lat, lon := 32.3701, -86.3002
	c.Latitude, c.Longitude = &lat, &lon
	c.GeometryQuality = "engineered_polygon"
	c.CoverageGeoJSON = poly
	c.Bounds = &inventory.Bounds{MinLatitude: 32.37, MaxLatitude: 32.392, MinLongitude: -86.31, MaxLongitude: -86.258}
	applyImport(t, s, inventory.Merge, []inventory.LTECell{c})

	var buf strings.Builder
	meta, err := s.ExportCells(context.Background(), inventory.CellFilter{}, &buf)
	if err != nil || meta.RecordCount != 1 {
		t.Fatalf("meta=%+v err=%v", meta, err)
	}
	lines := strings.SplitN(strings.TrimRight(buf.String(), "\n"), "\n", 2)
	if lines[0] != strings.Join(inventory.CSVColumns, ",") {
		t.Fatalf("header mismatch: %q", lines[0])
	}
	got := inventory.ParseCSV(strings.NewReader(buf.String()))
	if len(got.Errors) != 0 || len(got.Cells) != 1 {
		t.Fatalf("re-import failed: cells=%d errors=%v", len(got.Cells), got.Errors)
	}
	if got.Cells[0].CoverageGeoJSON == "" {
		t.Fatal("geojson was not preserved through export/re-import")
	}
}

func TestFindBoundingBoxCandidatesOverlap(t *testing.T) {
	s := openTestStore(t)
	poly := testCell(1, 1)
	poly.CoverageGeoJSON = `{"type":"Polygon","coordinates":[[[-86.31,32.37],[-86.285,32.392],[-86.258,32.374],[-86.31,32.37]]]}`
	poly.Bounds = &inventory.Bounds{MinLatitude: 32.37, MaxLatitude: 32.392, MinLongitude: -86.31, MaxLongitude: -86.258}
	poly.GeometryQuality = "engineered_polygon"

	point := testCell(2, 1)
	lat, lon := 32.34, -86.27
	point.Latitude, point.Longitude = &lat, &lon
	point.GeometryQuality = "point_radius"

	far := testCell(3, 2)
	farLat, farLon := 10.0, 10.0
	far.Latitude, far.Longitude = &farLat, &farLon
	far.GeometryQuality = "point_radius"

	applyImport(t, s, inventory.Merge, []inventory.LTECell{poly, point, far})

	candidates, err := s.FindBoundingBoxCandidates(context.Background(), inventory.Bounds{
		MinLatitude: 32.30, MaxLatitude: 32.40, MinLongitude: -86.35, MaxLongitude: -86.20,
	}, &inventory.PLMN{MCC: "311", MNC: "435"})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates (polygon + point), got %d: %+v", len(candidates), candidates)
	}
}

func TestCurrentInventoryVersionReflectsLatestApply(t *testing.T) {
	s := openTestStore(t)
	if v, err := s.CurrentInventoryVersion(context.Background()); err != nil || v != nil {
		t.Fatalf("expected no version yet: %+v %v", v, err)
	}
	applyImport(t, s, inventory.Merge, []inventory.LTECell{testCell(1, 1)})
	v, err := s.CurrentInventoryVersion(context.Background())
	if err != nil || v == nil || v.RecordCount != 1 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}

func TestMarkImportFailedSetsStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	id := inventory.NewID("imp")
	if err := s.CreateImport(ctx, inventory.InventoryImport{ID: id, Mode: inventory.Merge, Status: inventory.ImportStatusPending, CreatedAt: time.Now().UTC(), SourceSHA256: "h", SourceFilename: "f"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkImportFailed(ctx, id, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetImport(ctx, id)
	if err != nil || got == nil || got.Status != inventory.ImportStatusFailed || got.CompletedAt == nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestCreateCellInsertsAndComputesBounds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	c := testCell(1, 1)
	created, err := s.CreateCell(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.ECI != c.ECI {
		t.Fatalf("unexpected created cell: %+v", created)
	}
	got, err := s.GetCell(ctx, created.ID)
	if err != nil || got == nil || got.ECI != c.ECI {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestCreateCellRejectsDuplicateECGI(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	c := testCell(1, 1)
	if _, err := s.CreateCell(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCell(ctx, c); !errors.Is(err, inventory.ErrCellAlreadyExists) {
		t.Fatalf("got %v, want inventory.ErrCellAlreadyExists", err)
	}
}

func TestDeleteCellSucceeds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created, err := s.CreateCell(ctx, testCell(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCell(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCell(ctx, created.ID)
	if err != nil || got != nil {
		t.Fatalf("expected cell to be gone, got=%+v err=%v", got, err)
	}
}

func TestDeleteCellNotFound(t *testing.T) {
	s := openTestStore(t)
	if err := s.DeleteCell(context.Background(), 12345); !errors.Is(err, inventory.ErrCellNotFound) {
		t.Fatalf("got %v, want inventory.ErrCellNotFound", err)
	}
}

// TestDeleteCellBlockedByGeocodes proves DeleteCell blocks (rather than
// cascades) when cell_geocodes still references the cell - a real
// end-to-end check of the FK-guard query against the actual database, not
// just the fakeRepo simulation in internal/inventory's own tests.
func TestDeleteCellBlockedByGeocodes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created, err := s.CreateCell(ctx, testCell(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.CreateEntry(ctx, created.PLMN.MCC, created.PLMN.MNC, created.PLMN.MNCLength, created.ECI, geocode.SAME, "001101")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCell(ctx, created.ID); !errors.Is(err, inventory.ErrCellHasGeocodes) {
		t.Fatalf("got %v, want inventory.ErrCellHasGeocodes", err)
	}
	// After removing the mapping, delete succeeds.
	if err := s.DeleteEntry(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCell(ctx, created.ID); err != nil {
		t.Fatalf("expected delete to succeed once the geo code mapping is gone: %v", err)
	}
}
