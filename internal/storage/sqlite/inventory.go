package sqlite

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/vectorcore/cbc/internal/inventory"
)

// cellColumns is the fixed SELECT list scanCell expects, shared by ListCells,
// GetCell, ExportCells, and FindBoundingBoxCandidates.
const cellColumns = `id,mcc,mnc,mnc_length,eci,enb_id,local_cell_id,tac,
	COALESCE(cell_name,''),COALESCE(mme_name,''),
	latitude,longitude,nominal_radius_m,azimuth_deg,beamwidth_deg,COALESCE(coverage_geojson,''),
	bbox_min_lat,bbox_min_lon,bbox_max_lat,bbox_max_lon,
	geometry_quality,COALESCE(source,''),COALESCE(source_record_id,''),COALESCE(source_version,''),
	active,created_at,updated_at`

func cellKey(p inventory.PLMN, eci uint32) string {
	return fmt.Sprintf("%s/%s/%d/%d", p.MCC, p.MNC, p.MNCLength, eci)
}

type rowScanner interface{ Scan(dest ...any) error }

func scanCell(row rowScanner) (inventory.LTECell, error) {
	var c inventory.LTECell
	var lat, lon, radius, azimuth, beam, bMinLat, bMinLon, bMaxLat, bMaxLon sql.NullFloat64
	var created, updated string
	if err := row.Scan(&c.ID, &c.PLMN.MCC, &c.PLMN.MNC, &c.PLMN.MNCLength, &c.ECI, &c.ENBID, &c.LocalCellID, &c.TAC,
		&c.CellName, &c.MMEName,
		&lat, &lon, &radius, &azimuth, &beam, &c.CoverageGeoJSON,
		&bMinLat, &bMinLon, &bMaxLat, &bMaxLon,
		&c.GeometryQuality, &c.Source, &c.SourceRecordID, &c.SourceVersion,
		&c.Active, &created, &updated); err != nil {
		return c, err
	}
	if lat.Valid {
		v := lat.Float64
		c.Latitude = &v
	}
	if lon.Valid {
		v := lon.Float64
		c.Longitude = &v
	}
	if radius.Valid {
		v := radius.Float64
		c.NominalRadiusM = &v
	}
	if azimuth.Valid {
		v := azimuth.Float64
		c.AzimuthDeg = &v
	}
	if beam.Valid {
		v := beam.Float64
		c.BeamwidthDeg = &v
	}
	if bMinLat.Valid && bMinLon.Valid && bMaxLat.Valid && bMaxLon.Valid {
		c.Bounds = &inventory.Bounds{MinLatitude: bMinLat.Float64, MinLongitude: bMinLon.Float64, MaxLatitude: bMaxLat.Float64, MaxLongitude: bMaxLon.Float64}
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return c, err
	}
	c.CreatedAt = t
	if t, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return c, err
	}
	c.UpdatedAt = t
	return c, nil
}

