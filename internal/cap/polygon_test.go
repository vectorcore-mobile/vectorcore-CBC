package cap

import (
	"encoding/json"
	"math"
	"testing"
)

func TestPolygonToGeoJSONValidTriangle(t *testing.T) {
	b, err := PolygonToGeoJSON("32.665969,-86.433563 32.689087,-86.260529 32.542183,-86.22757")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "Polygon" {
		t.Fatalf("type=%q", got.Type)
	}
	ring := got.Coordinates[0]
	if len(ring) != 4 {
		t.Fatalf("expected auto-closed 4-position ring, got %d", len(ring))
	}
	if ring[0] != ring[3] {
		t.Fatalf("ring did not auto-close: %v", ring)
	}
	// [longitude, latitude] order.
	if ring[0][0] != -86.433563 || ring[0][1] != 32.665969 {
		t.Fatalf("expected lon/lat swap, got %v", ring[0])
	}
}

func TestPolygonToGeoJSONAlreadyClosedNotDuplicated(t *testing.T) {
	b, err := PolygonToGeoJSON("0,0 0,10 10,10 0,0")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Coordinates[0]) != 4 {
		t.Fatalf("expected no extra closing position, got %d", len(got.Coordinates[0]))
	}
}

func TestPolygonToGeoJSONRejectsTooFewPositions(t *testing.T) {
	if _, err := PolygonToGeoJSON("0,0 10,10"); err == nil {
		t.Fatal("expected error for fewer than 3 positions")
	}
}

func TestPolygonToGeoJSONRejectsMalformedPair(t *testing.T) {
	if _, err := PolygonToGeoJSON("0,0 10 20,20"); err == nil {
		t.Fatal("expected error for a malformed coordinate pair")
	}
}

func TestPolygonToGeoJSONRejectsNonNumeric(t *testing.T) {
	if _, err := PolygonToGeoJSON("a,b 0,10 10,10"); err == nil {
		t.Fatal("expected error for non-numeric coordinates")
	}
}

func TestCircleToGeoJSONValidCircle(t *testing.T) {
	b, err := CircleToGeoJSON("35,-95 10")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "Polygon" {
		t.Fatalf("type=%q", got.Type)
	}
	ring := got.Coordinates[0]
	if len(ring) != circleSegments+1 {
		t.Fatalf("expected %d vertices plus closing point, got %d", circleSegments, len(ring))
	}
	if ring[0] != ring[len(ring)-1] {
		t.Fatalf("ring did not close: first=%v last=%v", ring[0], ring[len(ring)-1])
	}
	// Every vertex should sit ~10km from the center (35,-95), converted back
	// through the same simple degree-per-km approximation used to generate
	// it, within a small tolerance for floating point/trig error.
	const centerLat, centerLon, radiusKm = 35.0, -95.0, 10.0
	for i, pt := range ring[:len(ring)-1] {
		lon, lat := pt[0], pt[1]
		dLat := (lat - centerLat) * kmPerDegreeLatitude
		dLon := (lon - centerLon) * kmPerDegreeLatitude * math.Cos(centerLat*math.Pi/180)
		dist := math.Hypot(dLat, dLon)
		if math.Abs(dist-radiusKm) > 0.01 {
			t.Fatalf("vertex %d distance=%.4fkm, want ~%.1fkm (lon=%.6f lat=%.6f)", i, dist, radiusKm, lon, lat)
		}
	}
}

func TestCircleToGeoJSONRejectsMissingRadius(t *testing.T) {
	if _, err := CircleToGeoJSON("35,-95"); err == nil {
		t.Fatal("expected error for a circle with no radius field")
	}
}

func TestCircleToGeoJSONRejectsMalformedCenter(t *testing.T) {
	if _, err := CircleToGeoJSON("35 10"); err == nil {
		t.Fatal("expected error for a center with no comma-separated lat,lon")
	}
}

func TestCircleToGeoJSONRejectsNonNumeric(t *testing.T) {
	if _, err := CircleToGeoJSON("a,b 10"); err == nil {
		t.Fatal("expected error for non-numeric center")
	}
	if _, err := CircleToGeoJSON("35,-95 notanumber"); err == nil {
		t.Fatal("expected error for non-numeric radius")
	}
}

func TestCircleToGeoJSONRejectsNonPositiveRadius(t *testing.T) {
	if _, err := CircleToGeoJSON("35,-95 0"); err == nil {
		t.Fatal("expected error for zero radius")
	}
	if _, err := CircleToGeoJSON("35,-95 -5"); err == nil {
		t.Fatal("expected error for negative radius")
	}
}
