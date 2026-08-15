import L from 'leaflet'

// Mirrors internal/cap/polygon.go's CircleToGeoJSON exactly: the CBC's
// spatial matcher (internal/inventory/selection.go) has no native circle
// math, so a drawn circle is approximated as an N-vertex polygon ring right
// here, immediately, using the same simple degree-per-km math (no geodesic
// library - matching the precision level already used server-side).
const CIRCLE_SEGMENTS = 64
const KM_PER_DEGREE_LATITUDE = 111.32

function circleToRing(lat, lng, radiusMeters) {
  const radiusKm = radiusMeters / 1000
  const deltaLat = radiusKm / KM_PER_DEGREE_LATITUDE
  const deltaLon = radiusKm / (KM_PER_DEGREE_LATITUDE * Math.cos((lat * Math.PI) / 180))
  const ring = []
  for (let i = 0; i < CIRCLE_SEGMENTS; i++) {
    const theta = (2 * Math.PI * i) / CIRCLE_SEGMENTS
    ring.push([lng + deltaLon * Math.cos(theta), lat + deltaLat * Math.sin(theta)])
  }
  ring.push(ring[0])
  return ring
}

// layerToPolygonRings returns one shape's rings in GeoJSON Polygon
// coordinate shape ([exteriorRing, ...holes]) regardless of whether it was
// drawn as a polygon, rectangle, or circle.
function layerToPolygonRings(layer) {
  if (layer instanceof L.Circle) {
    const { lat, lng } = layer.getLatLng()
    return [circleToRing(lat, lng, layer.getRadius())]
  }
  const geo = layer.toGeoJSON()
  return geo.geometry?.coordinates || null
}

// featureGroupToGeoJSON converts every shape drawn in featureGroup (via
// leaflet-draw) into a bare GeoJSON Polygon (one shape) or MultiPolygon
// (more than one shape) - the shape internal/inventory.ParseGeometry
// accepts directly. Returns null if nothing has been drawn.
export function featureGroupToGeoJSON(featureGroup) {
  if (!featureGroup) return null
  const polygons = []
  featureGroup.eachLayer((layer) => {
    const rings = layerToPolygonRings(layer)
    if (rings && rings.length) polygons.push(rings)
  })
  if (polygons.length === 0) return null
  if (polygons.length === 1) {
    return { type: 'Polygon', coordinates: polygons[0] }
  }
  return { type: 'MultiPolygon', coordinates: polygons }
}
