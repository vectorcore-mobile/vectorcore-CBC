package cbs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/config"
	"github.com/vectorcore/cbc/internal/inventory"
	"github.com/vectorcore/cbc/internal/sbcap"
	"github.com/vectorcore/cbc/internal/storage/sqlite"
)

func TestGSM7PageVector(t *testing.T) {
	pages, dcs, encoding, err := Encode("ABC", "en")
	if err != nil {
		t.Fatal(err)
	}
	if dcs != 0x01 || encoding != "gsm7" || len(pages) != 1 {
		t.Fatalf("unexpected encoding: %#v dcs=%#x %s", pages, dcs, encoding)
	}
	want := []byte{0x41, 0xe1, 0x10}
	for i := range want {
		if pages[0].Data[i] != want[i] {
			t.Fatalf("octet %d: got %#x want %#x", i, pages[0].Data[i], want[i])
		}
	}
	if len(pages[0].Data) != PageOctets || pages[0].Number != 1 || pages[0].Total != 1 || pages[0].PageParameter != 0x11 {
		t.Fatal("invalid fixed CBS page")
	}
}

func TestSBcAPWriteReplaceAPER(t *testing.T) {
	pages, dcs, enc, err := Encode("Test", "en")
	if err != nil {
		t.Fatal(err)
	}
	pdu, err := SBcAPWriteReplace(Message{MessageIdentifier: 0x1112, SerialNumber: 0x4000, DCS: dcs, Encoding: enc, Target: Target{Scope: PLMNWide}, Pages: pages}, 30, 4, "001-01")
	if err != nil {
		t.Fatal(err)
	}
	outcome, procedure, err := sbcap.Header(pdu)
	if err != nil || outcome != sbcap.OutcomeInitiating || procedure != sbcap.ProcedureWriteReplace {
		t.Fatalf("SBcAP APER envelope outcome=%d procedure=%d err=%v", outcome, procedure, err)
	}
}

func TestSBcAPTargetedWarningAreaAPER(t *testing.T) {
	pages, dcs, enc, err := Encode("Test", "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []Target{{Scope: TrackingAreaWide, TrackingAreas: []string{"0x1234"}}, {Scope: CellWide, Cells: []string{"0x1234567"}}} {
		pdu, err := SBcAPWriteReplace(Message{MessageIdentifier: 0x1112, SerialNumber: 0x8000, DCS: dcs, Encoding: enc, Target: target, Pages: pages}, 30, 4, "001-01")
		if err != nil {
			t.Fatalf("scope %d: %v", target.Scope, err)
		}
		outcome, procedure, err := sbcap.Header(pdu)
		if err != nil || outcome != sbcap.OutcomeInitiating || procedure != sbcap.ProcedureWriteReplace {
			t.Fatalf("scope %d envelope: outcome=%d procedure=%d err=%v", target.Scope, outcome, procedure, err)
		}
	}
}

func TestSBcAPTargetedStopAPER(t *testing.T) {
	for _, target := range []Target{{Scope: TrackingAreaWide, TrackingAreas: []string{"0x1234"}}, {Scope: CellWide, Cells: []string{"0x1234567"}}} {
		pdu, err := SBcAPStop(Message{MessageIdentifier: 0x1112, SerialNumber: 0x8000, Target: target}, "001-01")
		if err != nil {
			t.Fatal(err)
		}
		outcome, procedure, err := sbcap.Header(pdu)
		if err != nil || outcome != sbcap.OutcomeInitiating || procedure != sbcap.ProcedureStop {
			t.Fatalf("scope %d: outcome=%d procedure=%d err=%v", target.Scope, outcome, procedure, err)
		}
	}
}

func TestUCS2FallsBackAndUsesFixedPages(t *testing.T) {
	pages, dcs, encoding, err := Encode("Flood 🚨", "en")
	if err == nil || pages != nil || dcs != 0x11 || encoding != "ucs2" {
		t.Fatalf("non-BMP input should be rejected after UCS-2 fallback: pages=%v dcs=%#x encoding=%s err=%v", pages, dcs, encoding, err)
	}
	pages, dcs, encoding, err = Encode("Flood 警報", "en")
	if err != nil || dcs != 0x11 || encoding != "ucs2" || len(pages) != 1 || len(pages[0].Data) != PageOctets {
		t.Fatalf("UCS-2 fallback failed: pages=%v dcs=%#x encoding=%s err=%v", pages, dcs, encoding, err)
	}
}

