// Package sbcap encodes LTE SBcAP messages using libosmo-sbcap, the APER
// implementation used by the osmo-cbc reference implementation.  This keeps
// the Go service focused on lifecycle and transport while the ASN.1 boundary
// remains interoperable with the reference CBC.
package sbcap

/*
#cgo pkg-config: libosmo-sbcap libosmocore
#cgo CFLAGS: -I/usr/src/osmo-cbc/src/sbcap/skel
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <osmocom/core/msgb.h>
#include <osmocom/core/utils.h>
#include <osmocom/sbcap/sbcap_common.h>

static int vc_alloc(uint8_t **buf, size_t *size, size_t n) {
	*buf = malloc(n);
	if (!*buf) return -ENOMEM;
	*size = n;
	return 0;
}

static int vc_octets(uint8_t **buf, size_t *size, const uint8_t *src, size_t n) {
	int rc = vc_alloc(buf, size, n);
	if (rc) return rc;
	memcpy(*buf, src, n);
	return 0;
}

static int vc_warning_area(SBcAP_Write_Replace_Warning_Request_IEs_t *ie, int scope, const uint8_t plmn[3], const uint32_t *ids, size_t count) {
	A_SEQUENCE_OF(void) *list;
	if (scope == 1) return 0;
	if (!plmn || !ids || !count) return -EINVAL;
	ie->value.choice.Warning_Area_List.present = scope == 2 ? SBcAP_Warning_Area_List_PR_tracking_Area_List_for_Warning : SBcAP_Warning_Area_List_PR_cell_ID_List;
	list = scope == 2 ? (void *)&ie->value.choice.Warning_Area_List.choice.tracking_Area_List_for_Warning.list : (void *)&ie->value.choice.Warning_Area_List.choice.cell_ID_List.list;
	for (size_t i=0; i<count; i++) {
		if (scope == 2) {
			SBcAP_TAI_t *tai=CALLOC(1,sizeof(*tai)); if(!tai) return -ENOMEM;
			if(vc_octets(&tai->pLMNidentity.buf,&tai->pLMNidentity.size,plmn,3)||vc_alloc(&tai->tAC.buf,&tai->tAC.size,2)) return -ENOMEM;
			tai->tAC.buf[0]=ids[i]>>8; tai->tAC.buf[1]=ids[i]; ASN_SEQUENCE_ADD(list,tai);
		} else if (scope == 3) {
			SBcAP_EUTRAN_CGI_t *ecgi=CALLOC(1,sizeof(*ecgi)); if(!ecgi || ids[i] > 0x0fffffff) return -EINVAL;
			if(vc_octets(&ecgi->pLMNidentity.buf,&ecgi->pLMNidentity.size,plmn,3)||vc_alloc(&ecgi->cell_ID.buf,&ecgi->cell_ID.size,4)) return -ENOMEM;
			uint32_t v=ids[i]<<4; ecgi->cell_ID.buf[0]=v>>24;ecgi->cell_ID.buf[1]=v>>16;ecgi->cell_ID.buf[2]=v>>8;ecgi->cell_ID.buf[3]=v;ecgi->cell_ID.bits_unused=4; ASN_SEQUENCE_ADD(list,ecgi);
		} else return -EINVAL;
	}
	return 0;
}

static int vc_sbcap_write_replace(uint16_t message_id, uint16_t serial, uint8_t dcs,
		const uint8_t *contents, size_t contents_len, uint16_t repetition_period,
		uint16_t broadcasts, int scope, const uint8_t *plmn, const uint32_t *ids, size_t id_count, uint8_t *out, size_t out_cap) {
	SBcAP_SBC_AP_PDU_t *pdu = NULL;
	SBcAP_Write_Replace_Warning_Request_IEs_t *ie;
	A_SEQUENCE_OF(void) *list;
	struct msgb *msg = NULL;
	int rc = -EINVAL;

	if (!contents || !contents_len || contents_len > 9600 || !out) return -EINVAL;
	pdu = sbcap_pdu_alloc();
	if (!pdu) return -ENOMEM;
	pdu->present = SBcAP_SBC_AP_PDU_PR_initiatingMessage;
	pdu->choice.initiatingMessage.procedureCode = SBcAP_ProcedureId_Write_Replace_Warning;
	pdu->choice.initiatingMessage.criticality = SBcAP_Criticality_reject;
	pdu->choice.initiatingMessage.value.present = SBcAP_InitiatingMessage__value_PR_Write_Replace_Warning_Request;
	list = (void *)&pdu->choice.initiatingMessage.value.choice.Write_Replace_Warning_Request.protocolIEs.list;

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(5, SBcAP_Criticality_reject,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Message_Identifier);
	if (!ie || vc_alloc(&ie->value.choice.Message_Identifier.buf, &ie->value.choice.Message_Identifier.size, 2)) goto done;
	ie->value.choice.Message_Identifier.buf[0] = message_id >> 8;
	ie->value.choice.Message_Identifier.buf[1] = message_id;
	ie->value.choice.Message_Identifier.size = 2;
	ie->value.choice.Message_Identifier.bits_unused = 0;
	ASN_SEQUENCE_ADD(list, ie);

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(11, SBcAP_Criticality_reject,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Serial_Number);
	if (!ie || vc_alloc(&ie->value.choice.Serial_Number.buf, &ie->value.choice.Serial_Number.size, 2)) goto done;
	ie->value.choice.Serial_Number.buf[0] = serial >> 8;
	ie->value.choice.Serial_Number.buf[1] = serial;
	ie->value.choice.Serial_Number.size = 2;
	ie->value.choice.Serial_Number.bits_unused = 0;
	ASN_SEQUENCE_ADD(list, ie);
	if (scope != 1) {
		ie = sbcap_alloc_Write_Replace_Warning_Request_IE(15, SBcAP_Criticality_ignore, SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Warning_Area_List);
		if (!ie || vc_warning_area(ie,scope,plmn,ids,id_count)) goto done;
		ASN_SEQUENCE_ADD(list, ie);
	}

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(10, SBcAP_Criticality_reject,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Repetition_Period);
	if (!ie) goto done;
	ie->value.choice.Repetition_Period = repetition_period;
	ASN_SEQUENCE_ADD(list, ie);

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(7, SBcAP_Criticality_reject,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Number_of_Broadcasts_Requested);
	if (!ie) goto done;
	ie->value.choice.Number_of_Broadcasts_Requested = broadcasts;
	ASN_SEQUENCE_ADD(list, ie);

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(3, SBcAP_Criticality_ignore,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Data_Coding_Scheme);
	if (!ie || vc_octets(&ie->value.choice.Data_Coding_Scheme.buf, &ie->value.choice.Data_Coding_Scheme.size, &dcs, 1)) goto done;
	ie->value.choice.Data_Coding_Scheme.bits_unused = 0;
	ASN_SEQUENCE_ADD(list, ie);

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(16, SBcAP_Criticality_ignore,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Warning_Message_Content);
	if (!ie || vc_octets(&ie->value.choice.Warning_Message_Content.buf, &ie->value.choice.Warning_Message_Content.size, contents, contents_len)) goto done;
	ASN_SEQUENCE_ADD(list, ie);

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(20, SBcAP_Criticality_reject,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Concurrent_Warning_Message_Indicator);
	if (!ie) goto done;
	ie->value.choice.Concurrent_Warning_Message_Indicator = SBcAP_Concurrent_Warning_Message_Indicator_true;
	ASN_SEQUENCE_ADD(list, ie);

	ie = sbcap_alloc_Write_Replace_Warning_Request_IE(24, SBcAP_Criticality_ignore,
		SBcAP_Write_Replace_Warning_Request_IEs__value_PR_Send_Write_Replace_Warning_Indication);
	if (!ie) goto done;
	ie->value.choice.Send_Write_Replace_Warning_Indication = SBcAP_Send_Write_Replace_Warning_Indication_true;
	ASN_SEQUENCE_ADD(list, ie);

	msg = sbcap_encode(pdu);
	if (!msg) goto done;
	if (msgb_length(msg) > out_cap) { rc = -ENOSPC; goto done; }
	memcpy(out, msgb_data(msg), msgb_length(msg));
	rc = msgb_length(msg);
done:
	if (msg) msgb_free(msg);
	if (pdu) sbcap_pdu_free(pdu);
	return rc;
}

static int vc_sbcap_header(const uint8_t *data, size_t n, int *outcome, int *procedure) {
	struct msgb *msg;
	SBcAP_SBC_AP_PDU_t *pdu;
	if (!data || !n || !outcome || !procedure) return -EINVAL;
	msg = msgb_alloc(n, "vectorcore-sbcap-rx");
	if (!msg) return -ENOMEM;
	memcpy(msgb_put(msg, n), data, n);
	pdu = sbcap_decode(msg);
	msgb_free(msg);
	if (!pdu) return -EBADMSG;
	*outcome = pdu->present;
	*procedure = sbcap_pdu_get_procedure_code(pdu);
	sbcap_pdu_free(pdu);
	return 0;
}

static int vc_sbcap_stop(uint16_t message_id, uint16_t serial, uint8_t *out, size_t out_cap) {
	SBcAP_SBC_AP_PDU_t *pdu = sbcap_pdu_alloc();
	SBcAP_Stop_Warning_Request_IEs_t *ie;
	A_SEQUENCE_OF(void) *list;
	struct msgb *msg = NULL;
	int rc = -EINVAL;
	if (!pdu || !out) return -ENOMEM;
	pdu->present = SBcAP_SBC_AP_PDU_PR_initiatingMessage;
	pdu->choice.initiatingMessage.procedureCode = SBcAP_ProcedureId_Stop_Warning;
	pdu->choice.initiatingMessage.criticality = SBcAP_Criticality_reject;
	pdu->choice.initiatingMessage.value.present = SBcAP_InitiatingMessage__value_PR_Stop_Warning_Request;
	list = (void *)&pdu->choice.initiatingMessage.value.choice.Stop_Warning_Request.protocolIEs.list;
	ie = sbcap_alloc_Stop_Warning_Request_IE(5, SBcAP_Criticality_reject, SBcAP_Stop_Warning_Request_IEs__value_PR_Message_Identifier);
	if (!ie || vc_alloc(&ie->value.choice.Message_Identifier.buf, &ie->value.choice.Message_Identifier.size, 2)) goto done;
	ie->value.choice.Message_Identifier.buf[0] = message_id >> 8; ie->value.choice.Message_Identifier.buf[1] = message_id;
	ie->value.choice.Message_Identifier.bits_unused = 0; ASN_SEQUENCE_ADD(list, ie);
	ie = sbcap_alloc_Stop_Warning_Request_IE(11, SBcAP_Criticality_reject, SBcAP_Stop_Warning_Request_IEs__value_PR_Serial_Number);
	if (!ie || vc_alloc(&ie->value.choice.Serial_Number.buf, &ie->value.choice.Serial_Number.size, 2)) goto done;
	ie->value.choice.Serial_Number.buf[0] = serial >> 8; ie->value.choice.Serial_Number.buf[1] = serial;
	ie->value.choice.Serial_Number.bits_unused = 0; ASN_SEQUENCE_ADD(list, ie);
	msg = sbcap_encode(pdu); if (!msg) goto done;
	if (msgb_length(msg) > out_cap) { rc = -ENOSPC; goto done; }
	memcpy(out, msgb_data(msg), msgb_length(msg)); rc = msgb_length(msg);
done:
	if (msg) msgb_free(msg); if (pdu) sbcap_pdu_free(pdu); return rc;
}

static int vc_sbcap_stop_target(uint16_t message_id, uint16_t serial, int scope, const uint8_t *plmn, const uint32_t *ids, size_t count, uint8_t *out, size_t out_cap) {
	SBcAP_SBC_AP_PDU_t *pdu=sbcap_pdu_alloc(); SBcAP_Stop_Warning_Request_IEs_t *ie; A_SEQUENCE_OF(void)*list; struct msgb *msg=NULL; int rc=-EINVAL;
	if(!pdu||!out)return -ENOMEM; pdu->present=SBcAP_SBC_AP_PDU_PR_initiatingMessage;pdu->choice.initiatingMessage.procedureCode=SBcAP_ProcedureId_Stop_Warning;pdu->choice.initiatingMessage.criticality=SBcAP_Criticality_reject;pdu->choice.initiatingMessage.value.present=SBcAP_InitiatingMessage__value_PR_Stop_Warning_Request;list=(void*)&pdu->choice.initiatingMessage.value.choice.Stop_Warning_Request.protocolIEs.list;
	ie=sbcap_alloc_Stop_Warning_Request_IE(5,SBcAP_Criticality_reject,SBcAP_Stop_Warning_Request_IEs__value_PR_Message_Identifier);if(!ie||vc_alloc(&ie->value.choice.Message_Identifier.buf,&ie->value.choice.Message_Identifier.size,2))goto done;ie->value.choice.Message_Identifier.buf[0]=message_id>>8;ie->value.choice.Message_Identifier.buf[1]=message_id;ASN_SEQUENCE_ADD(list,ie);
	ie=sbcap_alloc_Stop_Warning_Request_IE(11,SBcAP_Criticality_reject,SBcAP_Stop_Warning_Request_IEs__value_PR_Serial_Number);if(!ie||vc_alloc(&ie->value.choice.Serial_Number.buf,&ie->value.choice.Serial_Number.size,2))goto done;ie->value.choice.Serial_Number.buf[0]=serial>>8;ie->value.choice.Serial_Number.buf[1]=serial;ASN_SEQUENCE_ADD(list,ie);
	if(scope!=1){ie=sbcap_alloc_Stop_Warning_Request_IE(15,SBcAP_Criticality_ignore,SBcAP_Stop_Warning_Request_IEs__value_PR_Warning_Area_List);if(!ie||!plmn||!ids||!count)goto done;ie->value.choice.Warning_Area_List.present=scope==2?SBcAP_Warning_Area_List_PR_tracking_Area_List_for_Warning:SBcAP_Warning_Area_List_PR_cell_ID_List;for(size_t i=0;i<count;i++){if(scope==2){SBcAP_TAI_t*x=CALLOC(1,sizeof(*x));if(!x||vc_octets(&x->pLMNidentity.buf,&x->pLMNidentity.size,plmn,3)||vc_alloc(&x->tAC.buf,&x->tAC.size,2))goto done;x->tAC.buf[0]=ids[i]>>8;x->tAC.buf[1]=ids[i];ASN_SEQUENCE_ADD(&ie->value.choice.Warning_Area_List.choice.tracking_Area_List_for_Warning.list,x);}else if(scope==3){SBcAP_EUTRAN_CGI_t*x=CALLOC(1,sizeof(*x));if(!x||ids[i]>0xfffffff||vc_octets(&x->pLMNidentity.buf,&x->pLMNidentity.size,plmn,3)||vc_alloc(&x->cell_ID.buf,&x->cell_ID.size,4))goto done;uint32_t v=ids[i]<<4;x->cell_ID.buf[0]=v>>24;x->cell_ID.buf[1]=v>>16;x->cell_ID.buf[2]=v>>8;x->cell_ID.buf[3]=v;x->cell_ID.bits_unused=4;ASN_SEQUENCE_ADD(&ie->value.choice.Warning_Area_List.choice.cell_ID_List.list,x);}else goto done;}ASN_SEQUENCE_ADD(list,ie);}
	msg=sbcap_encode(pdu);if(!msg)goto done;if(msgb_length(msg)>out_cap){rc=-ENOSPC;goto done;}memcpy(out,msgb_data(msg),msgb_length(msg));rc=msgb_length(msg);done:if(msg)msgb_free(msg);if(pdu)sbcap_pdu_free(pdu);return rc;
}

static int vc_sbcap_success(uint8_t procedure, uint16_t message_id, uint16_t serial, uint8_t *out, size_t out_cap) {
	SBcAP_SBC_AP_PDU_t *pdu = sbcap_pdu_alloc(); struct msgb *msg = NULL; int rc = -EINVAL;
	if (!pdu || !out || (procedure != 0 && procedure != 1)) return -EINVAL;
	pdu->present = SBcAP_SBC_AP_PDU_PR_successfulOutcome;
	pdu->choice.successfulOutcome.procedureCode = procedure;
	pdu->choice.successfulOutcome.criticality = SBcAP_Criticality_reject;
	if (procedure == 0) {
		SBcAP_Write_Replace_Warning_Response_IEs_t *ie; A_SEQUENCE_OF(void) *list;
		pdu->choice.successfulOutcome.value.present = SBcAP_SuccessfulOutcome__value_PR_Write_Replace_Warning_Response;
		list=(void*)&pdu->choice.successfulOutcome.value.choice.Write_Replace_Warning_Response.protocolIEs.list;
		ie=CALLOC(1,sizeof(*ie)); if(!ie)goto done; ie->id=5;ie->criticality=SBcAP_Criticality_reject;ie->value.present=SBcAP_Write_Replace_Warning_Response_IEs__value_PR_Message_Identifier;
		if(vc_alloc(&ie->value.choice.Message_Identifier.buf,&ie->value.choice.Message_Identifier.size,2))goto done;ie->value.choice.Message_Identifier.buf[0]=message_id>>8;ie->value.choice.Message_Identifier.buf[1]=message_id;ASN_SEQUENCE_ADD(list,ie);
		ie=CALLOC(1,sizeof(*ie)); if(!ie)goto done; ie->id=11;ie->criticality=SBcAP_Criticality_reject;ie->value.present=SBcAP_Write_Replace_Warning_Response_IEs__value_PR_Serial_Number;
		if(vc_alloc(&ie->value.choice.Serial_Number.buf,&ie->value.choice.Serial_Number.size,2))goto done;ie->value.choice.Serial_Number.buf[0]=serial>>8;ie->value.choice.Serial_Number.buf[1]=serial;ASN_SEQUENCE_ADD(list,ie);
	} else {
		SBcAP_Stop_Warning_Response_IEs_t *ie; A_SEQUENCE_OF(void) *list;
		pdu->choice.successfulOutcome.value.present = SBcAP_SuccessfulOutcome__value_PR_Stop_Warning_Response;
		list=(void*)&pdu->choice.successfulOutcome.value.choice.Stop_Warning_Response.protocolIEs.list;
		ie=CALLOC(1,sizeof(*ie));if(!ie)goto done;ie->id=5;ie->criticality=SBcAP_Criticality_reject;ie->value.present=SBcAP_Stop_Warning_Response_IEs__value_PR_Message_Identifier;
		if(vc_alloc(&ie->value.choice.Message_Identifier.buf,&ie->value.choice.Message_Identifier.size,2))goto done;ie->value.choice.Message_Identifier.buf[0]=message_id>>8;ie->value.choice.Message_Identifier.buf[1]=message_id;ASN_SEQUENCE_ADD(list,ie);
		ie=CALLOC(1,sizeof(*ie));if(!ie)goto done;ie->id=11;ie->criticality=SBcAP_Criticality_reject;ie->value.present=SBcAP_Stop_Warning_Response_IEs__value_PR_Serial_Number;
		if(vc_alloc(&ie->value.choice.Serial_Number.buf,&ie->value.choice.Serial_Number.size,2))goto done;ie->value.choice.Serial_Number.buf[0]=serial>>8;ie->value.choice.Serial_Number.buf[1]=serial;ASN_SEQUENCE_ADD(list,ie);
	}
	msg=sbcap_encode(pdu);if(!msg)goto done;if(msgb_length(msg)>out_cap){rc=-ENOSPC;goto done;}memcpy(out,msgb_data(msg),msgb_length(msg));rc=msgb_length(msg);
done: if(msg)msgb_free(msg);if(pdu)sbcap_pdu_free(pdu);return rc;
}

static int vc_sbcap_response_ids(const uint8_t *data, size_t n, int procedure, uint16_t *message_id, uint16_t *serial) {
	struct msgb *msg; SBcAP_SBC_AP_PDU_t *pdu; void *x, *y;
	if(!data||!n||!message_id||!serial)return -EINVAL; msg=msgb_alloc(n,"vectorcore-sbcap-response");if(!msg)return -ENOMEM;memcpy(msgb_put(msg,n),data,n);pdu=sbcap_decode(msg);msgb_free(msg);if(!pdu)return -EBADMSG;
	if(pdu->present!=SBcAP_SBC_AP_PDU_PR_successfulOutcome || sbcap_pdu_get_procedure_code(pdu)!=procedure){sbcap_pdu_free(pdu);return -EPROTO;}
	if(procedure==0){A_SEQUENCE_OF(void)*list=(void*)&pdu->choice.successfulOutcome.value.choice.Write_Replace_Warning_Response.protocolIEs.list;x=sbcap_as_find_ie(list,5);y=sbcap_as_find_ie(list,11);if(!x||!y){sbcap_pdu_free(pdu);return -EBADMSG;}SBcAP_Write_Replace_Warning_Response_IEs_t *a=x,*b=y;if(a->value.choice.Message_Identifier.size!=2||b->value.choice.Serial_Number.size!=2){sbcap_pdu_free(pdu);return -EBADMSG;}*message_id=(a->value.choice.Message_Identifier.buf[0]<<8)|a->value.choice.Message_Identifier.buf[1];*serial=(b->value.choice.Serial_Number.buf[0]<<8)|b->value.choice.Serial_Number.buf[1];}
	else if(procedure==1){A_SEQUENCE_OF(void)*list=(void*)&pdu->choice.successfulOutcome.value.choice.Stop_Warning_Response.protocolIEs.list;x=sbcap_as_find_ie(list,5);y=sbcap_as_find_ie(list,11);if(!x||!y){sbcap_pdu_free(pdu);return -EBADMSG;}SBcAP_Stop_Warning_Response_IEs_t *a=x,*b=y;if(a->value.choice.Message_Identifier.size!=2||b->value.choice.Serial_Number.size!=2){sbcap_pdu_free(pdu);return -EBADMSG;}*message_id=(a->value.choice.Message_Identifier.buf[0]<<8)|a->value.choice.Message_Identifier.buf[1];*serial=(b->value.choice.Serial_Number.buf[0]<<8)|b->value.choice.Serial_Number.buf[1];}
	else {sbcap_pdu_free(pdu);return -EINVAL;} sbcap_pdu_free(pdu);return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	ProcedureWriteReplace = 0
	ProcedureStop         = 1

	OutcomeInitiating   = 1
	OutcomeSuccessful   = 2
	OutcomeUnsuccessful = 3
)

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
	buf := make([]byte, 10_000)
	if scope < 1 || scope > 3 || (scope != 1 && (len(plmn) != 3 || len(ids) == 0)) {
		return nil, fmt.Errorf("invalid SBcAP warning area")
	}
	var plmnPtr *C.uint8_t
	var idsPtr *C.uint32_t
	if len(plmn) > 0 {
		plmnPtr = (*C.uint8_t)(unsafe.Pointer(&plmn[0]))
	}
	if len(ids) > 0 {
		idsPtr = (*C.uint32_t)(unsafe.Pointer(&ids[0]))
	}
	rc := C.vc_sbcap_write_replace(C.uint16_t(messageID), C.uint16_t(serial), C.uint8_t(dcs),
		(*C.uint8_t)(unsafe.Pointer(&contents[0])), C.size_t(len(contents)), C.uint16_t(repetitionPeriod), C.uint16_t(broadcasts),
		C.int(scope), plmnPtr, idsPtr, C.size_t(len(ids)), (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if rc < 0 {
		return nil, fmt.Errorf("encode SBcAP Write-Replace-Warning-Request: %d", int(rc))
	}
	return buf[:int(rc)], nil
}

// Header decodes the APER envelope with libosmo-sbcap and returns its outcome
// class and TS 29.168 procedure code. It is used to validate MME responses.
func Header(pdu []byte) (outcome, procedure int, err error) {
	if len(pdu) == 0 {
		return 0, 0, fmt.Errorf("empty SBcAP PDU")
	}
	var cOutcome, cProcedure C.int
	rc := C.vc_sbcap_header((*C.uint8_t)(unsafe.Pointer(&pdu[0])), C.size_t(len(pdu)), &cOutcome, &cProcedure)
	if rc != 0 {
		return 0, 0, fmt.Errorf("decode SBcAP APER PDU: %d", int(rc))
	}
	return int(cOutcome), int(cProcedure), nil
}

// ResponseIDs decodes a successful MME response and returns its correlation
// identifiers. Both are mandatory for accepted warning procedures.
func ResponseIDs(pdu []byte, procedure int) (uint16, uint16, error) {
	if len(pdu) == 0 {
		return 0, 0, fmt.Errorf("empty SBcAP PDU")
	}
	var id, serial C.uint16_t
	rc := C.vc_sbcap_response_ids((*C.uint8_t)(unsafe.Pointer(&pdu[0])), C.size_t(len(pdu)), C.int(procedure), &id, &serial)
	if rc != 0 {
		return 0, 0, fmt.Errorf("decode SBcAP response IEs: %d", int(rc))
	}
	return uint16(id), uint16(serial), nil
}

// Stop encodes a TS 29.168 Stop-Warning-Request for a previously allocated
// message identifier and serial number.
func Stop(messageID, serial uint16) ([]byte, error) {
	buf := make([]byte, 1024)
	rc := C.vc_sbcap_stop(C.uint16_t(messageID), C.uint16_t(serial), (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if rc < 0 {
		return nil, fmt.Errorf("encode SBcAP Stop-Warning-Request: %d", int(rc))
	}
	return buf[:int(rc)], nil
}

func StopTarget(messageID, serial uint16, scope int, plmn []byte, ids []uint32) ([]byte, error) {
	if scope < 1 || scope > 3 || (scope != 1 && (len(plmn) != 3 || len(ids) == 0)) {
		return nil, fmt.Errorf("invalid SBcAP warning area")
	}
	buf := make([]byte, 1024)
	var pp *C.uint8_t
	var ip *C.uint32_t
	if len(plmn) > 0 {
		pp = (*C.uint8_t)(unsafe.Pointer(&plmn[0]))
	}
	if len(ids) > 0 {
		ip = (*C.uint32_t)(unsafe.Pointer(&ids[0]))
	}
	rc := C.vc_sbcap_stop_target(C.uint16_t(messageID), C.uint16_t(serial), C.int(scope), pp, ip, C.size_t(len(ids)), (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if rc < 0 {
		return nil, fmt.Errorf("encode SBcAP Stop-Warning-Request: %d", int(rc))
	}
	return buf[:int(rc)], nil
}

// SuccessResponse is used by the MME simulator integration tests.
func SuccessResponse(procedure int, messageID, serial uint16) ([]byte, error) {
	buf := make([]byte, 1024)
	rc := C.vc_sbcap_success(C.uint8_t(procedure), C.uint16_t(messageID), C.uint16_t(serial), (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
	if rc < 0 {
		return nil, fmt.Errorf("encode SBcAP successful outcome: %d", int(rc))
	}
	return buf[:int(rc)], nil
}
