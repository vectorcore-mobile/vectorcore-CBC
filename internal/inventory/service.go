package inventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

// ErrUploadTooLarge is returned by Import when the uploaded file exceeds the
// configured maximum import size. The upload is rejected before any part of
// it is applied.
var ErrUploadTooLarge = errors.New("uploaded file exceeds the configured maximum import size")

// ErrInvalidImportMode is returned by Import when mode is not one of
// validate-only, merge, or replace.
var ErrInvalidImportMode = errors.New("invalid import mode")

const defaultMaxImportSizeBytes = 10 * 1024 * 1024

// Service is the inventory subsystem's only entry point for the HTTP layer.
// It holds no SQL and no CSV-parsing details of its own; both live behind
// InventoryRepository and the ParseCSV/geometry helpers respectively.
type Service struct {
	repo          InventoryRepository
	matcher       SpatialMatcher
	maxImportSize int64
}

// NewService constructs the inventory service. maxImportSize<=0 selects the
// 10 MiB default.
func NewService(repo InventoryRepository, matcher SpatialMatcher, maxImportSize int64) *Service {
	if maxImportSize <= 0 {
		maxImportSize = defaultMaxImportSizeBytes
	}
	return &Service{repo: repo, matcher: matcher, maxImportSize: maxImportSize}
}

// MaxImportSizeBytes returns the configured upload bound so the HTTP layer
// can enforce it before the multipart body is even parsed.
func (s *Service) MaxImportSizeBytes() int64 { return s.maxImportSize }

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Import parses, validates, and (for merge/replace) applies one CSV upload.
// The upload is bounded, hashed, and never trusted as a filesystem path; the
// full sequence - parse, validate, normalize, create the audit record,
// apply-in-one-transaction, commit - matches the handoff's required
// transaction semantics: a structurally invalid import never partially
// modifies lte_cells.
func (s *Service) Import(ctx context.Context, filename string, r io.Reader, mode ImportMode) (*InventoryImport, error) {
	switch mode {
	case ValidateOnly, Merge, Replace:
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidImportMode, mode)
	}

	counter := &countingReader{r: r}
	result := ParseCSV(io.LimitReader(counter, s.maxImportSize+1))
	if counter.n > s.maxImportSize {
		return nil, ErrUploadTooLarge
	}

	safeName := filepath.Base(strings.TrimSpace(filename))
	if safeName == "" || safeName == "." || safeName == string(filepath.Separator) {
		safeName = "upload.csv"
	}

	now := time.Now().UTC()
	imp := InventoryImport{
		ID:             NewID("imp"),
		SourceFilename: safeName,
		SourceSHA256:   result.SHA256,
		Mode:           mode,
		RowsReceived:   result.Rows,
		RowsValid:      len(result.Cells),
		RowsRejected:   result.Rows - len(result.Cells),
		CreatedAt:      now,
	}

	fatal := len(result.Errors) > 0
	switch {
	case mode == ValidateOnly:
		imp.Status = ImportStatusValidated
		completed := now
		imp.CompletedAt = &completed
	case fatal:
		imp.Status = ImportStatusFailed
		completed := now
		imp.CompletedAt = &completed
	default:
		imp.Status = ImportStatusPending
	}

	if err := s.repo.CreateImport(ctx, imp); err != nil {
		return nil, fmt.Errorf("record import: %w", err)
	}
	if len(result.Errors) > 0 {
		if err := s.repo.StoreImportErrors(ctx, imp.ID, result.Errors); err != nil {
			return nil, fmt.Errorf("record import errors: %w", err)
		}
	}

	if mode == ValidateOnly || fatal {
		return s.repo.GetImport(ctx, imp.ID)
	}

	versionName := "import-" + imp.ID
	if _, err := s.repo.ApplyImport(ctx, ApplyImportInput{
		ImportID:       imp.ID,
		Mode:           mode,
		Cells:          result.Cells,
		VersionName:    versionName,
		SourceFilename: safeName,
		SourceSHA256:   result.SHA256,
	}); err != nil {
		slog.Error("cell inventory apply failed", "import_id", imp.ID, "error", err)
		failure := []ValidationError{{Code: "apply_failed", Message: "applying validated rows to the inventory failed"}}
		if serr := s.repo.StoreImportErrors(ctx, imp.ID, failure); serr != nil {
			slog.Error("cell inventory failed to record apply failure", "import_id", imp.ID, "error", serr)
		}
		if merr := s.repo.MarkImportFailed(ctx, imp.ID, time.Now().UTC()); merr != nil {
			slog.Error("cell inventory failed to mark import failed", "import_id", imp.ID, "error", merr)
		}
		return nil, fmt.Errorf("apply import: %w", err)
	}
	return s.repo.GetImport(ctx, imp.ID)
}

func (s *Service) GetImport(ctx context.Context, importID string) (*InventoryImport, error) {
	return s.repo.GetImport(ctx, importID)
}

func (s *Service) ListImportErrors(ctx context.Context, importID string) ([]ValidationError, error) {
	return s.repo.ListImportErrors(ctx, importID)
}

func (s *Service) ListCells(ctx context.Context, filter CellFilter) ([]LTECell, int, error) {
	return s.repo.ListCells(ctx, filter)
}

func (s *Service) GetCell(ctx context.Context, id int64) (*LTECell, error) {
	return s.repo.GetCell(ctx, id)
}

func (s *Service) Export(ctx context.Context, filter CellFilter, w io.Writer) (ExportMeta, error) {
	return s.repo.ExportCells(ctx, filter, w)
}

// SelectionPreview validates the requested area, finds SQLite bounding-box
// candidates, evaluates each precisely in Go, and groups the result by MME
// and TAC. It never transmits SBcAP; this is a preview only.
func (s *Service) SelectionPreview(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	policy := req.Policy
	if policy == "" {
		policy = PolicyConservativeIntersection
	}
	if policy != PolicyConservativeIntersection {
		return nil, fmt.Errorf("unsupported selection policy %q", policy)
	}
	area, err := ParseGeometry(req.Area)
	if err != nil {
		return nil, fmt.Errorf("invalid request area: %w", err)
	}

	var plmnFilter *PLMN
	if req.PLMN.MCC != "" || req.PLMN.MNC != "" {
		p := req.PLMN
		plmnFilter = &p
	}
	candidates, err := s.repo.FindBoundingBoxCandidates(ctx, area.Bounds(), plmnFilter)
	if err != nil {
		return nil, fmt.Errorf("find candidates: %w", err)
	}

	selected := make([]SelectedCell, 0, len(candidates))
	for _, c := range candidates {
		ok, reason := s.matcher.Evaluate(area, c)
		if !ok {
			continue
		}
		selected = append(selected, SelectedCell{
			ID:              c.ID,
			ECI:             c.ECI,
			TAC:             c.TAC,
			ENBID:           c.ENBID,
			MMEName:         c.MMEName,
			GeometryQuality: c.GeometryQuality,
			SelectionReason: reason,
		})
	}

	warnings := []string{}
	versionName := "unversioned"
	if v, verr := s.repo.CurrentInventoryVersion(ctx); verr != nil {
		warnings = append(warnings, "unable to determine current inventory version")
	} else if v != nil {
		versionName = v.VersionName
	} else {
		warnings = append(warnings, "no inventory version has been applied yet")
	}

	return &SelectionResult{
		InventoryVersion: versionName,
		Policy:           policy,
		CandidateCount:   len(candidates),
		SelectedCount:    len(selected),
		Cells:            selected,
		MMEPlans:         BuildMMEPlans(req.PLMN, selected),
		Warnings:         warnings,
	}, nil
}