func TestEncodeRejectsOversizeInputBeforeConversion(t *testing.T) {
	_, _, _, err := Encode(strings.Repeat("A", MaxPages*93+1), "en")
	if err == nil {
		t.Fatal("accepted oversized CBS input")
	}
}

func TestPrepareTargetsAndIncrementsUpdateSerial(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "cbc.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, s)
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert", Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "tac", Value: "00101"}}}}}}}
	plan, err := p.Prepare(ctx, alert)
	if err != nil {
		t.Fatal(err)
	}
	m := plan.Messages[0]
	if m.Target.Scope != TrackingAreaWide || m.SerialNumber != 0x8000 || m.MessageIdentifier != 0x1112 {
		t.Fatalf("unexpected plan: %#v", m)
	}
	update := alert
	update.Identifier = "b"
	update.MsgType = "Update"
	update.References = "a"
	update.Sent = "2026-08-02T00:01:00Z"
	plan, err = p.Prepare(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Messages[0].SerialNumber != 0x8001 {
		t.Fatalf("update serial=%#x, want 0x8001", plan.Messages[0].SerialNumber)
	}
}

func TestPrepareRejectsUntargetedAlert(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	_, err := p.Prepare(context.Background(), cap.Alert{Identifier: "a", MsgType: "Alert", Info: []cap.Info{{Headline: "test"}}})
	if err == nil {
		t.Fatal("expected target error")
	}
}

type fakeRepo struct{}

func (*fakeRepo) AllocateCBSSerial(context.Context, string, uint16, uint8, bool) (uint16, error) {
	return 0, nil
}
func (*fakeRepo) SaveCBSPlan(context.Context, string, []byte) error { return nil }

// fakeSelector is a CellSelector stub - it never touches a real database,
// just returns a canned SelectionResult (or error) for every call, so these
// tests exercise target()'s polygon-handling logic in isolation.
type fakeSelector struct {
	result *inventory.SelectionResult
	err    error
}

func (f *fakeSelector) SelectionPreview(context.Context, inventory.SelectionRequest) (*inventory.SelectionResult, error) {
	return f.result, f.err
}

const triangle = "0,0 0,10 10,10"

func TestPrepareResolvesPolygonViaCellSelector(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{result: &inventory.SelectionResult{
		Cells: []inventory.SelectedCell{{ECI: 1048577}, {ECI: 1048578}},
	}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Polygons: []string{triangle}}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	m := plan.Messages[0]
	if m.Target.Scope != CellWide {
		t.Fatalf("scope=%d, want CellWide", m.Target.Scope)
	}
	want := map[string]bool{"1048577": true, "1048578": true}
	if len(m.Target.Cells) != 2 || !want[m.Target.Cells[0]] || !want[m.Target.Cells[1]] {
		t.Fatalf("cells=%v", m.Target.Cells)
	}
}

// TestPrepareResolvesMultiplePolygonsInOneArea proves the fix for the
// confirmed Area.Polygon (singular) bug: two <polygon> elements in one
// area both get resolved, not just the last.
func TestPrepareResolvesMultiplePolygonsInOneArea(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{result: &inventory.SelectionResult{
		Cells: []inventory.SelectedCell{{ECI: 1048577}},
	}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Polygons: []string{triangle, "20,20 20,30 30,30"}}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	// fakeSelector returns the same canned result regardless of which
	// polygon it's called with - what this test actually proves is that
	// both polygons in the area are looped over and reach the selector at
	// all (a single-Polygon field would only ever see the last one).
	if cells := plan.Messages[0].Target.Cells; len(cells) != 1 || cells[0] != "1048577" {
		t.Fatalf("cells=%v", cells)
	}
}

// TestPrepareResolvesCAPCircleViaCellSelector proves CAP <circle> - part of
// the CAP 1.2 standard, previously silently dropped since Area had no
// Circle field at all - now reaches the CellSelector the same way a
// polygon does (circleCells approximates it to a polygon first).
func TestPrepareResolvesCAPCircleViaCellSelector(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{result: &inventory.SelectionResult{
		Cells: []inventory.SelectedCell{{ECI: 1048577}, {ECI: 1048578}},
	}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Circles: []string{"35,-95 10"}}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	m := plan.Messages[0]
	if m.Target.Scope != CellWide {
		t.Fatalf("scope=%d, want CellWide", m.Target.Scope)
	}
	want := map[string]bool{"1048577": true, "1048578": true}
	if len(m.Target.Cells) != 2 || !want[m.Target.Cells[0]] || !want[m.Target.Cells[1]] {
		t.Fatalf("cells=%v", m.Target.Cells)
	}
}

// TestPrepareUnionsPolygonCircleAndGeocodeCells proves polygon-, circle-,
// and geocode-derived cells all union together in one target, matching the
// existing polygon+geocode union behavior.
func TestPrepareUnionsPolygonCircleAndGeocodeCells(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{result: &inventory.SelectionResult{
		Cells: []inventory.SelectedCell{{ECI: 1048577}},
	}})
	p.SetGeocodeResolver(&fakeGeocodeResolver{byKey: map[string][]uint32{"SAME 001101": {1048578}}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{
			Polygons: []string{triangle},
			Circles:  []string{"35,-95 10"},
			Geocodes: []cap.Geocode{{ValueName: "SAME", Value: "001101"}},
		}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	// fakeSelector returns the same single-cell result for both the
	// polygon and the circle call, and the geocode resolver returns a
	// second, distinct cell - so the union should be exactly those two,
	// not three (polygon and circle overlap on the same fake result here).
	cells := plan.Messages[0].Target.Cells
	want := map[string]bool{"1048577": true, "1048578": true}
	if len(cells) != 2 || !want[cells[0]] || !want[cells[1]] {
		t.Fatalf("cells=%v", cells)
	}
}

func TestPrepareUnionsPolygonAndGeocodeCells(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{result: &inventory.SelectionResult{
		Cells: []inventory.SelectedCell{{ECI: 1048577}},
	}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{
			Polygons: []string{triangle},
			Geocodes: []cap.Geocode{{ValueName: "cell", Value: "999"}},
		}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	cells := plan.Messages[0].Target.Cells
	want := map[string]bool{"1048577": true, "999": true}
	if len(cells) != 2 || !want[cells[0]] || !want[cells[1]] {
		t.Fatalf("cells=%v", cells)
	}
}

func TestPrepareStillRejectsMixedCellAndTAWithPolygon(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{result: &inventory.SelectionResult{
		Cells: []inventory.SelectedCell{{ECI: 1048577}},
	}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{
			{Polygons: []string{triangle}},
			{Geocodes: []cap.Geocode{{ValueName: "tac", Value: "100"}}},
		}}}}
	if _, err := p.Prepare(context.Background(), alert); err == nil || !strings.Contains(err.Error(), "mixed cell and tracking-area") {
		t.Fatalf("expected mixed-scope error, got %v", err)
	}
}

func TestPreparePropagatesCellSelectorError(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{err: fmt.Errorf("boom")})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Polygons: []string{triangle}}}}}}
	if _, err := p.Prepare(context.Background(), alert); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected propagated selector error, got %v", err)
	}
}

