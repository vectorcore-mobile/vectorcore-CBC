package sbcap

import "fmt"

// Procedure codes for the MME-originated indications this codec decodes
// (SBC-AP-Constants.asn). All three are initiating-message-only: the MME
// sends them, the CBC never replies. Restart/Failure are unsolicited eNB
// state reports; Write-Replace-Warning-Indication is solicited - it only
// arrives because Write() always sets Send-Write-Replace-Warning-Indication
// (idSendWriteReplaceWarningIndication) on the original request.
const (
	ProcedureWriteReplaceWarningIndication = 3
	ProcedurePWSRestartIndication          = 5
	ProcedurePWSFailureIndication          = 6
)

// Protocol-IE-ID values used by PWS-Restart-Indication / PWS-Failure-Indication.
const (
	idGlobalENBID       = 28
	idRestartedCellList = 30
	idListOfTAIsRestart = 31
	idFailedCellList    = 33
)

// Bounds from SBC-AP-Constants.asn: maxnoofRestartedCells=256,
// maxnoofRestartTAIs=2048, maxnoofFailedCells=256. These differ from the
// Warning-Area-List's ECGIList/TAI-List-for-Warning bounds (1..65535) - each
// SEQUENCE OF's count must use its own type's declared size constraint, not
// a shared assumption, since the constrained-whole-number bit width depends
// on the exact range (see aper.go's range buckets).
const (
	maxRestartedCells = 256
	maxRestartTAIs    = 2048
	maxFailedCells    = 256
)

// ENBID is the decoded ENB-ID CHOICE. Kind is one of "macro" (20-bit id),
// "home" (28-bit id), or the extension alternatives "short-macro" (18-bit)
// and "long-macro" (21-bit).
type ENBID struct {
	Kind  string
	Value uint32
	Bits  int
}

// decodeFixedBitString reads an n-bit fixed-size BIT STRING value. Per the
// aligned-PER "short fixed length" rule verified against OCTET_STRING_aper.c
// (X.691 §16 NOTE 1), a fixed BIT/OCTET STRING needing more than 2 octets is
// octet-aligned before its content; one needing <=2 octets is not.
func decodeFixedBitString(r *bitReader, bits int) (uint32, error) {
	if (bits+7)/8 > 2 {
		if err := r.align(); err != nil {
			return 0, err
		}
	}
	v, err := r.getBits(bits)
	return uint32(v), err
}

func decodeENBID(r *bitReader) (ENBID, error) {
	ext, err := r.getBits(1)
	if err != nil {
		return ENBID{}, err
	}
	if ext == 0 {
		idx, err := getConstrainedWholeNumber(r, 0, 1)
		if err != nil {
			return ENBID{}, err
		}
		if idx == 0 {
			v, err := decodeFixedBitString(r, 20)
			if err != nil {
				return ENBID{}, err
			}
			return ENBID{Kind: "macro", Value: v, Bits: 20}, nil
		}
		v, err := decodeFixedBitString(r, 28)
		if err != nil {
			return ENBID{}, err
		}
		return ENBID{Kind: "home", Value: v, Bits: 28}, nil
	}

	// Extension alternative: index is a "normally small non-negative whole
	// number" (X.691 §10.6), and per constr_CHOICE_aper.c's decode, the
	// selected extension value is itself open-type wrapped (unlike root
	// alternatives, which are not).
	idx, err := getNormallySmallNonNegativeWholeNumber(r)
	if err != nil {
		return ENBID{}, err
	}
	content, err := getOpenType(r)
	if err != nil {
		return ENBID{}, err
	}
	er := newBitReader(content)
	switch idx {
	case 0:
		v, err := decodeFixedBitString(er, 18)
		if err != nil {
			return ENBID{}, err
		}
		return ENBID{Kind: "short-macro", Value: v, Bits: 18}, nil
	case 1:
		v, err := decodeFixedBitString(er, 21)
		if err != nil {
			return ENBID{}, err
		}
		return ENBID{Kind: "long-macro", Value: v, Bits: 21}, nil
	default:
		return ENBID{}, fmt.Errorf("sbcap: unsupported ENB-ID extension alternative %d", idx)
	}
}

