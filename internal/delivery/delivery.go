// Package delivery maintains LTE MME SBcAP SCTP peer associations.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/cbs"
	"github.com/vectorcore/cbc/internal/config"
	"github.com/vectorcore/cbc/internal/sbcap"
	"github.com/vectorcore/cbc/internal/storage"
)

// MetricsRecorder receives counts for MME-originated indications. Defined
// here (not in internal/service) so internal/delivery has no dependency on
// internal/service - cmd/cbc/main.go wires a *service.Service in via
// SetMetricsRecorder once both exist, since service.New itself takes a
// Publisher and so must be constructed after delivery.New.
type MetricsRecorder interface {
	IncrementRestartIndications()
	IncrementFailureIndications()
	IncrementRestartRebroadcasts()
}

// errNotConnected is returned when a send is attempted on an MME association
// that isn't currently up. Per TS 29.168 the CBC maintains this association
// persistently (see Run); it is deliberately never dialed inline from a
// send, so a caller gets a fast, clear failure instead of an unbounded wait.
var errNotConnected = errors.New("MME association is not connected")

// pendingJob is the response a sendAndAwait call is currently waiting for.
type pendingJob struct {
	procedure int
	messageID uint16
	serial    uint16
	result    chan error // buffered(1): a late/mismatched delivery never blocks
}

// mmeAssociation is one configured MME peer's persistent SCTP association.
// Run's per-peer goroutine owns dialing and reconnecting it; readLoop (also
// run from that goroutine) is the only reader; sendAndAwait, called from
// Publish or the eNB-restart handler, is the only writer, and serializes
// itself against concurrent callers so at most one request is ever
// in-flight on the connection at a time - simple to reason about, and
// sufficient because a second sender just waits its turn rather than
// needing its own correlation slot.
type mmeAssociation struct {
	peer config.SBcAPPeer

	sendMu sync.Mutex // serializes whole send-and-await transactions

	connMu sync.Mutex
	conn   net.Conn
	done   chan struct{} // closed when conn dies; recreated on each (re)connect

	awaitMu  sync.Mutex
	awaiting *pendingJob
}

func (a *mmeAssociation) setConn(conn net.Conn) chan struct{} {
	done := make(chan struct{})
	a.connMu.Lock()
	a.conn, a.done = conn, done
	a.connMu.Unlock()
	return done
}

func (a *mmeAssociation) current() (net.Conn, chan struct{}) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.conn, a.done
}

func (a *mmeAssociation) clearConn(conn net.Conn) {
	a.connMu.Lock()
	if a.conn == conn {
		a.conn = nil
	}
	a.connMu.Unlock()
}

// writeWithTimeout bounds conn.Write even when the connection doesn't
// support deadlines - confirmed true of this project's real SCTP
// connections: github.com/ishidawataru/sctp's SCTPConn.SetWriteDeadline
// unconditionally returns syscall.EOPNOTSUPP on Linux (it uses a raw
// blocking syscall.SendmsgN, not one integrated with Go's netpoller), so
// treating that error as fatal would fail every real send. If the deadline
// call isn't supported, the write instead runs in its own goroutine and the
// connection is forcibly closed if it hasn't finished by timeout - net.Pipe
// and other deadline-capable conns (used in tests) still get a real
// deadline set first, so this only changes behavior for connections that
// need the fallback.
func writeWithTimeout(conn net.Conn, pdu []byte, timeout time.Duration) error {
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err == nil {
		_, err := conn.Write(pdu)
		return err
	} else if !errors.Is(err, syscall.EOPNOTSUPP) {
		return err
	}
	done := make(chan error, 1)
	go func() { _, err := conn.Write(pdu); done <- err }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = conn.Close() // best effort: forces the blocked Write to unblock
		return fmt.Errorf("write timed out after %s", timeout)
	}
}

