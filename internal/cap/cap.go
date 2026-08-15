package cap

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

const Namespace = "urn:oasis:names:tc:emergency:cap:1.2"

type Alert struct {
	XMLName    xml.Name `xml:"alert"`
	Identifier string   `xml:"identifier"`
	Sender     string   `xml:"sender"`
	Sent       string   `xml:"sent"`
	Status     string   `xml:"status"`
	MsgType    string   `xml:"msgType"`
	Scope      string   `xml:"scope"`
	References string   `xml:"references"`
	Info       []Info   `xml:"info"`
}

type Info struct {
	Language    string      `xml:"language" json:"language,omitempty"`
	Category    string      `xml:"category" json:"category,omitempty"`
	Event       string      `xml:"event" json:"event,omitempty"`
	Urgency     string      `xml:"urgency" json:"urgency,omitempty"`
	Severity    string      `xml:"severity" json:"severity,omitempty"`
	Certainty   string      `xml:"certainty" json:"certainty,omitempty"`
	Expires     string      `xml:"expires" json:"expires,omitempty"`
	Headline    string      `xml:"headline" json:"headline,omitempty"`
	Description string      `xml:"description" json:"description,omitempty"`
	Instruction string      `xml:"instruction" json:"instruction,omitempty"`
	EventCodes  []EventCode `xml:"eventCode" json:"eventCodes,omitempty"`
	Parameters  []Parameter `xml:"parameter" json:"parameters,omitempty"`
	Areas       []Area      `xml:"area" json:"areas,omitempty"`
}
type Area struct {
	Description string    `xml:"areaDesc" json:"description"`
	Polygons    []string  `xml:"polygon" json:"polygons,omitempty"`
	Circles     []string  `xml:"circle" json:"circles,omitempty"`
	Geocodes    []Geocode `xml:"geocode" json:"geocodes,omitempty"`
}
type Geocode struct {
	ValueName string `xml:"valueName" json:"value_name"`
	Value     string `xml:"value" json:"value"`
}

// EventCode is CAP's <eventCode> - e.g. <valueName>SAME</valueName> carrying
// an EAS/SAME event code (EAN/CAE/RMT/etc), used by internal/cbs to help
// classify an alert's CMAS/WEA Message Identifier.
type EventCode struct {
	ValueName string `xml:"valueName" json:"value_name"`
	Value     string `xml:"value" json:"value"`
}

// Parameter is CAP's <parameter> - e.g. IPAWS's <valueName>WEAHandling</valueName>,
// used by internal/cbs to help classify an alert's CMAS/WEA Message
// Identifier.
type Parameter struct {
	ValueName string `xml:"valueName" json:"value_name"`
	Value     string `xml:"value" json:"value"`
}

func Parse(data []byte) (Alert, error) {
	var a Alert
	if err := xml.Unmarshal(data, &a); err != nil {
		return Alert{}, fmt.Errorf("decode CAP: %w", err)
	}
	if a.XMLName.Space != "" && a.XMLName.Space != Namespace {
		return Alert{}, fmt.Errorf("unexpected CAP namespace %q", a.XMLName.Space)
	}
	if err := a.Validate(); err != nil {
		return Alert{}, err
	}
	return a, nil
}

func (a Alert) Validate() error {
	if strings.TrimSpace(a.Identifier) == "" || strings.TrimSpace(a.Sender) == "" || strings.TrimSpace(a.Sent) == "" {
		return fmt.Errorf("CAP identifier, sender and sent are required")
	}
	if _, err := time.Parse(time.RFC3339, a.Sent); err != nil {
		return fmt.Errorf("CAP sent: %w", err)
	}
	switch a.MsgType {
	case "Alert", "Update", "Cancel":
	default:
		return fmt.Errorf("unsupported CAP msgType %q", a.MsgType)
	}
	if a.MsgType != "Cancel" && len(a.Info) == 0 {
		return fmt.Errorf("CAP %s requires at least one info block", a.MsgType)
	}
	for _, info := range a.Info {
		if info.Expires != "" {
			if _, err := time.Parse(time.RFC3339, info.Expires); err != nil {
				return fmt.Errorf("CAP expires: %w", err)
			}
		}
	}
	return nil
}

func (a Alert) ReferenceIDs() []string { return strings.Fields(a.References) }
