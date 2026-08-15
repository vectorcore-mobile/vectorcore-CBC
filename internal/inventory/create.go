package inventory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidCell is returned by Service.CreateCell when the input fails
// validation; the message lists every field problem found (see cellFrom -
// the same per-row validation csv.go's CSV import already uses).
var ErrInvalidCell = errors.New("invalid cell")

// ErrCellAlreadyExists is returned by Service.CreateCell when a cell with
// the same (mcc, mnc, mncLength, eci) already exists.
var ErrCellAlreadyExists = errors.New("a cell with this PLMN and ECI already exists")

// ErrCellNotFound is returned by Service.DeleteCell when no cell matches
// the given ID.
var ErrCellNotFound = errors.New("cell not found")

// ErrCellHasGeocodes is returned by Service.DeleteCell when the cell is
// still referenced by one or more geo code mappings - deletion is blocked
// rather than silently cascading, so removing a cell never silently
// orphans or deletes geo code data.
var ErrCellHasGeocodes = errors.New("cell has geo code mappings and cannot be deleted - remove its geo code mappings first")

// CreateCellInput carries a hand-entered cell (from the web UI's Add Cell
// popup, or any other direct API caller) - every LTECell field except ID,
// ECI, Bounds, and the timestamps, which are all computed or assigned by
// CreateCell/the repository rather than trusted from the caller.
type CreateCellInput struct {
	PLMN            PLMN
	ENBID           uint32
	LocalCellID     uint8
	TAC             uint16
	CellName        string
	MMEName         string
	Latitude        *float64
	Longitude       *float64
	NominalRadiusM  *float64
	AzimuthDeg      *float64
	BeamwidthDeg    *float64
	CoverageGeoJSON string
	GeometryQuality string
	Source          string
	SourceRecordID  string
	SourceVersion   string
	Active          bool
}

func floatOrEmpty(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

// validateNewCell applies the exact same per-field rules csv.go's cellFrom
// already enforces for CSV import, by building the same get(column string)
// string adapter cellFrom expects - reusing that logic outright rather
// than duplicating its range/enum checks in a second validator that could
// drift out of sync with it. ECI is always computed from ENBID/
// LocalCellID (never taken from the caller), so the ECI/eNB/local-cell
// consistency check cellFrom performs always trivially passes here - this
// removes the mismatch-error class entirely instead of surfacing it.
func validateNewCell(in CreateCellInput) (LTECell, []fieldError) {
	eci := in.ENBID<<8 | uint32(in.LocalCellID)
	get := func(k string) string {
		switch k {
		case "mcc":
			return in.PLMN.MCC
		case "mnc":
			return in.PLMN.MNC
		case "mnc_length":
			return strconv.Itoa(in.PLMN.MNCLength)
		case "eci":
			return strconv.FormatUint(uint64(eci), 10)
		case "enb_id":
			return strconv.FormatUint(uint64(in.ENBID), 10)
		case "local_cell_id":
			return strconv.Itoa(int(in.LocalCellID))
		case "tac":
			return strconv.Itoa(int(in.TAC))
		case "cell_name":
			return in.CellName
		case "mme_name":
			return in.MMEName
		case "latitude":
			return floatOrEmpty(in.Latitude)
		case "longitude":
			return floatOrEmpty(in.Longitude)
		case "nominal_radius_m":
			return floatOrEmpty(in.NominalRadiusM)
		case "azimuth_deg":
			return floatOrEmpty(in.AzimuthDeg)
		case "beamwidth_deg":
			return floatOrEmpty(in.BeamwidthDeg)
		case "geometry_quality":
			return in.GeometryQuality
		case "source":
			return in.Source
		case "source_record_id":
			return in.SourceRecordID
		case "source_version":
			return in.SourceVersion
		case "active":
			return strconv.FormatBool(in.Active)
		case "coverage_geojson":
			return in.CoverageGeoJSON
		default:
			return ""
		}
	}
	return cellFrom(get)
}

// CreateCell validates in and inserts a single new cell. Existing
// CSV-imported cells are untouched - this is purely additive.
func (s *Service) CreateCell(ctx context.Context, in CreateCellInput) (*LTECell, error) {
	c, fieldErrs := validateNewCell(in)
	if len(fieldErrs) > 0 {
		msgs := make([]string, len(fieldErrs))
		for i, fe := range fieldErrs {
			msgs[i] = fmt.Sprintf("%s: %s", fe.Column, fe.Message)
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidCell, strings.Join(msgs, "; "))
	}
	return s.repo.CreateCell(ctx, c)
}

// DeleteCell removes one cell by ID. Blocks (ErrCellHasGeocodes) rather
// than cascading if the cell still has geo code mappings referencing it.
func (s *Service) DeleteCell(ctx context.Context, id int64) error {
	return s.repo.DeleteCell(ctx, id)
}