// sendAndAwait writes pdu and blocks for the matching successful/
// unsuccessful outcome, a timeout, or the connection dying - whichever
// happens first. Safe to call concurrently; concurrent callers simply queue
// behind sendMu.
func (a *mmeAssociation) sendAndAwait(pdu []byte, procedure int, messageID, serial uint16, timeout time.Duration) error {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()

	conn, done := a.current()
	if conn == nil {
		return fmt.Errorf("MME %s: %w", a.peer.Name, errNotConnected)
	}

	job := &pendingJob{procedure: procedure, messageID: messageID, serial: serial, result: make(chan error, 1)}
	a.awaitMu.Lock()
	a.awaiting = job
	a.awaitMu.Unlock()
	defer func() {
		a.awaitMu.Lock()
		if a.awaiting == job {
			a.awaiting = nil
		}
		a.awaitMu.Unlock()
	}()

	if err := writeWithTimeout(conn, pdu, timeout); err != nil {
		return fmt.Errorf("MME %s: write: %w", a.peer.Name, err)
	}

	select {
	case err := <-job.result:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("MME %s: timed out waiting for a response", a.peer.Name)
	case <-done:
		return fmt.Errorf("MME %s: connection lost while awaiting a response", a.peer.Name)
	}
}

// Publisher sends prepared CBS messages to every configured MME over a
// persistent SCTP association per peer (established and maintained by Run,
// independent of alert traffic - TS 29.168 requires the CBC to establish
// the association, and it must stay up so an unsolicited MME indication,
// most importantly a PWS-Restart-Indication, is never missed).
type Publisher struct {
	cfg     config.SBcAP
	prepare *cbs.Preparer
	store   storage.Store
	metrics MetricsRecorder

	assocs map[string]*mmeAssociation // keyed by peer name; built once in New

	// dial defaults to sbcap.DialSCTP; tests substitute a net.Pipe()-based
	// fake (matching internal/sbcap/peer_test.go's convention) so this
	// package's tests don't depend on the host having real SCTP support.
	dial func(ctx context.Context, address string) (net.Conn, error)
}

func New(cfg config.SBcAP, p *cbs.Preparer, store storage.Store) *Publisher {
	pub := &Publisher{cfg: cfg, prepare: p, store: store, assocs: map[string]*mmeAssociation{}}
	pub.dial = func(ctx context.Context, address string) (net.Conn, error) {
		return sbcap.DialSCTP(ctx, cfg.LocalAddress, address)
	}
	for _, peer := range cfg.Peers {
		pub.assocs[peer.Name] = &mmeAssociation{peer: peer}
	}
	return pub
}

// SetMetricsRecorder wires a metrics sink for MME-originated indications.
// Optional: if never called, indications are still handled and logged, just
// not counted.
func (p *Publisher) SetMetricsRecorder(m MetricsRecorder) { p.metrics = m }

// Run dials and maintains every configured MME peer's SCTP association
// until ctx is cancelled, reconnecting with backoff on failure or drop. It
// blocks, so callers run it in its own goroutine; it is a no-op if SBcAP
// isn't enabled.
func (p *Publisher) Run(ctx context.Context) {
	if !p.cfg.Enabled {
		return
	}
	var wg sync.WaitGroup
	for _, a := range p.assocs {
		a := a
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.maintain(ctx, a)
		}()
	}
	wg.Wait()
}

func (p *Publisher) maintain(ctx context.Context, a *mmeAssociation) {
	backoff := p.cfg.ReconnectMin
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, p.cfg.ResponseTimeout)
		conn, err := p.dial(dialCtx, a.peer.Address)
		cancel()
		if err != nil {
			slog.Warn("MME dial failed", "peer", a.peer.Name, "address", a.peer.Address, "error", err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff *= 2; backoff > p.cfg.ReconnectMax {
				backoff = p.cfg.ReconnectMax
			}
			continue
		}
		slog.Info("MME association established", "peer", a.peer.Name, "address", a.peer.Address)
		connectedAt := time.Now()

		done := a.setConn(conn)
		p.readLoop(a, conn)
		_ = conn.Close()
		a.clearConn(conn)
		select {
		case <-done:
		default:
			close(done)
		}
		if ctx.Err() != nil {
			return
		}

		// A connection that lived a good while failed for an unremarkable
		// reason (e.g. a brief network blip) - reconnect immediately. One
		// that died almost as soon as it was established is treated like a
		// dial failure for backoff purposes: without this, a peer that
		// accepts the SCTP association and then immediately drops it (for
		// any reason - a real instance of this was observed and it took an
		// MME process down) causes an unbounded, delay-free reconnect storm.
		if time.Since(connectedAt) < p.cfg.ReconnectMin {
			slog.Warn("MME association dropped almost immediately, backing off before retrying",
				"peer", a.peer.Name, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff *= 2; backoff > p.cfg.ReconnectMax {
				backoff = p.cfg.ReconnectMax
			}
		} else {
			backoff = p.cfg.ReconnectMin
			slog.Warn("MME association lost, reconnecting", "peer", a.peer.Name)
		}
	}
}