// getNormallySmallNonNegativeWholeNumber implements X.691 §10.6 (verified
// against aper_get_nsnnwn in the reference runtime): a single bit selects
// between a 6-bit inline value (0..63) and an aligned, length-prefixed
// larger value. Only the common small-value and one-octet cases are
// supported; anything else is out of scope and errors rather than
// mis-decoding.
func getNormallySmallNonNegativeWholeNumber(r *bitReader) (int64, error) {
	b, err := r.getBits(1)
	if err != nil {
		return 0, err
	}
	if b == 0 {
		v, err := r.getBits(6)
		return int64(v), err
	}
	if err := r.align(); err != nil {
		return 0, err
	}
	b2, err := r.getBits(1)
	if err != nil {
		return 0, err
	}
	if b2 == 1 {
		return 0, fmt.Errorf("sbcap: unsupported large normally-small-non-negative-whole-number encoding")
	}
	length, err := r.getBits(7)
	if err != nil {
		return 0, err
	}
	if length > 4 {
		return 0, fmt.Errorf("sbcap: normally-small-non-negative-whole-number length %d exceeds the supported 4 octets", length)
	}
	v, err := r.getBits(int(length) * 8)
	return int64(v), err
}

// GlobalENBID identifies the eNB that sent an indication.
type GlobalENBID struct {
	PLMN  []byte
	ENBID ENBID
}

// decodeGlobalENBID implements Global-ENB-ID ::= SEQUENCE{pLMNidentity,
// eNB-ID, iE-Extensions OPTIONAL, ...} - extensible, one optional field.
func decodeGlobalENBID(r *bitReader) (GlobalENBID, error) {
	ext, err := r.getBits(1)
	if err != nil {
		return GlobalENBID{}, err
	}
	if ext != 0 {
		return GlobalENBID{}, fmt.Errorf("sbcap: Global-ENB-ID extension additions are not supported")
	}
	optExt, err := r.getBits(1)
	if err != nil {
		return GlobalENBID{}, err
	}
	if optExt != 0 {
		return GlobalENBID{}, fmt.Errorf("sbcap: Global-ENB-ID iE-Extensions are not supported")
	}
	if err := r.align(); err != nil {
		return GlobalENBID{}, err
	}
	plmn, err := r.readOctets(3)
	if err != nil {
		return GlobalENBID{}, err
	}
	enbID, err := decodeENBID(r)
	if err != nil {
		return GlobalENBID{}, err
	}
	return GlobalENBID{PLMN: append([]byte(nil), plmn...), ENBID: enbID}, nil
}

type ecgiEntry struct {
	plmn []byte
	eci  uint32
}

// decodeECGIListBounded decodes a SEQUENCE(SIZE(lb..ub)) OF EUTRAN-CGI -
// shared by Restarted-Cell-List and Failed-Cell-List, which have the same
// element type but must each use their own declared size constraint.
func decodeECGIListBounded(content []byte, lb, ub int64) ([]ecgiEntry, error) {
	r := newBitReader(content)
	count, err := getConstrainedWholeNumber(r, lb, ub)
	if err != nil {
		return nil, err
	}
	out := make([]ecgiEntry, 0, count)
	for i := int64(0); i < count; i++ {
		plmn, eci, err := decodeEUTRANCGI(r)
		if err != nil {
			return nil, err
		}
		out = append(out, ecgiEntry{plmn, eci})
	}
	return out, nil
}

type taiEntry struct {
	plmn []byte
	tac  uint16
}

