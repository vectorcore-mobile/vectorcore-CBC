package inventory

import (
	"encoding/json"
	"fmt"
)

// geoPoint is a coordinate in GeoJSON's [longitude, latitude] order.
type geoPoint struct {
	Lon, Lat float64
}

// Ring is a closed sequence of positions (first position equals last).
type Ring []geoPoint

// Geometry is a validated, normalized coverage or request area. Polygons
// holds one entry per polygon (a MultiPolygon has more than one); each
// polygon is a list of rings where the first ring is the exterior and any
// further rings are holes, matching GeoJSON nesting.
type Geometry struct {
	Type     string
	Polygons [][]Ring
}

type rawGeoJSON struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// ParseGeometry validates a GeoJSON Polygon or MultiPolygon. It rejects
// malformed JSON, unsupported geometry types, empty geometry, out-of-range
// coordinates, and rings with fewer than 3 distinct vertices. As a
// documented, tested normalization it auto-closes a ring whose last position
// does not repeat the first, provided the ring otherwise has at least 3
// vertices; it never repairs any other malformed geometry.
func ParseGeometry(raw []byte) (Geometry, error) {
	var g rawGeoJSON
	if err := json.Unmarshal(raw, &g); err != nil {
		return Geometry{}, fmt.Errorf("invalid GeoJSON: %w", err)
	}
	switch g.Type {
	case "Polygon":
		var coords [][][]float64
		if err := json.Unmarshal(g.Coordinates, &coords); err != nil {
			return Geometry{}, fmt.Errorf("invalid Polygon coordinates: %w", err)
		}
		rings, err := buildRings(coords)
		if err != nil {
			return Geometry{}, err
		}
		return Geometry{Type: "Polygon", Polygons: [][]Ring{rings}}, nil
	case "MultiPolygon":
		var coords [][][][]float64
		if err := json.Unmarshal(g.Coordinates, &coords); err != nil {
			return Geometry{}, fmt.Errorf("invalid MultiPolygon coordinates: %w", err)
		}
		if len(coords) == 0 {
			return Geometry{}, fmt.Errorf("MultiPolygon has no polygons")
		}
		polys := make([][]Ring, 0, len(coords))
		for pi, polyCoords := range coords {
			rings, err := buildRings(polyCoords)
			if err != nil {
				return Geometry{}, fmt.Errorf("polygon %d: %w", pi, err)
			}
			polys = append(polys, rings)
		}
		return Geometry{Type: "MultiPolygon", Polygons: polys}, nil
	default:
		return Geometry{}, fmt.Errorf("unsupported geometry type %q: only Polygon and MultiPolygon are accepted", g.Type)
	}
}

func buildRings(coords [][][]float64) ([]Ring, error) {
	if len(coords) == 0 {
		return nil, fmt.Errorf("polygon has no rings")
	}
	rings := make([]Ring, 0, len(coords))
	for ri, positions := range coords {
		if len(positions) < 3 {
			return nil, fmt.Errorf("ring %d has fewer than 3 positions", ri)
		}
		ring := make(Ring, 0, len(positions)+1)
		for pi, pos := range positions {
			if len(pos) < 2 {
				return nil, fmt.Errorf("ring %d position %d is not a [longitude,latitude] pair", ri, pi)
			}
			lon, lat := pos[0], pos[1]
			if lon < -180 || lon > 180 {
				return nil, fmt.Errorf("ring %d position %d longitude %.6f out of range", ri, pi, lon)
			}
			if lat < -90 || lat > 90 {
				return nil, fmt.Errorf("ring %d position %d latitude %.6f out of range", ri, pi, lat)
			}
			ring = append(ring, geoPoint{Lon: lon, Lat: lat})
		}
		if ring[0] != ring[len(ring)-1] {
			ring = append(ring, ring[0])
		}
		if len(ring) < 4 {
			return nil, fmt.Errorf("ring %d does not close into a polygon with at least 3 distinct vertices", ri)
		}
		rings = append(rings, ring)
	}
	return rings, nil
}

// Bounds returns the geometry's bounding box.
func (g Geometry) Bounds() Bounds {
	var b Bounds
	first := true
	for _, poly := range g.Polygons {
		for _, ring := range poly {
			for _, p := range ring {
				if first {
					b = Bounds{MinLatitude: p.Lat, MaxLatitude: p.Lat, MinLongitude: p.Lon, MaxLongitude: p.Lon}
					first = false
					continue
				}
				if p.Lat < b.MinLatitude {
					b.MinLatitude = p.Lat
				}
				if p.Lat > b.MaxLatitude {
					b.MaxLatitude = p.Lat
				}
				if p.Lon < b.MinLongitude {
					b.MinLongitude = p.Lon
				}
				if p.Lon > b.MaxLongitude {
					b.MaxLongitude = p.Lon
				}
			}
		}
	}
	return b
}

// MarshalCanonical re-serializes the geometry to normalized GeoJSON text.
// This is what gets stored and exported, so ring auto-closing is reflected
// consistently rather than only in memory.
func (g Geometry) MarshalCanonical() (string, error) {
	type doc struct {
		Type        string `json:"type"`
		Coordinates any    `json:"coordinates"`
	}
	switch g.Type {
	case "Polygon":
		b, err := json.Marshal(doc{Type: g.Type, Coordinates: ringsToCoords(g.Polygons[0])})
		return string(b), err
	case "MultiPolygon":
		coords := make([][][][2]float64, len(g.Polygons))
		for i, poly := range g.Polygons {
			coords[i] = ringsToCoords(poly)
		}
		b, err := json.Marshal(doc{Type: g.Type, Coordinates: coords})
		return string(b), err
	default:
		return "", fmt.Errorf("unsupported geometry type %q", g.Type)
	}
}

