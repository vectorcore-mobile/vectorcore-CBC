package sbcap

import "fmt"

// bitWriter packs bits MSB-first into a growing byte buffer, matching the
// Aligned PER (X.691) bit order used throughout TS 29.168.
type bitWriter struct {
	buf  []byte
	nbit int
}

func (w *bitWriter) putBits(value uint64, count int) {
	for i := count - 1; i >= 0; i-- {
		byteIdx := w.nbit / 8
		for byteIdx >= len(w.buf) {
			w.buf = append(w.buf, 0)
		}
		if (value>>uint(i))&1 != 0 {
			w.buf[byteIdx] |= 1 << uint(7-(w.nbit%8))
		}
		w.nbit++
	}
}

// align pads with zero bits up to the next octet boundary.
func (w *bitWriter) align() {
	if r := w.nbit % 8; r != 0 {
		w.putBits(0, 8-r)
	}
}

// putOctets appends whole octets directly; the writer must already be
// octet-aligned (callers in this package always align() first).
func (w *bitWriter) putOctets(b []byte) {
	w.buf = append(w.buf, b...)
	w.nbit += len(b) * 8
}

func (w *bitWriter) bytes() []byte { return w.buf }

// bitReader unpacks MSB-first bits from a byte slice.
type bitReader struct {
	buf  []byte
	nbit int
}

func newBitReader(b []byte) *bitReader { return &bitReader{buf: b} }

func (r *bitReader) getBits(count int) (uint64, error) {
	var v uint64
	for i := 0; i < count; i++ {
		byteIdx := r.nbit / 8
		if byteIdx >= len(r.buf) {
			return 0, fmt.Errorf("sbcap: unexpected end of PDU while reading %d bits", count)
		}
		bit := (r.buf[byteIdx] >> uint(7-(r.nbit%8))) & 1
		v = v<<1 | uint64(bit)
		r.nbit++
	}
	return v, nil
}

func (r *bitReader) align() error {
	if rem := r.nbit % 8; rem != 0 {
		if _, err := r.getBits(8 - rem); err != nil {
			return err
		}
	}
	return nil
}

// readOctets reads n whole octets; the reader must already be octet-aligned.
func (r *bitReader) readOctets(n int) ([]byte, error) {
	if r.nbit%8 != 0 {
		return nil, fmt.Errorf("sbcap: reader not octet-aligned")
	}
	start := r.nbit / 8
	if n < 0 || start+n > len(r.buf) {
		return nil, fmt.Errorf("sbcap: unexpected end of PDU reading %d octets", n)
	}
	r.nbit += n * 8
	return r.buf[start : start+n], nil
}

// remainingBits reports how many unread bits remain, for detecting trailing
// garbage after decoding a complete PDU.
func (r *bitReader) remainingBits() int { return len(r.buf)*8 - r.nbit }
