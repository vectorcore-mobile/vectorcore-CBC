package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/storage/sqlite"
)

func TestIngestDeduplicatesAndSupersedes(t *testing.T) {
	s := New(nil, nil)
	a := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert", Info: []cap.Info{{Event: "test"}}}
	if err := s.Ingest(a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Identifier = "b"
	b.MsgType = "Update"
	b.References = "a"
	b.Sent = "2026-08-02T00:01:00Z"
	if err := s.Ingest(b); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Alert("a"); r.State != "superseded" {
		t.Fatalf("state = %q", r.State)
	}
}

func TestIngestDoesNotRetainUnpreparedAlert(t *testing.T) {
	s := New(nil, failingPublisher{})
	a := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert", Info: []cap.Info{{Event: "test"}}}
	if err := s.Ingest(a); err == nil {
		t.Fatal("expected prepare failure")
	}
	if _, ok := s.Alert("a"); ok {
		t.Fatal("unprepared alert was retained")
	}
}

type failingPublisher struct{}

func (failingPublisher) Publish(cap.Alert) error { return errors.New("no target") }

// TestExpireRemovesFromInMemoryIndex guards against a regression to the old
// "mark expired, keep forever" behavior: once an alert's CAP expiry has
// passed, Expire must drop it from both the DB (exercised separately in
// internal/storage/sqlite's own test) and this Service's in-memory alerts
// map, so it stops appearing in Alerts()/Alert() immediately - no retention
// window.
func TestExpireRemovesFromInMemoryIndex(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "svc.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s := New(store, nil)
	a := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Event: "test", Expires: "2026-08-02T01:00:00Z"}}}
	if err := s.Ingest(a); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Alert("a"); !ok {
		t.Fatal("alert not present after ingest")
	}
	if err := s.Expire(ctx, time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Alert("a"); ok {
		t.Fatal("expired alert should have been removed from the in-memory index")
	}
	for _, r := range s.Alerts() {
		if r.Alert.Identifier == "a" {
			t.Fatal("expired alert should not appear in Alerts()")
		}
	}
}