func cellWhereClause(filter inventory.CellFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.Active != nil {
		clauses = append(clauses, "active=?")
		args = append(args, *filter.Active)
	}
	if filter.MCC != "" {
		clauses = append(clauses, "mcc=?")
		args = append(args, filter.MCC)
	}
	if filter.MNC != "" {
		clauses = append(clauses, "mnc=?")
		args = append(args, filter.MNC)
	}
	if filter.TAC != nil {
		clauses = append(clauses, "tac=?")
		args = append(args, *filter.TAC)
	}
	if filter.MMEName != "" {
		clauses = append(clauses, "mme_name=?")
		args = append(args, filter.MMEName)
	}
	if filter.ENBID != nil {
		clauses = append(clauses, "enb_id=?")
		args = append(args, *filter.ENBID)
	}
	if filter.ECI != nil {
		clauses = append(clauses, "eci=?")
		args = append(args, *filter.ECI)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// CreateImport persists the audit record for one import request.
func (s *Store) CreateImport(ctx context.Context, imp inventory.InventoryImport) error {
	var versionID, completedAt any
	if imp.InventoryVersionID != "" {
		versionID = imp.InventoryVersionID
	}
	if imp.CompletedAt != nil {
		completedAt = imp.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO inventory_imports(
		id,inventory_version_id,source_filename,source_sha256,mode,status,
		rows_received,rows_valid,rows_rejected,inserted_count,updated_count,deactivated_count,warning_count,
		created_at,completed_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		imp.ID, versionID, imp.SourceFilename, imp.SourceSHA256, string(imp.Mode), string(imp.Status),
		imp.RowsReceived, imp.RowsValid, imp.RowsRejected, imp.InsertedCount, imp.UpdatedCount, imp.DeactivatedCount, imp.WarningCount,
		imp.CreatedAt.UTC().Format(time.RFC3339Nano), completedAt)
	return err
}

// MarkImportFailed records that a merge/replace import's apply phase failed,
// as its own statement outside the rolled-back transaction.
func (s *Store) MarkImportFailed(ctx context.Context, importID string, completedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE inventory_imports SET status=?,completed_at=? WHERE id=?`,
		string(inventory.ImportStatusFailed), completedAt.UTC().Format(time.RFC3339Nano), importID)
	return err
}

// StoreImportErrors persists the bounded validation (or apply-failure)
// errors for an import.
func (s *Store) StoreImportErrors(ctx context.Context, importID string, errs []inventory.ValidationError) error {
	if len(errs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO inventory_import_errors(import_id,row_number,column_name,error_code,error_message) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range errs {
		var row, col any
		if e.Row > 0 {
			row = e.Row
		}
		if e.Column != "" {
			col = e.Column
		}
		if _, err := stmt.ExecContext(ctx, importID, row, col, e.Code, e.Message); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyImport transactionally applies a validated merge/replace import:
// insert/update matching cells, deactivate absent cells for replace, create
// the resulting inventory_versions row, and mark the import completed - all
// in one transaction so a failure leaves the live inventory untouched.
func (s *Store) ApplyImport(ctx context.Context, input inventory.ApplyImportInput) (inventory.ApplyResult, error) {
	var out inventory.ApplyResult
	if input.Mode != inventory.Merge && input.Mode != inventory.Replace {
		return out, fmt.Errorf("invalid import mode %q for ApplyImport", input.Mode)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	seen := map[string]bool{}
	for _, c := range input.Cells {
		seen[cellKey(c.PLMN, c.ECI)] = true
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM lte_cells WHERE mcc=? AND mnc=? AND mnc_length=? AND eci=?`,
			c.PLMN.MCC, c.PLMN.MNC, c.PLMN.MNCLength, c.ECI).Scan(&exists); err != nil {
			return out, err
		}
		var lat, lon, radius, azimuth, beam, bMinLat, bMinLon, bMaxLat, bMaxLon, geojson any
		if c.Latitude != nil {
			lat = *c.Latitude
		}
		if c.Longitude != nil {
			lon = *c.Longitude
		}
		if c.NominalRadiusM != nil {
			radius = *c.NominalRadiusM
		}
		if c.AzimuthDeg != nil {
			azimuth = *c.AzimuthDeg
		}
		if c.BeamwidthDeg != nil {
			beam = *c.BeamwidthDeg
		}
		if c.Bounds != nil {
			bMinLat, bMinLon, bMaxLat, bMaxLon = c.Bounds.MinLatitude, c.Bounds.MinLongitude, c.Bounds.MaxLatitude, c.Bounds.MaxLongitude
		}
		if c.CoverageGeoJSON != "" {
			geojson = c.CoverageGeoJSON
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO lte_cells(
			mcc,mnc,mnc_length,eci,enb_id,local_cell_id,tac,cell_name,mme_name,
			latitude,longitude,nominal_radius_m,azimuth_deg,beamwidth_deg,coverage_geojson,
			bbox_min_lat,bbox_min_lon,bbox_max_lat,bbox_max_lon,
			geometry_quality,source,source_record_id,source_version,active,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(mcc,mnc,mnc_length,eci) DO UPDATE SET
			enb_id=excluded.enb_id,local_cell_id=excluded.local_cell_id,tac=excluded.tac,
			cell_name=excluded.cell_name,mme_name=excluded.mme_name,
			latitude=excluded.latitude,longitude=excluded.longitude,nominal_radius_m=excluded.nominal_radius_m,
			azimuth_deg=excluded.azimuth_deg,beamwidth_deg=excluded.beamwidth_deg,coverage_geojson=excluded.coverage_geojson,
			bbox_min_lat=excluded.bbox_min_lat,bbox_min_lon=excluded.bbox_min_lon,bbox_max_lat=excluded.bbox_max_lat,bbox_max_lon=excluded.bbox_max_lon,
			geometry_quality=excluded.geometry_quality,source=excluded.source,source_record_id=excluded.source_record_id,source_version=excluded.source_version,
			active=excluded.active,updated_at=excluded.updated_at`,
			c.PLMN.MCC, c.PLMN.MNC, c.PLMN.MNCLength, c.ECI, c.ENBID, c.LocalCellID, c.TAC, c.CellName, c.MMEName,
			lat, lon, radius, azimuth, beam, geojson,
			bMinLat, bMinLon, bMaxLat, bMaxLon,
			c.GeometryQuality, c.Source, c.SourceRecordID, c.SourceVersion, c.Active, now, now)
		if err != nil {
			return out, err
		}
		if exists == 0 {
			out.Inserted++
		} else {
			out.Updated++
		}
	}

	if input.Mode == inventory.Replace {
		rows, err := tx.QueryContext(ctx, `SELECT mcc,mnc,mnc_length,eci FROM lte_cells WHERE active=1`)
		if err != nil {
			return out, err
		}
		type key struct {
			mcc, mnc string
			mncLen   int
			eci      uint32
		}
		var toDeactivate []key
		for rows.Next() {
			var k key
			if err := rows.Scan(&k.mcc, &k.mnc, &k.mncLen, &k.eci); err != nil {
				rows.Close()
				return out, err
			}
			if !seen[cellKey(inventory.PLMN{MCC: k.mcc, MNC: k.mnc, MNCLength: k.mncLen}, k.eci)] {
				toDeactivate = append(toDeactivate, k)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return out, err
		}
		rows.Close()
		for _, k := range toDeactivate {
			if _, err := tx.ExecContext(ctx, `UPDATE lte_cells SET active=0,updated_at=? WHERE mcc=? AND mnc=? AND mnc_length=? AND eci=?`,
				now, k.mcc, k.mnc, k.mncLen, k.eci); err != nil {
				return out, err
			}
			out.Deactivated++
		}
	}

	versionID := inventory.NewID("ver")
	if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_versions(id,version_name,source_filename,source_sha256,import_mode,record_count,status,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		versionID, input.VersionName, input.SourceFilename, input.SourceSHA256, string(input.Mode), len(input.Cells), "active", now); err != nil {
		return out, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inventory_imports SET status=?,inventory_version_id=?,inserted_count=?,updated_count=?,deactivated_count=?,completed_at=? WHERE id=?`,
		string(inventory.ImportStatusCompleted), versionID, out.Inserted, out.Updated, out.Deactivated, now, input.ImportID); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// GetImport returns one import's audit record, or nil if unknown.
func (s *Store) GetImport(ctx context.Context, importID string) (*inventory.InventoryImport, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,COALESCE(inventory_version_id,''),source_filename,source_sha256,mode,status,
		rows_received,rows_valid,rows_rejected,inserted_count,updated_count,deactivated_count,warning_count,created_at,completed_at
		FROM inventory_imports WHERE id=?`, importID)
	var imp inventory.InventoryImport
	var mode, status, created string
	var completed sql.NullString
	if err := row.Scan(&imp.ID, &imp.InventoryVersionID, &imp.SourceFilename, &imp.SourceSHA256, &mode, &status,
		&imp.RowsReceived, &imp.RowsValid, &imp.RowsRejected, &imp.InsertedCount, &imp.UpdatedCount, &imp.DeactivatedCount, &imp.WarningCount,
		&created, &completed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	imp.Mode = inventory.ImportMode(mode)
	imp.Status = inventory.ImportStatus(status)
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	imp.CreatedAt = t
	if completed.Valid {
		ct, err := time.Parse(time.RFC3339Nano, completed.String)
		if err != nil {
			return nil, err
		}
		imp.CompletedAt = &ct
	}
	return &imp, nil
}

// ListImportErrors returns the bounded validation errors stored for one
// import, in insertion order.
func (s *Store) ListImportErrors(ctx context.Context, importID string) ([]inventory.ValidationError, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(row_number,0),COALESCE(column_name,''),error_code,error_message FROM inventory_import_errors WHERE import_id=? ORDER BY id`, importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []inventory.ValidationError
	for rows.Next() {
		var e inventory.ValidationError
		if err := rows.Scan(&e.Row, &e.Column, &e.Code, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListCells returns cells matching filter plus the total matching count.
func (s *Store) ListCells(ctx context.Context, filter inventory.CellFilter) ([]inventory.LTECell, int, error) {
	where, args := cellWhereClause(filter)
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM lte_cells"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, "SELECT "+cellColumns+" FROM lte_cells"+where+" ORDER BY id LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var cells []inventory.LTECell
	for rows.Next() {
		c, err := scanCell(rows)
		if err != nil {
			return nil, 0, err
		}
		cells = append(cells, c)
	}
	return cells, total, rows.Err()
}

// GetCell returns one cell by primary key, or nil if not found.
func (s *Store) GetCell(ctx context.Context, id int64) (*inventory.LTECell, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+cellColumns+" FROM lte_cells WHERE id=?", id)
	c, err := scanCell(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ExportCells streams matching cells as canonical-format CSV rows to w.
func (s *Store) ExportCells(ctx context.Context, filter inventory.CellFilter, w io.Writer) (inventory.ExportMeta, error) {
	where, args := cellWhereClause(filter)
	rows, err := s.db.QueryContext(ctx, "SELECT "+cellColumns+" FROM lte_cells"+where+" ORDER BY id", args...)
	if err != nil {
		return inventory.ExportMeta{}, err
	}
	defer rows.Close()

	cw := csv.NewWriter(w)
	if err := cw.Write(inventory.CSVColumns); err != nil {
		return inventory.ExportMeta{}, err
	}
	count := 0
	for rows.Next() {
		c, err := scanCell(rows)
		if err != nil {
			return inventory.ExportMeta{}, err
		}
		if err := cw.Write(cellToRow(c)); err != nil {
			return inventory.ExportMeta{}, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return inventory.ExportMeta{}, err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return inventory.ExportMeta{}, err
	}

	meta := inventory.ExportMeta{ExportedAt: time.Now().UTC(), RecordCount: count, VersionName: "unversioned"}
	if v, err := s.CurrentInventoryVersion(ctx); err == nil && v != nil {
		meta.VersionName = v.VersionName
	}
	return meta, nil
}

func cellToRow(c inventory.LTECell) []string {
	f := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'g', -1, 64)
	}
	return []string{
		c.PLMN.MCC, c.PLMN.MNC, strconv.Itoa(c.PLMN.MNCLength),
		strconv.FormatUint(uint64(c.ECI), 10), strconv.FormatUint(uint64(c.ENBID), 10), strconv.Itoa(int(c.LocalCellID)), strconv.Itoa(int(c.TAC)),
		c.CellName, c.MMEName,
		f(c.Latitude), f(c.Longitude), f(c.NominalRadiusM), f(c.AzimuthDeg), f(c.BeamwidthDeg),
		c.GeometryQuality, c.Source, c.SourceRecordID, c.SourceVersion,
		strconv.FormatBool(c.Active), c.CoverageGeoJSON,
	}
}

// FindBoundingBoxCandidates returns active cells (optionally scoped to a
// PLMN) whose stored bounding box overlaps bounds, plus point-only cells
// (no coverage polygon) whose lat/lon falls within bounds - the SQLite-side
// candidate filter ahead of precise Go geometry comparison.
func (s *Store) FindBoundingBoxCandidates(ctx context.Context, bounds inventory.Bounds, plmn *inventory.PLMN) ([]inventory.LTECell, error) {
	clauses := []string{"active=1"}
	var args []any
	if plmn != nil {
		clauses = append(clauses, "mcc=? AND mnc=?")
		args = append(args, plmn.MCC, plmn.MNC)
	}
	clauses = append(clauses, `(
		(bbox_min_lon IS NOT NULL AND bbox_min_lon<=? AND bbox_max_lon>=? AND bbox_min_lat<=? AND bbox_max_lat>=?)
		OR
		(bbox_min_lon IS NULL AND latitude IS NOT NULL AND longitude IS NOT NULL AND longitude BETWEEN ? AND ? AND latitude BETWEEN ? AND ?)
	)`)
	args = append(args, bounds.MaxLongitude, bounds.MinLongitude, bounds.MaxLatitude, bounds.MinLatitude,
		bounds.MinLongitude, bounds.MaxLongitude, bounds.MinLatitude, bounds.MaxLatitude)
	rows, err := s.db.QueryContext(ctx, "SELECT "+cellColumns+" FROM lte_cells WHERE "+strings.Join(clauses, " AND "), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []inventory.LTECell
	for rows.Next() {
		c, err := scanCell(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCell satisfies inventory.InventoryRepository - a single-row insert
// (not an upsert like ApplyImport), so an existing cell is never silently
// overwritten by a create call.
func (s *Store) CreateCell(ctx context.Context, c inventory.LTECell) (*inventory.LTECell, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var lat, lon, radius, azimuth, beam, bMinLat, bMinLon, bMaxLat, bMaxLon, geojson any
	if c.Latitude != nil {
		lat = *c.Latitude
	}
	if c.Longitude != nil {
		lon = *c.Longitude
	}
	if c.NominalRadiusM != nil {
		radius = *c.NominalRadiusM
	}
	if c.AzimuthDeg != nil {
		azimuth = *c.AzimuthDeg
	}
	if c.BeamwidthDeg != nil {
		beam = *c.BeamwidthDeg
	}
	if c.Bounds != nil {
		bMinLat, bMinLon, bMaxLat, bMaxLon = c.Bounds.MinLatitude, c.Bounds.MinLongitude, c.Bounds.MaxLatitude, c.Bounds.MaxLongitude
	}
	if c.CoverageGeoJSON != "" {
		geojson = c.CoverageGeoJSON
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO lte_cells(
		mcc,mnc,mnc_length,eci,enb_id,local_cell_id,tac,cell_name,mme_name,
		latitude,longitude,nominal_radius_m,azimuth_deg,beamwidth_deg,coverage_geojson,
		bbox_min_lat,bbox_min_lon,bbox_max_lat,bbox_max_lon,
		geometry_quality,source,source_record_id,source_version,active,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.PLMN.MCC, c.PLMN.MNC, c.PLMN.MNCLength, c.ECI, c.ENBID, c.LocalCellID, c.TAC, c.CellName, c.MMEName,
		lat, lon, radius, azimuth, beam, geojson,
		bMinLat, bMinLon, bMaxLat, bMaxLon,
		c.GeometryQuality, c.Source, c.SourceRecordID, c.SourceVersion, c.Active, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, inventory.ErrCellAlreadyExists
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCell(ctx, id)
}

// DeleteCell satisfies inventory.InventoryRepository - blocks (rather than
// cascades) when cell_geocodes still references this cell, per the
// operator's call to keep deletion from silently orphaning geo code data.
func (s *Store) DeleteCell(ctx context.Context, id int64) error {
	var refs int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cell_geocodes WHERE cell_id=?`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return inventory.ErrCellHasGeocodes
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM lte_cells WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return inventory.ErrCellNotFound
	}
	return nil
}

// CurrentInventoryVersion returns the most recently applied inventory
// version, or nil if no merge/replace import has ever completed.
func (s *Store) CurrentInventoryVersion(ctx context.Context) (*inventory.InventoryVersion, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,version_name,COALESCE(source_filename,''),source_sha256,import_mode,record_count,status,created_at
		FROM inventory_versions ORDER BY created_at DESC, id DESC LIMIT 1`)
	var v inventory.InventoryVersion
	var mode, created string
	if err := row.Scan(&v.ID, &v.VersionName, &v.SourceFilename, &v.SourceSHA256, &mode, &v.RecordCount, &v.Status, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	v.ImportMode = inventory.ImportMode(mode)
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	v.CreatedAt = t
	return &v, nil
}
