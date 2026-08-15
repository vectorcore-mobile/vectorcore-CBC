package inventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRepo is a minimal in-memory InventoryRepository for exercising Service
// orchestration without a database. Bounding-box filtering and CSV/export
// correctness are covered at the SQLite layer instead; here
// FindBoundingBoxCandidates simply returns every active (PLMN-matching)
// cell.
type fakeRepo struct {
	mu              sync.Mutex
	imports         map[string]InventoryImport
	errors          map[string][]ValidationError
	cells           []LTECell
	nextID          int64
	versions        []InventoryVersion
	applyErr        error
	geocodedCellIDs map[int64]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{imports: map[string]InventoryImport{}, errors: map[string][]ValidationError{}}
}

func fakeCellKey(c LTECell) string {
	return fmt.Sprintf("%s/%s/%d/%d", c.PLMN.MCC, c.PLMN.MNC, c.PLMN.MNCLength, c.ECI)
}

func (f *fakeRepo) CreateImport(ctx context.Context, imp InventoryImport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imports[imp.ID] = imp
	return nil
}

func (f *fakeRepo) MarkImportFailed(ctx context.Context, importID string, completedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	imp := f.imports[importID]
	imp.Status = ImportStatusFailed
	imp.CompletedAt = &completedAt
	f.imports[importID] = imp
	return nil
}

func (f *fakeRepo) StoreImportErrors(ctx context.Context, importID string, errs []ValidationError) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors[importID] = append(f.errors[importID], errs...)
	return nil
}

func (f *fakeRepo) ApplyImport(ctx context.Context, input ApplyImportInput) (ApplyResult, error) {
	if f.applyErr != nil {
		return ApplyResult{}, f.applyErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	existing := map[string]int{}
	for i, c := range f.cells {
		existing[fakeCellKey(c)] = i
	}
	var result ApplyResult
	seen := map[string]bool{}
	for _, c := range input.Cells {
		key := fakeCellKey(c)
		seen[key] = true
		if idx, ok := existing[key]; ok {
			c.ID = f.cells[idx].ID
			f.cells[idx] = c
			result.Updated++
		} else {
			f.nextID++
			c.ID = f.nextID
			f.cells = append(f.cells, c)
			existing[key] = len(f.cells) - 1
			result.Inserted++
		}
	}
	if input.Mode == Replace {
		for i, c := range f.cells {
			if c.Active && !seen[fakeCellKey(c)] {
				f.cells[i].Active = false
				result.Deactivated++
			}
		}
	}
	f.versions = append(f.versions, InventoryVersion{ID: NewID("ver"), VersionName: input.VersionName, RecordCount: len(input.Cells), Status: "active", CreatedAt: time.Now().UTC()})
	imp := f.imports[input.ImportID]
	imp.Status = ImportStatusCompleted
	imp.InsertedCount, imp.UpdatedCount, imp.DeactivatedCount = result.Inserted, result.Updated, result.Deactivated
	f.imports[input.ImportID] = imp
	return result, nil
}

func (f *fakeRepo) GetImport(ctx context.Context, importID string) (*InventoryImport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	imp, ok := f.imports[importID]
	if !ok {
		return nil, nil
	}
	return &imp, nil
}

func (f *fakeRepo) ListImportErrors(ctx context.Context, importID string) ([]ValidationError, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.errors[importID], nil
}

func (f *fakeRepo) ListCells(ctx context.Context, filter CellFilter) ([]LTECell, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]LTECell{}, f.cells...)
	return out, len(out), nil
}

