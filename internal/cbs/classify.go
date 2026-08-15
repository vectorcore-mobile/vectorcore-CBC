package cbs

import (
	"strings"

	"github.com/vectorcore/cbc/internal/cap"
)

// Classification labels for CMAS/WEA Message Identifier selection. These are
// used both as classify's return values and as config.CBS.MessageIdentifiers
// override keys.
const (
	classPresidential   = "presidential"
	classAmber          = "amber"
	classTest           = "test"
	classExercise       = "exercise"
	classPublicSafety   = "public-safety"    // WEA 3.0, see messageIdentifier
	classStateLocalTest = "state-local-test" // WEA 3.0, see messageIdentifier
)

// atisMessageIdentifiers are the standard CMAS/WEA Message Identifier values
// assigned by 3GPP TS 23.041 section 9.4.1.2.2's CMAS range (0x1112-0x1130),
// cross-checked against AOSP's SmsCbConstants.java reference implementation.
// These are fixed by the standard, not operator-specific, so they're
// hardcoded rather than left to YAML config - config.CBS.MessageIdentifiers
// remains available only as a rare manual override (see messageIdentifier).
//
// Note the spec groups these by severity first, then urgency: all four
// Extreme combinations (0x1113-0x1116) precede all four Severe combinations
// (0x1117-0x111A) - it's not urgency-major as the labels might suggest.
var atisMessageIdentifiers = map[string]uint16{
	classPresidential:            0x1112,
	"extreme-immediate-observed": 0x1113,
	"extreme-immediate-likely":   0x1114,
	"extreme-expected-observed":  0x1115,
	"extreme-expected-likely":    0x1116,
	"severe-immediate-observed":  0x1117,
	"severe-immediate-likely":    0x1118,
	"severe-expected-observed":   0x1119,
	"severe-expected-likely":     0x111A,
	classAmber:                   0x111B,
	classTest:                    0x111C,
	classExercise:                0x111D,
	classPublicSafety:            0x112C,
	classStateLocalTest:          0x112E,
}

// classify inspects a CAP alert's status plus one info block's eventCode/
// parameter/severity/urgency/certainty fields and returns the CMAS/WEA
// classification label that determines its Message Identifier, or "" if
// none of the standard categories apply (callers use
// cfg.DefaultMessageIdentifier in that case - this is not an error).
//
// Checked in order, first match wins:
//  1. CAP <status> (Exercise/Test) - the most reliable signal, since it's a
//     native, already-validated CAP enum field.
//  2. <eventCode valueName="SAME"> EAN/CAE/RMT - the confirmed EAS/SAME
//     event codes IPAWS carries through to WEA (national/AMBER/monthly test).
//  3. <parameter valueName="WEAHandling"> - the real IPAWS convention for
//     signalling Presidential/AMBER/Test/Public Safety/State-Local-Test
//     handling explicitly.
//  4. The Severity x Urgency x Certainty grid ATIS-0700006 defines for
//     Extreme/Severe threat alerts - anything outside that grid (Moderate/
//     Minor severity, Possible/Unlikely certainty, etc.) isn't a CMAS
//     category at all.
func classify(status string, info cap.Info) string {
	switch strings.TrimSpace(status) {
	case "Exercise":
		return classExercise
	case "Test":
		return classTest
	}

	for _, ec := range info.EventCodes {
		if !strings.EqualFold(strings.TrimSpace(ec.ValueName), "SAME") {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(ec.Value)) {
		case "EAN":
			return classPresidential
		case "CAE":
			return classAmber
		case "RMT":
			return classTest
		}
	}

	for _, p := range info.Parameters {
		if !strings.EqualFold(strings.TrimSpace(p.ValueName), "WEAHandling") {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(p.Value))
		switch {
		case strings.Contains(v, "presidential"):
			return classPresidential
		case strings.Contains(v, "amber") || strings.Contains(v, "child abduction"):
			return classAmber
		case strings.Contains(v, "state") && strings.Contains(v, "test"):
			return classStateLocalTest
		case strings.Contains(v, "test"):
			return classTest
		case strings.Contains(v, "public safety"):
			return classPublicSafety
		}
	}

	severity := strings.TrimSpace(info.Severity)
	urgency := strings.TrimSpace(info.Urgency)
	certainty := strings.TrimSpace(info.Certainty)
	if severity != "Extreme" && severity != "Severe" {
		return ""
	}
	if urgency != "Immediate" && urgency != "Expected" {
		return ""
	}
	if certainty != "Observed" && certainty != "Likely" {
		return ""
	}
	return strings.ToLower(severity) + "-" + strings.ToLower(urgency) + "-" + strings.ToLower(certainty)
}
