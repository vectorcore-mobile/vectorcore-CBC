package delivery

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/cbs"
	"github.com/vectorcore/cbc/internal/config"
	"github.com/vectorcore/cbc/internal/sbcap"
	"github.com/vectorcore/cbc/internal/storage"
	"github.com/vectorcore/cbc/internal/storage/sqlite"
)

// A real reference PWS-Restart-Indication PDU (the same one validated
// against a reference C ASN.1 APER encoder in
// internal/sbcap/restart_test.go): PLMN 311/435, macro eNB id 5000,
// restarted ECI 1280001, restarted TAC 100.
const restartIndicationHex = "00054028000003001e0009000013415301388010001c00080013415300013880001f00080000001341530064"

type fakeMetrics struct {
	mu                                                          sync.Mutex
	restartIndications, failureIndications, restartRebroadcasts int
}

func (f *fakeMetrics) IncrementRestartIndications() {
	f.mu.Lock()
	f.restartIndications++
	f.mu.Unlock()
}
func (f *fakeMetrics) IncrementFailureIndications() {
	f.mu.Lock()
	f.failureIndications++
	f.mu.Unlock()
}
func (f *fakeMetrics) IncrementRestartRebroadcasts() {
	f.mu.Lock()
	f.restartRebroadcasts++
	f.mu.Unlock()
}
func (f *fakeMetrics) get() (restarts, failures, rebroadcasts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restartIndications, f.failureIndications, f.restartRebroadcasts
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met before timeout")
	}
}

func testConfig() config.SBcAP {
	return config.SBcAP{
		Enabled: true, PLMN: "311-435", RepetitionPeriod: 30, Broadcasts: 4,
		ResponseTimeout: 2 * time.Second, ReconnectMin: 10 * time.Millisecond, ReconnectMax: 50 * time.Millisecond,
		Peers: []config.SBcAPPeer{{Name: "mme-1", Address: "unused"}},
	}
}

func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "d.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// newTestPublisher wires a Publisher whose dial function hands out
// net.Pipe() pairs instead of real SCTP sockets (matching
// internal/sbcap/peer_test.go's convention), so these tests run without
// OS-level SCTP support. Each dial call's simulated-MME-side connection is
// sent on the returned channel.
func newTestPublisher(cfg config.SBcAP, store *sqlite.Store) (*Publisher, chan net.Conn) {
	preparer := cbs.New(config.CBS{DefaultMessageIdentifier: 0x1112, AllowPLMNWide: false}, store)
	pub := New(cfg, preparer, store)
	mmeConns := make(chan net.Conn, 8)
	pub.dial = func(ctx context.Context, address string) (net.Conn, error) {
		client, server := net.Pipe()
		mmeConns <- server
		return client, nil
	}
	return pub, mmeConns
}

// mmeAutoRespond runs a simulated MME on conn: every Write-Replace-Warning-
// Request or Stop-Warning-Request it reads gets an immediate
// SuccessResponse with matching IDs (decoded from the request itself via
// sbcap.RequestIDs, not assumed). Every request PDU seen is also forwarded
// on seen, if non-nil. Exits when conn errors/closes.
func mmeAutoRespond(conn net.Conn, seen chan<- []byte) {
	go func() {
		buf := make([]byte, 16*1024)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			pdu := append([]byte(nil), buf[:n]...)
			outcome, procedure, err := sbcap.Header(pdu)
			if err != nil || outcome != sbcap.OutcomeInitiating {
				continue
			}
			switch procedure {
			case sbcap.ProcedureWriteReplace, sbcap.ProcedureStop:
				if seen != nil {
					select {
					case seen <- pdu:
					default:
					}
				}
				msgID, serial, err := sbcap.RequestIDs(pdu)
				if err != nil {
					continue
				}
				resp, err := sbcap.SuccessResponse(procedure, msgID, serial, sbcap.CauseMessageAccepted)
				if err == nil {
					_, _ = conn.Write(resp)
				}
			}
		}
	}()
}

func TestRunEstablishesAssociationWithoutPublish(t *testing.T) {
	store := openTestStore(t)
	pub, mmeConns := newTestPublisher(testConfig(), store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pub.Run(ctx)

	select {
	case conn := <-mmeConns:
		conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("expected the association to be dialed without any Publish call")
	}
}

func testAlert(id, tac string) cap.Alert {
	return cap.Alert{
		Identifier: id, Sender: "cbe", Sent: "2026-08-03T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Event: "Test", Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "tac", Value: tac}}}}}},
	}
}

func TestUnsolicitedRestartIndicationThenPublishStillCorrelates(t *testing.T) {
	store := openTestStore(t)
	pub, mmeConns := newTestPublisher(testConfig(), store)
	metrics := &fakeMetrics{}
	pub.SetMetricsRecorder(metrics)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pub.Run(ctx)

	var mmeConn net.Conn
	select {
	case mmeConn = <-mmeConns:
	case <-time.After(2 * time.Second):
		t.Fatal("no dial observed")
	}
	defer mmeConn.Close()
	mmeAutoRespond(mmeConn, nil)

	restartPDU, err := hex.DecodeString(restartIndicationHex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mmeConn.Write(restartPDU); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		n, _, _ := metrics.get()
		return n == 1
	})

	if err := pub.Publish(testAlert("alert-1", "1")); err != nil {
		t.Fatalf("Publish after an unsolicited indication should still correlate its own response: %v", err)
	}
}