// decodeTAIRestartList decodes List-of-TAIs-Restart ::=
// SEQUENCE(SIZE(1..maxnoofRestartTAIs)) OF SEQUENCE{tai: TAI}. The wrapper
// SEQUENCE has one mandatory field and is not extensible, so it contributes
// no preamble bits - decoding a member is bit-for-bit identical to decoding
// a bare TAI.
func decodeTAIRestartList(content []byte) ([]taiEntry, error) {
	r := newBitReader(content)
	count, err := getConstrainedWholeNumber(r, 1, maxRestartTAIs)
	if err != nil {
		return nil, err
	}
	out := make([]taiEntry, 0, count)
	for i := int64(0); i < count; i++ {
		plmn, tac, err := decodeTAI(r)
		if err != nil {
			return nil, err
		}
		out = append(out, taiEntry{plmn, tac})
	}
	return out, nil
}

// RestartedCell and RestartedTAI are the public, per-entry decoded forms
// used by RestartIndication.
type RestartedCell struct {
	PLMN []byte
	ECI  uint32
}
type RestartedTAI struct {
	PLMN []byte
	TAC  uint16
}

// RestartIndication is the decoded form of a PWS-Restart-Indication: an eNB
// identified by GlobalENBID has restarted and lost its broadcast state for
// RestartedCells / RestartedTAIs.
type RestartIndication struct {
	GlobalENBID    GlobalENBID
	RestartedCells []RestartedCell
	RestartedTAIs  []RestartedTAI
}

// DecodeRestartIndication decodes a PWS-Restart-Indication PDU. All three
// IEs (Restarted-Cell-List, Global-ENB-ID, List-of-TAIs-Restart) are
// mandatory per TS 29.168; List-of-EAIs-Restart (optional, Emergency-Area-ID
// targeting) is not implemented, matching the rest of this codebase's scope.
func DecodeRestartIndication(pdu []byte) (*RestartIndication, error) {
	decoded, err := decodePDU(pdu)
	if err != nil {
		return nil, err
	}
	if decoded.choiceIndex != pduInitiating || int(decoded.procedureCode) != ProcedurePWSRestartIndication {
		return nil, fmt.Errorf("sbcap: PDU is not a PWS-Restart-Indication")
	}

	cellsIE, ok := findIE(decoded.ies, idRestartedCellList)
	if !ok {
		return nil, fmt.Errorf("sbcap: PWS-Restart-Indication missing mandatory Restarted-Cell-List")
	}
	cells, err := decodeECGIListBounded(cellsIE.value, 1, maxRestartedCells)
	if err != nil {
		return nil, fmt.Errorf("sbcap: decode Restarted-Cell-List: %w", err)
	}

	enbIE, ok := findIE(decoded.ies, idGlobalENBID)
	if !ok {
		return nil, fmt.Errorf("sbcap: PWS-Restart-Indication missing mandatory Global-ENB-ID")
	}
	genb, err := decodeGlobalENBID(newBitReader(enbIE.value))
	if err != nil {
		return nil, fmt.Errorf("sbcap: decode Global-ENB-ID: %w", err)
	}

	taisIE, ok := findIE(decoded.ies, idListOfTAIsRestart)
	if !ok {
		return nil, fmt.Errorf("sbcap: PWS-Restart-Indication missing mandatory List-of-TAIs-Restart")
	}
	tais, err := decodeTAIRestartList(taisIE.value)
	if err != nil {
		return nil, fmt.Errorf("sbcap: decode List-of-TAIs-Restart: %w", err)
	}

	ri := &RestartIndication{GlobalENBID: genb}
	for _, c := range cells {
		ri.RestartedCells = append(ri.RestartedCells, RestartedCell{PLMN: c.plmn, ECI: c.eci})
	}
	for _, t := range tais {
		ri.RestartedTAIs = append(ri.RestartedTAIs, RestartedTAI{PLMN: t.plmn, TAC: t.tac})
	}
	return ri, nil
}

// FailedCell is the public per-entry decoded form used by FailureIndication.
type FailedCell struct {
	PLMN []byte
	ECI  uint32
}

