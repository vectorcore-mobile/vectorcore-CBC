package geocode_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/geocode"
	"github.com/vectorcore/cbc/internal/inventory"
	"github.com/vectorcore/cbc/internal/storage/sqlite"
)

// newTestStore opens a temp sqlite store and seeds it with the repo's
// canonical example cells (ECIs 1048577, 1048578, 1048833), the same fixture
// internal/httpapi's inventory tests use.
func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "geo.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	csv, err := os.ReadFile("../../docs/example-lte-cell-inventory.csv")
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.NewService(store, inventory.NewGoSpatialMatcher(), 0)
	if _, err := inv.Import(ctx, "seed.csv", bytes.NewReader(csv), inventory.Merge); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCreateListDelete(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))

	entry, err := svc.Create(ctx, "311", "435", 3, 1048577, geocode.SAME, "001101")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ECI != 1048577 || entry.CodeType != geocode.SAME || entry.Code != "001101" {
		t.Fatalf("unexpected entry: %+v", entry)
	}

	entries, total, err := svc.List(ctx, geocode.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("got %d/%d entries, want 1/1", len(entries), total)
	}

	if err := svc.Delete(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	_, total, err = svc.List(ctx, geocode.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("got %d entries after delete, want 0", total)
	}
}

func TestCreateUnknownCellFails(t *testing.T) {
	svc := geocode.NewService(newTestStore(t))
	_, err := svc.Create(context.Background(), "311", "435", 3, 9999999, geocode.SAME, "001101")
	if !errors.Is(err, geocode.ErrCellNotFound) {
		t.Fatalf("got %v, want geocode.ErrCellNotFound", err)
	}
}

func TestDeleteUnknownEntryFails(t *testing.T) {
	svc := geocode.NewService(newTestStore(t))
	err := svc.Delete(context.Background(), 12345)
	if !errors.Is(err, geocode.ErrEntryNotFound) {
		t.Fatalf("got %v, want geocode.ErrEntryNotFound", err)
	}
}

func TestResolveCellsMultipleCellsSameCode(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))
	for _, eci := range []uint32{1048577, 1048578, 1048833} {
		if _, err := svc.Create(ctx, "311", "435", 3, eci, geocode.SAME, "001101"); err != nil {
			t.Fatal(err)
		}
	}
	cells, err := svc.ResolveCells(ctx, "SAME", "001101")
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3: %v", len(cells), cells)
	}
}

func TestResolveCellsNoMatchIsNotAnError(t *testing.T) {
	svc := geocode.NewService(newTestStore(t))
	cells, err := svc.ResolveCells(context.Background(), "SAME", "999999")
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 0 {
		t.Fatalf("got %v, want no matches", cells)
	}
}

func TestImportMergeThenReplace(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))

	mergeCSV := "mcc,mnc,mnc_length,eci,code_type,code\n" +
		"311,435,3,1048577,SAME,001101\n" +
		"311,435,3,1048578,UGC,ALZ057\n"
	result, err := svc.Import(ctx, strings.NewReader(mergeCSV), geocode.Merge)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 2 || len(result.Errors) != 0 {
		t.Fatalf("merge import: %+v", result)
	}
	_, total, err := svc.List(ctx, geocode.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("got %d entries after merge, want 2", total)
	}

	replaceCSV := "mcc,mnc,mnc_length,eci,code_type,code\n" +
		"311,435,3,1048833,SAME,001001\n"
	result, err = svc.Import(ctx, strings.NewReader(replaceCSV), geocode.Replace)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 1 || result.Deleted != 2 {
		t.Fatalf("replace import: %+v", result)
	}
	_, total, err = svc.List(ctx, geocode.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("got %d entries after replace, want 1", total)
	}
}

func TestImportReportsUnknownCellAsRowError(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))
	csv := "mcc,mnc,mnc_length,eci,code_type,code\n" +
		"311,435,3,9999999,SAME,001101\n"
	result, err := svc.Import(ctx, strings.NewReader(csv), geocode.Merge)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 0 || len(result.Errors) != 1 || result.Errors[0].Code != "cell_not_found" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCreateListDeleteCode(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))

	code, err := svc.CreateCode(ctx, "state", "AL01", "Test state code")
	if err != nil {
		t.Fatal(err)
	}
	if code.Type != "STATE" || code.Code != "AL01" || code.Description != "Test state code" {
		t.Fatalf("unexpected code (type should be uppercased): %+v", code)
	}

	codes, err := svc.ListCodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 1 {
		t.Fatalf("got %d codes, want 1", len(codes))
	}

	if err := svc.DeleteCode(ctx, code.ID); err != nil {
		t.Fatal(err)
	}
	codes, err = svc.ListCodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 0 {
		t.Fatalf("got %d codes after delete, want 0", len(codes))
	}
}

func TestCreateCodeRejectsBlankTypeOrCode(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))
	if _, err := svc.CreateCode(ctx, "", "AL01", ""); !errors.Is(err, geocode.ErrCodeRequired) {
		t.Fatalf("got %v, want geocode.ErrCodeRequired", err)
	}
	if _, err := svc.CreateCode(ctx, "STATE", "", ""); !errors.Is(err, geocode.ErrCodeRequired) {
		t.Fatalf("got %v, want geocode.ErrCodeRequired", err)
	}
}

func TestDeleteUnknownCodeFails(t *testing.T) {
	svc := geocode.NewService(newTestStore(t))
	err := svc.DeleteCode(context.Background(), 12345)
	if !errors.Is(err, geocode.ErrCodeNotFound) {
		t.Fatalf("got %v, want geocode.ErrCodeNotFound", err)
	}
}

func TestCreateAndResolveArbitraryCodeType(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))
	if _, err := svc.Create(ctx, "311", "435", 3, 1048577, "STATE", "AL01"); err != nil {
		t.Fatal(err)
	}
	cells, err := svc.ResolveCells(ctx, "STATE", "AL01")
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0] != 1048577 {
		t.Fatalf("got %v, want [1048577]", cells)
	}
}

func TestExportRoundTrips(t *testing.T) {
	ctx := context.Background()
	svc := geocode.NewService(newTestStore(t))
	if _, err := svc.Create(ctx, "311", "435", 3, 1048577, geocode.SAME, "001101"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := svc.Export(ctx, geocode.Filter{}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "mcc,mnc,mnc_length,eci,code_type,code") || !strings.Contains(out, "311,435,3,1048577,SAME,001101") {
		t.Fatalf("unexpected export:\n%s", out)
	}
}