// readLoop is the only goroutine that ever reads conn. It classifies every
// inbound PDU via sbcap.Header: successful/unsuccessful outcomes correlate
// with whatever sendAndAwait call is currently in flight; anything else is
// an MME-initiated indication, dispatched by procedure code. It returns
// when the connection dies (the caller then reconnects).
func (p *Publisher) readLoop(a *mmeAssociation, conn net.Conn) {
	buf := make([]byte, 16*1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			p.failAwaiting(a, fmt.Errorf("MME %s: connection lost: %w", a.peer.Name, err))
			return
		}
		pdu := append([]byte(nil), buf[:n]...)
		outcome, procedure, err := sbcap.Header(pdu)
		if err != nil {
			slog.Warn("sbcap: failed to decode inbound PDU", "peer", a.peer.Name, "error", err)
			continue
		}
		switch outcome {
		case sbcap.OutcomeSuccessful:
			p.deliverResponse(a, pdu, procedure, nil)
		case sbcap.OutcomeUnsuccessful:
			p.deliverResponse(a, pdu, procedure, fmt.Errorf("MME %s: request rejected (unsuccessful outcome)", a.peer.Name))
		default:
			p.handleIndication(a, pdu, procedure)
		}
	}
}

func (p *Publisher) deliverResponse(a *mmeAssociation, pdu []byte, procedure int, outcomeErr error) {
	a.awaitMu.Lock()
	job := a.awaiting
	a.awaitMu.Unlock()
	if job == nil || job.procedure != procedure {
		slog.Warn("sbcap: response with no matching in-flight request", "peer", a.peer.Name, "procedure", procedure)
		return
	}
	if outcomeErr == nil {
		msgID, serial, err := sbcap.ResponseIDs(pdu, procedure)
		if err != nil {
			slog.Warn("sbcap: failed to decode response correlation IDs", "peer", a.peer.Name, "error", err)
			return
		}
		if msgID != job.messageID || serial != job.serial {
			slog.Warn("sbcap: response correlation mismatch", "peer", a.peer.Name,
				"want_id", job.messageID, "want_serial", job.serial, "got_id", msgID, "got_serial", serial)
			return
		}
	}
	select {
	case job.result <- outcomeErr:
	default:
	}
}

func (p *Publisher) failAwaiting(a *mmeAssociation, err error) {
	a.awaitMu.Lock()
	job := a.awaiting
	a.awaitMu.Unlock()
	if job != nil {
		select {
		case job.result <- err:
		default:
		}
	}
}

