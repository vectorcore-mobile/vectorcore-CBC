package cap

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// circleSegments is the number of vertices used to approximate a CAP
// <circle> as a polygon ring. internal/inventory's spatial matcher has no
// native circle math - and never actually consults a cell's NominalRadiusM
// when no coverage_geojson is set - so a circle is approximated as a
// polygon immediately at parse time, reusing the same tested
// Intersects/ContainsPoint/SelectionPreview pipeline PolygonToGeoJSON
// already feeds.
const circleSegments = 64

// kmPerDegreeLatitude is the standard approximation (Earth's mean radius),
// matching the precision level already used elsewhere in this codebase -
// no geodesic library is used anywhere in internal/inventory either.
const kmPerDegreeLatitude = 111.32

// PolygonToGeoJSON converts a CAP 1.2 Area.Polygon coordinate string
// ("lat,lon lat,lon ...", TS/CAP's native whitespace-separated pair format)
// into a validated GeoJSON Polygon, swapping to GeoJSON's [longitude,
// latitude] position order and auto-closing the ring if the caller didn't
// repeat the first vertex.
func PolygonToGeoJSON(polygon string) ([]byte, error) {
	fields := strings.Fields(polygon)
	if len(fields) < 3 {
		return nil, fmt.Errorf("CAP polygon has fewer than 3 positions: %q", polygon)
	}
	coords := make([][2]float64, 0, len(fields)+1)
	for _, pair := range fields {
		parts := strings.SplitN(pair, ",", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed CAP coordinate pair %q", pair)
		}
		lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid CAP latitude in %q: %w", pair, err)
		}
		lon, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid CAP longitude in %q: %w", pair, err)
		}
		coords = append(coords, [2]float64{lon, lat})
	}
	if coords[0] != coords[len(coords)-1] {
		coords = append(coords, coords[0])
	}
	doc := struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}{Type: "Polygon", Coordinates: [][][2]float64{coords}}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// CircleToGeoJSON converts a CAP 1.2 Area.Circle coordinate string
// ("lat,lon radius", TS/CAP's native format with radius in kilometers)
// into a validated GeoJSON Polygon approximating the circle with
// circleSegments vertices. See the circleSegments doc comment for why an
// approximation, not a native circle type.
func CircleToGeoJSON(circle string) ([]byte, error) {
	fields := strings.Fields(circle)
	if len(fields) != 2 {
		return nil, fmt.Errorf("CAP circle must be \"lat,lon radius\": %q", circle)
	}
	parts := strings.SplitN(fields[0], ",", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed CAP circle center %q", fields[0])
	}
	lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid CAP circle latitude in %q: %w", fields[0], err)
	}
	lon, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid CAP circle longitude in %q: %w", fields[0], err)
	}
	radiusKm, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid CAP circle radius in %q: %w", fields[1], err)
	}
	if radiusKm <= 0 {
		return nil, fmt.Errorf("CAP circle radius must be positive: %q", circle)
	}

	deltaLat := radiusKm / kmPerDegreeLatitude
	deltaLon := radiusKm / (kmPerDegreeLatitude * math.Cos(lat*math.Pi/180))

	coords := make([][2]float64, 0, circleSegments+1)
	for i := 0; i < circleSegments; i++ {
		theta := 2 * math.Pi * float64(i) / float64(circleSegments)
		coords = append(coords, [2]float64{lon + deltaLon*math.Cos(theta), lat + deltaLat*math.Sin(theta)})
	}
	coords = append(coords, coords[0])

	doc := struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}{Type: "Polygon", Coordinates: [][][2]float64{coords}}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return b, nil
}
