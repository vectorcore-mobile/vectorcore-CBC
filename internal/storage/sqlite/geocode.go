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

	"github.com/vectorcore/cbc/internal/geocode"
)

// lookupCellID resolves the internal lte_cells.id for the ECGI identity
// (mcc, mnc, mncLength, eci) - the same identity CSV import/export already
// use for cell inventory, so operators reference cells the same way in both
// features.
func lookupCellID(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, mcc, mnc string, mncLength int, eci uint32) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM lte_cells WHERE mcc=? AND mnc=? AND mnc_length=? AND eci=?`, mcc, mnc, mncLength, eci).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, geocode.ErrCellNotFound
	}
	return id, err
}

// CreateEntry satisfies geocode.Repository.
func (s *Store) CreateEntry(ctx context.Context, mcc, mnc string, mncLength int, eci uint32, codeType geocode.CodeType, code string) (*geocode.Entry, error) {
	cellID, err := lookupCellID(ctx, s.db, mcc, mnc, mncLength, eci)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO cell_geocodes(cell_id,code_type,code,created_at) VALUES(?,?,?,?)`,
		cellID, string(codeType), code, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &geocode.Entry{ID: id, CellID: cellID, ECI: eci, CodeType: codeType, Code: code, CreatedAt: now}, nil
}

// DeleteEntry satisfies geocode.Repository.
func (s *Store) DeleteEntry(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cell_geocodes WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return geocode.ErrEntryNotFound
	}
	return nil
}

const geocodeListColumns = `cg.id,cg.cell_id,lc.eci,COALESCE(lc.cell_name,''),cg.code_type,cg.code,cg.created_at`

func scanGeocodeEntry(row rowScanner) (geocode.Entry, error) {
	var e geocode.Entry
	var codeType, created string
	if err := row.Scan(&e.ID, &e.CellID, &e.ECI, &e.CellName, &codeType, &e.Code, &created); err != nil {
		return e, err
	}
	e.CodeType = geocode.CodeType(codeType)
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return e, err
	}
	e.CreatedAt = t
	return e, nil
}

