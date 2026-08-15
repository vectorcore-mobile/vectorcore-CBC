package sbcap

import "fmt"

const (
	ProcedureWriteReplace = 0
	ProcedureStop         = 1

	// Header's outcome classification matches asn1c's 1-indexed
	// SBC-AP-PDU CHOICE `present` enum (0 is reserved for "unset").
	OutcomeInitiating   = 1
	OutcomeSuccessful   = 2
	OutcomeUnsuccessful = 3
)

// buildWarningAreaIE returns the Warning-Area-List IE for scope 2
// (tracking-area-wide, ids are TACs) or 3 (cell-wide, ids are 28-bit ECIs),
// or nil for scope 1 (PLMN-wide: no Warning-Area-List IE at all).
func buildWarningAreaIE(scope int, plmn []byte, ids []uint32) (*protocolIE, error) {
	switch scope {
	case 1:
		return nil, nil
	case 2:
		tacs := make([]uint16, len(ids))
		for i, id := range ids {
			if id > 0xffff {
				return nil, fmt.Errorf("sbcap: TAC %d exceeds the 16-bit TAC range", id)
			}
			tacs[i] = uint16(id)
		}
		value, err := encodeWarningAreaListTAIs(plmn, tacs)
		if err != nil {
			return nil, err
		}
		return &protocolIE{id: idWarningAreaList, criticality: critIgnore, value: value}, nil
	case 3:
		value, err := encodeWarningAreaListCells(plmn, ids)
		if err != nil {
			return nil, err
		}
		return &protocolIE{id: idWarningAreaList, criticality: critIgnore, value: value}, nil
	default:
		return nil, fmt.Errorf("sbcap: invalid SBcAP warning area scope %d", scope)
	}
}

// WriteReplace encodes a TS 29.168 Write-Replace-Warning-Request. contents
// must be the TS 23.041 page container: page count, then 82 octets and a
// used-length octet for each page.
func WriteReplace(messageID, serial uint16, dcs byte, contents []byte, repetitionPeriod, broadcasts uint16) ([]byte, error) {
	return WriteReplaceTarget(messageID, serial, dcs, contents, repetitionPeriod, broadcasts, 1, nil, nil)
}

// WriteReplaceTarget encodes a PLMN (1), tracking-area (2), or E-UTRAN cell
// (3) Warning-Area-List. ids are TACs for scope 2 and 28-bit ECIs for scope 3.
func WriteReplaceTarget(messageID, serial uint16, dcs byte, contents []byte, repetitionPeriod, broadcasts uint16, scope int, plmn []byte, ids []uint32) ([]byte, error) {
	if len(contents) == 0 || len(contents) > 9600 {
		return nil, fmt.Errorf("invalid warning message contents length %d", len(contents))
	}
	if scope < 1 || scope > 3 || (scope != 1 && (len(plmn) != 3 || len(ids) == 0)) {
		return nil, fmt.Errorf("invalid SBcAP warning area")
	}

	msgContent, err := encodeWarningMessageContent(contents)
	if err != nil {
		return nil, err
	}
	repContent, err := encodeConstrainedInt(0, 4096, int64(repetitionPeriod))
	if err != nil {
		return nil, err
	}
	broadcastsContent, err := encodeConstrainedInt(0, 65535, int64(broadcasts))
	if err != nil {
		return nil, err
	}

	ies := []protocolIE{
		{id: idMessageIdentifier, criticality: critReject, value: encode16(messageID)},
		{id: idSerialNumber, criticality: critReject, value: encode16(serial)},
	}
	wa, err := buildWarningAreaIE(scope, plmn, ids)
	if err != nil {
		return nil, err
	}
	if wa != nil {
		ies = append(ies, *wa)
	}
	ies = append(ies,
		protocolIE{id: idRepetitionPeriod, criticality: critReject, value: repContent},
		protocolIE{id: idNumberOfBroadcastsRequested, criticality: critReject, value: broadcastsContent},
		protocolIE{id: idDataCodingScheme, criticality: critIgnore, value: encode8(dcs)},
		protocolIE{id: idWarningMessageContent, criticality: critIgnore, value: msgContent},
		protocolIE{id: idConcurrentWarningMessageIndicator, criticality: critReject, value: []byte{}},
		protocolIE{id: idSendWriteReplaceWarningIndication, criticality: critIgnore, value: []byte{}},
	)
	return encodePDU(pduInitiating, procWriteReplaceWarning, critReject, ies)
}

// Stop encodes a TS 29.168 Stop-Warning-Request for a previously allocated
// message identifier and serial number.
func Stop(messageID, serial uint16) ([]byte, error) {
	return StopTarget(messageID, serial, 1, nil, nil)
}

// StopTarget preserves the original Warning-Area-List when cancelling a
// broadcast.
func StopTarget(messageID, serial uint16, scope int, plmn []byte, ids []uint32) ([]byte, error) {
	if scope < 1 || scope > 3 || (scope != 1 && (len(plmn) != 3 || len(ids) == 0)) {
		return nil, fmt.Errorf("invalid SBcAP warning area")
	}
	ies := []protocolIE{
		{id: idMessageIdentifier, criticality: critReject, value: encode16(messageID)},
		{id: idSerialNumber, criticality: critReject, value: encode16(serial)},
	}
	wa, err := buildWarningAreaIE(scope, plmn, ids)
	if err != nil {
		return nil, err
	}
	if wa != nil {
		ies = append(ies, *wa)
	}
	return encodePDU(pduInitiating, procStopWarning, critReject, ies)
}

