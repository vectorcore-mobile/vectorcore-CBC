package sbcap

import (
	"context"
	"net"
	"testing"
)

func TestExchangeWithSimulatedMME(t *testing.T) {
	cbc, mme := net.Pipe()
	defer cbc.Close()
	go func() {
		defer mme.Close()
		buf := make([]byte, 2048)
		if _, err := mme.Read(buf); err != nil {
			return
		}
		response, err := SuccessResponse(ProcedureWriteReplace, 0x1112, 0x4000, CauseMessageAccepted)
		if err == nil {
			_, _ = mme.Write(response)
		}
	}()
	request, err := WriteReplace(0x1112, 0x4000, 1, append(append([]byte{1}, make([]byte, 82)...), 1), 30, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = ExchangeFor(context.Background(), cbc, request, ProcedureWriteReplace, 0x1112, 0x4000); err != nil {
		t.Fatal(err)
	}
}

func TestExchangeRejectsMismatchedMMECorrelation(t *testing.T) {
	cbc, mme := net.Pipe()
	defer cbc.Close()
	go func() {
		defer mme.Close()
		buf := make([]byte, 2048)
		if _, err := mme.Read(buf); err != nil {
			return
		}
		response, err := SuccessResponse(ProcedureWriteReplace, 0x1112, 0x4001, CauseMessageAccepted)
		if err == nil {
			_, _ = mme.Write(response)
		}
	}()
	request, err := WriteReplace(0x1112, 0x4000, 1, append(append([]byte{1}, make([]byte, 82)...), 1), 30, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = ExchangeFor(context.Background(), cbc, request, ProcedureWriteReplace, 0x1112, 0x4000); err == nil {
		t.Fatal("accepted MME response with mismatched serial")
	}
}

func TestStopExchangeWithSimulatedMME(t *testing.T) {
	cbc, mme := net.Pipe()
	defer cbc.Close()
	go func() {
		defer mme.Close()
		buf := make([]byte, 2048)
		if _, err := mme.Read(buf); err != nil {
			return
		}
		response, err := SuccessResponse(ProcedureStop, 0x1112, 0x8000, CauseMessageAccepted)
		if err == nil {
			_, _ = mme.Write(response)
		}
	}()
	request, err := StopTarget(0x1112, 0x8000, 2, []byte{0x00, 0xf1, 0x10}, []uint32{0x1234})
	if err != nil {
		t.Fatal(err)
	}
	if err = ExchangeFor(context.Background(), cbc, request, ProcedureStop, 0x1112, 0x8000); err != nil {
		t.Fatal(err)
	}
}