func (f *fakeRepo) GetCell(ctx context.Context, id int64) (*LTECell, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cells {
		if c.ID == id {
			cc := c
			return &cc, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ExportCells(ctx context.Context, filter CellFilter, w io.Writer) (ExportMeta, error) {
	return ExportMeta{}, nil
}

func (f *fakeRepo) FindBoundingBoxCandidates(ctx context.Context, bounds Bounds, plmn *PLMN) ([]LTECell, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []LTECell
	for _, c := range f.cells {
		if !c.Active {
			continue
		}
		if plmn != nil && (c.PLMN.MCC != plmn.MCC || c.PLMN.MNC != plmn.MNC) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// geocodedCellIDs lets tests simulate a cell already referenced by
// cell_geocodes, so DeleteCell's block-not-cascade behavior can be
// exercised without a real geocode repository.
func (f *fakeRepo) CreateCell(ctx context.Context, c LTECell) (*LTECell, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.cells {
		if existing.PLMN == c.PLMN && existing.ECI == c.ECI {
			return nil, ErrCellAlreadyExists
		}
	}
	f.nextID++
	c.ID = f.nextID
	f.cells = append(f.cells, c)
	cc := c
	return &cc, nil
}

func (f *fakeRepo) DeleteCell(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.geocodedCellIDs[id] {
		return ErrCellHasGeocodes
	}
	for i, c := range f.cells {
		if c.ID == id {
			f.cells = append(f.cells[:i], f.cells[i+1:]...)
			return nil
		}
	}
	return ErrCellNotFound
}

func (f *fakeRepo) CurrentInventoryVersion(ctx context.Context) (*InventoryVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.versions) == 0 {
		return nil, nil
	}
	v := f.versions[len(f.versions)-1]
	return &v, nil
}

func TestServiceImportValidateOnlyDoesNotModifyInventory(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	imp, err := svc.Import(context.Background(), "cells.csv", strings.NewReader(csvFromRows(validRow())), ValidateOnly)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Status != ImportStatusValidated {
		t.Fatalf("status=%q", imp.Status)
	}
	if len(repo.cells) != 0 {
		t.Fatalf("validate-only must not modify cells, got %d", len(repo.cells))
	}
}

func TestServiceImportMergeInsertsCells(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	imp, err := svc.Import(context.Background(), "cells.csv", strings.NewReader(csvFromRows(validRow())), Merge)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Status != ImportStatusCompleted || imp.InsertedCount != 1 {
		t.Fatalf("imp=%+v", imp)
	}
	if len(repo.cells) != 1 {
		t.Fatalf("expected 1 cell, got %d", len(repo.cells))
	}
}

func TestServiceImportRejectsInvalidMode(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	_, err := svc.Import(context.Background(), "cells.csv", strings.NewReader(csvFromRows(validRow())), ImportMode("bogus"))
	if !errors.Is(err, ErrInvalidImportMode) {
		t.Fatalf("err=%v", err)
	}
}

func TestServiceImportEnforcesUploadSizeBound(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NewGoSpatialMatcher(), 5) // 5 bytes, far smaller than any real CSV
	_, err := svc.Import(context.Background(), "cells.csv", strings.NewReader(csvFromRows(validRow())), ValidateOnly)
	if !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("err=%v", err)
	}
}

func TestServiceImportFatalRowErrorsPreventApply(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	bad := validRow()
	bad[3] = "not-a-number"
	imp, err := svc.Import(context.Background(), "cells.csv", strings.NewReader(csvFromRows(bad)), Merge)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Status != ImportStatusFailed {
		t.Fatalf("status=%q", imp.Status)
	}
	if len(repo.cells) != 0 {
		t.Fatalf("fatal row errors must prevent any apply, got %d cells", len(repo.cells))
	}
}

func TestServiceImportApplyFailureMarksImportFailed(t *testing.T) {
	repo := newFakeRepo()
	repo.applyErr = fmt.Errorf("boom")
	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	_, err := svc.Import(context.Background(), "cells.csv", strings.NewReader(csvFromRows(validRow())), Merge)
	if err == nil {
		t.Fatal("expected apply failure to surface as an error")
	}
	if len(repo.imports) != 1 {
		t.Fatalf("expected an import record")
	}
	for _, imp := range repo.imports {
		if imp.Status != ImportStatusFailed {
			t.Fatalf("status=%q", imp.Status)
		}
	}
}

func TestServiceSelectionPreviewSelectsAndGroups(t *testing.T) {
	repo := newFakeRepo()
	plmn := PLMN{MCC: "311", MNC: "435", MNCLength: 3}
	inside := LTECell{ID: 1, PLMN: plmn, ECI: 1, TAC: 1, MMEName: "MME1", Active: true,
		CoverageGeoJSON: `{"type":"Polygon","coordinates":[[[2,2],[2,8],[8,8],[8,2],[2,2]]]}`}
	outside := LTECell{ID: 2, PLMN: plmn, ECI: 2, TAC: 2, MMEName: "MME1", Active: true,
		CoverageGeoJSON: `{"type":"Polygon","coordinates":[[[50,50],[50,60],[60,60],[60,50],[50,50]]]}`}
	repo.cells = []LTECell{inside, outside}

	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	result, err := svc.SelectionPreview(context.Background(), SelectionRequest{
		PLMN:   plmn,
		Policy: PolicyConservativeIntersection,
		Area:   []byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidateCount != 2 || result.SelectedCount != 1 {
		t.Fatalf("result=%+v", result)
	}
	if result.Cells[0].SelectionReason != ReasonCoverageIntersection {
		t.Fatalf("reason=%q", result.Cells[0].SelectionReason)
	}
	if len(result.MMEPlans) != 1 || result.MMEPlans[0].MMEName != "MME1" {
		t.Fatalf("mmePlans=%+v", result.MMEPlans)
	}
}

func TestServiceSelectionPreviewRejectsMalformedGeometry(t *testing.T) {
	svc := NewService(newFakeRepo(), NewGoSpatialMatcher(), 0)
	_, err := svc.SelectionPreview(context.Background(), SelectionRequest{
		Policy: PolicyConservativeIntersection,
		Area:   []byte(`{"type":"Polygon","coordinates":[]}`),
	})
	if err == nil {
		t.Fatal("expected malformed request geometry to be rejected")
	}
}

func TestServiceSelectionPreviewRejectsUnsupportedPolicy(t *testing.T) {
	svc := NewService(newFakeRepo(), NewGoSpatialMatcher(), 0)
	_, err := svc.SelectionPreview(context.Background(), SelectionRequest{
		Policy: "unsupported-policy",
		Area:   []byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`),
	})
	if err == nil {
		t.Fatal("expected unsupported policy to be rejected")
	}
}

func validCreateCellInput() CreateCellInput {
	return CreateCellInput{
		PLMN: PLMN{MCC: "311", MNC: "435", MNCLength: 3},
		// ENBID=4096, LocalCellID=1 -> ECI = 4096<<8|1 = 1048577.
		ENBID: 4096, LocalCellID: 1, TAC: 1,
		GeometryQuality: "unknown", Active: true,
	}
}

func TestServiceCreateCellComputesECI(t *testing.T) {
	svc := NewService(newFakeRepo(), NewGoSpatialMatcher(), 0)
	c, err := svc.CreateCell(context.Background(), validCreateCellInput())
	if err != nil {
		t.Fatal(err)
	}
	if c.ECI != 1048577 {
		t.Fatalf("got ECI %d, want 1048577 (4096<<8|1)", c.ECI)
	}
	if c.PLMN.MCC != "311" || c.PLMN.MNC != "435" {
		t.Fatalf("unexpected PLMN: %+v", c.PLMN)
	}
}

func TestServiceCreateCellRejectsInvalidInput(t *testing.T) {
	svc := NewService(newFakeRepo(), NewGoSpatialMatcher(), 0)
	in := validCreateCellInput()
	in.GeometryQuality = "" // required
	_, err := svc.CreateCell(context.Background(), in)
	if !errors.Is(err, ErrInvalidCell) {
		t.Fatalf("got %v, want ErrInvalidCell", err)
	}
}

func TestServiceCreateCellRejectsDuplicateECI(t *testing.T) {
	svc := NewService(newFakeRepo(), NewGoSpatialMatcher(), 0)
	ctx := context.Background()
	if _, err := svc.CreateCell(ctx, validCreateCellInput()); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateCell(ctx, validCreateCellInput())
	if !errors.Is(err, ErrCellAlreadyExists) {
		t.Fatalf("got %v, want ErrCellAlreadyExists", err)
	}
}

func TestServiceDeleteCellNotFound(t *testing.T) {
	svc := NewService(newFakeRepo(), NewGoSpatialMatcher(), 0)
	err := svc.DeleteCell(context.Background(), 12345)
	if !errors.Is(err, ErrCellNotFound) {
		t.Fatalf("got %v, want ErrCellNotFound", err)
	}
}

func TestServiceDeleteCellBlockedByGeocodes(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	ctx := context.Background()
	c, err := svc.CreateCell(ctx, validCreateCellInput())
	if err != nil {
		t.Fatal(err)
	}
	repo.geocodedCellIDs = map[int64]bool{c.ID: true}
	if err := svc.DeleteCell(ctx, c.ID); !errors.Is(err, ErrCellHasGeocodes) {
		t.Fatalf("got %v, want ErrCellHasGeocodes", err)
	}
}

func TestServiceDeleteCellSucceeds(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, NewGoSpatialMatcher(), 0)
	ctx := context.Background()
	c, err := svc.CreateCell(ctx, validCreateCellInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteCell(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	cells, total, err := svc.ListCells(ctx, CellFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(cells) != 0 {
		t.Fatalf("got %d cells after delete, want 0", total)
	}
}
