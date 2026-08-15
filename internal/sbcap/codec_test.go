package sbcap

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplaceDecodesToWriteReplaceEnvelope(t *testing.T) {
	page := make([]byte, 82)
	for i := range page {
		page[i] = 0x0d
	}
	contents := append([]byte{1}, page...)
	contents = append(contents, 5)
	pdu, err := WriteReplace(0x1112, 0x1234, 0x01, contents, 30, 4)
	if err != nil {
		t.Fatal(err)
	}
	outcome, procedure, err := Header(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != OutcomeInitiating || procedure != ProcedureWriteReplace {
		t.Fatalf("decoded envelope = outcome %d procedure %d", outcome, procedure)
	}
}

func TestStopDecodesToStopEnvelope(t *testing.T) {
	pdu, err := Stop(0x1112, 0x1234)
	if err != nil {
		t.Fatal(err)
	}
	outcome, procedure, err := Header(pdu)
	if err != nil || outcome != OutcomeInitiating || procedure != ProcedureStop {
		t.Fatalf("decoded envelope outcome=%d procedure=%d err=%v", outcome, procedure, err)
	}
}

func TestResponseIDs(t *testing.T) {
	pdu, err := SuccessResponse(ProcedureWriteReplace, 0x1112, 0x4001, CauseMessageAccepted)
	if err != nil {
		t.Fatal(err)
	}
	id, serial, err := ResponseIDs(pdu, ProcedureWriteReplace)
	if err != nil || id != 0x1112 || serial != 0x4001 {
		t.Fatalf("id=%#x serial=%#x err=%v", id, serial, err)
	}
}

func TestErrorIndicationRoundTrip(t *testing.T) {
	pdu, err := ErrorIndication(CauseMissingMandatoryElement)
	if err != nil {
		t.Fatal(err)
	}
	outcome, procedure, err := Header(pdu)
	if err != nil || outcome != OutcomeInitiating || procedure != procErrorIndication {
		t.Fatalf("outcome=%d procedure=%d err=%v", outcome, procedure, err)
	}
	cause, hasCause, err := DecodeErrorIndication(pdu)
	if err != nil || !hasCause || cause != CauseMissingMandatoryElement {
		t.Fatalf("cause=%d hasCause=%v err=%v", cause, hasCause, err)
	}
}

// TestGoldenVectors reproduces, byte for byte, the output the previous
// CGO-based encoder produced for the same inputs (captured in
// testdata/*.hex before that implementation was replaced). This is the
// primary correctness guard for the hand-written APER codec: any bit-
// packing mistake shows up here as a byte mismatch, not just "some bytes
// that still happen to decode."
func TestGoldenVectors(t *testing.T) {
	plmn := []byte{0x13, 0x41, 0x53} // TBCD for MCC=311 MNC=435
	contents := append(append([]byte{1}, make([]byte, 82)...), 1)

	cases := []struct {
		name string
		pdu  func() ([]byte, error)
	}{
		{"write_replace_plmn_wide", func() ([]byte, error) {
			return WriteReplace(0x1112, 0x4000, 1, contents, 30, 4)
		}},
		{"write_replace_cell_wide", func() ([]byte, error) {
			return WriteReplaceTarget(0x1112, 0x4000, 1, contents, 30, 4, 3, plmn, []uint32{1280001, 1280002})
		}},
		{"write_replace_ta_wide", func() ([]byte, error) {
			return WriteReplaceTarget(0x1112, 0x4000, 1, contents, 30, 4, 2, plmn, []uint32{100, 200})
		}},
		{"stop_plmn_wide", func() ([]byte, error) {
			return Stop(0x1112, 0x4000)
		}},
		{"stop_cell_wide", func() ([]byte, error) {
			return StopTarget(0x1112, 0x8000, 3, plmn, []uint32{1280001, 1280002})
		}},
		{"stop_ta_wide", func() ([]byte, error) {
			return StopTarget(0x1112, 0x8000, 2, plmn, []uint32{100, 200})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.pdu()
			if err != nil {
				t.Fatal(err)
			}
			want := readGoldenHex(t, c.name)
			if hex.EncodeToString(got) != want {
				t.Fatalf("byte mismatch\n got: %s\nwant: %s", hex.EncodeToString(got), want)
			}
		})
	}
}

// success_write_replace.hex and success_stop.hex were captured from the old
// implementation, which omitted the mandatory Cause IE (a spec gap this
// rewrite fixes - see messages.go). They are intentionally not compared
// byte-for-byte; SuccessResponse's own round trip (TestResponseIDs) and the
// simulated-MME exchange in peer_test.go cover it instead.

func readGoldenHex(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".hex"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
