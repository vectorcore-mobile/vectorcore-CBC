package cap

import "testing"

func TestParse(t *testing.T) {
	a, err := Parse([]byte(`<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2"><identifier>a-1</identifier><sender>cbe</sender><sent>2026-08-02T00:00:00Z</sent><status>Actual</status><msgType>Alert</msgType><scope>Public</scope><info><event>Flood</event></info></alert>`))
	if err != nil || a.Identifier != "a-1" || a.Info[0].Event != "Flood" {
		t.Fatalf("Parse() = %#v, %v", a, err)
	}
}

// TestParseMultiplePolygonsAndCircles proves the fix for a confirmed bug:
// Area.Polygon used to be a bare string, so encoding/xml silently kept only
// the last of multiple <polygon> elements and dropped <circle> entirely
// (there was no field for it at all). EAG's CBE.jsx already emits multiple
// <polygon>/<circle> elements per area, so this was a live interop gap.
func TestParseMultiplePolygonsAndCircles(t *testing.T) {
	a, err := Parse([]byte(`<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2"><identifier>a-1</identifier><sender>cbe</sender><sent>2026-08-02T00:00:00Z</sent><status>Actual</status><msgType>Alert</msgType><scope>Public</scope><info><event>Flood</event><area><areaDesc>two polygons and two circles</areaDesc><polygon>30,-90 30,-91 31,-91 31,-90 30,-90</polygon><polygon>40,-100 40,-101 41,-101 41,-100 40,-100</polygon><circle>35,-95 10</circle><circle>36,-96 5</circle></area></info></alert>`))
	if err != nil {
		t.Fatal(err)
	}
	area := a.Info[0].Areas[0]
	if len(area.Polygons) != 2 {
		t.Fatalf("expected both polygons retained, got %d: %+v", len(area.Polygons), area.Polygons)
	}
	if area.Polygons[0] != "30,-90 30,-91 31,-91 31,-90 30,-90" || area.Polygons[1] != "40,-100 40,-101 41,-101 41,-100 40,-100" {
		t.Fatalf("polygons=%+v", area.Polygons)
	}
	if len(area.Circles) != 2 {
		t.Fatalf("expected both circles retained, got %d: %+v", len(area.Circles), area.Circles)
	}
	if area.Circles[0] != "35,-95 10" || area.Circles[1] != "36,-96 5" {
		t.Fatalf("circles=%+v", area.Circles)
	}
}

func TestParseEventCodeAndParameter(t *testing.T) {
	a, err := Parse([]byte(`<alert xmlns="urn:oasis:names:tc:emergency:cap:1.2"><identifier>a-1</identifier><sender>cbe</sender><sent>2026-08-02T00:00:00Z</sent><status>Actual</status><msgType>Alert</msgType><scope>Public</scope><info><event>Tornado Warning</event><severity>Severe</severity><urgency>Immediate</urgency><certainty>Observed</certainty><eventCode><valueName>SAME</valueName><value>TOR</value></eventCode><parameter><valueName>WEAHandling</valueName><value>Imminent Threat</value></parameter></info></alert>`))
	if err != nil {
		t.Fatal(err)
	}
	info := a.Info[0]
	if len(info.EventCodes) != 1 || info.EventCodes[0].ValueName != "SAME" || info.EventCodes[0].Value != "TOR" {
		t.Fatalf("eventCodes=%+v", info.EventCodes)
	}
	if len(info.Parameters) != 1 || info.Parameters[0].ValueName != "WEAHandling" || info.Parameters[0].Value != "Imminent Threat" {
		t.Fatalf("parameters=%+v", info.Parameters)
	}
}