// FailureIndication is the decoded form of a PWS-Failure-Indication: the eNB
// identified by GlobalENBID could not broadcast to FailedCells.
type FailureIndication struct {
	GlobalENBID GlobalENBID
	FailedCells []FailedCell
}

// DecodeFailureIndication decodes a PWS-Failure-Indication PDU. Both IEs
// (Failed-Cell-List, Global-ENB-ID) are mandatory per TS 29.168.
func DecodeFailureIndication(pdu []byte) (*FailureIndication, error) {
	decoded, err := decodePDU(pdu)
	if err != nil {
		return nil, err
	}
	if decoded.choiceIndex != pduInitiating || int(decoded.procedureCode) != ProcedurePWSFailureIndication {
		return nil, fmt.Errorf("sbcap: PDU is not a PWS-Failure-Indication")
	}

	cellsIE, ok := findIE(decoded.ies, idFailedCellList)
	if !ok {
		return nil, fmt.Errorf("sbcap: PWS-Failure-Indication missing mandatory Failed-Cell-List")
	}
	cells, err := decodeECGIListBounded(cellsIE.value, 1, maxFailedCells)
	if err != nil {
		return nil, fmt.Errorf("sbcap: decode Failed-Cell-List: %w", err)
	}

	enbIE, ok := findIE(decoded.ies, idGlobalENBID)
	if !ok {
		return nil, fmt.Errorf("sbcap: PWS-Failure-Indication missing mandatory Global-ENB-ID")
	}
	genb, err := decodeGlobalENBID(newBitReader(enbIE.value))
	if err != nil {
		return nil, fmt.Errorf("sbcap: decode Global-ENB-ID: %w", err)
	}

	fi := &FailureIndication{GlobalENBID: genb}
	for _, c := range cells {
		fi.FailedCells = append(fi.FailedCells, FailedCell{PLMN: c.plmn, ECI: c.eci})
	}
	return fi, nil
}

// WriteReplaceWarningIndication is the decoded form of a Write-Replace-
// Warning-Indication: the MME's confirmation that (MessageIdentifier,
// SerialNumber) has been scheduled for broadcast. Broadcast-Scheduled-Area-
// List (which cells/TAs specifically) is optional per TS 29.168 and not
// decoded, matching this codec's existing scope for other optional,
// deeper-nested area lists (e.g. RestartIndication's List-of-EAIs-Restart).
type WriteReplaceWarningIndication struct {
	MessageIdentifier uint16
	SerialNumber      uint16
}

// DecodeWriteReplaceWarningIndication decodes a Write-Replace-Warning-
// Indication PDU. Message-Identifier and Serial-Number are both mandatory
// per TS 29.168.
func DecodeWriteReplaceWarningIndication(pdu []byte) (*WriteReplaceWarningIndication, error) {
	decoded, err := decodePDU(pdu)
	if err != nil {
		return nil, err
	}
	if decoded.choiceIndex != pduInitiating || int(decoded.procedureCode) != ProcedureWriteReplaceWarningIndication {
		return nil, fmt.Errorf("sbcap: PDU is not a Write-Replace-Warning-Indication")
	}
	msgIE, ok := findIE(decoded.ies, idMessageIdentifier)
	if !ok {
		return nil, fmt.Errorf("sbcap: Write-Replace-Warning-Indication missing mandatory Message-Identifier")
	}
	serialIE, ok := findIE(decoded.ies, idSerialNumber)
	if !ok {
		return nil, fmt.Errorf("sbcap: Write-Replace-Warning-Indication missing mandatory Serial-Number")
	}
	msgID, err := decode16(msgIE.value)
	if err != nil {
		return nil, err
	}
	serial, err := decode16(serialIE.value)
	if err != nil {
		return nil, err
	}
	return &WriteReplaceWarningIndication{MessageIdentifier: msgID, SerialNumber: serial}, nil
}
