package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/storage"
)

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string, busy time.Duration) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite: database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=%d", path, busy.Milliseconds()))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// dropCellGeocodesCodeTypeCheck rebuilds cell_geocodes if its on-disk schema
// still carries the old CHECK(code_type IN ('SAME','UGC')) constraint -
// SQLite bakes CHECK into a table's stored schema, so a plain "CREATE TABLE
// IF NOT EXISTS" with the constraint removed is a no-op against a database
// created before geo codes were generalized beyond SAME/UGC. A no-op
// rebuild (table already missing, or already migrated) is cheap and safe to
// run on every startup.
func dropCellGeocodesCodeTypeCheck(ctx context.Context, db *sql.DB) error {
	var createSQL sql.NullString
	err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='cell_geocodes'`).Scan(&createSQL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !createSQL.Valid || !strings.Contains(createSQL.String, "CHECK") {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE cell_geocodes_migrated (id INTEGER PRIMARY KEY AUTOINCREMENT,cell_id INTEGER NOT NULL REFERENCES lte_cells(id),code_type TEXT NOT NULL,code TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(cell_id,code_type,code))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cell_geocodes_migrated(id,cell_id,code_type,code,created_at) SELECT id,cell_id,code_type,code,created_at FROM cell_geocodes`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE cell_geocodes`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE cell_geocodes_migrated RENAME TO cell_geocodes`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Migrate(ctx context.Context) error {
	if err := dropCellGeocodesCodeTypeCheck(ctx, s.db); err != nil {
		return fmt.Errorf("migrate cell_geocodes: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS alerts (
		identifier TEXT PRIMARY KEY, cap_json BLOB NOT NULL, state TEXT NOT NULL,
		received_at TEXT NOT NULL, expires_at TEXT
	); CREATE INDEX IF NOT EXISTS alerts_state_expiry ON alerts(state, expires_at);
	CREATE TABLE IF NOT EXISTS audit_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL, type TEXT NOT NULL,
		alert_id TEXT, detail TEXT NOT NULL
	); CREATE INDEX IF NOT EXISTS audit_events_at ON audit_events(at DESC);
	CREATE TABLE IF NOT EXISTS cbs_allocations (
		alert_key TEXT PRIMARY KEY, message_identifier INTEGER NOT NULL, scope INTEGER NOT NULL,
		message_code INTEGER NOT NULL, update_number INTEGER NOT NULL
	); CREATE TABLE IF NOT EXISTS cbs_plans (
		alert_identifier TEXT PRIMARY KEY, plan_json BLOB NOT NULL, created_at TEXT NOT NULL
	); CREATE TABLE IF NOT EXISTS lte_cells (id INTEGER PRIMARY KEY AUTOINCREMENT,mcc TEXT NOT NULL,mnc TEXT NOT NULL,mnc_length INTEGER NOT NULL,eci INTEGER NOT NULL,enb_id INTEGER NOT NULL,local_cell_id INTEGER NOT NULL,tac INTEGER NOT NULL,cell_name TEXT,mme_name TEXT,latitude REAL,longitude REAL,nominal_radius_m REAL,azimuth_deg REAL,beamwidth_deg REAL,coverage_geojson TEXT,bbox_min_lat REAL,bbox_min_lon REAL,bbox_max_lat REAL,bbox_max_lon REAL,geometry_quality TEXT NOT NULL DEFAULT 'unknown',source TEXT,source_record_id TEXT,source_version TEXT,active INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(mcc,mnc,mnc_length,eci)); CREATE INDEX IF NOT EXISTS idx_lte_cells_tac ON lte_cells(mcc,mnc,tac); CREATE INDEX IF NOT EXISTS idx_lte_cells_mme ON lte_cells(mme_name); CREATE INDEX IF NOT EXISTS idx_lte_cells_bbox ON lte_cells(bbox_min_lon,bbox_max_lon,bbox_min_lat,bbox_max_lat); CREATE TABLE IF NOT EXISTS inventory_versions (id TEXT PRIMARY KEY,version_name TEXT NOT NULL UNIQUE,source_filename TEXT,source_sha256 TEXT NOT NULL,import_mode TEXT NOT NULL,record_count INTEGER NOT NULL,status TEXT NOT NULL,created_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS inventory_imports (id TEXT PRIMARY KEY,inventory_version_id TEXT,source_filename TEXT NOT NULL,source_sha256 TEXT NOT NULL,mode TEXT NOT NULL,status TEXT NOT NULL,rows_received INTEGER NOT NULL DEFAULT 0,rows_valid INTEGER NOT NULL DEFAULT 0,rows_rejected INTEGER NOT NULL DEFAULT 0,inserted_count INTEGER NOT NULL DEFAULT 0,updated_count INTEGER NOT NULL DEFAULT 0,deactivated_count INTEGER NOT NULL DEFAULT 0,warning_count INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,completed_at TEXT,FOREIGN KEY(inventory_version_id) REFERENCES inventory_versions(id)); CREATE TABLE IF NOT EXISTS inventory_import_errors (id INTEGER PRIMARY KEY AUTOINCREMENT,import_id TEXT NOT NULL,row_number INTEGER,column_name TEXT,error_code TEXT NOT NULL,error_message TEXT NOT NULL,FOREIGN KEY(import_id) REFERENCES inventory_imports(id));
	CREATE TABLE IF NOT EXISTS cell_geocodes (id INTEGER PRIMARY KEY AUTOINCREMENT,cell_id INTEGER NOT NULL REFERENCES lte_cells(id),code_type TEXT NOT NULL,code TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(cell_id,code_type,code));
	CREATE INDEX IF NOT EXISTS idx_cell_geocodes_lookup ON cell_geocodes(code_type,code);
	CREATE TABLE IF NOT EXISTS geo_codes (id INTEGER PRIMARY KEY AUTOINCREMENT,type TEXT NOT NULL,code TEXT NOT NULL,description TEXT,created_at TEXT NOT NULL,UNIQUE(type,code));
	CREATE INDEX IF NOT EXISTS idx_geo_codes_lookup ON geo_codes(type,code);`)
	if err != nil {
		return err
	}
	// Backfill: any (code_type, code) pair already tagged on a cell but
	// predating the Geo Codes registry gets a registry row (blank
	// description, since cell_geocodes never had one) so it shows up in the
	// registry and stays selectable from the Cell Mappings dropdown. Safe to
	// run on every startup - it only inserts pairs not already present.
	_, err = s.db.ExecContext(ctx, `INSERT INTO geo_codes(type,code,description,created_at)
		SELECT DISTINCT cg.code_type, cg.code, '', ?
		FROM cell_geocodes cg
		WHERE NOT EXISTS (SELECT 1 FROM geo_codes gc WHERE gc.type = cg.code_type AND gc.code = cg.code)`,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) LoadAlerts(ctx context.Context) ([]storage.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cap_json,state,received_at FROM alerts ORDER BY received_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []storage.Record
	for rows.Next() {
		var raw []byte
		var state, received string
		if err := rows.Scan(&raw, &state, &received); err != nil {
			return nil, err
		}
		var r storage.Record
		if err := json.Unmarshal(raw, &r.Alert); err != nil {
			return nil, fmt.Errorf("decode stored CAP: %w", err)
		}
		r.State = state
		r.ReceivedAt, err = time.Parse(time.RFC3339Nano, received)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *Store) Upsert(ctx context.Context, r storage.Record, references []string) error {
	raw, err := json.Marshal(r.Alert)
	if err != nil {
		return err
	}
	var expires any
	if t, ok := expiry(r.Alert); ok {
		expires = t.Format(time.RFC3339Nano)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO alerts(identifier,cap_json,state,received_at,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(identifier) DO UPDATE SET cap_json=excluded.cap_json,state=excluded.state,received_at=excluded.received_at,expires_at=excluded.expires_at`, r.Alert.Identifier, raw, r.State, r.ReceivedAt.UTC().Format(time.RFC3339Nano), expires); err != nil {
		return err
	}
	referenceState := "superseded"
	eventType := "alert_received"
	if r.Alert.MsgType == "Update" {
		eventType = "alert_updated"
	}
	if r.Alert.MsgType == "Cancel" {
		referenceState, eventType = "cancelled", "alert_cancelled"
	}
	for _, id := range references {
		if _, err = tx.ExecContext(ctx, `UPDATE alerts SET state=? WHERE identifier=? AND state='active'`, referenceState, id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(at,type,alert_id,detail) VALUES(?,?,?,?)`, time.Now().UTC().Format(time.RFC3339Nano), eventType, r.Alert.Identifier, "received from CBE")
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Expire removes alerts whose CAP expiry has passed from the local alert
// store, as soon as they cross it - there is no retention window. Matches
// both 'active' (the normal case: this sweep is what makes them expire) and
// 'expired' (a one-time catch-up for rows persisted by a previous version of
// this method, which only marked them expired instead of removing them) so
// nothing lingers indefinitely. An audit_events row is still recorded before
// the delete, so the fact that this alert existed and expired remains
// visible in the audit trail even after the alert row itself is gone.
func (s *Store) Expire(ctx context.Context, now time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT identifier FROM alerts WHERE state IN ('active','expired') AND expires_at IS NOT NULL AND expires_at<=?`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(at,type,alert_id,detail) VALUES(?,?,?,?)`, now.UTC().Format(time.RFC3339Nano), "alert_expired", id, "CAP expires time reached; removed from local alert store"); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM alerts WHERE identifier=?`, id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) Audit(ctx context.Context, limit int) ([]storage.AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,at,type,COALESCE(alert_id,''),detail FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []storage.AuditEvent
	for rows.Next() {
		var e storage.AuditEvent
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Type, &e.AlertID, &e.Detail); err != nil {
			return nil, err
		}
		e.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SaveCBSPlan(ctx context.Context, identifier string, plan []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cbs_plans(alert_identifier,plan_json,created_at) VALUES(?,?,?) ON CONFLICT(alert_identifier) DO UPDATE SET plan_json=excluded.plan_json,created_at=excluded.created_at`, identifier, plan, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) CBSPlan(ctx context.Context, identifier string) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT plan_json FROM cbs_plans WHERE alert_identifier=?`, identifier).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}
func (s *Store) AllocateCBSSerial(ctx context.Context, alertKey string, messageIdentifier uint16, scope uint8, update bool) (uint16, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var storedID, storedScope, code, number int
	err = tx.QueryRowContext(ctx, `SELECT message_identifier,scope,message_code,update_number FROM cbs_allocations WHERE alert_key=?`, alertKey).Scan(&storedID, &storedScope, &code, &number)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cbs_allocations WHERE message_identifier=? AND scope=?`, messageIdentifier, scope).Scan(&count); err != nil {
			return 0, err
		}
		if count >= 1024 {
			return 0, fmt.Errorf("all 1024 CBS message codes are allocated for message identifier %#04x/scope %d", messageIdentifier, scope)
		}
		var max sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT MAX(message_code) FROM cbs_allocations WHERE message_identifier=? AND scope=?`, messageIdentifier, scope).Scan(&max); err != nil {
			return 0, err
		}
		code = 0
		if max.Valid {
			code = int(max.Int64 + 1)
		}
		number = 0
		_, err = tx.ExecContext(ctx, `INSERT INTO cbs_allocations(alert_key,message_identifier,scope,message_code,update_number) VALUES(?,?,?,?,?)`, alertKey, messageIdentifier, scope, code, number)
	} else if err == nil {
		if uint16(storedID) != messageIdentifier || uint8(storedScope) != scope {
			return 0, fmt.Errorf("CBS allocation key has incompatible message identifier or scope")
		}
		if update {
			number = (number + 1) & 0x0f
			_, err = tx.ExecContext(ctx, `UPDATE cbs_allocations SET update_number=? WHERE alert_key=?`, number, alertKey)
		}
	}
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return uint16(scope)<<14 | uint16(code)<<4 | uint16(number), nil
}
func expiry(a cap.Alert) (time.Time, bool) {
	var earliest time.Time
	for _, info := range a.Info {
		if info.Expires == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, info.Expires)
		if err != nil {
			continue
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	return earliest, !earliest.IsZero()
}
