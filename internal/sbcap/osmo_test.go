package sbcap

import "testing"

func TestWriteReplaceUsesSBcAPAPER(t *testing.T) {
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

func TestStopUsesSBcAPAPER(t *testing.T) {
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
	pdu, err := SuccessResponse(ProcedureWriteReplace, 0x1112, 0x4001)
	if err != nil {
		t.Fatal(err)
	}
	id, serial, err := ResponseIDs(pdu, ProcedureWriteReplace)
	if err != nil || id != 0x1112 || serial != 0x4001 {
		t.Fatalf("id=%#x serial=%#x err=%v", id, serial, err)
	}
}