func (p *Publisher) handleIndication(a *mmeAssociation, pdu []byte, procedure int) {
	switch procedure {
	case sbcap.ProcedureWriteReplaceWarningIndication:
		wi, err := sbcap.DecodeWriteReplaceWarningIndication(pdu)
		if err != nil {
			slog.Warn("sbcap: failed to decode Write-Replace-Warning-Indication", "peer", a.peer.Name, "error", err)
			return
		}
		slog.Info("sbcap: MME confirmed broadcast scheduled", "peer", a.peer.Name,
			"message_id", wi.MessageIdentifier, "serial", wi.SerialNumber)
	case sbcap.ProcedurePWSRestartIndication:
		ri, err := sbcap.DecodeRestartIndication(pdu)
		if err != nil {
			slog.Warn("sbcap: failed to decode PWS-Restart-Indication", "peer", a.peer.Name, "error", err)
			return
		}
		slog.Info("eNB restart indication received", "peer", a.peer.Name,
			"restarted_cells", len(ri.RestartedCells), "restarted_tais", len(ri.RestartedTAIs))
		if p.metrics != nil {
			p.metrics.IncrementRestartIndications()
		}
		// Its own goroutine: handling this means sending a new request and
		// awaiting its response on this same connection, and readLoop must
		// stay free to keep reading - including that very response.
		go p.handleRestart(a, ri)
	case sbcap.ProcedurePWSFailureIndication:
		fi, err := sbcap.DecodeFailureIndication(pdu)
		if err != nil {
			slog.Warn("sbcap: failed to decode PWS-Failure-Indication", "peer", a.peer.Name, "error", err)
			return
		}
		slog.Warn("eNB failure indication received", "peer", a.peer.Name, "failed_cells", len(fi.FailedCells))
		if p.metrics != nil {
			p.metrics.IncrementFailureIndications()
		}
	default:
		slog.Info("sbcap: unhandled inbound indication", "peer", a.peer.Name, "procedure", procedure)
	}
}

// handleRestart re-broadcasts every currently active alert whose CBS plan
// targets any of the cells/TAs the indication says just restarted, scoped
// to only the overlapping subset, reusing the exact message identifier and
// serial number already allocated for that alert (this is a re-affirmation
// of an existing broadcast, not a new one). Cancelled, expired, or
// superseded alerts are never touched, and a message with no overlap at all
// is never re-sent.
func (p *Publisher) handleRestart(a *mmeAssociation, ri *sbcap.RestartIndication) {
	ctx := context.Background()

	if plmn, err := cbs.PlmnTBCD(p.cfg.PLMN); err == nil && len(ri.GlobalENBID.PLMN) == 3 {
		if string(plmn) != string(ri.GlobalENBID.PLMN) {
			slog.Warn("sbcap: restart indication for a different PLMN, ignoring",
				"peer", a.peer.Name, "configured_plmn", p.cfg.PLMN)
			return
		}
	}

	restartedECIs := make(map[uint32]bool, len(ri.RestartedCells))
	for _, c := range ri.RestartedCells {
		restartedECIs[c.ECI] = true
	}
	restartedTACs := make(map[uint16]bool, len(ri.RestartedTAIs))
	for _, t := range ri.RestartedTAIs {
		restartedTACs[t.TAC] = true
	}

	records, err := p.store.LoadAlerts(ctx)
	if err != nil {
		slog.Error("eNB restart handling: load alerts failed", "peer", a.peer.Name, "error", err)
		return
	}
	for _, rec := range records {
		if rec.State != "active" {
			continue
		}
		raw, err := p.store.CBSPlan(ctx, rec.Alert.Identifier)
		if err != nil || len(raw) == 0 {
			continue
		}
		var plan cbs.Plan
		if err := json.Unmarshal(raw, &plan); err != nil {
			slog.Warn("eNB restart handling: decode CBS plan failed", "alert", rec.Alert.Identifier, "error", err)
			continue
		}
		for _, m := range plan.Messages {
			narrowed, ok := narrowTargetToRestart(m.Target, restartedECIs, restartedTACs)
			if !ok {
				continue
			}
			narrowedMsg := m
			narrowedMsg.Target = narrowed
			pdu, err := cbs.SBcAPWriteReplace(narrowedMsg, p.cfg.RepetitionPeriod, p.cfg.Broadcasts, p.cfg.PLMN)
			if err != nil {
				slog.Error("eNB restart handling: encode failed", "alert", rec.Alert.Identifier, "error", err)
				continue
			}
			if err := a.sendAndAwait(pdu, sbcap.ProcedureWriteReplace, m.MessageIdentifier, m.SerialNumber, p.cfg.ResponseTimeout); err != nil {
				slog.Error("eNB restart handling: re-broadcast failed", "peer", a.peer.Name, "alert", rec.Alert.Identifier, "error", err)
				continue
			}
			slog.Info("eNB restart handling: re-broadcast sent", "peer", a.peer.Name,
				"alert", rec.Alert.Identifier, "cells", narrowed.Cells, "tracking_areas", narrowed.TrackingAreas)
			if p.metrics != nil {
				p.metrics.IncrementRestartRebroadcasts()
			}
		}
	}
}

