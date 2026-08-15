package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/geocode"
	"github.com/vectorcore/cbc/internal/inventory"
	"github.com/vectorcore/cbc/internal/service"
	"github.com/vectorcore/cbc/internal/storage/sqlite"
)

func newInventoryTestHandler(t *testing.T, maxImportBytes int64) http.Handler {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "inv.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	inv := inventory.NewService(store, inventory.NewGoSpatialMatcher(), maxImportBytes)
	geo := geocode.NewService(store)
	return New(service.New(nil, nil), inv, geo, "validate-only", "0.1.0-test")
}

func exampleCSV(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../docs/example-lte-cell-inventory.csv")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// multipartCSVRequest builds a multipart upload with an explicit
// Content-Type on the file part, since Go's own multipart.Writer would
// otherwise default to application/octet-stream via CreateFormFile - which
// the server accepts too, but this exercises the text/csv path.
func multipartCSVRequest(t *testing.T, target, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, target, &buf)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

func TestImportMultipartValidateOnlyAcceptsExampleCSV(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	r := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=validate-only", "lte-cell-inventory.csv", exampleCSV(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got inventoryImportView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "validated" || got.RowsRejected != 0 || got.RowsValid != 3 {
		t.Fatalf("got=%+v", got)
	}
}

func TestImportMultipartMergeThenGetImportAndCells(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	r := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=merge", "lte-cell-inventory.csv", exampleCSV(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var imp inventoryImportView
	if err := json.Unmarshal(w.Body.Bytes(), &imp); err != nil {
		t.Fatal(err)
	}
	if imp.Status != "completed" || imp.Inserted != 3 {
		t.Fatalf("imp=%+v", imp)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/cell-inventory/imports/"+imp.ImportID, nil)
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get import status=%d body=%s", getW.Code, getW.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/cell-inventory/cells", nil)
	listW := httptest.NewRecorder()
	h.ServeHTTP(listW, listReq)
	var list cellListBody
	if err := json.Unmarshal(listW.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 3 {
		t.Fatalf("list=%+v", list)
	}
}

func TestImportUploadSizeLimit(t *testing.T) {
	h := newInventoryTestHandler(t, 32) // far smaller than the example CSV
	r := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=validate-only", "lte-cell-inventory.csv", exampleCSV(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code < 400 || w.Code >= 500 {
		t.Fatalf("expected a 4xx rejection for an oversized upload, got %d: %s", w.Code, w.Body.String())
	}
}