func TestReconnectAfterConnectionDrop(t *testing.T) {
	store := openTestStore(t)
	pub, mmeConns := newTestPublisher(testConfig(), store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pub.Run(ctx)

	select {
	case conn := <-mmeConns:
		conn.Close() // simulate the association dropping
	case <-time.After(2 * time.Second):
		t.Fatal("no initial dial observed")
	}

	select {
	case conn := <-mmeConns:
		conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("expected Run's reconnect loop to redial after the connection dropped")
	}
}

func TestNarrowTargetToRestart(t *testing.T) {
	ecis := map[uint32]bool{1280001: true}
	tacs := map[uint16]bool{100: true}

	if _, ok := narrowTargetToRestart(cbs.Target{Scope: cbs.CellWide, Cells: []string{"1280001", "1280002"}}, ecis, tacs); !ok {
		t.Fatal("expected an overlapping cell-wide target to narrow, not be excluded")
	}
	if got, ok := narrowTargetToRestart(cbs.Target{Scope: cbs.CellWide, Cells: []string{"1280001", "1280002"}}, ecis, tacs); !ok || len(got.Cells) != 1 || got.Cells[0] != "1280001" {
		t.Fatalf("expected narrowed target {1280001}, got %+v ok=%v", got, ok)
	}
	if _, ok := narrowTargetToRestart(cbs.Target{Scope: cbs.CellWide, Cells: []string{"9999999"}}, ecis, tacs); ok {
		t.Fatal("expected a non-overlapping cell-wide target to be excluded")
	}
	if got, ok := narrowTargetToRestart(cbs.Target{Scope: cbs.TrackingAreaWide, TrackingAreas: []string{"100", "200"}}, ecis, tacs); !ok || len(got.TrackingAreas) != 1 || got.TrackingAreas[0] != "100" {
		t.Fatalf("expected narrowed TA target {100}, got %+v ok=%v", got, ok)
	}
	if _, ok := narrowTargetToRestart(cbs.Target{Scope: cbs.PLMNWide}, ecis, tacs); ok {
		t.Fatal("PLMN-wide targets must never be auto-resent")
	}
}

func seedAlertWithPlan(t *testing.T, store *sqlite.Store, id, state string, target cbs.Target) {
	t.Helper()
	ctx := context.Background()
	alert := cap.Alert{Identifier: id, Sender: "cbe", Sent: "2026-08-03T00:00:00Z", MsgType: "Alert", Info: []cap.Info{{Event: "Test"}}}
	if err := store.Upsert(ctx, storage.Record{Alert: alert, ReceivedAt: time.Now().UTC(), State: state}, nil); err != nil {
		t.Fatal(err)
	}
	plan := cbs.Plan{AlertIdentifier: id, Messages: []cbs.Message{{
		MessageIdentifier: 0x1112, SerialNumber: 0x4000, DCS: 1, Encoding: "gsm7", Target: target,
		Pages: []cbs.Page{{Number: 1, Total: 1, PageParameter: 0, Data: make([]byte, cbs.PageOctets)}},
	}}}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCBSPlan(ctx, id, raw); err != nil {
		t.Fatal(err)
	}
}

func TestHandleRestartOnlyRebroadcastsOverlappingActiveAlerts(t *testing.T) {
	store := openTestStore(t)
	// active, overlapping the restarted ECI 1280001 -> must be re-broadcast.
	seedAlertWithPlan(t, store, "overlap-active", "active", cbs.Target{Scope: cbs.CellWide, Cells: []string{"1280001", "1280002"}})
	// active, but no overlap with the restarted cell -> must not be sent.
	seedAlertWithPlan(t, store, "no-overlap-active", "active", cbs.Target{Scope: cbs.CellWide, Cells: []string{"9999999"}})
	// overlapping, but cancelled -> must not be sent.
	seedAlertWithPlan(t, store, "overlap-cancelled", "cancelled", cbs.Target{Scope: cbs.CellWide, Cells: []string{"1280001"}})

	pub, mmeConns := newTestPublisher(testConfig(), store)
	metrics := &fakeMetrics{}
	pub.SetMetricsRecorder(metrics)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pub.Run(ctx)

	var mmeConn net.Conn
	select {
	case mmeConn = <-mmeConns:
	case <-time.After(2 * time.Second):
		t.Fatal("no dial observed")
	}
	defer mmeConn.Close()
	seenRequests := make(chan []byte, 8)
	mmeAutoRespond(mmeConn, seenRequests)

	restartPDU, err := hex.DecodeString(restartIndicationHex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mmeConn.Write(restartPDU); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, func() bool {
		_, _, rebroadcasts := metrics.get()
		return rebroadcasts == 1
	})

	select {
	case <-seenRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("expected exactly one re-broadcast Write-Replace-Warning-Request to reach the MME")
	}
	select {
	case pdu := <-seenRequests:
		t.Fatalf("expected only one re-broadcast request, got a second: %d bytes", len(pdu))
	case <-time.After(200 * time.Millisecond):
	}
}