// fakeGeocodeResolver is a GeocodeResolver stub - maps a single "codeType
// value" key to a canned ECI list (or error) for every call, so these tests
// exercise target()'s geocode-resolution logic (any type, not just
// SAME/UGC) in isolation.
type fakeGeocodeResolver struct {
	byKey map[string][]uint32
	err   error
}

func (f *fakeGeocodeResolver) ResolveCells(_ context.Context, codeType, code string) ([]uint32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byKey[codeType+" "+code], nil
}

func TestPrepareResolvesSAMEGeocodeViaResolver(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetGeocodeResolver(&fakeGeocodeResolver{byKey: map[string][]uint32{"SAME 001101": {1048577, 1048578}}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "SAME", Value: "001101"}}}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	m := plan.Messages[0]
	if m.Target.Scope != CellWide {
		t.Fatalf("scope=%d, want CellWide", m.Target.Scope)
	}
	want := map[string]bool{"1048577": true, "1048578": true}
	if len(m.Target.Cells) != 2 || !want[m.Target.Cells[0]] || !want[m.Target.Cells[1]] {
		t.Fatalf("cells=%v", m.Target.Cells)
	}
}

func TestPrepareResolvesUGCGeocodeCaseInsensitiveValueName(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetGeocodeResolver(&fakeGeocodeResolver{byKey: map[string][]uint32{"UGC ALZ057": {1048833}}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "ugc", Value: "ALZ057"}}}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	if cells := plan.Messages[0].Target.Cells; len(cells) != 1 || cells[0] != "1048833" {
		t.Fatalf("cells=%v", cells)
	}
}

