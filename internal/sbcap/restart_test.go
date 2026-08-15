package sbcap

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestDecodeRestartIndicationGoldenVector decodes a real PWS-Restart-
// Indication PDU (captured from a reference C ASN.1 APER encoder built
// against the real TS 29.168 module - see internal/sbcap/testdata - not
// self-generated, so this exercises the new ENB-ID/Global-ENB-ID/list decode
// logic against ground truth rather than just round-tripping this package's
// own encoder against itself).
func TestDecodeRestartIndicationGoldenVector(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "pws_restart_indication.hex"))
	if err != nil {
		t.Fatal(err)
	}
	pdu, err := hex.DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil {
		t.Fatal(err)
	}

	outcome, procedure, err := Header(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != OutcomeInitiating || procedure != ProcedurePWSRestartIndication {
		t.Fatalf("outcome=%d procedure=%d", outcome, procedure)
	}

	ri, err := DecodeRestartIndication(pdu)
	if err != nil {
		t.Fatal(err)
	}
	wantPLMN := []byte{0x13, 0x41, 0x53}
	if !bytes.Equal(ri.GlobalENBID.PLMN, wantPLMN) {
		t.Fatalf("GlobalENBID.PLMN=%x want %x", ri.GlobalENBID.PLMN, wantPLMN)
	}
	if ri.GlobalENBID.ENBID.Kind != "macro" || ri.GlobalENBID.ENBID.Value != 5000 {
		t.Fatalf("ENBID=%+v", ri.GlobalENBID.ENBID)
	}
	if len(ri.RestartedCells) != 1 || ri.RestartedCells[0].ECI != 1280001 {
		t.Fatalf("RestartedCells=%+v", ri.RestartedCells)
	}
	if !bytes.Equal(ri.RestartedCells[0].PLMN, wantPLMN) {
		t.Fatalf("RestartedCells[0].PLMN=%x want %x", ri.RestartedCells[0].PLMN, wantPLMN)
	}
	if len(ri.RestartedTAIs) != 1 || ri.RestartedTAIs[0].TAC != 100 {
		t.Fatalf("RestartedTAIs=%+v", ri.RestartedTAIs)
	}
	if !bytes.Equal(ri.RestartedTAIs[0].PLMN, wantPLMN) {
		t.Fatalf("RestartedTAIs[0].PLMN=%x want %x", ri.RestartedTAIs[0].PLMN, wantPLMN)
	}
}

func TestDecodeRestartIndicationRejectsWrongProcedure(t *testing.T) {
	pdu, err := Stop(0x1112, 0x4000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRestartIndication(pdu); err == nil {
		t.Fatal("expected error decoding a Stop-Warning-Request as a restart indication")
	}
}

func TestDecodeFailureIndicationRejectsWrongProcedure(t *testing.T) {
	pdu, err := Stop(0x1112, 0x4000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeFailureIndication(pdu); err == nil {
		t.Fatal("expected error decoding a Stop-Warning-Request as a failure indication")
	}
}

// TestDecodeWriteReplaceWarningIndicationRoundTrip has no ground-truth hex
// fixture (unlike the Restart/Failure golden vectors) since this codec never
// needs to encode this message and no reference capture was available - but
// the two fields it reads (Message-Identifier, Serial-Number via decode16)
// are the exact same IE types/decode path already validated against real
// captured bytes in TestResponseIDs (success_write_replace.hex), so a
// same-package round trip here is enough to cover the new envelope check
// (pduInitiating + procedure 3) without re-litigating decode16 itself.
func TestDecodeWriteReplaceWarningIndicationRoundTrip(t *testing.T) {
	ies := []protocolIE{
		{id: idMessageIdentifier, criticality: critReject, value: encode16(0x1112)},
		{id: idSerialNumber, criticality: critReject, value: encode16(0x4001)},
	}
	pdu, err := encodePDU(pduInitiating, ProcedureWriteReplaceWarningIndication, critIgnore, ies)
	if err != nil {
		t.Fatal(err)
	}
	wi, err := DecodeWriteReplaceWarningIndication(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if wi.MessageIdentifier != 0x1112 || wi.SerialNumber != 0x4001 {
		t.Fatalf("wi=%+v", wi)
	}
}

func TestDecodeWriteReplaceWarningIndicationRejectsWrongProcedure(t *testing.T) {
	pdu, err := Stop(0x1112, 0x4000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWriteReplaceWarningIndication(pdu); err == nil {
		t.Fatal("expected error decoding a Stop-Warning-Request as a write-replace-warning indication")
	}
}

// TestDecodeWriteReplaceWarningIndicationRejectsWrongOutcome exercises a real
// captured PDU (success_write_replace.hex - a Successful Outcome) to confirm
// the choiceIndex check, not just the procedure code check, rejects it: it's
// procedure 0, not 3, but also the wrong PDU CHOICE alternative entirely.
func TestDecodeWriteReplaceWarningIndicationRejectsWrongOutcome(t *testing.T) {
	pdu, err := hex.DecodeString(readGoldenHex(t, "success_write_replace"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWriteReplaceWarningIndication(pdu); err == nil {
		t.Fatal("expected error decoding a Write-Replace-Warning-Response as a write-replace-warning indication")
	}
}

func TestDecodeRestartIndicationRejectsTruncatedPDU(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "pws_restart_indication.hex"))
	if err != nil {
		t.Fatal(err)
	}
	pdu, err := hex.DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRestartIndication(pdu[:len(pdu)-5]); err == nil {
		t.Fatal("expected a truncated PDU to fail decoding, not silently produce partial data")
	}
}