// narrowTargetToRestart intersects a stored CBS message's target with the
// restarted ECIs/TACs. PLMN-wide messages (no cell/TA scoping) are never
// auto-resent by this logic.
func narrowTargetToRestart(t cbs.Target, ecis map[uint32]bool, tacs map[uint16]bool) (cbs.Target, bool) {
	switch t.Scope {
	case cbs.CellWide:
		var overlap []string
		for _, c := range t.Cells {
			v, err := strconv.ParseUint(strings.TrimSpace(c), 0, 32)
			if err != nil {
				continue
			}
			if ecis[uint32(v)] {
				overlap = append(overlap, c)
			}
		}
		if len(overlap) == 0 {
			return cbs.Target{}, false
		}
		return cbs.Target{Scope: cbs.CellWide, Cells: overlap}, true
	case cbs.TrackingAreaWide:
		var overlap []string
		for _, ta := range t.TrackingAreas {
			v, err := strconv.ParseUint(strings.TrimSpace(ta), 0, 32)
			if err != nil {
				continue
			}
			if tacs[uint16(v)] {
				overlap = append(overlap, ta)
			}
		}
		if len(overlap) == 0 {
			return cbs.Target{}, false
		}
		return cbs.Target{Scope: cbs.TrackingAreaWide, TrackingAreas: overlap}, true
	default:
		return cbs.Target{}, false
	}
}

func (p *Publisher) Close() {
	for _, a := range p.assocs {
		if conn, _ := a.current(); conn != nil {
			_ = conn.Close()
		}
	}
}

func (p *Publisher) Publish(a cap.Alert) error {
	if !p.cfg.Enabled {
		return p.prepare.Publish(a)
	}
	if a.MsgType == "Cancel" {
		for _, id := range a.ReferenceIDs() {
			raw, err := p.store.CBSPlan(context.Background(), id)
			if err != nil || len(raw) == 0 {
				return fmt.Errorf("load referenced CBS plan %q: %w", id, err)
			}
			var plan cbs.Plan
			if err = json.Unmarshal(raw, &plan); err != nil {
				return fmt.Errorf("decode referenced CBS plan %q: %w", id, err)
			}
			for _, m := range plan.Messages {
				pdu, err := cbs.SBcAPStop(m, p.cfg.PLMN)
				if err != nil {
					return err
				}
				if err = p.send(pdu, sbcap.ProcedureStop, m.MessageIdentifier, m.SerialNumber); err != nil {
					return err
				}
			}
		}
		return nil
	}
	plan, err := p.prepare.Prepare(context.Background(), a)
	if err != nil {
		return err
	}
	for _, m := range plan.Messages {
		pdu, err := cbs.SBcAPWriteReplace(m, p.cfg.RepetitionPeriod, p.cfg.Broadcasts, p.cfg.PLMN)
		if err != nil {
			return err
		}
		if err = p.send(pdu, sbcap.ProcedureWriteReplace, m.MessageIdentifier, m.SerialNumber); err != nil {
			return err
		}
	}
	return nil
}

// send delivers pdu to every configured MME peer over its persistent
// association (established by Run), in the order they're configured. If a
// peer isn't currently connected, this fails fast rather than dialing
// inline.
func (p *Publisher) send(pdu []byte, procedure int, messageID, serial uint16) error {
	for _, peer := range p.cfg.Peers {
		a := p.assocs[peer.Name]
		if err := a.sendAndAwait(pdu, procedure, messageID, serial, p.cfg.ResponseTimeout); err != nil {
			return err
		}
	}
	return nil
}
