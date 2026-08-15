package sbcap

import "testing"

func FuzzHeader(f *testing.F) {
	pdu, err := WriteReplace(0x1112, 0x4000, 1, append(append([]byte{1}, make([]byte, 82)...), 1), 30, 4)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(pdu)
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, raw []byte) { _, _, _ = Header(raw) })
}
