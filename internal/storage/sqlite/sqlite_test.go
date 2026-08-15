package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/storage"
)

func TestLifecyclePersistsReferencesAndExpiry(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "cbc.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	first := cap.Alert{Identifier: "first", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert", Info: []cap.Info{{Event: "Flood", Expires: "2026-08-02T01:00:00Z"}}}
	if err := s.Upsert(ctx, storage.Record{Alert: first, ReceivedAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), State: "active"}, nil); err != nil {
		t.Fatal(err)
	}
	update := first
	update.Identifier = "update"
	update.MsgType = "Update"
	update.References = "first"
	update.Sent = "2026-08-02T00:05:00Z"
	if err := s.Upsert(ctx, storage.Record{Alert: update, ReceivedAt: time.Date(2026, 8, 2, 0, 5, 0, 0, time.UTC), State: "active"}, update.ReferenceIDs()); err != nil {
		t.Fatal(err)
	}
	records, err := s.LoadAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, r := range records {
		states[r.Alert.Identifier] = r.State
	}
	if states["first"] != "superseded" || states["update"] != "active" {
		t.Fatalf("unexpected states: %#v", states)
	}
	ids, err := s.Expire(ctx, time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "update" {
		t.Fatalf("expired = %v", ids)
	}
	events, err := s.Audit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want 3", len(events))
	}

	records, err = s.LoadAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Alert.Identifier == "update" {
			t.Fatalf("expired alert %q should have been removed, not just marked expired: %#v", r.Alert.Identifier, r)
		}
	}

	// A second Expire call at the same time is a no-op - nothing left to expire.
	ids, err = s.Expire(ctx, time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("second Expire() call: got %v, want none (already removed)", ids)
	}
}

// TestMigrateRebuildsLegacyCellGeocodesCheck simulates a database created
// before geo codes were generalized beyond SAME/UGC: cell_geocodes exists
// with the old CHECK(code_type IN ('SAME','UGC')) baked into its stored
// schema, and a couple of rows already tagged. Migrate should rebuild the
// table (dropping the CHECK, preserving the rows) and backfill a Geo Codes
// registry entry for each pre-existing (code_type, code) pair.
func TestMigrateRebuildsLegacyCellGeocodesCheck(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "cbc.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Run the real migration once to get a correct lte_cells (and every
	// other table) in place, then hand-roll cell_geocodes back to the
	// pre-generalization shape - CHECK constraint included, exactly as a
	// real database predating this change would have it in sqlite_master -
	// with a couple of rows already tagged.
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE cell_geocodes`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE cell_geocodes (id INTEGER PRIMARY KEY AUTOINCREMENT,cell_id INTEGER NOT NULL REFERENCES lte_cells(id),code_type TEXT NOT NULL CHECK(code_type IN ('SAME','UGC')),code TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(cell_id,code_type,code))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO lte_cells(mcc,mnc,mnc_length,eci,enb_id,local_cell_id,tac,created_at,updated_at) VALUES ('311','435',3,1048577,4096,1,1,'2026-08-03T00:00:00Z','2026-08-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO cell_geocodes(cell_id,code_type,code,created_at) VALUES (1,'SAME','001051','2026-08-03T20:58:08Z'),(1,'UGC','AL01000','2026-08-03T20:58:38Z')`); err != nil {
		t.Fatal(err)
	}

	// Re-run Migrate the way a real restart would - this is the pass that
	// should detect and rebuild the legacy CHECK.
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Old rows survived the rebuild.
	rows, err := s.db.QueryContext(ctx, `SELECT code_type, code FROM cell_geocodes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var ct, code string
		if err := rows.Scan(&ct, &code); err != nil {
			t.Fatal(err)
		}
		got = append(got, ct+" "+code)
	}
	if len(got) != 2 || got[0] != "SAME 001051" || got[1] != "UGC AL01000" {
		t.Fatalf("cell_geocodes after migrate = %v", got)
	}

	// The CHECK constraint is gone - a new type can now be inserted.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO cell_geocodes(cell_id,code_type,code,created_at) VALUES (1,'STATE','AL01','2026-08-05T00:00:00Z')`); err != nil {
		t.Fatalf("insert of a non-SAME/UGC type should now succeed: %v", err)
	}

	// The pre-existing pairs were backfilled into the registry.
	regRows, err := s.db.QueryContext(ctx, `SELECT type, code FROM geo_codes ORDER BY type`)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for regRows.Next() {
		var typ, code string
		if err := regRows.Scan(&typ, &code); err != nil {
			t.Fatal(err)
		}
		codes = append(codes, typ+" "+code)
	}
	want := map[string]bool{"SAME 001051": true, "UGC AL01000": true}
	if len(codes) != 2 || !want[codes[0]] || !want[codes[1]] {
		t.Fatalf("geo_codes backfill = %v", codes)
	}
}

// TestMigrateIsIdempotentOnAlreadyMigratedSchema confirms a second Migrate
// call against an already-current database (no CHECK constraint, registry
// already populated) is a safe no-op - matters since Migrate runs on every
// startup, not just once.
func TestMigrateIsIdempotentOnAlreadyMigratedSchema(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "cbc.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}
