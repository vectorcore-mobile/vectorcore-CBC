package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vectorcore/cbc/internal/service"
)

func TestOperatorEndpointsAreUnauthenticated(t *testing.T) {
	h := New(service.New(nil, nil), nil, nil, "", "0.1.0-test")
	for _, tc := range []struct {
		path string
		want int
	}{{"/healthz", 204}, {"/v1/alerts", 200}, {"/v1/version", 200}} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.path, w.Code, tc.want)
		}
	}
}

// TestDisabledCellInventoryReturns404NotRedirect guards against a regression
// where an unregistered path (e.g. cell-inventory routes when that feature
// is disabled) fell through to the "/" catch-all and got redirected to the
// HTML UI shell instead of 404ing - which broke the UI's fetch() calls,
// which expect JSON and choked trying to parse the redirected HTML.
func TestDisabledCellInventoryReturns404NotRedirect(t *testing.T) {
	h := New(service.New(nil, nil), nil, nil, "", "0.1.0-test") // inv == nil: feature disabled
	r := httptest.NewRequest(http.MethodGet, "/v1/cell-inventory/cells", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 (no redirect)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "" && ct != "text/plain; charset=utf-8" {
		t.Fatalf("expected a plain 404 body, got Content-Type %q body %q", ct, w.Body.String())
	}
}

func TestBareRootRedirectsToUI(t *testing.T) {
	h := New(service.New(nil, nil), nil, nil, "", "0.1.0-test")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/ui/" {
		t.Fatalf("got %d Location=%q, want 302 Location=/ui/", w.Code, w.Header().Get("Location"))
	}
}
