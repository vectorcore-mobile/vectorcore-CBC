package cbs

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/config"
)

func TestWEAExampleFailsClosedWithoutLTEGeocode(t *testing.T) {
	raw, err := os.ReadFile("../../docs/wea_2026-08-02T04-09-05-355Z.xml")
	if err != nil {
		t.Fatal(err)
	}
	alert, err := cap.Parse(raw)
	if err != nil {
		t.Fatalf("CAP parse failed: %v", err)
	}
	if got := alert.Info[0].Areas[0].Polygons; len(got) == 0 {
		t.Fatal("polygon was not retained")
	}
	_, err = New(config.CBS{DefaultMessageIdentifier: 0x1112}, &fakeRepo{}).Prepare(context.Background(), alert)
	if err == nil || !strings.Contains(err.Error(), "no recognised cell or tracking-area geocode") {
		t.Fatalf("example must fail closed without an LTE target, got %v", err)
	}
}