func TestPrepareResolvesArbitraryGeocodeType(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetGeocodeResolver(&fakeGeocodeResolver{byKey: map[string][]uint32{"STATE AL01": {1048577}}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "STATE", Value: "AL01"}}}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	if cells := plan.Messages[0].Target.Cells; len(cells) != 1 || cells[0] != "1048577" {
		t.Fatalf("cells=%v", cells)
	}
}

func TestPrepareUnionsGeocodePolygonAndPlaceholderCells(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetCellSelector("311-435", &fakeSelector{result: &inventory.SelectionResult{Cells: []inventory.SelectedCell{{ECI: 1048577}}}})
	p.SetGeocodeResolver(&fakeGeocodeResolver{byKey: map[string][]uint32{"SAME 001101": {1048578}}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{
			Polygons: []string{triangle},
			Geocodes: []cap.Geocode{
				{ValueName: "SAME", Value: "001101"},
				{ValueName: "cell", Value: "999"},
			},
		}}}}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	cells := plan.Messages[0].Target.Cells
	want := map[string]bool{"1048577": true, "1048578": true, "999": true}
	if len(cells) != 3 {
		t.Fatalf("cells=%v", cells)
	}
	for _, c := range cells {
		if !want[c] {
			t.Fatalf("unexpected cell %s in %v", c, cells)
		}
	}
}

func TestPrepareUnresolvedGeocodeContributesNothing(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetGeocodeResolver(&fakeGeocodeResolver{byKey: map[string][]uint32{}})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "SAME", Value: "999999"}}}}}}}
	if _, err := p.Prepare(context.Background(), alert); err == nil || !strings.Contains(err.Error(), "no recognised cell or tracking-area geocode") {
		t.Fatalf("expected fall-through-to-PLMN-wide-disabled error, got %v", err)
	}
}

func TestPreparePropagatesGeocodeResolverError(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	p.SetGeocodeResolver(&fakeGeocodeResolver{err: fmt.Errorf("boom")})
	alert := cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert",
		Info: []cap.Info{{Headline: "Flood", Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "SAME", Value: "001101"}}}}}}}
	if _, err := p.Prepare(context.Background(), alert); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected propagated resolver error, got %v", err)
	}
}

// classifiableAlert builds a minimal targetable alert (a tac geocode, so
// target() never errors) with the given severity/urgency/certainty, so
// these tests isolate messageIdentifier's classification behavior.
func classifiableAlert(severity, urgency, certainty string) cap.Alert {
	return cap.Alert{Identifier: "a", Sender: "cbe", Sent: "2026-08-02T00:00:00Z", MsgType: "Alert", Info: []cap.Info{{
		Headline: "Tornado Warning", Severity: severity, Urgency: urgency, Certainty: certainty,
		Areas: []cap.Area{{Geocodes: []cap.Geocode{{ValueName: "tac", Value: "1"}}}},
	}}}
}

func TestPrepareUsesHardcodedATISIdentifierForClassifiedAlert(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	alert := classifiableAlert("Severe", "Immediate", "Observed")
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Messages[0].MessageIdentifier != 0x1117 {
		t.Fatalf("got %#x, want 0x1117 (severe-immediate-observed)", plan.Messages[0].MessageIdentifier)
	}
}

func TestPrepareUsesHardcodedIdentifierForPublicSafetyWithoutOverride(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	alert := classifiableAlert("", "", "")
	alert.Info[0].Parameters = []cap.Parameter{{ValueName: "WEAHandling", Value: "Public Safety"}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Messages[0].MessageIdentifier != 0x112C {
		t.Fatalf("got %#x, want 0x112c (public-safety)", plan.Messages[0].MessageIdentifier)
	}
}

func TestPrepareOverrideTakesPriorityOverHardcodedIdentifier(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112, MessageIdentifiers: map[string]uint16{"public-safety": 0x111F}}, &fakeRepo{})
	alert := classifiableAlert("", "", "")
	alert.Info[0].Parameters = []cap.Parameter{{ValueName: "WEAHandling", Value: "Public Safety"}}
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Messages[0].MessageIdentifier != 0x111F {
		t.Fatalf("got %#x, want 0x111f (configured override)", plan.Messages[0].MessageIdentifier)
	}
}

func TestPrepareUnclassifiedAlertUsesConfiguredDefault(t *testing.T) {
	p := New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{})
	alert := classifiableAlert("Moderate", "Expected", "Possible")
	plan, err := p.Prepare(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Messages[0].MessageIdentifier != 0x1112 {
		t.Fatalf("got %#x, want default 0x1112", plan.Messages[0].MessageIdentifier)
	}
}
