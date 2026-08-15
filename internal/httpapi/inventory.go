package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/vectorcore/cbc/internal/inventory"
)

type inventoryImportView struct {
	ImportID       string `json:"importId"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	SourceFilename string `json:"sourceFilename"`
	SourceSHA256   string `json:"sourceSha256"`
	RowsReceived   int    `json:"rowsReceived"`
	RowsValid      int    `json:"rowsValid"`
	RowsRejected   int    `json:"rowsRejected"`
	Inserted       int    `json:"inserted"`
	Updated        int    `json:"updated"`
	Deactivated    int    `json:"deactivated"`
	Warnings       int    `json:"warnings"`
}

func toImportView(imp *inventory.InventoryImport) inventoryImportView {
	return inventoryImportView{
		ImportID: imp.ID, Mode: string(imp.Mode), Status: string(imp.Status),
		SourceFilename: imp.SourceFilename, SourceSHA256: imp.SourceSHA256,
		RowsReceived: imp.RowsReceived, RowsValid: imp.RowsValid, RowsRejected: imp.RowsRejected,
		Inserted: imp.InsertedCount, Updated: imp.UpdatedCount, Deactivated: imp.DeactivatedCount,
		Warnings: imp.WarningCount,
	}
}

type importInput struct {
	Mode    string `query:"mode" enum:"validate-only,merge,replace" doc:"Import mode; defaults to the server's configured default"`
	RawBody huma.MultipartFormFiles[struct {
		// Real clients vary in what Content-Type they send for a .csv part
		// (curl and Go's own multipart.Writer default to
		// application/octet-stream), so all three common values are
		// accepted rather than rejecting a valid upload on a mismatched
		// declared type.
		File huma.FormFile `form:"file" contentType:"text/csv,application/octet-stream,text/plain" required:"true"`
	}]
}
type importOutput struct{ Body inventoryImportView }

type importIDInput struct {
	ImportID string `path:"importID"`
}
type importErrorsOutput struct{ Body []inventory.ValidationError }

type cellListBody struct {
	Cells []inventory.LTECell `json:"cells"`
	Total int                 `json:"total"`
}

// Optional numeric/boolean filters are plain strings, not pointers: huma
// v2.39.1 panics on pointer-typed query/form/header/path parameters (it
// cannot yet allocate them lazily during binding), so "not provided" is
// represented as an empty string and parsed by hand in the handler.
type listCellsInput struct {
	Active  string `query:"active" doc:"true or false"`
	MCC     string `query:"mcc"`
	MNC     string `query:"mnc"`
	TAC     string `query:"tac"`
	MMEName string `query:"mmeName"`
	ENBID   string `query:"enbId"`
	ECI     string `query:"eci"`
	Limit   int    `query:"limit" default:"50"`
	Offset  int    `query:"offset" default:"0"`
}
type listCellsOutput struct{ Body cellListBody }

type cellIDInput struct {
	CellID int64 `path:"cellID"`
}
type cellOutput struct{ Body inventory.LTECell }

type exportInput struct {
	Format string `query:"format" default:"csv"`
	Active string `query:"active" doc:"true or false"`
}

// parseCellFilter turns the string-typed optional query parameters shared by
// listCellsInput and exportInput into a CellFilter, or a 400 error.
func parseCellFilter(active, mcc, mnc, tac, mmeName, enbID, eci string, limit, offset int) (inventory.CellFilter, error) {
	filter := inventory.CellFilter{MCC: mcc, MNC: mnc, MMEName: mmeName, Limit: limit, Offset: offset}
	if active != "" {
		b, err := strconv.ParseBool(active)
		if err != nil {
			return filter, fmt.Errorf("active must be a boolean value")
		}
		filter.Active = &b
	}
	if tac != "" {
		v, err := strconv.ParseUint(tac, 10, 16)
		if err != nil {
			return filter, fmt.Errorf("tac must be a valid 16-bit TAC value")
		}
		t := uint16(v)
		filter.TAC = &t
	}
	if enbID != "" {
		v, err := strconv.ParseUint(enbID, 10, 32)
		if err != nil {
			return filter, fmt.Errorf("enbId must be a valid eNB ID value")
		}
		e := uint32(v)
		filter.ENBID = &e
	}
	if eci != "" {
		v, err := strconv.ParseUint(eci, 10, 32)
		if err != nil {
			return filter, fmt.Errorf("eci must be a valid ECI value")
		}
		e := uint32(v)
		filter.ECI = &e
	}
	return filter, nil
}

type exportOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	InventoryVersion   string `header:"X-Inventory-Version"`
	ExportedAt         string `header:"X-Exported-At"`
	RecordCount        int    `header:"X-Record-Count"`
	Body               []byte
}

type selectionInput struct{ Body inventory.SelectionRequest }
type selectionOutput struct{ Body inventory.SelectionResult }

type createCellBody struct {
	MCC             string   `json:"mcc"`
	MNC             string   `json:"mnc"`
	MNCLength       int      `json:"mncLength"`
	ENBID           uint32   `json:"enbId"`
	LocalCellID     uint8    `json:"localCellId"`
	TAC             uint16   `json:"tac"`
	CellName        string   `json:"cellName,omitempty"`
	MMEName         string   `json:"mmeName,omitempty"`
	Latitude        *float64 `json:"latitude,omitempty"`
	Longitude       *float64 `json:"longitude,omitempty"`
	NominalRadiusM  *float64 `json:"nominalRadiusM,omitempty"`
	AzimuthDeg      *float64 `json:"azimuthDeg,omitempty"`
	BeamwidthDeg    *float64 `json:"beamwidthDeg,omitempty"`
	CoverageGeoJSON string   `json:"coverageGeoJSON,omitempty"`
	GeometryQuality string   `json:"geometryQuality"`
	Source          string   `json:"source,omitempty"`
	SourceRecordID  string   `json:"sourceRecordId,omitempty"`
	SourceVersion   string   `json:"sourceVersion,omitempty"`
	Active          bool     `json:"active"`
}
type createCellInput struct{ Body createCellBody }

// registerCellInventory adds the cell-inventory import/export/selection
// endpoints. It is only called when the cell_inventory feature is enabled,
// keeping the operational endpoints in api.go unaffected when it is not.
func registerCellInventory(api huma.API, inv *inventory.Service, defaultMode inventory.ImportMode) {
	huma.Register(api, huma.Operation{
		OperationID: "import-cell-inventory",
		Method:      http.MethodPost,
		Path:        "/v1/cell-inventory/imports",
		Summary:     "Import LTE cell inventory from CSV",
		Errors:      []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusInternalServerError},
	}, func(ctx context.Context, in *importInput) (*importOutput, error) {
		mode := inventory.ImportMode(in.Mode)
		if in.Mode == "" {
			mode = defaultMode
		}
		file := in.RawBody.Data().File
		if !file.IsSet {
			return nil, huma.Error400BadRequest("multipart field 'file' is required")
		}
		imp, err := inv.Import(ctx, file.Filename, file.File, mode)
		if err != nil {
			switch {
			case errors.Is(err, inventory.ErrUploadTooLarge):
				return nil, huma.Error413RequestEntityTooLarge("uploaded file exceeds the configured maximum import size")
			case errors.Is(err, inventory.ErrInvalidImportMode):
				return nil, huma.Error400BadRequest(err.Error())
			default:
				return nil, huma.Error500InternalServerError("cell inventory import failed")
			}
		}
		return &importOutput{Body: toImportView(imp)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-cell-inventory-import",
		Method:      http.MethodGet,
		Path:        "/v1/cell-inventory/imports/{importID}",
		Summary:     "Get a cell inventory import result",
		Errors:      []int{http.StatusNotFound},
	}, func(ctx context.Context, in *importIDInput) (*importOutput, error) {
		imp, err := inv.GetImport(ctx, in.ImportID)
		if err != nil {
			return nil, huma.Error500InternalServerError("import lookup failed")
		}
		if imp == nil {
			return nil, huma.Error404NotFound("import not found")
		}
		return &importOutput{Body: toImportView(imp)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-cell-inventory-import-errors",
		Method:      http.MethodGet,
		Path:        "/v1/cell-inventory/imports/{importID}/errors",
		Summary:     "Get a cell inventory import's row validation errors",
		Errors:      []int{http.StatusNotFound},
	}, func(ctx context.Context, in *importIDInput) (*importErrorsOutput, error) {
		imp, err := inv.GetImport(ctx, in.ImportID)
		if err != nil {
			return nil, huma.Error500InternalServerError("import lookup failed")
		}
		if imp == nil {
			return nil, huma.Error404NotFound("import not found")
		}
		errs, err := inv.ListImportErrors(ctx, in.ImportID)
		if err != nil {
			return nil, huma.Error500InternalServerError("import error lookup failed")
		}
		if errs == nil {
			errs = []inventory.ValidationError{}
		}
		return &importErrorsOutput{Body: errs}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-cell-inventory-cells",
		Method:      http.MethodGet,
		Path:        "/v1/cell-inventory/cells",
		Summary:     "List LTE cell inventory",
		Errors:      []int{http.StatusBadRequest},
	}, func(ctx context.Context, in *listCellsInput) (*listCellsOutput, error) {
		filter, err := parseCellFilter(in.Active, in.MCC, in.MNC, in.TAC, in.MMEName, in.ENBID, in.ECI, in.Limit, in.Offset)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		cells, total, err := inv.ListCells(ctx, filter)
		if err != nil {
			return nil, huma.Error500InternalServerError("list cells failed")
		}
		if cells == nil {
			cells = []inventory.LTECell{}
		}
		return &listCellsOutput{Body: cellListBody{Cells: cells, Total: total}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-cell-inventory-cell",
		Method:      http.MethodGet,
		Path:        "/v1/cell-inventory/cells/{cellID}",
		Summary:     "Get one LTE cell",
		Errors:      []int{http.StatusNotFound},
	}, func(ctx context.Context, in *cellIDInput) (*cellOutput, error) {
		c, err := inv.GetCell(ctx, in.CellID)
		if err != nil {
			return nil, huma.Error500InternalServerError("get cell failed")
		}
		if c == nil {
			return nil, huma.Error404NotFound("cell not found")
		}
		return &cellOutput{Body: *c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "export-cell-inventory",
		Method:      http.MethodGet,
		Path:        "/v1/cell-inventory/export",
		Summary:     "Export LTE cell inventory as canonical CSV",
		Errors:      []int{http.StatusBadRequest},
	}, func(ctx context.Context, in *exportInput) (*exportOutput, error) {
		if in.Format != "" && in.Format != "csv" {
			return nil, huma.Error400BadRequest("only format=csv is supported")
		}
		filter, err := parseCellFilter(in.Active, "", "", "", "", "", "", 0, 0)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		var buf bytes.Buffer
		meta, err := inv.Export(ctx, filter, &buf)
		if err != nil {
			return nil, huma.Error500InternalServerError("export failed")
		}
		return &exportOutput{
			ContentType:        "text/csv",
			ContentDisposition: `attachment; filename="lte-cell-inventory.csv"`,
			InventoryVersion:   meta.VersionName,
			ExportedAt:         meta.ExportedAt.UTC().Format(time.RFC3339),
			RecordCount:        meta.RecordCount,
			Body:               buf.Bytes(),
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-cell-inventory-cell",
		Method:      http.MethodPost,
		Path:        "/v1/cell-inventory/cells",
		Summary:     "Create one LTE cell",
		Errors:      []int{http.StatusBadRequest, http.StatusConflict},
	}, func(ctx context.Context, in *createCellInput) (*cellOutput, error) {
		b := in.Body
		created, err := inv.CreateCell(ctx, inventory.CreateCellInput{
			PLMN:            inventory.PLMN{MCC: b.MCC, MNC: b.MNC, MNCLength: b.MNCLength},
			ENBID:           b.ENBID,
			LocalCellID:     b.LocalCellID,
			TAC:             b.TAC,
			CellName:        b.CellName,
			MMEName:         b.MMEName,
			Latitude:        b.Latitude,
			Longitude:       b.Longitude,
			NominalRadiusM:  b.NominalRadiusM,
			AzimuthDeg:      b.AzimuthDeg,
			BeamwidthDeg:    b.BeamwidthDeg,
			CoverageGeoJSON: b.CoverageGeoJSON,
			GeometryQuality: b.GeometryQuality,
			Source:          b.Source,
			SourceRecordID:  b.SourceRecordID,
			SourceVersion:   b.SourceVersion,
			Active:          b.Active,
		})
		if err != nil {
			switch {
			case errors.Is(err, inventory.ErrInvalidCell):
				return nil, huma.Error400BadRequest(err.Error())
			case errors.Is(err, inventory.ErrCellAlreadyExists):
				return nil, huma.Error409Conflict(err.Error())
			default:
				return nil, huma.Error500InternalServerError("create cell failed")
			}
		}
		return &cellOutput{Body: *created}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-cell-inventory-cell",
		Method:      http.MethodDelete,
		Path:        "/v1/cell-inventory/cells/{cellID}",
		Summary:     "Delete one LTE cell",
		Errors:      []int{http.StatusNotFound, http.StatusConflict},
	}, func(ctx context.Context, in *cellIDInput) (*noContent, error) {
		if err := inv.DeleteCell(ctx, in.CellID); err != nil {
			switch {
			case errors.Is(err, inventory.ErrCellNotFound):
				return nil, huma.Error404NotFound("cell not found")
			case errors.Is(err, inventory.ErrCellHasGeocodes):
				return nil, huma.Error409Conflict(err.Error())
			default:
				return nil, huma.Error500InternalServerError("delete cell failed")
			}
		}
		return &noContent{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "cell-inventory-selection-preview",
		Method:      http.MethodPost,
		Path:        "/v1/cell-inventory/selection-preview",
		Summary:     "Preview LTE cell selection for a CAP-style polygon (preview only; never transmits SBcAP)",
		Errors:      []int{http.StatusBadRequest},
	}, func(ctx context.Context, in *selectionInput) (*selectionOutput, error) {
		result, err := inv.SelectionPreview(ctx, in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &selectionOutput{Body: *result}, nil
	})
}
