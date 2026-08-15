package inventory

import (
	"strings"
	"testing"
)

func TestParseGeometryValidPolygonAndBounds(t *testing.T) {
	g, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	b := g.Bounds()
	if b.MinLatitude != 0 || b.MaxLatitude != 10 || b.MinLongitude != 0 || b.MaxLongitude != 10 {
		t.Fatalf("bounds=%+v", b)
	}
}

func TestParseGeometryValidMultiPolygon(t *testing.T) {
	g, err := ParseGeometry([]byte(`{"type":"MultiPolygon","coordinates":[
		[[[0,0],[0,10],[10,10],[10,0],[0,0]]],
		[[[20,20],[20,30],[30,30],[30,20],[20,20]]]
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Polygons) != 2 {
		t.Fatalf("expected 2 polygons, got %d", len(g.Polygons))
	}
}

func TestParseGeometryRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseGeometry([]byte(`{not json`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseGeometryRejectsUnsupportedType(t *testing.T) {
	if _, err := ParseGeometry([]byte(`{"type":"Point","coordinates":[0,0]}`)); err == nil {
		t.Fatal("expected error for unsupported geometry type")
	}
}

func TestParseGeometryRejectsEmptyGeometry(t *testing.T) {
	if _, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[]}`)); err == nil {
		t.Fatal("expected error for empty polygon")
	}
	if _, err := ParseGeometry([]byte(`{"type":"MultiPolygon","coordinates":[]}`)); err == nil {
		t.Fatal("expected error for empty multipolygon")
	}
}

func TestParseGeometryRejectsTooFewVertices(t *testing.T) {
	if _, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[1,1]]]}`)); err == nil {
		t.Fatal("expected error for a ring with fewer than 3 positions")
	}
}

func TestParseGeometryRejectsOutOfRangeCoordinates(t *testing.T) {
	if _, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[200,10],[0,0]]]}`)); err == nil {
		t.Fatal("expected error for out-of-range longitude")
	}
	if _, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,100],[10,10],[0,0]]]}`)); err == nil {
		t.Fatal("expected error for out-of-range latitude")
	}
}

func TestParseGeometryAutoClosesUnclosedRingAndRoundTrips(t *testing.T) {
	g, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	ring := g.Polygons[0][0]
	if ring[0] != ring[len(ring)-1] {
		t.Fatalf("expected auto-closed ring, got %v", ring)
	}
	norm, err := g.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseGeometry([]byte(norm)); err != nil {
		t.Fatalf("normalized geometry did not re-parse: %v", err)
	}
}

func TestValidateCoverageGeoJSONNormalizesAndComputesBounds(t *testing.T) {
	norm, bounds, err := ValidateCoverageGeoJSON(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0]]]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(norm, `"type":"Polygon"`) {
		t.Fatalf("normalized output missing type: %s", norm)
	}
	if bounds.MaxLatitude != 10 || bounds.MaxLongitude != 10 {
		t.Fatalf("bounds=%+v", bounds)
	}
}

func TestContainsPointHandlesHoles(t *testing.T) {
	g, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[
		[[0,0],[0,10],[10,10],[10,0],[0,0]],
		[[3,3],[3,7],[7,7],[7,3],[3,3]]
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if g.ContainsPoint(5, 5) {
		t.Fatal("point inside the hole must not be contained")
	}
	if !g.ContainsPoint(1, 1) {
		t.Fatal("point inside the annulus must be contained")
	}
	if g.ContainsPoint(20, 20) {
		t.Fatal("point outside the exterior must not be contained")
	}
}

func TestIntersectsEdgeCrossing(t *testing.T) {
	a, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[5,5],[5,15],[15,15],[15,5],[5,5]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !Intersects(a, b) {
		t.Fatal("expected overlapping squares to intersect")
	}
}

func TestIntersectsContainment(t *testing.T) {
	a, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,20],[20,20],[20,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[5,5],[5,8],[8,8],[8,5],[5,5]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !Intersects(a, b) {
		t.Fatal("expected fully nested polygon to intersect via containment")
	}
}

func TestIntersectsDisjoint(t *testing.T) {
	a, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[0,0],[0,10],[10,10],[10,0],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[50,50],[50,60],[60,60],[60,50],[50,50]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if Intersects(a, b) {
		t.Fatal("expected disjoint squares to not intersect")
	}
}

func TestIntersectsMultiPolygon(t *testing.T) {
	a, err := ParseGeometry([]byte(`{"type":"MultiPolygon","coordinates":[
		[[[0,0],[0,10],[10,10],[10,0],[0,0]]],
		[[[50,50],[50,60],[60,60],[60,50],[50,50]]]
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseGeometry([]byte(`{"type":"Polygon","coordinates":[[[55,55],[55,58],[58,58],[58,55],[55,55]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !Intersects(a, b) {
		t.Fatal("expected MultiPolygon to intersect via its second sub-polygon")
	}
}
