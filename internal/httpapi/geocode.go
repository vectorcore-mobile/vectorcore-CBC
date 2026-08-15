package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/vectorcore/cbc/internal/geocode"
)

type geocodeListBody struct {
	Entries []geocode.Entry `json:"entries"`
	Total   int             `json:"total"`
}

// Optional filters are plain strings, not pointers - see listCellsInput's
// comment in inventory.go for why (huma v2.39.1 panics on pointer-typed
// query params).
type listGeocodesInput struct {
	CodeType string `query:"codeType" doc:"Geo code type, e.g. SAME or UGC"`
	Code     string `query:"code"`
	CellID   string `query:"cellId"`
	Limit    int    `query:"limit" default:"50"`
	Offset   int    `query:"offset" default:"0"`
}
type listGeocodesOutput struct{ Body geocodeListBody }

func parseGeocodeFilter(codeType, code, cellID string, limit, offset int) (geocode.Filter, error) {
	filter := geocode.Filter{CodeType: codeType, Code: code, Limit: limit, Offset: offset}
	if cellID != "" {
		v, err := strconv.ParseInt(cellID, 10, 64)
		if err != nil {
			return filter, fmt.Errorf("cellId must be a valid cell ID")
		}
		filter.CellID = &v
	}
	return filter, nil
}

type createGeocodeBody struct {
	MCC       string `json:"mcc"`
	MNC       string `json:"mnc"`
	MNCLength int    `json:"mncLength"`
	ECI       uint32 `json:"eci"`
	CodeType  string `json:"codeType" doc:"Geo code type, e.g. SAME or UGC"`
	Code      string `json:"code"`
}
type createGeocodeInput struct{ Body createGeocodeBody }
type geocodeOutput struct{ Body geocode.Entry }

type geocodeIDInput struct {
	ID int64 `path:"id"`
}

type geocodeImportInput struct {
	Mode    string `query:"mode" enum:"validate-only,merge,replace" doc:"Import mode; defaults to the server's configured default"`
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" contentType:"text/csv,application/octet-stream,text/plain" required:"true"`
	}]
}
type geocodeImportOutput struct{ Body geocode.ImportResult }

type geocodeExportInput struct {
	Format   string `query:"format" default:"csv"`
	CodeType string `query:"codeType" doc:"Geo code type, e.g. SAME or UGC"`
	Code     string `query:"code"`
}
type geocodeExportOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

type resolveGeocodeBody struct {
	CodeType string `json:"codeType" doc:"Geo code type, e.g. SAME or UGC"`
	Code     string `json:"code"`
}
type resolveGeocodeInput struct{ Body resolveGeocodeBody }
type resolveGeocodeResultBody struct {
	Cells []uint32 `json:"cells"`
}
type resolveGeocodeOutput struct{ Body resolveGeocodeResultBody }

type geoCodeListBody struct {
	Codes []geocode.Code `json:"codes"`
}
type listGeoCodesOutput struct{ Body geoCodeListBody }

type createGeoCodeBody struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	Description string `json:"description,omitempty"`
}
type createGeoCodeInput struct{ Body createGeoCodeBody }
type geoCodeOutput struct{ Body geocode.Code }

type geoCodeIDInput struct {
	ID int64 `path:"id"`
}

