package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/storage"
)

// Publisher is the boundary between CAP ingest and LTE radio-network delivery.
// Implementations will map an accepted alert to SBcAP procedures.
type Publisher interface{ Publish(cap.Alert) error }

type Record = storage.Record

type Service struct {
	mu        sync.RWMutex
	alerts    map[string]Record
	store     storage.Store
	publisher Publisher
	lastError string
	connected bool
	ingested  uint64
	// rejected counts alerts that never became active because Publish
	// (CBS preparation/targeting) errored - e.g. no recognised cell/TA
	// geocode, an unresolvable CMAS classification. Distinct from
	// failureIndications below, which is a real RAN-reported delivery
	// failure for an alert that *was* accepted and sent.
	rejected uint64

	// MME-originated SBcAP indication counters, incremented via
	// delivery.MetricsRecorder (see cmd/cbc/main.go's wiring).
	restartIndications  uint64
	failureIndications  uint64
	restartRebroadcasts uint64
}

func New(store storage.Store, p Publisher) *Service {
	return &Service{alerts: make(map[string]Record), store: store, publisher: p}
}

func (s *Service) Recover(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	if _, err := s.store.Expire(ctx, time.Now().UTC()); err != nil {
		return err
	}
	records, err := s.store.LoadAlerts(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range records {
		s.alerts[r.Alert.Identifier] = r
	}
	return nil
}

func (s *Service) SetConnected(v bool) { s.mu.Lock(); s.connected = v; s.mu.Unlock() }
func (s *Service) SetError(err error) {
	s.mu.Lock()
	if err != nil {
		s.lastError = err.Error()
	} else {
		s.lastError = ""
	}
	s.mu.Unlock()
}

func (s *Service) Ingest(a cap.Alert) error {
	s.mu.Lock()
	if old, ok := s.alerts[a.Identifier]; ok && old.Alert.Sent == a.Sent && old.Alert.MsgType == a.MsgType {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	// An alert that cannot be prepared for safe CBS delivery must not become
	// active state. This also makes an XMPP retry attempt safe and repeatable.
	if s.publisher != nil {
		if err := s.publisher.Publish(a); err != nil {
			s.SetError(fmt.Errorf("prepare %s: %w", a.Identifier, err))
			s.mu.Lock()
			s.rejected++
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.alerts[a.Identifier]; ok && old.Alert.Sent == a.Sent && old.Alert.MsgType == a.MsgType {
		return nil
	}
	state := "active"
	if a.MsgType == "Cancel" {
		state = "cancelled"
	}
	r := Record{Alert: a, ReceivedAt: time.Now().UTC(), State: state}
	if s.store != nil {
		if err := s.store.Upsert(context.Background(), r, a.ReferenceIDs()); err != nil {
			return fmt.Errorf("persist alert: %w", err)
		}
	}
	referenceState := "superseded"
	if a.MsgType == "Cancel" {
		referenceState = "cancelled"
	}
	for _, id := range a.ReferenceIDs() {
		if prior, ok := s.alerts[id]; ok && prior.State == "active" {
			prior.State = referenceState
			s.alerts[id] = prior
		}
	}
	s.alerts[a.Identifier] = r
	s.ingested++
	return nil
}

// Expire removes alerts whose CAP expiry has passed from both the DB
// (storage.Store.Expire) and this Service's in-memory index, as soon as
// they cross it - expired alerts are not retained.
func (s *Service) Expire(ctx context.Context, now time.Time) error {
	if s.store == nil {
		return nil
	}
	ids, err := s.store.Expire(ctx, now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.alerts, id)
	}
	return nil
}
func (s *Service) Audit(ctx context.Context, limit int) ([]storage.AuditEvent, error) {
	if s.store == nil {
		return nil, nil
	}
	return s.store.Audit(ctx, limit)
}
func (s *Service) CBSPlan(ctx context.Context, identifier string) ([]byte, error) {
	if s.store == nil {
		return nil, nil
	}
	return s.store.CBSPlan(ctx, identifier)
}

func (s *Service) Alerts() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := make([]Record, 0, len(s.alerts))
	for _, a := range s.alerts {
		r = append(r, a)
	}
	sort.Slice(r, func(i, j int) bool { return r[i].ReceivedAt.After(r[j].ReceivedAt) })
	return r
}
func (s *Service) Alert(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.alerts[id]
	return r, ok
}
func (s *Service) Ready() bool       { s.mu.RLock(); defer s.mu.RUnlock(); return s.connected }
func (s *Service) LastError() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.lastError }

// IncrementRestartIndications, IncrementFailureIndications, and
// IncrementRestartRebroadcasts satisfy delivery.MetricsRecorder - wired via
// Publisher.SetMetricsRecorder in cmd/cbc/main.go once both this Service and
// the delivery.Publisher exist.
func (s *Service) IncrementRestartIndications() {
	s.mu.Lock()
	s.restartIndications++
	s.mu.Unlock()
}
func (s *Service) IncrementFailureIndications() {
	s.mu.Lock()
	s.failureIndications++
	s.mu.Unlock()
}
func (s *Service) IncrementRestartRebroadcasts() {
	s.mu.Lock()
	s.restartRebroadcasts++
	s.mu.Unlock()
}

type Metrics struct {
	Alerts    int
	Active    int
	Connected bool
	Ingested  uint64
	// Rejected counts alerts that never became active because CBS
	// preparation/targeting failed (e.g. no recognised cell/TA geocode,
	// an unresolvable CMAS classification) - not a delivery failure.
	Rejected            uint64
	RestartIndications  uint64
	FailureIndications  uint64
	RestartRebroadcasts uint64
}

func (s *Service) Metrics() Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := Metrics{
		Alerts: len(s.alerts), Connected: s.connected, Ingested: s.ingested, Rejected: s.rejected,
		RestartIndications: s.restartIndications, FailureIndications: s.failureIndications, RestartRebroadcasts: s.restartRebroadcasts,
	}
	for _, r := range s.alerts {
		if r.State == "active" {
			m.Active++
		}
	}
	return m
}
