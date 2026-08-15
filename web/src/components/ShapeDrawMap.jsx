import React, { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import 'leaflet-draw'
import 'leaflet-draw/dist/leaflet.draw.css'
import { featureGroupToGeoJSON } from '../lib/geometry.js'

// ShapeDrawMap is a reusable Leaflet + leaflet-draw popup, mirroring the
// map-popup pattern in /usr/src/vectorcore-eag's CBE.jsx (read-only
// reference): a persistently-mounted map (kept alive behind display:none
// while closed, so drawn shapes survive between opens) with polygon/
// rectangle/circle drawing enabled. Used both for a cell's coverage area
// and for Selection Preview's request area, so the map setup lives in one
// place instead of two copies.
//
// Exposes clear() via ref - since the map stays mounted across popup
// open/close cycles by design, a caller that resets its own form state
// (e.g. opening a fresh "Add Cell" after cancelling a previous attempt)
// needs an explicit way to also clear whatever shapes are still drawn on
// the map, rather than the two silently drifting out of sync.
const ShapeDrawMap = forwardRef(function ShapeDrawMap({ open, onClose, onShapesChange, onMapClick, title = 'Draw Area' }, ref) {
  const containerRef = useRef(null)
  const mapRef = useRef(null)
  const featureGroupRef = useRef(null)
  const pickedMarkerRef = useRef(null)
  const [shapeCount, setShapeCount] = useState(0)

  const onShapesChangeRef = useRef(onShapesChange)
  useEffect(() => { onShapesChangeRef.current = onShapesChange })
  const onMapClickRef = useRef(onMapClick)
  useEffect(() => { onMapClickRef.current = onMapClick })

  // Init map + draw controls once; kept mounted for the component's whole
  // lifetime regardless of `open`, so shapes aren't lost when the popup
  // closes and reopens.
  useEffect(() => {
    if (!containerRef.current || mapRef.current) return

    const map = L.map(containerRef.current, { zoomControl: true, attributionControl: true })
    map.setView([38, -96], 4)

    // Light basemap - better contrast for the orange draw overlay than a
    // dark basemap, matching EAG's CBE.jsx choice for the same reason.
    L.tileLayer('https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png', {
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/">CARTO</a>',
      subdomains: 'abcd',
      maxZoom: 19,
    }).addTo(map)

    const featureGroup = new L.FeatureGroup().addTo(map)
    featureGroupRef.current = featureGroup

    const drawControl = new L.Control.Draw({
      position: 'topright',
      draw: {
        polygon: { allowIntersection: false, showArea: false, shapeOptions: { color: '#f5a623' } },
        rectangle: { showArea: false, shapeOptions: { color: '#f5a623' } },
        circle: { shapeOptions: { color: '#f5a623' } },
        circlemarker: false,
        marker: false,
        polyline: false,
      },
      edit: { featureGroup, remove: true },
    })
    map.addControl(drawControl)

    const notify = () => {
      setShapeCount(featureGroup.getLayers().length)
      onShapesChangeRef.current?.(featureGroupToGeoJSON(featureGroup))
    }
    map.on(L.Draw.Event.CREATED, (e) => { featureGroup.addLayer(e.layer); notify() })
    map.on(L.Draw.Event.EDITED, notify)
    map.on(L.Draw.Event.DELETED, notify)

    // Optional: clicking the map (independent of the draw toolbar) picks a
    // point - used by the Add Cell popup to set Latitude/Longitude.
    // CircleMarker is vector-rendered (no icon image asset), sidestepping
    // Leaflet's well-known default-marker-icon-path issue under bundlers.
    map.on('click', (e) => {
      if (!onMapClickRef.current) return
      if (pickedMarkerRef.current) map.removeLayer(pickedMarkerRef.current)
      pickedMarkerRef.current = L.circleMarker(e.latlng, { radius: 6, color: '#3a8fd4', fillOpacity: 1 }).addTo(map)
      onMapClickRef.current(e.latlng)
    })

    mapRef.current = map

    return () => {
      map.remove()
      mapRef.current = null
    }
  }, [])

  // The map container sits behind display:none while closed (0x0 sized) -
  // recalculate tile layout once it becomes visible again.
  useEffect(() => {
    if (!open || !mapRef.current) return
    const id = setTimeout(() => mapRef.current.invalidateSize(), 50)
    return () => clearTimeout(id)
  }, [open])

  const clearShapes = () => {
    featureGroupRef.current?.clearLayers()
    setShapeCount(0)
    onShapesChange?.(null)
  }

  useImperativeHandle(ref, () => ({ clear: clearShapes }), [])

  return (
    <div
      className="modal-backdrop"
      style={{
        display: open ? 'flex' : 'none',
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.5)',
        alignItems: 'center', justifyContent: 'center', zIndex: 1000,
      }}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div style={{
        background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6,
        width: '90vw', height: '85vh', maxWidth: 1400,
        display: 'flex', flexDirection: 'column', overflow: 'hidden',
      }}>
        <div style={{
          display: 'flex', justifyContent: 'space-between', alignItems: 'center',
          padding: '12px 16px', borderBottom: '1px solid var(--border)',
        }}>
          <div style={{ fontFamily: 'var(--font-ui)', fontWeight: 700, letterSpacing: '0.05em', textTransform: 'uppercase', color: 'var(--accent)' }}>
            {title}
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <span style={{ fontSize: 12, color: 'var(--muted)' }}>{shapeCount} shape{shapeCount === 1 ? '' : 's'} drawn</span>
            <button type="button" className="btn" onClick={clearShapes} disabled={shapeCount === 0}>Clear Shapes</button>
            <button type="button" className="btn btn-primary" onClick={onClose}>Done</button>
          </div>
        </div>
        <div ref={containerRef} style={{ flex: 1, width: '100%' }} />
      </div>
    </div>
  )
})

export default ShapeDrawMap
