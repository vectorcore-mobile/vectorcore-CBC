package cbs

import (
	"testing"

	"github.com/vectorcore/cbc/internal/cap"
)

func TestClassifyStatusTestAndExercise(t *testing.T) {
	if got := classify("Test", cap.Info{}); got != classTest {
		t.Fatalf("got %q, want %q", got, classTest)
	}
	if got := classify("Exercise", cap.Info{}); got != classExercise {
		t.Fatalf("got %q, want %q", got, classExercise)
	}
}

func TestClassifySAMEEventCodes(t *testing.T) {
	cases := map[string]string{"EAN": classPresidential, "CAE": classAmber, "RMT": classTest}
	for code, want := range cases {
		info := cap.Info{EventCodes: []cap.EventCode{{ValueName: "SAME", Value: code}}}
		if got := classify("Actual", info); got != want {
			t.Fatalf("SAME %s: got %q, want %q", code, got, want)
		}
	}
}

func TestClassifyWEAHandlingParameter(t *testing.T) {
	cases := map[string]string{
		"Presidential":          classPresidential,
		"AMBER":                 classAmber,
		"Child Abduction":       classAmber,
		"Public Safety":         classPublicSafety,
		"State/Local WEA Test":  classStateLocalTest,
		"Required Monthly Test": classTest,
	}
	for value, want := range cases {
		info := cap.Info{Parameters: []cap.Parameter{{ValueName: "WEAHandling", Value: value}}}
		if got := classify("Actual", info); got != want {
			t.Fatalf("WEAHandling %q: got %q, want %q", value, got, want)
		}
	}
}

func TestClassifySeverityUrgencyCertaintyGrid(t *testing.T) {
	cases := []struct {
		severity, urgency, certainty, want string
	}{
		{"Extreme", "Immediate", "Observed", "extreme-immediate-observed"},
		{"Extreme", "Immediate", "Likely", "extreme-immediate-likely"},
		{"Extreme", "Expected", "Observed", "extreme-expected-observed"},
		{"Extreme", "Expected", "Likely", "extreme-expected-likely"},
		{"Severe", "Immediate", "Observed", "severe-immediate-observed"},
		{"Severe", "Immediate", "Likely", "severe-immediate-likely"},
		{"Severe", "Expected", "Observed", "severe-expected-observed"},
		{"Severe", "Expected", "Likely", "severe-expected-likely"},
	}
	for _, c := range cases {
		info := cap.Info{Severity: c.severity, Urgency: c.urgency, Certainty: c.certainty}
		if got := classify("Actual", info); got != c.want {
			t.Fatalf("%+v: got %q, want %q", c, got, c.want)
		}
	}
}

func TestClassifyOutsideGridReturnsEmpty(t *testing.T) {
	cases := []cap.Info{
		{Severity: "Moderate", Urgency: "Immediate", Certainty: "Observed"},
		{Severity: "Minor", Urgency: "Immediate", Certainty: "Observed"},
		{Severity: "Extreme", Urgency: "Future", Certainty: "Observed"},
		{Severity: "Extreme", Urgency: "Immediate", Certainty: "Possible"},
		{},
	}
	for _, info := range cases {
		if got := classify("Actual", info); got != "" {
			t.Fatalf("%+v: got %q, want \"\"", info, got)
		}
	}
}

func TestClassifyStatusTakesPriorityOverGrid(t *testing.T) {
	info := cap.Info{Severity: "Extreme", Urgency: "Immediate", Certainty: "Observed"}
	if got := classify("Test", info); got != classTest {
		t.Fatalf("got %q, want %q (status should win over the severity grid)", got, classTest)
	}
}

// TestAtisMessageIdentifiers pins every label to its 3GPP TS 23.041
// section 9.4.1.2.2 value (cross-checked against AOSP's SmsCbConstants.java)
// so a future edit can't silently re-shuffle the Extreme/Severe blocks the
// way this table once did.
func TestAtisMessageIdentifiers(t *testing.T) {
	want := map[string]uint16{
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
	if len(atisMessageIdentifiers) != len(want) {
		t.Fatalf("got %d entries, want %d", len(atisMessageIdentifiers), len(want))
	}
	for label, id := range want {
		if got := atisMessageIdentifiers[label]; got != id {
			t.Errorf("%s: got %#x, want %#x", label, got, id)
		}
	}
}
