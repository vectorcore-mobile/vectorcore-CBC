package cbs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vectorcore/cbc/internal/sbcap"
)

// SBcAPWriteReplace creates an LTE SBcAP Write-Replace-Warning-Request from a
// CBS message. The reference encoder currently represents PLMN-wide delivery;
// a caller must never drop a more specific CAP target, so TA/cell plans are
// rejected until their Warning-Area-List mapping is configured.
func SBcAPWriteReplace(m Message, repetitionPeriod, broadcasts uint16, plmn string) ([]byte, error) {
	if len(m.Pages) == 0 || len(m.Pages) > MaxPages {
		return nil, fmt.Errorf("invalid CBS page count %d", len(m.Pages))
	}
	contents := make([]byte, 1, 1+len(m.Pages)*(PageOctets+1))
	contents[0] = byte(len(m.Pages))
	for i, page := range m.Pages {
		if len(page.Data) != PageOctets || page.Number != uint8(i+1) || page.Total != uint8(len(m.Pages)) {
			return nil, fmt.Errorf("invalid CBS page %d", i+1)
		}
		contents = append(contents, page.Data...)
		used := PageOctets
		if i == len(m.Pages)-1 {
			used = usedOctets(page, m.Encoding)
		}
		contents = append(contents, byte(used))
	}
	bcd, ids, err := targetWire(m.Target, plmn)
	if err != nil {
		return nil, err
	}
	return sbcap.WriteReplaceTarget(m.MessageIdentifier, m.SerialNumber, m.DCS, contents, repetitionPeriod, broadcasts, int(m.Target.Scope), bcd, ids)
}

// SBcAPStop preserves the original warning area when cancelling a broadcast.
func SBcAPStop(m Message, plmn string) ([]byte, error) {
	bcd, ids, err := targetWire(m.Target, plmn)
	if err != nil {
		return nil, err
	}
	return sbcap.StopTarget(m.MessageIdentifier, m.SerialNumber, int(m.Target.Scope), bcd, ids)
}
func targetWire(target Target, plmn string) ([]byte, []uint32, error) {
	bcd, err := PlmnTBCD(plmn)
	if err != nil {
		return nil, nil, err
	}
	var values []string
	if target.Scope == TrackingAreaWide {
		values = target.TrackingAreas
	} else if target.Scope == CellWide {
		values = target.Cells
	}
	if target.Scope != PLMNWide && target.Scope != TrackingAreaWide && target.Scope != CellWide {
		return nil, nil, fmt.Errorf("invalid target scope %d", target.Scope)
	}
	ids := make([]uint32, len(values))
	for i, v := range values {
		n, e := strconv.ParseUint(strings.TrimSpace(v), 0, 32)
		if e != nil {
			return nil, nil, fmt.Errorf("invalid target %q: %w", v, e)
		}
		ids[i] = uint32(n)
	}
	return bcd, ids, nil
}

// PlmnTBCD converts a configured "MCC-MNC" PLMN string into its 3-octet TBCD
// wire form, matching the encoding SBcAP puts in every Warning-Area-List
// entry. Exported so callers outside this package (e.g. internal/delivery's
// eNB-restart handling) can compare a decoded Global-ENB-ID's PLMN against
// the CBC's own configured PLMN without duplicating this logic.
func PlmnTBCD(v string) ([]byte, error) {
	d := strings.ReplaceAll(strings.TrimSpace(v), "-", "")
	if len(d) != 5 && len(d) != 6 {
		return nil, fmt.Errorf("PLMN must be MCC-MNC (5 or 6 digits)")
	}
	for _, r := range d {
		if r < '0' || r > '9' {
			return nil, fmt.Errorf("invalid PLMN %q", v)
		}
	}
	b := func(i int) byte { return d[i] - '0' }
	mnc3 := byte(0xf)
	if len(d) == 6 {
		mnc3 = b(3)
	}
	return []byte{b(0) | b(1)<<4, b(2) | mnc3<<4, b(len(d)-2) | b(len(d)-1)<<4}, nil
}

func usedOctets(page Page, encoding string) int {
	if encoding == "ucs2" {
		for i := PageOctets - 2; i >= 0; i -= 2 {
			if page.Data[i] != 0 || page.Data[i+1] != 0x0d {
				return i + 2
			}
		}
		return 0
	}
	for i := PageOctets - 1; i >= 0; i-- {
		if page.Data[i] != 0x0d {
			return i + 1
		}
	}
	return 0
}
