package sbcap

import (
	"bytes"
	"testing"
)

func TestConstrainedWholeNumberRoundTrip(t *testing.T) {
	cases := []struct{ lb, ub, value int64 }{
		{0, 0, 0},   // range 1
		{0, 2, 1},   // range 3 (CHOICE index style)
		{0, 255, 0}, // range 256 (one-octet case)
		{0, 255, 255},
		{0, 65535, 9}, // range 65536 (two-octet case)
		{0, 65535, 65535},
		{1, 4096, 30}, // Repetition-Period style
		{0, 2, 2},     // Criticality-style ENUMERATED{reject,ignore,notify}
	}
	for _, c := range cases {
		w := &bitWriter{}
		if err := putConstrainedWholeNumber(w, c.lb, c.ub, c.value); err != nil {
			t.Fatalf("put(%d,%d,%d): %v", c.lb, c.ub, c.value, err)
		}
		w.align()
		r := newBitReader(w.bytes())
		got, err := getConstrainedWholeNumber(r, c.lb, c.ub)
		if err != nil {
			t.Fatalf("get(%d,%d) after put %d: %v", c.lb, c.ub, c.value, err)
		}
		if got != c.value {
			t.Fatalf("round trip mismatch: put %d got %d (lb=%d ub=%d)", c.value, got, c.lb, c.ub)
		}
	}
}

func TestConstrainedWholeNumberRejectsOutOfRange(t *testing.T) {
	w := &bitWriter{}
	if err := putConstrainedWholeNumber(w, 0, 10, 11); err == nil {
		t.Fatal("expected out-of-range value to be rejected")
	}
}

func TestOpenTypeRoundTripOneOctetLength(t *testing.T) {
	w := &bitWriter{}
	w.putBits(1, 3) // misalign on purpose to exercise the align() call
	content := []byte{0xaa, 0xbb, 0xcc}
	if err := putOpenType(w, content); err != nil {
		t.Fatal(err)
	}
	r := newBitReader(w.bytes())
	if _, err := r.getBits(3); err != nil {
		t.Fatal(err)
	}
	got, err := getOpenType(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("got %x want %x", got, content)
	}
}

func TestOpenTypeRoundTripTwoOctetLength(t *testing.T) {
	content := make([]byte, 200) // >127, exercises the two-octet length form
	for i := range content {
		content[i] = byte(i)
	}
	w := &bitWriter{}
	if err := putOpenType(w, content); err != nil {
		t.Fatal(err)
	}
	r := newBitReader(w.bytes())
	got, err := getOpenType(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("200-byte open type content did not round trip")
	}
}

func TestOpenTypeRejectsOversizedContent(t *testing.T) {
	w := &bitWriter{}
	if err := putOpenType(w, make([]byte, 16384)); err == nil {
		t.Fatal("expected content at the 16384 boundary to be rejected")
	}
}

func TestBitsForMatchesMinimalEncodingWidth(t *testing.T) {
	cases := map[int64]int{2: 1, 3: 2, 4: 2, 5: 3, 255: 8}
	for rng, want := range cases {
		if got := bitsFor(rng); got != want {
			t.Fatalf("bitsFor(%d) = %d, want %d", rng, got, want)
		}
	}
}
