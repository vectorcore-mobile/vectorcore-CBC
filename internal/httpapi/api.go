// Package httpapi exposes the CBC operator API through Huma/OpenAPI.
package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/vectorcore/cbc/internal/geocode"
	"github.com/vectorcore/cbc/internal/inventory"
	"github.com/vectorcore/cbc/internal/service"
)

// New builds the CBC operator API. inv, geo, and defaultImportMode may be
// zero values (nil / nil / "") to leave the cell-inventory and geo-codes
// endpoints unregistered when that feature is disabled - both are gated
// together since geo-codes reference lte_cells rows and are meaningless
// without it.
func New(s *service.Service, inv *inventory.Service, geo *geocode.Service, defaultImportMode string, version string) http.Handler {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("VectorCore CBC API", "0.1.0"))
	registerOperational(api, s, version)
	var handler http.Handler = mux
	if inv != nil {
		registerCellInventory(api, inv, inventory.ImportMode(defaultImportMode))
		registerGeocodes(api, geo, geocode.ImportMode(defaultImportMode))
		maxImportBytes := inv.MaxImportSizeBytes()
		humago.MultipartMaxMemory = maxImportBytes
		handler = limitImportBody(maxImportBytes, mux)
	}
	mux.Handle("/ui/", uiHandler())
	// Only the bare root gets the convenience redirect to the UI - anything
	// else unmatched (e.g. an API path disabled by config, like
	// cell-inventory when the feature is off) must 404, not redirect. A
	// redirect here would otherwise send a JSON-expecting fetch() to the
	// HTML SPA shell, which then fails obscurely trying to parse it as JSON.
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	return handler
}

// limitImportBody bounds the multipart upload body before Huma parses it:
// huma's own MaxBodyBytes only applies to non-multipart request bodies, so
// without this an oversized upload would be buffered/spilled to disk before
// the handler ever runs.
func limitImportBody(max int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && (strings.HasPrefix(r.URL.Path, "/v1/cell-inventory/imports") || strings.HasPrefix(r.URL.Path, "/v1/geocodes/import")) {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}

type emptyInput struct{}
type noContent struct{}
type alertsOutput struct{ Body []service.Record }
type alertInput struct {
	ID string `path:"id"`
}
type alertOutput struct{ Body service.Record }
type planInput struct {
	ID string `path:"id"`
}
type planOutput struct{ Body any }
type auditOutput struct{ Body any }
type metricsOutput struct{ Body service.Metrics }
type versionBody struct {
	Version string `json:"version"`
}
type versionOutput struct{ Body versionBody }

func registerOperational(api huma.API, s *service.Service, version string) {
	huma.Register(api, huma.Operation{OperationID: "version", Method: http.MethodGet, Path: "/v1/version", Summary: "CBC build version"}, func(_ context.Context, _ *emptyInput) (*versionOutput, error) {
		return &versionOutput{Body: versionBody{Version: version}}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "health", Method: http.MethodGet, Path: "/healthz", Summary: "Liveness probe"}, func(_ context.Context, _ *emptyInput) (*noContent, error) { return &noContent{}, nil })
	huma.Register(api, huma.Operation{OperationID: "ready", Method: http.MethodGet, Path: "/readyz", Summary: "Readiness probe", Errors: []int{http.StatusServiceUnavailable}}, func(_ context.Context, _ *emptyInput) (*noContent, error) {
		if !s.Ready() {
			return nil, huma.Error503ServiceUnavailable("CBE XMPP session is not established")
		}
		return &noContent{}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "metrics", Method: http.MethodGet, Path: "/metrics", Summary: "CBC operational metrics"}, func(_ context.Context, _ *emptyInput) (*metricsOutput, error) {
		return &metricsOutput{Body: s.Metrics()}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "list-alerts", Method: http.MethodGet, Path: "/v1/alerts", Summary: "List CAP alerts"}, func(_ context.Context, _ *emptyInput) (*alertsOutput, error) {
		return &alertsOutput{Body: s.Alerts()}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "get-alert", Method: http.MethodGet, Path: "/v1/alerts/{id}", Summary: "Get CAP alert", Errors: []int{http.StatusNotFound}}, func(_ context.Context, in *alertInput) (*alertOutput, error) {
		a, ok := s.Alert(in.ID)
		if !ok {
			return nil, huma.Error404NotFound("alert not found")
		}
		return &alertOutput{Body: a}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "get-cbs-plan", Method: http.MethodGet, Path: "/v1/alerts/{id}/cbs", Summary: "Get prepared CBS plan", Errors: []int{http.StatusNotFound}}, func(ctx context.Context, in *planInput) (*planOutput, error) {
		raw, err := s.CBSPlan(ctx, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("CBS plan unavailable")
		}
		if raw == nil {
			return nil, huma.Error404NotFound("CBS plan not found")
		}
		return &planOutput{Body: jsonRaw(raw)}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "list-audit", Method: http.MethodGet, Path: "/v1/audit", Summary: "List lifecycle audit events"}, func(ctx context.Context, _ *emptyInput) (*auditOutput, error) {
		events, err := s.Audit(ctx, 100)
		if err != nil {
			return nil, huma.Error500InternalServerError("audit unavailable")
		}
		return &auditOutput{Body: events}, nil
	})
}

type jsonRaw []byte

func (j jsonRaw) MarshalJSON() ([]byte, error) { return j, nil }
