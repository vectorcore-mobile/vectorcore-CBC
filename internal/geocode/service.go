package geocode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrInvalidImportMode is returned by Import when mode is not one of
// validate-only, merge, or replace.
var ErrInvalidImportMode = errors.New("invalid import mode")

// ErrCellNotFound is returned by Create when no lte_cells row matches the
// given PLMN+ECI.
var ErrCellNotFound = errors.New("cell not found")

// ErrEntryNotFound is returned by Delete when no row matches the given ID.
var ErrEntryNotFound = errors.New("geocode entry not found")

// ErrCodeNotFound is returned by DeleteCode when no registry row matches
// the given ID.
var ErrCodeNotFound = errors.New("geo code not found")

// Repository is the storage contract Service depends on. *sqlite.Store
// satisfies this directly.
type Repository interface {
	// CreateEntry looks up the cell identified by (mcc, mnc, mncLength, eci)
	// and inserts one (cell, code) mapping row. Returns ErrCellNotFound if no
	// such cell exists.
	CreateEntry(ctx context.Context, mcc, mnc string, mncLength int, eci uint32, codeType CodeType, code string) (*Entry, error)
	DeleteEntry(ctx context.Context, id int64) error
	ListEntries(ctx context.Context, f Filter) ([]Entry, int, error)
	// ResolveCells returns the ECIs of every active cell tagged with
	// (codeType, code). Zero matches is not an error - it means the code
	// isn't in this operator's footprint.
	ResolveCells(ctx context.Context, codeType, code string) ([]uint32, error)
	// ApplyGeocodeImport resolves each row's cell by (mcc, mnc, mncLength,
	// eci) and applies it per mode, all in one transaction. Rows whose cell
	// can't be found are reported back as ValidationErrors rather than
	// failing the whole import.
	ApplyGeocodeImport(ctx context.Context, rows []PendingRow, mode ImportMode) (inserted, deleted int, rowErrors []RowError, err error)
	ExportEntries(ctx context.Context, f Filter, w io.Writer) error

	// CreateCode inserts one Geo Codes registry entry.
	CreateCode(ctx context.Context, codeType, code, description string) (*Code, error)
	DeleteCode(ctx context.Context, id int64) error
	ListCodes(ctx context.Context) ([]Code, error)
}

// Service is the geocode subsystem's only entry point for the HTTP layer and
// for internal/cbs's live alert-targeting path (via ResolveCells).
type Service struct{ repo Repository }

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) Create(ctx context.Context, mcc, mnc string, mncLength int, eci uint32, codeType CodeType, code string) (*Entry, error) {
	return s.repo.CreateEntry(ctx, mcc, mnc, mncLength, eci, codeType, code)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.DeleteEntry(ctx, id)
}

func (s *Service) List(ctx context.Context, f Filter) ([]Entry, int, error) {
	return s.repo.ListEntries(ctx, f)
}

// ResolveCells satisfies cbs.GeocodeResolver.
func (s *Service) ResolveCells(ctx context.Context, codeType, code string) ([]uint32, error) {
	return s.repo.ResolveCells(ctx, codeType, code)
}

// Import parses and (for merge/replace) applies one CSV upload. Unlike
// internal/inventory.Import, this has no persisted audit-log row - the
// result is returned synchronously and not stored, matching this table's
// small, hand-curated scope.
func (s *Service) Import(ctx context.Context, r io.Reader, mode ImportMode) (*ImportResult, error) {
	switch mode {
	case ValidateOnly, Merge, Replace:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidImportMode, mode)
	}

	parsed := ParseCSV(r)
	result := &ImportResult{
		RowsReceived: parsed.RowCount,
		RowsValid:    len(parsed.Rows),
		RowsRejected: parsed.RowCount - len(parsed.Rows),
		Errors:       parsed.Errors,
	}
	if mode == ValidateOnly || len(parsed.Errors) > 0 {
		return result, nil
	}

	inserted, deleted, rowErrors, err := s.repo.ApplyGeocodeImport(ctx, parsed.Rows, mode)
	if err != nil {
		return nil, fmt.Errorf("apply import: %w", err)
	}
	result.Inserted = inserted
	result.Deleted = deleted
	result.Errors = append(result.Errors, rowErrors...)
	result.RowsRejected += len(rowErrors)
	result.RowsValid -= len(rowErrors)
	return result, nil
}

func (s *Service) Export(ctx context.Context, f Filter, w io.Writer) error {
	return s.repo.ExportEntries(ctx, f, w)
}

// ErrCodeRequired is returned by CreateCode when type or code is blank.
var ErrCodeRequired = errors.New("type and code are required")

// CreateCode adds one Geo Codes registry entry. codeType is uppercased and
// trimmed server-side - the UI is expected to force this too, but the
// registry is the single source of truth other mappings draw from, so it's
// validated here rather than trusted from the caller.
func (s *Service) CreateCode(ctx context.Context, codeType, code, description string) (*Code, error) {
	codeType = strings.ToUpper(strings.TrimSpace(codeType))
	code = strings.TrimSpace(code)
	if codeType == "" || code == "" {
		return nil, ErrCodeRequired
	}
	return s.repo.CreateCode(ctx, codeType, code, strings.TrimSpace(description))
}

func (s *Service) DeleteCode(ctx context.Context, id int64) error {
	return s.repo.DeleteCode(ctx, id)
}

func (s *Service) ListCodes(ctx context.Context) ([]Code, error) {
	return s.repo.ListCodes(ctx)
}