func TestImportInvalidModeRejected(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	r := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=bogus", "lte-cell-inventory.csv", exampleCSV(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 400/422 for invalid mode, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetImportErrorsNotFound(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	r := httptest.NewRequest(http.MethodGet, "/v1/cell-inventory/imports/does-not-exist/errors", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestExportCellsHeadersAndCanonicalCSV(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	importReq := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=merge", "lte-cell-inventory.csv", exampleCSV(t))
	h.ServeHTTP(httptest.NewRecorder(), importReq)

	r := httptest.NewRequest(http.MethodGet, "/v1/cell-inventory/export?format=csv", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("content-type=%q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "lte-cell-inventory.csv") {
		t.Fatalf("content-disposition=%q", cd)
	}
	if w.Header().Get("X-Record-Count") != "3" {
		t.Fatalf("x-record-count=%q", w.Header().Get("X-Record-Count"))
	}
	if w.Header().Get("X-Inventory-Version") == "" || w.Header().Get("X-Exported-At") == "" {
		t.Fatal("expected version/exported-at headers to be populated")
	}

	// Exported CSV must itself validate cleanly (round trip).
	revalidate := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=validate-only", "roundtrip.csv", w.Body.Bytes())
	revalidateW := httptest.NewRecorder()
	h.ServeHTTP(revalidateW, revalidate)
	var got inventoryImportView
	if err := json.Unmarshal(revalidateW.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RowsRejected != 0 || got.RowsValid != 3 {
		t.Fatalf("re-import of export failed: %+v body=%s", got, revalidateW.Body.String())
	}
}

func TestSelectionPreviewHappyPathAndMalformedGeometry(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	importReq := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=merge", "lte-cell-inventory.csv", exampleCSV(t))
	h.ServeHTTP(httptest.NewRecorder(), importReq)

	body, _ := json.Marshal(inventory.SelectionRequest{
		PLMN:   inventory.PLMN{MCC: "311", MNC: "435", MNCLength: 3},
		Policy: inventory.PolicyConservativeIntersection,
		Area:   []byte(`{"type":"Polygon","coordinates":[[[-86.3100,32.3700],[-86.2800,32.3900],[-86.2500,32.3600],[-86.3100,32.3700]]]}`),
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/cell-inventory/selection-preview", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result inventory.SelectionResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SelectedCount == 0 {
		t.Fatalf("expected at least one selected cell: %+v", result)
	}

	badBody, _ := json.Marshal(inventory.SelectionRequest{
		Policy: inventory.PolicyConservativeIntersection,
		Area:   []byte(`{"type":"Polygon","coordinates":[]}`),
	})
	badReq := httptest.NewRequest(http.MethodPost, "/v1/cell-inventory/selection-preview", bytes.NewReader(badBody))
	badReq.Header.Set("Content-Type", "application/json")
	badW := httptest.NewRecorder()
	h.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed geometry, got %d: %s", badW.Code, badW.Body.String())
	}
}

func validCreateCellBody() createCellBody {
	return createCellBody{
		MCC: "311", MNC: "435", MNCLength: 3,
		ENBID: 4096, LocalCellID: 1, TAC: 1,
		GeometryQuality: "unknown", Active: true,
	}
}

func TestCreateCellComputesECI(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	w := doJSON(t, h, http.MethodPost, "/v1/cell-inventory/cells", validCreateCellBody())
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var c inventory.LTECell
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.ECI != 1048577 {
		t.Fatalf("got ECI %d, want 1048577 (4096<<8|1)", c.ECI)
	}
}

func TestCreateCellRejectsInvalidInput(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	b := validCreateCellBody()
	b.GeometryQuality = ""
	w := doJSON(t, h, http.MethodPost, "/v1/cell-inventory/cells", b)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateCellRejectsDuplicateReturns409(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	doJSON(t, h, http.MethodPost, "/v1/cell-inventory/cells", validCreateCellBody())
	w := doJSON(t, h, http.MethodPost, "/v1/cell-inventory/cells", validCreateCellBody())
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteCellSucceedsAndNotFoundAfter(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	w := doJSON(t, h, http.MethodPost, "/v1/cell-inventory/cells", validCreateCellBody())
	var c inventory.LTECell
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	del := doJSON(t, h, http.MethodDelete, "/v1/cell-inventory/cells/"+strconv.FormatInt(c.ID, 10), nil)
	if del.Code != http.StatusNoContent && del.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", del.Code, del.Body.String())
	}
	get := doJSON(t, h, http.MethodGet, "/v1/cell-inventory/cells/"+strconv.FormatInt(c.ID, 10), nil)
	if get.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d: %s", get.Code, get.Body.String())
	}
}

func TestDeleteCellNotFoundReturns404(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	w := doJSON(t, h, http.MethodDelete, "/v1/cell-inventory/cells/99999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteCellWithGeocodesReturns409(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	w := doJSON(t, h, http.MethodPost, "/v1/cell-inventory/cells", validCreateCellBody())
	var c inventory.LTECell
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	mapping := doJSON(t, h, http.MethodPost, "/v1/geocodes", createGeocodeBody{
		MCC: c.PLMN.MCC, MNC: c.PLMN.MNC, MNCLength: c.PLMN.MNCLength, ECI: c.ECI, CodeType: "SAME", Code: "001101",
	})
	if mapping.Code != http.StatusOK && mapping.Code != http.StatusCreated {
		t.Fatalf("mapping create: status=%d body=%s", mapping.Code, mapping.Body.String())
	}
	del := doJSON(t, h, http.MethodDelete, "/v1/cell-inventory/cells/"+strconv.FormatInt(c.ID, 10), nil)
	if del.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", del.Code, del.Body.String())
	}
}
