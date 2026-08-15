package sbcap

import "fmt"

// putConstrainedWholeNumber implements the Aligned PER encoding of a
// constrained whole number in [lb,ub] (X.691 §10.5, verified against a
// reference ASN.1 Aligned PER runtime's constrained-whole-number routines):
//
//   - range == 1:        zero bits (the value is implied).
//   - range in 2..255:   minimal bits (ceil(log2(range))), no alignment -
//     this is how CHOICE indices and small ENUMERATEDs pack tight.
//   - range == 256:      octet-align, then exactly 8 bits.
//   - range in 257..65536: octet-align, then exactly 16 bits.
//
// Wider ranges never occur in the SBcAP subset this codec implements and
// return an error rather than silently mis-encoding.
func putConstrainedWholeNumber(w *bitWriter, lb, ub, value int64) error {
	if value < lb || value > ub {
		return fmt.Errorf("sbcap: value %d out of range [%d,%d]", value, lb, ub)
	}
	rng := ub - lb + 1
	v := uint64(value - lb)
	switch {
	case rng == 1:
		return nil
	case rng <= 255:
		w.putBits(v, bitsFor(rng))
		return nil
	case rng == 256:
		w.align()
		w.putBits(v, 8)
		return nil
	case rng <= 65536:
		w.align()
		w.putBits(v, 16)
		return nil
	default:
		return fmt.Errorf("sbcap: constrained whole number range %d exceeds the supported 65536", rng)
	}
}

func getConstrainedWholeNumber(r *bitReader, lb, ub int64) (int64, error) {
	rng := ub - lb + 1
	switch {
	case rng == 1:
		return lb, nil
	case rng <= 255:
		v, err := r.getBits(bitsFor(rng))
		if err != nil {
			return 0, err
		}
		return lb + int64(v), nil
	case rng == 256:
		if err := r.align(); err != nil {
			return 0, err
		}
		v, err := r.getBits(8)
		if err != nil {
			return 0, err
		}
		return lb + int64(v), nil
	case rng <= 65536:
		if err := r.align(); err != nil {
			return 0, err
		}
		v, err := r.getBits(16)
		if err != nil {
			return 0, err
		}
		return lb + int64(v), nil
	default:
		return 0, fmt.Errorf("sbcap: constrained whole number range %d exceeds the supported 65536", rng)
	}
}

// bitsFor returns the minimum bits needed to represent 0..rng-1 for
// 2 <= rng <= 255, matching asn1c's bit-field sizing (X.691 §10.5.7.1).
func bitsFor(rng int64) int {
	n := 0
	for (int64(1) << uint(n)) < rng {
		n++
	}
	return n
}

// putOpenType octet-aligns, writes the ASN.1 open-type length determinant
// (1 octet if len(content)<=127, 2 octets with a "10" prefix if <16384),
// then the content. Every SBcAP message body and every individual
// Protocol-IE value is wrapped this way. Content at or beyond 16384 octets
// is out of scope (no CBS warning message this codec builds gets close).
//
// A value that itself encodes to zero bits (this codec's only case: the
// ENUMERATED{true} IEs) is not written as a zero-length open type: the
// reference ASN.1 runtime this codec's wire format was verified against
// always emits a minimum one-byte placeholder for a zero-bit value (X.691
// §10.1.3's "all-zero-bits padding" case), so an empty open type is
// widened to a single zero byte to match real-world interop expectations.
func putOpenType(w *bitWriter, content []byte) error {
	w.align()
	if len(content) == 0 {
		content = []byte{0}
	}
	n := len(content)
	switch {
	case n <= 127:
		w.putBits(uint64(n), 8)
	case n < 16384:
		w.putBits(uint64(n)|0x8000, 16)
	default:
		return fmt.Errorf("sbcap: open type content length %d exceeds the supported 16384", n)
	}
	w.putOctets(content)
	return nil
}

func getOpenType(r *bitReader) ([]byte, error) {
	if err := r.align(); err != nil {
		return nil, err
	}
	first, err := r.getBits(8)
	if err != nil {
		return nil, err
	}
	var n int
	if first&0x80 == 0 {
		n = int(first & 0x7f)
	} else if first&0x40 == 0 {
		second, err := r.getBits(8)
		if err != nil {
			return nil, err
		}
		n = int((first&0x3f)<<8 | second)
	} else {
		return nil, fmt.Errorf("sbcap: fragmented open type content is not supported")
	}
	return r.readOctets(n)
}