func geocodeWhereClause(f geocode.Filter) (string, []any) {
	var clauses []string
	var args []any
	if f.CodeType != "" {
		clauses = append(clauses, "cg.code_type=?")
		args = append(args, f.CodeType)
	}
	if f.Code != "" {
		clauses = append(clauses, "cg.code=?")
		args = append(args, f.Code)
	}
	if f.CellID != nil {
		clauses = append(clauses, "cg.cell_id=?")
		args = append(args, *f.CellID)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// ListEntries satisfies geocode.Repository.
func (s *Store) ListEntries(ctx context.Context, f geocode.Filter) ([]geocode.Entry, int, error) {
	where, args := geocodeWhereClause(f)
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cell_geocodes cg JOIN lte_cells lc ON lc.id=cg.cell_id"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, "SELECT "+geocodeListColumns+" FROM cell_geocodes cg JOIN lte_cells lc ON lc.id=cg.cell_id"+where+" ORDER BY cg.id LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []geocode.Entry
	for rows.Next() {
		e, err := scanGeocodeEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// ResolveCells satisfies geocode.Repository - the live alert-targeting
// lookup used by cbs.Preparer via GeocodeResolver. Only active cells are
// returned, matching FindBoundingBoxCandidates' treatment of active.
func (s *Store) ResolveCells(ctx context.Context, codeType, code string) ([]uint32, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lc.eci FROM cell_geocodes cg JOIN lte_cells lc ON lc.id=cg.cell_id WHERE cg.code_type=? AND cg.code=? AND lc.active=1`, codeType, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var eci uint32
		if err := rows.Scan(&eci); err != nil {
			return nil, err
		}
		out = append(out, eci)
	}
	return out, rows.Err()
}

// ApplyGeocodeImport satisfies geocode.Repository - resolves each row's
// cell, applies inserts (and, for Replace, a full wipe first) in one
// transaction. Rows whose cell can't be found are reported back as row
// errors rather than failing the whole import, since one bad row shouldn't
// block the rest.
func (s *Store) ApplyGeocodeImport(ctx context.Context, rowsIn []geocode.PendingRow, mode geocode.ImportMode) (int, int, []geocode.RowError, error) {
	if mode != geocode.Merge && mode != geocode.Replace {
		return 0, 0, nil, fmt.Errorf("invalid import mode %q for ApplyImport", mode)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	defer tx.Rollback()

	deleted := 0
	if mode == geocode.Replace {
		res, err := tx.ExecContext(ctx, `DELETE FROM cell_geocodes`)
		if err != nil {
			return 0, 0, nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, 0, nil, err
		}
		deleted = int(n)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	inserted := 0
	var rowErrors []geocode.RowError
	for i, r := range rowsIn {
		cellID, err := lookupCellID(ctx, tx, r.MCC, r.MNC, r.MNCLength, r.ECI)
		if errors.Is(err, geocode.ErrCellNotFound) {
			rowErrors = append(rowErrors, geocode.RowError{Row: i + 2, Column: "eci", Code: "cell_not_found",
				Message: fmt.Sprintf("no cell matches mcc=%s mnc=%s mnc_length=%d eci=%d", r.MCC, r.MNC, r.MNCLength, r.ECI)})
			continue
		}
		if err != nil {
			return 0, 0, nil, err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO cell_geocodes(cell_id,code_type,code,created_at) VALUES(?,?,?,?)
			ON CONFLICT(cell_id,code_type,code) DO NOTHING`, cellID, string(r.CodeType), r.Code, now)
		if err != nil {
			return 0, 0, nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, 0, nil, err
		}
		inserted += int(n)
	}
	return inserted, deleted, rowErrors, tx.Commit()
}

// CreateCode satisfies geocode.Repository - inserts one Geo Codes registry
// entry. A duplicate (type, code) violates the table's UNIQUE constraint
// and is returned to the caller as a plain error.
func (s *Store) CreateCode(ctx context.Context, codeType, code, description string) (*geocode.Code, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO geo_codes(type,code,description,created_at) VALUES(?,?,?,?)`,
		codeType, code, description, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &geocode.Code{ID: id, Type: codeType, Code: code, Description: description, CreatedAt: now}, nil
}

// DeleteCode satisfies geocode.Repository.
func (s *Store) DeleteCode(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM geo_codes WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return geocode.ErrCodeNotFound
	}
	return nil
}

// ListCodes satisfies geocode.Repository.
func (s *Store) ListCodes(ctx context.Context) ([]geocode.Code, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,type,code,COALESCE(description,''),created_at FROM geo_codes ORDER BY type,code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []geocode.Code
	for rows.Next() {
		var c geocode.Code
		var created string
		if err := rows.Scan(&c.ID, &c.Type, &c.Code, &c.Description, &created); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		c.CreatedAt = t
		out = append(out, c)
	}
	return out, rows.Err()
}

// ExportEntries satisfies geocode.Repository - streams the canonical CSV
// (mcc,mnc,mnc_length,eci,code_type,code), the same identity columns used
// on import.
func (s *Store) ExportEntries(ctx context.Context, f geocode.Filter, w io.Writer) error {
	where, args := geocodeWhereClause(f)
	rows, err := s.db.QueryContext(ctx, "SELECT lc.mcc,lc.mnc,lc.mnc_length,lc.eci,cg.code_type,cg.code FROM cell_geocodes cg JOIN lte_cells lc ON lc.id=cg.cell_id"+where+" ORDER BY cg.id", args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	cw := csv.NewWriter(w)
	if err := cw.Write(geocode.CSVColumns); err != nil {
		return err
	}
	for rows.Next() {
		var mcc, mnc, codeType, code string
		var mncLength int
		var eci uint32
		if err := rows.Scan(&mcc, &mnc, &mncLength, &eci, &codeType, &code); err != nil {
			return err
		}
		if err := cw.Write([]string{mcc, mnc, strconv.Itoa(mncLength), strconv.FormatUint(uint64(eci), 10), codeType, code}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}