func ringsToCoords(rings []Ring) [][][2]float64 {
	out := make([][][2]float64, len(rings))
	for i, ring := range rings {
		positions := make([][2]float64, len(ring))
		for j, p := range ring {
			positions[j] = [2]float64{p.Lon, p.Lat}
		}
		out[i] = positions
	}
	return out
}

// ContainsPoint reports whether (lon,lat) lies inside the geometry, applying
// the even-odd rule across every ring of every polygon so holes are
// respected.
func (g Geometry) ContainsPoint(lon, lat float64) bool {
	p := geoPoint{Lon: lon, Lat: lat}
	for _, poly := range g.Polygons {
		if pointInPolygonRings(poly, p) {
			return true
		}
	}
	return false
}

func pointInPolygonRings(rings []Ring, p geoPoint) bool {
	crossings := 0
	for _, ring := range rings {
		crossings += rayCrossings(ring, p)
	}
	return crossings%2 == 1
}

// rayCrossings counts crossings of a horizontal ray cast from p in the
// direction of increasing longitude against ring's edges (PNPOLY / even-odd
// rule).
func rayCrossings(ring Ring, p geoPoint) int {
	count := 0
	for i := 0; i < len(ring)-1; i++ {
		a, b := ring[i], ring[i+1]
		if (a.Lat > p.Lat) == (b.Lat > p.Lat) {
			continue
		}
		t := (p.Lat - a.Lat) / (b.Lat - a.Lat)
		if a.Lon+t*(b.Lon-a.Lon) > p.Lon {
			count++
		}
	}
	return count
}

// Intersects reports whether a and b share any area: a fast bounding-box
// reject, then vertex-in-polygon containment (handles full nesting) and
// edge-segment intersection (handles partial overlap).
func Intersects(a, b Geometry) bool {
	if !boundsOverlap(a.Bounds(), b.Bounds()) {
		return false
	}
	for _, polyA := range a.Polygons {
		for _, polyB := range b.Polygons {
			if polygonsIntersect(polyA, polyB) {
				return true
			}
		}
	}
	return false
}

func boundsOverlap(a, b Bounds) bool {
	return a.MinLongitude <= b.MaxLongitude && a.MaxLongitude >= b.MinLongitude &&
		a.MinLatitude <= b.MaxLatitude && a.MaxLatitude >= b.MinLatitude
}

func polygonsIntersect(polyA, polyB []Ring) bool {
	if len(polyA) == 0 || len(polyB) == 0 {
		return false
	}
	for _, p := range polyA[0] {
		if pointInPolygonRings(polyB, p) {
			return true
		}
	}
	for _, p := range polyB[0] {
		if pointInPolygonRings(polyA, p) {
			return true
		}
	}
	for _, ringA := range polyA {
		for i := 0; i < len(ringA)-1; i++ {
			for _, ringB := range polyB {
				for j := 0; j < len(ringB)-1; j++ {
					if segmentsIntersect(ringA[i], ringA[i+1], ringB[j], ringB[j+1]) {
						return true
					}
				}
			}
		}
	}
	return false
}

func orientation(a, b, c geoPoint) int {
	val := (b.Lon-a.Lon)*(c.Lat-a.Lat) - (b.Lat-a.Lat)*(c.Lon-a.Lon)
	switch {
	case val > 0:
		return 1
	case val < 0:
		return 2
	default:
		return 0
	}
}

func onSegment(a, b, c geoPoint) bool {
	return b.Lon <= max(a.Lon, c.Lon) && b.Lon >= min(a.Lon, c.Lon) &&
		b.Lat <= max(a.Lat, c.Lat) && b.Lat >= min(a.Lat, c.Lat)
}

// segmentsIntersect is the standard orientation-based segment intersection
// test, including the collinear/touching edge cases.
func segmentsIntersect(p1, q1, p2, q2 geoPoint) bool {
	o1, o2 := orientation(p1, q1, p2), orientation(p1, q1, q2)
	o3, o4 := orientation(p2, q2, p1), orientation(p2, q2, q1)
	if o1 != o2 && o3 != o4 {
		return true
	}
	if o1 == 0 && onSegment(p1, p2, q1) {
		return true
	}
	if o2 == 0 && onSegment(p1, q2, q1) {
		return true
	}
	if o3 == 0 && onSegment(p2, p1, q2) {
		return true
	}
	if o4 == 0 && onSegment(p2, q1, q2) {
		return true
	}
	return false
}

// ValidateCoverageGeoJSON validates raw GeoJSON text for a cell's coverage
// geometry and returns the normalized text plus its bounding box.
func ValidateCoverageGeoJSON(raw string) (string, Bounds, error) {
	g, err := ParseGeometry([]byte(raw))
	if err != nil {
		return "", Bounds{}, err
	}
	norm, err := g.MarshalCanonical()
	if err != nil {
		return "", Bounds{}, err
	}
	return norm, g.Bounds(), nil
}
