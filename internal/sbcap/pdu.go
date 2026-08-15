package sbcap

import "fmt"

// Criticality values (SBC-AP-CommonDataTypes.asn: ENUMERATED{reject,ignore,notify}).
const (
	critReject = 0
	critIgnore = 1
	critNotify = 2
)

// SBC-AP-PDU CHOICE indices (SBC-AP-PDU-Descriptions.asn).
const (
	pduInitiating   = 0
	pduSuccessful   = 1
	pduUnsuccessful = 2
)

// Elementary procedure codes (SBC-AP-Constants.asn).
const (
	procWriteReplaceWarning = 0
	procStopWarning         = 1
	procErrorIndication     = 2
)

// protocolIE is one ProtocolIE-Field: an id/criticality pair plus its
// already-encoded value content (wrapped as an open type when written).
type protocolIE struct {
	id          int64
	criticality int64
	value       []byte
}

func putIEContainer(w *bitWriter, ies []protocolIE) error {
	if err := putConstrainedWholeNumber(w, 0, 65535, int64(len(ies))); err != nil {
		return err
	}
	for _, ie := range ies {
		if err := putConstrainedWholeNumber(w, 0, 65535, ie.id); err != nil {
			return err
		}
		if err := putConstrainedWholeNumber(w, 0, 2, ie.criticality); err != nil {
			return err
		}
		if err := putOpenType(w, ie.value); err != nil {
			return err
		}
	}
	return nil
}

func getIEContainer(r *bitReader) ([]protocolIE, error) {
	count, err := getConstrainedWholeNumber(r, 0, 65535)
	if err != nil {
		return nil, err
	}
	ies := make([]protocolIE, 0, count)
	for i := int64(0); i < count; i++ {
		id, err := getConstrainedWholeNumber(r, 0, 65535)
		if err != nil {
			return nil, err
		}
		crit, err := getConstrainedWholeNumber(r, 0, 2)
		if err != nil {
			return nil, err
		}
		value, err := getOpenType(r)
		if err != nil {
			return nil, err
		}
		ies = append(ies, protocolIE{id: id, criticality: crit, value: value})
	}
	return ies, nil
}

func findIE(ies []protocolIE, id int64) (protocolIE, bool) {
	for _, ie := range ies {
		if ie.id == id {
			return ie, true
		}
	}
	return protocolIE{}, false
}

// encodeMessageBody builds one of Write-Replace-Warning-Request/Response,
// Stop-Warning-Request/Response, or Error-Indication: an extensible SEQUENCE
// of {protocolIEs, protocolExtensions OPTIONAL, ...} where protocolExtensions
// is always absent (this codec does not implement any protocol extension).
func encodeMessageBody(ies []protocolIE) ([]byte, error) {
	w := &bitWriter{}
	w.putBits(0, 1) // SEQUENCE extension marker: no extension additions used
	w.putBits(0, 1) // protocolExtensions OPTIONAL presence bit: absent
	if err := putIEContainer(w, ies); err != nil {
		return nil, err
	}
	return w.bytes(), nil
}

func decodeMessageBody(content []byte) ([]protocolIE, error) {
	r := newBitReader(content)
	ext, err := r.getBits(1)
	if err != nil {
		return nil, err
	}
	if ext != 0 {
		return nil, fmt.Errorf("sbcap: message body extension additions are not supported")
	}
	extPresent, err := r.getBits(1)
	if err != nil {
		return nil, err
	}
	if extPresent != 0 {
		return nil, fmt.Errorf("sbcap: protocolExtensions are not supported")
	}
	return getIEContainer(r)
}

// encodePDU builds a complete SBC-AP-PDU: the CHOICE selector (extension bit
// + root-alternative index), then the InitiatingMessage/SuccessfulOutcome/
// UnsuccessfulOutcome envelope (procedureCode, criticality, value = open
// type wrapping the message body).
func encodePDU(choiceIndex, procedureCode, criticality int64, ies []protocolIE) ([]byte, error) {
	body, err := encodeMessageBody(ies)
	if err != nil {
		return nil, err
	}
	w := &bitWriter{}
	w.putBits(0, 1) // SBC-AP-PDU CHOICE extension marker: root alternative
	if err := putConstrainedWholeNumber(w, 0, 2, choiceIndex); err != nil {
		return nil, err
	}
	if err := putConstrainedWholeNumber(w, 0, 255, procedureCode); err != nil {
		return nil, err
	}
	if err := putConstrainedWholeNumber(w, 0, 2, criticality); err != nil {
		return nil, err
	}
	if err := putOpenType(w, body); err != nil {
		return nil, err
	}
	return w.bytes(), nil
}

// decodedPDU is the generic envelope shared by every SBC-AP-PDU this codec
// decodes.
type decodedPDU struct {
	choiceIndex   int64
	procedureCode int64
	criticality   int64
	ies           []protocolIE
}

func decodePDU(pdu []byte) (*decodedPDU, error) {
	if len(pdu) == 0 {
		return nil, fmt.Errorf("sbcap: empty PDU")
	}
	r := newBitReader(pdu)
	ext, err := r.getBits(1)
	if err != nil {
		return nil, err
	}
	if ext != 0 {
		return nil, fmt.Errorf("sbcap: SBC-AP-PDU extension alternatives are not supported")
	}
	idx, err := getConstrainedWholeNumber(r, 0, 2)
	if err != nil {
		return nil, err
	}
	proc, err := getConstrainedWholeNumber(r, 0, 255)
	if err != nil {
		return nil, err
	}
	crit, err := getConstrainedWholeNumber(r, 0, 2)
	if err != nil {
		return nil, err
	}
	body, err := getOpenType(r)
	if err != nil {
		return nil, err
	}
	ies, err := decodeMessageBody(body)
	if err != nil {
		return nil, err
	}
	return &decodedPDU{choiceIndex: idx, procedureCode: proc, criticality: crit, ies: ies}, nil
}
