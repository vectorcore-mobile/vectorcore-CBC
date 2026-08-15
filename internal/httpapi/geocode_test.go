package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/vectorcore/cbc/internal/geocode"
	"github.com/vectorcore/cbc/internal/service"
)

// seedCells imports the canonical example cell-inventory CSV into h so
// geocode tests have real lte_cells rows (ECIs 1048577, 1048578, 1048833) to
// reference.
func seedCells(t *testing.T, h http.Handler) {
	t.Helper()
	r := multipartCSVRequest(t, "/v1/cell-inventory/imports?mode=merge", "cells.csv", exampleCSV(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("seed cells: status=%d body=%s", w.Code, w.Body.String())
	}
}

func exampleGeocodesCSV(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../docs/example-cell-geocodes.csv")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGeocodesDisabledReturns404(t *testing.T) {
	h := New(service.New(nil, nil), nil, nil, "", "0.1.0-test")
	w := doJSON(t, h, http.MethodGet, "/v1/geocodes", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", w.Code)
	}
}

func TestCreateListDeleteGeocode(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	seedCells(t, h)

	w := doJSON(t, h, http.MethodPost, "/v1/geocodes", createGeocodeBody{MCC: "311", MNC: "435", MNCLength: 3, ECI: 1048577, CodeType: "SAME", Code: "001101"})
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var entry geocode.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ECI != 1048577 || entry.CodeType != geocode.SAME || entry.Code != "001101" {
		t.Fatalf("unexpected entry: %+v", entry)
	}

	w = doJSON(t, h, http.MethodGet, "/v1/geocodes", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", w.Code, w.Body.String())
	}
	var list geocodeListBody
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 {
		t.Fatalf("list=%+v", list)
	}

	w = doJSON(t, h, http.MethodDelete, "/v1/geocodes/"+strconv.FormatInt(entry.ID, 10), nil)
	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSON(t, h, http.MethodGet, "/v1/geocodes", nil)
	var list2 geocodeListBody
	if err := json.Unmarshal(w.Body.Bytes(), &list2); err != nil {
		t.Fatal(err)
	}
	if list2.Total != 0 {
		t.Fatalf("list after delete=%+v", list2)
	}
}

func TestCreateGeocodeUnknownCellReturns400(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	seedCells(t, h)
	w := doJSON(t, h, http.MethodPost, "/v1/geocodes", createGeocodeBody{MCC: "311", MNC: "435", MNCLength: 3, ECI: 9999999, CodeType: "SAME", Code: "001101"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResolveGeocode(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	seedCells(t, h)
	doJSON(t, h, http.MethodPost, "/v1/geocodes", createGeocodeBody{MCC: "311", MNC: "435", MNCLength: 3, ECI: 1048577, CodeType: "SAME", Code: "001101"})
	doJSON(t, h, http.MethodPost, "/v1/geocodes", createGeocodeBody{MCC: "311", MNC: "435", MNCLength: 3, ECI: 1048578, CodeType: "SAME", Code: "001101"})

	w := doJSON(t, h, http.MethodPost, "/v1/geocodes/resolve", resolveGeocodeBody{CodeType: "SAME", Code: "001101"})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result resolveGeocodeResultBody
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Cells) != 2 {
		t.Fatalf("cells=%v", result.Cells)
	}
}

func TestResolveGeocodeNoMatch(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	seedCells(t, h)
	w := doJSON(t, h, http.MethodPost, "/v1/geocodes/resolve", resolveGeocodeBody{CodeType: "UGC", Code: "ALZ999"})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result resolveGeocodeResultBody
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Cells) != 0 {
		t.Fatalf("expected no matches, got %v", result.Cells)
	}
}

func TestImportGeocodesCSV(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	seedCells(t, h)
	r := multipartCSVRequest(t, "/v1/geocodes/import?mode=merge", "geocodes.csv", exampleGeocodesCSV(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result geocode.ImportResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 8 || len(result.Errors) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestCreateListDeleteGeoCodeRegistryEntry(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)

	w := doJSON(t, h, http.MethodPost, "/v1/geocode-registry", createGeoCodeBody{Type: "state", Code: "AL01", Description: "Test state code"})
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var code geocode.Code
	if err := json.Unmarshal(w.Body.Bytes(), &code); err != nil {
		t.Fatal(err)
	}
	if code.Type != "STATE" || code.Code != "AL01" || code.Description != "Test state code" {
		t.Fatalf("unexpected code (type should be uppercased): %+v", code)
	}

	w = doJSON(t, h, http.MethodGet, "/v1/geocode-registry", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", w.Code, w.Body.String())
	}
	var list geoCodeListBody
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Codes) != 1 {
		t.Fatalf("list=%+v", list)
	}

	w = doJSON(t, h, http.MethodDelete, "/v1/geocode-registry/"+strconv.FormatInt(code.ID, 10), nil)
	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSON(t, h, http.MethodGet, "/v1/geocode-registry", nil)
	var list2 geoCodeListBody
	if err := json.Unmarshal(w.Body.Bytes(), &list2); err != nil {
		t.Fatal(err)
	}
	if len(list2.Codes) != 0 {
		t.Fatalf("list after delete=%+v", list2)
	}
}

func TestCreateGeoCodeRegistryEntryRequiresTypeAndCode(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	w := doJSON(t, h, http.MethodPost, "/v1/geocode-registry", createGeoCodeBody{Type: "", Code: "AL01"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteUnknownGeoCodeRegistryEntryReturns404(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	w := doJSON(t, h, http.MethodDelete, "/v1/geocode-registry/12345", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateAndResolveArbitraryGeocodeType(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	seedCells(t, h)
	doJSON(t, h, http.MethodPost, "/v1/geocodes", createGeocodeBody{MCC: "311", MNC: "435", MNCLength: 3, ECI: 1048577, CodeType: "STATE", Code: "AL01"})

	w := doJSON(t, h, http.MethodPost, "/v1/geocodes/resolve", resolveGeocodeBody{CodeType: "STATE", Code: "AL01"})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result resolveGeocodeResultBody
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Cells) != 1 {
		t.Fatalf("cells=%v", result.Cells)
	}
}

func TestExportGeocodesCSV(t *testing.T) {
	h := newInventoryTestHandler(t, 10*1024*1024)
	seedCells(t, h)
	doJSON(t, h, http.MethodPost, "/v1/geocodes", createGeocodeBody{MCC: "311", MNC: "435", MNCLength: 3, ECI: 1048577, CodeType: "SAME", Code: "001101"})

	r := httptest.NewRequest(http.MethodGet, "/v1/geocodes/export", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "mcc,mnc,mnc_length,eci,code_type,code") || !strings.Contains(body, "311,435,3,1048577,SAME,001101") {
		t.Fatalf("unexpected export body:\n%s", body)
	}
}