// registerGeocodes adds the Geo Codes registry endpoints and the
// code-to-cell mapping endpoints (any geocode type, not just SAME/UGC). It
// is only called when the cell_inventory feature is enabled (geo-codes
// reference lte_cells rows and are meaningless without it), keeping the
// operational endpoints in api.go unaffected when it is not.
func registerGeocodes(api huma.API, geo *geocode.Service, defaultMode geocode.ImportMode) {
	huma.Register(api, huma.Operation{
		OperationID: "list-geocodes",
		Method:      http.MethodGet,
		Path:        "/v1/geocodes",
		Summary:     "List geo-code-to-cell mappings",
		Errors:      []int{http.StatusBadRequest},
	}, func(ctx context.Context, in *listGeocodesInput) (*listGeocodesOutput, error) {
		filter, err := parseGeocodeFilter(in.CodeType, in.Code, in.CellID, in.Limit, in.Offset)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		entries, total, err := geo.List(ctx, filter)
		if err != nil {
			return nil, huma.Error500InternalServerError("list geocodes failed")
		}
		if entries == nil {
			entries = []geocode.Entry{}
		}
		return &listGeocodesOutput{Body: geocodeListBody{Entries: entries, Total: total}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-geocode",
		Method:      http.MethodPost,
		Path:        "/v1/geocodes",
		Summary:     "Tag one cell with a geo code",
		Errors:      []int{http.StatusBadRequest},
	}, func(ctx context.Context, in *createGeocodeInput) (*geocodeOutput, error) {
		b := in.Body
		entry, err := geo.Create(ctx, b.MCC, b.MNC, b.MNCLength, b.ECI, geocode.CodeType(b.CodeType), b.Code)
		if err != nil {
			switch {
			case errors.Is(err, geocode.ErrCellNotFound):
				return nil, huma.Error400BadRequest("no cell matches the given mcc/mnc/mncLength/eci")
			default:
				return nil, huma.Error500InternalServerError("create geocode failed")
			}
		}
		return &geocodeOutput{Body: *entry}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-geocode",
		Method:      http.MethodDelete,
		Path:        "/v1/geocodes/{id}",
		Summary:     "Delete one geo-code-to-cell mapping",
		Errors:      []int{http.StatusNotFound},
	}, func(ctx context.Context, in *geocodeIDInput) (*noContent, error) {
		if err := geo.Delete(ctx, in.ID); err != nil {
			if errors.Is(err, geocode.ErrEntryNotFound) {
				return nil, huma.Error404NotFound("geocode entry not found")
			}
			return nil, huma.Error500InternalServerError("delete geocode failed")
		}
		return &noContent{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "import-geocodes",
		Method:      http.MethodPost,
		Path:        "/v1/geocodes/import",
		Summary:     "Import geo-code-to-cell mappings from CSV",
		Errors:      []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, func(ctx context.Context, in *geocodeImportInput) (*geocodeImportOutput, error) {
		mode := geocode.ImportMode(in.Mode)
		if in.Mode == "" {
			mode = defaultMode
		}
		file := in.RawBody.Data().File
		if !file.IsSet {
			return nil, huma.Error400BadRequest("multipart field 'file' is required")
		}
		result, err := geo.Import(ctx, file.File, mode)
		if err != nil {
			switch {
			case errors.Is(err, geocode.ErrInvalidImportMode):
				return nil, huma.Error400BadRequest(err.Error())
			default:
				return nil, huma.Error500InternalServerError("geocode import failed")
			}
		}
		return &geocodeImportOutput{Body: *result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "export-geocodes",
		Method:      http.MethodGet,
		Path:        "/v1/geocodes/export",
		Summary:     "Export geo-code-to-cell mappings as canonical CSV",
		Errors:      []int{http.StatusBadRequest},
	}, func(ctx context.Context, in *geocodeExportInput) (*geocodeExportOutput, error) {
		if in.Format != "" && in.Format != "csv" {
			return nil, huma.Error400BadRequest("only format=csv is supported")
		}
		var buf bytes.Buffer
		if err := geo.Export(ctx, geocode.Filter{CodeType: in.CodeType, Code: in.Code}, &buf); err != nil {
			return nil, huma.Error500InternalServerError("export failed")
		}
		return &geocodeExportOutput{
			ContentType:        "text/csv",
			ContentDisposition: `attachment; filename="cell-geocodes.csv"`,
			Body:               buf.Bytes(),
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "resolve-geocode",
		Method:      http.MethodPost,
		Path:        "/v1/geocodes/resolve",
		Summary:     "Test which cells a geo code resolves to (same lookup live alert targeting uses)",
	}, func(ctx context.Context, in *resolveGeocodeInput) (*resolveGeocodeOutput, error) {
		cells, err := geo.ResolveCells(ctx, in.Body.CodeType, in.Body.Code)
		if err != nil {
			return nil, huma.Error500InternalServerError("resolve failed")
		}
		if cells == nil {
			cells = []uint32{}
		}
		return &resolveGeocodeOutput{Body: resolveGeocodeResultBody{Cells: cells}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-geo-code-registry",
		Method:      http.MethodGet,
		Path:        "/v1/geocode-registry",
		Summary:     "List Geo Codes registry entries (type/code/description)",
	}, func(ctx context.Context, in *emptyInput) (*listGeoCodesOutput, error) {
		codes, err := geo.ListCodes(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list geo codes failed")
		}
		if codes == nil {
			codes = []geocode.Code{}
		}
		return &listGeoCodesOutput{Body: geoCodeListBody{Codes: codes}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-geo-code-registry-entry",
		Method:      http.MethodPost,
		Path:        "/v1/geocode-registry",
		Summary:     "Add a Geo Codes registry entry",
		Errors:      []int{http.StatusBadRequest},
	}, func(ctx context.Context, in *createGeoCodeInput) (*geoCodeOutput, error) {
		b := in.Body
		code, err := geo.CreateCode(ctx, b.Type, b.Code, b.Description)
		if err != nil {
			switch {
			case errors.Is(err, geocode.ErrCodeRequired):
				return nil, huma.Error400BadRequest(err.Error())
			default:
				return nil, huma.Error500InternalServerError("create geo code failed")
			}
		}
		return &geoCodeOutput{Body: *code}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-geo-code-registry-entry",
		Method:      http.MethodDelete,
		Path:        "/v1/geocode-registry/{id}",
		Summary:     "Delete a Geo Codes registry entry",
		Errors:      []int{http.StatusNotFound},
	}, func(ctx context.Context, in *geoCodeIDInput) (*noContent, error) {
		if err := geo.DeleteCode(ctx, in.ID); err != nil {
			if errors.Is(err, geocode.ErrCodeNotFound) {
				return nil, huma.Error404NotFound("geo code not found")
			}
			return nil, huma.Error500InternalServerError("delete geo code failed")
		}
		return &noContent{}, nil
	})
}
