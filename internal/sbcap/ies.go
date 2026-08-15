package sbcap

import "fmt"

// Protocol-IE-ID values (SBC-AP-Constants.asn) for the IEs this codec
// implements.
const (
	idCause                             = 1
	idDataCodingScheme                  = 3
	idMessageIdentifier                 = 5
	idNumberOfBroadcastsRequested       = 7
	idRepetitionPeriod                  = 10
	idSerialNumber                      = 11
	idWarningAreaList                   = 15
	idWarningMessageContent             = 16
	idConcurrentWarningMessageIndicator = 20
	idSendWriteReplaceWarningIndication = 24
)

// Cause values (SBC-AP-IEs.asn: INTEGER(0..255) with named values). Only the
// ones this codec's callers actually produce/consume are named; any other
// value in range decodes fine, it's just reported numerically.
const (
	CauseMessageAccepted         = 0
	CauseParameterNotRecognised  = 1
	CauseParameterValueInvalid   = 2
	CauseMissingMandatoryElement = 6
	CauseUnspecifiedError        = 12
)

// fixedOctets16 / fixedOctets8 encode BIT STRING(SIZE(16))/SIZE(8) values
// (Message-Identifier, Serial-Number, Data-Coding-Scheme): a fixed size of
// <=2 octets packs with no alignment, matching asn1c's "X.691 #16 NOTE 1"
// short fixed-length rule (verified in OCTET_STRING_aper.c).
func encode16(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

func decode16(b []byte) (uint16, error) {
	if len(b) != 2 {
		return 0, fmt.Errorf("sbcap: expected a 2-octet fixed bit string, got %d octets", len(b))
	}
	return uint16(b[0])<<8 | uint16(b[1]), nil
}

func encode8(v byte) []byte { return []byte{v} }

func decode8(b []byte) (byte, error) {
	if len(b) != 1 {
		return 0, fmt.Errorf("sbcap: expected a 1-octet fixed bit string, got %d octets", len(b))
	}
	return b[0], nil
}

// encodeConstrainedInt/decodeConstrainedInt handle the small INTEGER IEs
// (Repetition-Period, Number-of-Broadcasts-Requested, Cause), each its own
// independently open-type-wrapped IE value.
func encodeConstrainedInt(lb, ub, value int64) ([]byte, error) {
	w := &bitWriter{}
	if err := putConstrainedWholeNumber(w, lb, ub, value); err != nil {
		return nil, err
	}
	return w.bytes(), nil
}

func decodeConstrainedInt(content []byte, lb, ub int64) (int64, error) {
	return getConstrainedWholeNumber(newBitReader(content), lb, ub)
}

// encodeWarningMessageContent / decodeWarningMessageContent implement
// Warning-Message-Content ::= OCTET STRING (SIZE(1..9600)): a variable-size
// octet string carries its own length determinant (range 1..9600 falls in
// the 257..65536 aligned two-octet bucket) ahead of the raw content.
func encodeWarningMessageContent(data []byte) ([]byte, error) {
	w := &bitWriter{}
	if err := putConstrainedWholeNumber(w, 1, 9600, int64(len(data))); err != nil {
		return nil, err
	}
	w.align()
	w.putOctets(data)
	return w.bytes(), nil
}

func decodeWarningMessageContent(content []byte) ([]byte, error) {
	r := newBitReader(content)
	n, err := getConstrainedWholeNumber(r, 1, 9600)
	if err != nil {
		return nil, err
	}
	if err := r.align(); err != nil {
		return nil, err
	}
	return r.readOctets(int(n))
}

// encodeEUTRANCGI / decodeEUTRANCGI implement EUTRAN-CGI ::= SEQUENCE {
// pLMNidentity PLMNidentity, cell-ID CellIdentity, iE-Extensions OPTIONAL, ... }.
// PLMNidentity (3 fixed octets) and CellIdentity (28 fixed bits = 4 octets)
// both exceed the 2-octet "short fixed length" threshold, so each is
// individually octet-aligned before its content (verified in
// OCTET_STRING_aper.c: "if (st->size > 2 ...) align").
func encodeEUTRANCGI(w *bitWriter, plmn []byte, eci uint32) error {
	if len(plmn) != 3 {
		return fmt.Errorf("sbcap: PLMN identity must be exactly 3 octets")
	}
	if eci > 0x0fffffff {
		return fmt.Errorf("sbcap: ECI %d exceeds the 28-bit E-UTRAN Cell Identity range", eci)
	}
	w.putBits(0, 1) // EUTRAN-CGI SEQUENCE extension marker: root
	w.putBits(0, 1) // iE-Extensions OPTIONAL presence: absent
	w.align()
	w.putOctets(plmn)
	w.align()
	w.putBits(uint64(eci), 28)
	return nil
}

func decodeEUTRANCGI(r *bitReader) (plmn []byte, eci uint32, err error) {
	ext, err := r.getBits(1)
	if err != nil {
		return nil, 0, err
	}
	if ext != 0 {
		return nil, 0, fmt.Errorf("sbcap: EUTRAN-CGI extension additions are not supported")
	}
	optExt, err := r.getBits(1)
	if err != nil {
		return nil, 0, err
	}
	if optExt != 0 {
		return nil, 0, fmt.Errorf("sbcap: EUTRAN-CGI iE-Extensions are not supported")
	}
	if err := r.align(); err != nil {
		return nil, 0, err
	}
	plmnBytes, err := r.readOctets(3)
	if err != nil {
		return nil, 0, err
	}
	if err := r.align(); err != nil {
		return nil, 0, err
	}
	v, err := r.getBits(28)
	if err != nil {
		return nil, 0, err
	}
	return append([]byte(nil), plmnBytes...), uint32(v), nil
}

// encodeTAI / decodeTAI implement TAI ::= SEQUENCE { pLMNidentity
// PLMNidentity, tAC TAC, iE-Extensions OPTIONAL } (not extensible - no "..."
// in the ASN.1). TAC is a fixed 2-octet string, at the 2-octet threshold, so
// it does not need its own alignment (and is already aligned here since it
// immediately follows the 3-octet, alignment-terminated PLMN field).
func encodeTAI(w *bitWriter, plmn []byte, tac uint16) error {
	if len(plmn) != 3 {
		return fmt.Errorf("sbcap: PLMN identity must be exactly 3 octets")
	}
	w.putBits(0, 1) // iE-Extensions OPTIONAL presence: absent
	w.align()
	w.putOctets(plmn)
	w.putBits(uint64(tac), 16)
	return nil
}

func decodeTAI(r *bitReader) (plmn []byte, tac uint16, err error) {
	optExt, err := r.getBits(1)
	if err != nil {
		return nil, 0, err
	}
	if optExt != 0 {
		return nil, 0, fmt.Errorf("sbcap: TAI iE-Extensions are not supported")
	}
	if err := r.align(); err != nil {
		return nil, 0, err
	}
	plmnBytes, err := r.readOctets(3)
	if err != nil {
		return nil, 0, err
	}
	v, err := r.getBits(16)
	if err != nil {
		return nil, 0, err
	}
	return append([]byte(nil), plmnBytes...), uint16(v), nil
}

// Warning-Area-List CHOICE root-alternative indices.
const (
	warningAreaCellIDList    = 0
	warningAreaTrackingAreas = 1
)

// encodeWarningAreaListCells / encodeWarningAreaListTAIs build the
// Warning-Area-List IE value (cell-wide or tracking-area-wide targeting).
// ECGIList/TAI-List-for-Warning are SEQUENCE(SIZE(1..65535)) OF, whose count
// falls in the 257..65536 aligned two-octet bucket.
func encodeWarningAreaListCells(plmn []byte, ecis []uint32) ([]byte, error) {
	w := &bitWriter{}
	w.putBits(0, 1)
	if err := putConstrainedWholeNumber(w, 0, 2, warningAreaCellIDList); err != nil {
		return nil, err
	}
	if err := putConstrainedWholeNumber(w, 1, 65535, int64(len(ecis))); err != nil {
		return nil, err
	}
	for _, eci := range ecis {
		if err := encodeEUTRANCGI(w, plmn, eci); err != nil {
			return nil, err
		}
	}
	return w.bytes(), nil
}

func encodeWarningAreaListTAIs(plmn []byte, tacs []uint16) ([]byte, error) {
	w := &bitWriter{}
	w.putBits(0, 1)
	if err := putConstrainedWholeNumber(w, 0, 2, warningAreaTrackingAreas); err != nil {
		return nil, err
	}
	if err := putConstrainedWholeNumber(w, 1, 65535, int64(len(tacs))); err != nil {
		return nil, err
	}
	for _, tac := range tacs {
		if err := encodeTAI(w, plmn, tac); err != nil {
			return nil, err
		}
	}
	return w.bytes(), nil
}

// warningArea is the decoded form of a Warning-Area-List IE value.
type warningArea struct {
	isCellList bool
	plmn       []byte
	ecis       []uint32
	tacs       []uint16
}

func decodeWarningAreaList(content []byte) (*warningArea, error) {
	r := newBitReader(content)
	ext, err := r.getBits(1)
	if err != nil {
		return nil, err
	}
	if ext != 0 {
		return nil, fmt.Errorf("sbcap: Warning-Area-List extension alternatives are not supported")
	}
	idx, err := getConstrainedWholeNumber(r, 0, 2)
	if err != nil {
		return nil, err
	}
	switch idx {
	case warningAreaCellIDList:
		count, err := getConstrainedWholeNumber(r, 1, 65535)
		if err != nil {
			return nil, err
		}
		wa := &warningArea{isCellList: true}
		for i := int64(0); i < count; i++ {
			plmn, eci, err := decodeEUTRANCGI(r)
			if err != nil {
				return nil, err
			}
			wa.plmn = plmn
			wa.ecis = append(wa.ecis, eci)
		}
		return wa, nil
	case warningAreaTrackingAreas:
		count, err := getConstrainedWholeNumber(r, 1, 65535)
		if err != nil {
			return nil, err
		}
		wa := &warningArea{isCellList: false}
		for i := int64(0); i < count; i++ {
			plmn, tac, err := decodeTAI(r)
			if err != nil {
				return nil, err
			}
			wa.plmn = plmn
			wa.tacs = append(wa.tacs, tac)
		}
		return wa, nil
	default:
		return nil, fmt.Errorf("sbcap: unsupported Warning-Area-List alternative %d (only cell-ID-List and tracking-Area-List-for-Warning are implemented)", idx)
	}
}