// ErrorIndication encodes a TS 29.168 Error-Indication with an optional
// Cause IE (Criticality-Diagnostics, also optional, is never emitted - this
// codec does not implement it).
func ErrorIndication(cause int) ([]byte, error) {
	causeContent, err := encodeConstrainedInt(0, 255, int64(cause))
	if err != nil {
		return nil, err
	}
	ies := []protocolIE{{id: idCause, criticality: critIgnore, value: causeContent}}
	return encodePDU(pduInitiating, procErrorIndication, critIgnore, ies)
}

// DecodeErrorIndication extracts the optional Cause IE from an
// Error-Indication PDU. hasCause is false if the sender omitted it (it is
// optional per TS 29.168).
func DecodeErrorIndication(pdu []byte) (cause int, hasCause bool, err error) {
	decoded, err := decodePDU(pdu)
	if err != nil {
		return 0, false, err
	}
	if decoded.choiceIndex != pduInitiating || int(decoded.procedureCode) != procErrorIndication {
		return 0, false, fmt.Errorf("sbcap: PDU is not an Error-Indication")
	}
	ie, ok := findIE(decoded.ies, idCause)
	if !ok {
		return 0, false, nil
	}
	v, err := decodeConstrainedInt(ie.value, 0, 255)
	if err != nil {
		return 0, false, err
	}
	return int(v), true, nil
}

// Header decodes the APER envelope and returns its outcome class and TS
// 29.168 procedure code. It is used to validate MME responses.
func Header(pdu []byte) (outcome, procedure int, err error) {
	decoded, err := decodePDU(pdu)
	if err != nil {
		return 0, 0, err
	}
	return int(decoded.choiceIndex) + 1, int(decoded.procedureCode), nil
}

// ResponseIDs decodes a successful MME response and returns its correlation
// identifiers. Both are mandatory for accepted warning procedures.
func ResponseIDs(pdu []byte, procedure int) (uint16, uint16, error) {
	decoded, err := decodePDU(pdu)
	if err != nil {
		return 0, 0, err
	}
	if decoded.choiceIndex != pduSuccessful || int(decoded.procedureCode) != procedure {
		return 0, 0, fmt.Errorf("sbcap: PDU is not a successful outcome for procedure %d", procedure)
	}
	msgIE, ok := findIE(decoded.ies, idMessageIdentifier)
	if !ok {
		return 0, 0, fmt.Errorf("sbcap: response missing Message-Identifier")
	}
	serialIE, ok := findIE(decoded.ies, idSerialNumber)
	if !ok {
		return 0, 0, fmt.Errorf("sbcap: response missing Serial-Number")
	}
	msgID, err := decode16(msgIE.value)
	if err != nil {
		return 0, 0, err
	}
	serial, err := decode16(serialIE.value)
	if err != nil {
		return 0, 0, err
	}
	return msgID, serial, nil
}

// RequestIDs decodes an initiating-message PDU (Write-Replace-Warning-
// Request or Stop-Warning-Request) and returns its Message-Identifier and
// Serial-Number - the symmetric counterpart to ResponseIDs, used by MME
// simulators to correlate a response with whatever request they received.
func RequestIDs(pdu []byte) (uint16, uint16, error) {
	decoded, err := decodePDU(pdu)
	if err != nil {
		return 0, 0, err
	}
	if decoded.choiceIndex != pduInitiating {
		return 0, 0, fmt.Errorf("sbcap: PDU is not an initiating message")
	}
	msgIE, ok := findIE(decoded.ies, idMessageIdentifier)
	if !ok {
		return 0, 0, fmt.Errorf("sbcap: request missing Message-Identifier")
	}
	serialIE, ok := findIE(decoded.ies, idSerialNumber)
	if !ok {
		return 0, 0, fmt.Errorf("sbcap: request missing Serial-Number")
	}
	msgID, err := decode16(msgIE.value)
	if err != nil {
		return 0, 0, err
	}
	serial, err := decode16(serialIE.value)
	if err != nil {
		return 0, 0, err
	}
	return msgID, serial, nil
}

// SuccessResponse encodes a successful-outcome response (Write-Replace-
// Warning-Response or Stop-Warning-Response) with the mandatory Cause IE -
// used by the MME simulator in tests. cause is one of the Cause* constants.
func SuccessResponse(procedure int, messageID, serial uint16, cause int) ([]byte, error) {
	var proc int64
	switch procedure {
	case ProcedureWriteReplace:
		proc = procWriteReplaceWarning
	case ProcedureStop:
		proc = procStopWarning
	default:
		return nil, fmt.Errorf("sbcap: invalid procedure %d", procedure)
	}
	causeContent, err := encodeConstrainedInt(0, 255, int64(cause))
	if err != nil {
		return nil, err
	}
	ies := []protocolIE{
		{id: idMessageIdentifier, criticality: critReject, value: encode16(messageID)},
		{id: idSerialNumber, criticality: critReject, value: encode16(serial)},
		{id: idCause, criticality: critReject, value: causeContent},
	}
	return encodePDU(pduSuccessful, proc, critReject, ies)
}
