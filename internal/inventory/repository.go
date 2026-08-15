package inventory

import (
	"context"
	"io"
	"time"
)

// InventoryRepository is the storage-agnostic contract the inventory Service
// depends on. SQL, CSV parsing, and HTTP concerns must stay out of it so a
// future PostgreSQL/PostGIS implementation can satisfy it unchanged.
type InventoryRepository interface {
	// CreateImport persists the audit record for one import request. It is
	// called once parsing/validation is complete and the final status is
	// already known for validate-only and rejected imports; merge/replace
	// imports that pass validation are created with a pending status and
	// completed by ApplyImport.
	CreateImport(ctx context.Context, imp InventoryImport) error

	// MarkImportFailed records that an in-flight merge/replace import's
	// transactional apply phase failed. It runs as its own statement,
	// separate from the rolled-back transaction.
	MarkImportFailed(ctx context.Context, importID string, completedAt time.Time) error

	// StoreImportErrors persists bounded per-row validation (or apply)
	// errors for an import.
	StoreImportErrors(ctx context.Context, importID string, errs []ValidationError) error

	// ApplyImport transactionally applies a validated merge/replace import:
	// insert/update matching cells, deactivate absent cells for replace,
	// create the resulting inventory_versions row, and mark the import
	// completed - all in one transaction. It must not be called for
	// validate-only imports.
	ApplyImport(ctx context.Context, input ApplyImportInput) (ApplyResult, error)

	// GetImport returns the audit record for one import, or nil if unknown.
	GetImport(ctx context.Context, importID string) (*InventoryImport, error)

	// ListImportErrors returns the bounded validation errors stored for one
	// import.
	ListImportErrors(ctx context.Context, importID string) ([]ValidationError, error)

	// ListCells returns cells matching filter plus the total matching count
	// (ignoring Limit/Offset) for pagination.
	ListCells(ctx context.Context, filter CellFilter) ([]LTECell, int, error)

	// GetCell returns one cell by primary key, or nil if not found.
	GetCell(ctx context.Context, id int64) (*LTECell, error)

	// ExportCells streams matching cells as canonical-format CSV rows to w
	// and returns metadata about the exported snapshot.
	ExportCells(ctx context.Context, filter CellFilter, w io.Writer) (ExportMeta, error)

	// FindBoundingBoxCandidates returns active cells (optionally scoped to a
	// PLMN) whose stored bounding box overlaps bounds, including point-only
	// cells that lack coverage geometry but fall within it. This is the
	// SQLite-side candidate filter ahead of precise Go geometry comparison.
	FindBoundingBoxCandidates(ctx context.Context, bounds Bounds, plmn *PLMN) ([]LTECell, error)

	// CurrentInventoryVersion returns the most recently applied inventory
	// version, or nil if no merge/replace import has ever completed.
	CurrentInventoryVersion(ctx context.Context) (*InventoryVersion, error)

	// CreateCell inserts a single new, already-validated cell. Returns
	// ErrCellAlreadyExists if a cell with the same (mcc, mnc, mncLength,
	// eci) already exists.
	CreateCell(ctx context.Context, c LTECell) (*LTECell, error)

	// DeleteCell removes one cell by ID. Returns ErrCellNotFound if no such
	// cell exists, or ErrCellHasGeocodes if it is still referenced by
	// cell_geocodes.
	DeleteCell(ctx context.Context, id int64) error
}
