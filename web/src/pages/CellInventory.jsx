import React, { useState, useRef } from 'react'
import { Upload, Download, XCircle, Trash2 } from 'lucide-react'
import { usePoller } from '../hooks/usePoller.js'
import { getCells, importCellInventory, exportCellInventory, previewSelection, createCell, deleteCell } from '../api/client.js'
import { useToast } from '../components/Toast.jsx'
import Spinner from '../components/Spinner.jsx'
import Badge from '../components/Badge.jsx'
import Modal from '../components/Modal.jsx'
import ShapeDrawMap from '../components/ShapeDrawMap.jsx'

const GEOMETRY_QUALITIES = ['engineered_polygon', 'propagation_model', 'sector_estimate', 'point_radius', 'site_point', 'unknown']

const EMPTY_CELL_FORM = {
  mcc: '', mnc: '', mncLength: 3,
  enbId: '', localCellId: '', tac: '',
  cellName: '', mmeName: '',
  latitude: '', longitude: '', nominalRadiusM: '', azimuthDeg: '', beamwidthDeg: '',
  geometryQuality: 'unknown', source: '', sourceRecordId: '', sourceVersion: '',
  active: true,
}

export default function CellInventory() {
  const toast = useToast()
  const fileInput = useRef(null)
  const coverageMapRef = useRef(null)
  const [mode, setMode] = useState('validate-only')
  const [importing, setImporting] = useState(false)
  const [preview, setPreview] = useState(null)
  const [previewErr, setPreviewErr] = useState(null)
  const [plmn, setPlmn] = useState({ mcc: '', mnc: '', mncLength: 3 })
  const [previewArea, setPreviewArea] = useState(null)
  const [previewMapOpen, setPreviewMapOpen] = useState(false)

  const [addOpen, setAddOpen] = useState(false)
  const [form, setForm] = useState(EMPTY_CELL_FORM)
  const [coverageArea, setCoverageArea] = useState(null)
  const [coverageMapOpen, setCoverageMapOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createErr, setCreateErr] = useState(null)
  const [deletingId, setDeletingId] = useState(null)

  const { data: cellData, error, errorStatus, loading, refresh } = usePoller(() => getCells({ limit: 200 }), 15000)

  const setF = (k, v) => setForm(f => ({ ...f, [k]: v }))

  const eci = (Number(form.enbId) >= 0 && Number(form.localCellId) >= 0 && form.enbId !== '' && form.localCellId !== '')
    ? (Number(form.enbId) << 8) | Number(form.localCellId)
    : null

  function openAdd() {
    setForm(EMPTY_CELL_FORM)
    setCoverageArea(null)
    setCreateErr(null)
    coverageMapRef.current?.clear()
    setAddOpen(true)
  }

  function handleMapClick({ lat, lng }) {
    setF('latitude', String(lat.toFixed(6)))
    setF('longitude', String(lng.toFixed(6)))
  }

  async function handleAddCell(e) {
    e.preventDefault()
    setCreateErr(null)
    setCreating(true)
    const numOrUndef = (v) => (v === '' || v === null || v === undefined ? undefined : Number(v))
    try {
      await createCell({
        mcc: form.mcc, mnc: form.mnc, mncLength: Number(form.mncLength),
        enbId: Number(form.enbId), localCellId: Number(form.localCellId), tac: Number(form.tac),
        cellName: form.cellName, mmeName: form.mmeName,
        latitude: numOrUndef(form.latitude), longitude: numOrUndef(form.longitude),
        nominalRadiusM: numOrUndef(form.nominalRadiusM), azimuthDeg: numOrUndef(form.azimuthDeg), beamwidthDeg: numOrUndef(form.beamwidthDeg),
        coverageGeoJSON: coverageArea ? JSON.stringify(coverageArea) : '',
        geometryQuality: form.geometryQuality,
        source: form.source, sourceRecordId: form.sourceRecordId, sourceVersion: form.sourceVersion,
        active: form.active,
      })
      toast.success('Cell created', `ECI ${eci}`)
      setAddOpen(false)
      refresh()
    } catch (err) {
      setCreateErr(err.message)
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete(cell) {
    if (!window.confirm(`Delete cell ECI ${cell.eci}${cell.cellName ? ` (${cell.cellName})` : ''}?`)) return
    setDeletingId(cell.id)
    try {
      await deleteCell(cell.id)
      toast.success('Cell deleted', `ECI ${cell.eci}`)
      refresh()
    } catch (err) {
      if (err.status === 409) {
        toast.error('Cannot delete', 'This cell has geo code mappings. Remove them first on the Geo Codes page.')
      } else {
        toast.error('Delete failed', err.message)
      }
    } finally {
      setDeletingId(null)
    }
  }

  async function handleImport(e) {
    const file = e.target.files?.[0]
    if (!file) return
    setImporting(true)
    try {
      const result = await importCellInventory(file, mode)
      toast.success('Import complete', `${result.status}: ${result.inserted} inserted, ${result.updated} updated, ${result.rowsRejected} rejected`)
      refresh()
    } catch (err) {
      toast.error('Import failed', err.message)
    } finally {
      setImporting(false)
      if (fileInput.current) fileInput.current.value = ''
    }
  }

  async function handleExport() {
    try {
      const { blob, filename } = await exportCellInventory('csv')
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename
      a.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      toast.error('Export failed', err.message)
    }
  }

  async function handlePreview(e) {
    e.preventDefault()
    setPreviewErr(null)
    setPreview(null)
    if (!previewArea) {
      setPreviewErr('Draw an area on the map first.')
      return
    }
    try {
      const result = await previewSelection({
        plmn: { mcc: plmn.mcc, mnc: plmn.mnc, mncLength: Number(plmn.mncLength) },
        policy: 'conservative-intersection',
        area: previewArea,
      })
      setPreview(result)
    } catch (err) {
      setPreviewErr(err.message || String(err))
    }
  }

  const cells = cellData?.cells || []

  if (errorStatus === 404) {
    return (
      <div>
        <div className="page-header">
          <div>
            <div className="page-title">Cell Inventory</div>
          </div>
        </div>
        <div className="empty-state">
          Cell inventory is not enabled on this CBC.
          Set <code>cell_inventory.enabled: true</code> in the server config to use this page.
        </div>
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="page-title">Cell Inventory</div>
          <div className="page-subtitle">{cellData ? `${cellData.total} cells` : ''}</div>
        </div>
        <div className="flex gap-8 items-center">
          <button className="btn btn-primary" onClick={openAdd}>+ Add Cell</button>
          <select className="select" value={mode} onChange={e => setMode(e.target.value)}>
            <option value="validate-only">validate-only</option>
            <option value="merge">merge</option>
            <option value="replace">replace</option>
          </select>
          <button className="btn" onClick={() => fileInput.current?.click()} disabled={importing}>
            <Upload size={14} /> Import CSV
          </button>
          <input ref={fileInput} type="file" accept=".csv,text/csv" style={{ display: 'none' }} onChange={handleImport} />
          <button className="btn" onClick={handleExport}>
            <Download size={14} /> Export CSV
          </button>
        </div>
      </div>

      {loading && !cellData && <Spinner />}
      {error && (
        <div className="error-state">
          <XCircle size={32} className="error-icon" />
          <div>{error}</div>
        </div>
      )}

      {cellData && (
        cells.length === 0 ? (
          <div className="empty-state">No cells imported</div>
        ) : (
          <div className="table-container">
            <table>
              <thead>
                <tr>
                  <th>ECI</th><th>eNB ID</th><th>Cell ID</th><th>TAC</th><th>Name</th><th>MME</th><th>State</th><th></th>
                </tr>
              </thead>
              <tbody>
                {cells.map(c => (
                  <tr key={c.id}>
                    <td className="mono">{c.eci}</td>
                    <td className="mono">{c.enbId}</td>
                    <td className="mono">{c.localCellId}</td>
                    <td className="mono">{c.tac}</td>
                    <td>{c.cellName || '—'}</td>
                    <td>{c.mmeName || '—'}</td>
                    <td><Badge state={c.active ? 'enabled' : 'disabled'} /></td>
                    <td>
                      <button className="btn-icon danger" onClick={() => handleDelete(c)} disabled={deletingId === c.id} title="Delete">
                        <Trash2 size={14} />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}
      {cellData && cells.length > 0 && (
        <div className="text-sm text-muted mt-8">Showing {cells.length} of {cellData.total} total cells</div>
      )}

      <div className="section-title mt-20">Selection Preview</div>
      <form onSubmit={handlePreview}>
        <div className="form-row-3">
          <div className="form-group">
            <label className="form-label">MCC</label>
            <input className="input mono" value={plmn.mcc} onChange={e => setPlmn({ ...plmn, mcc: e.target.value })} placeholder="311" required />
          </div>
          <div className="form-group">
            <label className="form-label">MNC</label>
            <input className="input mono" value={plmn.mnc} onChange={e => setPlmn({ ...plmn, mnc: e.target.value })} placeholder="435" required />
          </div>
          <div className="form-group">
            <label className="form-label">MNC Length</label>
            <select className="select" value={plmn.mncLength} onChange={e => setPlmn({ ...plmn, mncLength: e.target.value })}>
              <option value={2}>2</option>
              <option value={3}>3</option>
            </select>
          </div>
        </div>
        <div className="form-group mt-12">
          <label className="form-label">Area</label>
          <div className="flex gap-8 items-center">
            <span className="text-sm text-muted">{previewArea ? 'Area drawn' : 'No area drawn yet'}</span>
            <button type="button" className="btn" onClick={() => setPreviewMapOpen(true)}>Draw Area</button>
          </div>
        </div>
        <button className="btn btn-primary mt-12" type="submit">Preview Selection</button>
      </form>

      {previewErr && (
        <div className="error-state">
          <XCircle size={32} className="error-icon" />
          <div>{previewErr}</div>
        </div>
      )}
      {preview && (
        <div className="mt-16">
          <p>{preview.selectedCount} of {preview.candidateCount} candidate cells selected (policy: {preview.policy})</p>
          <div className="table-container">
            <table>
              <thead><tr><th>ECI</th><th>eNB ID</th><th>TAC</th><th>MME</th><th>Reason</th></tr></thead>
              <tbody>
                {(preview.cells || []).map(c => (
                  <tr key={c.id}>
                    <td className="mono">{c.eci}</td>
                    <td className="mono">{c.enbId}</td>
                    <td className="mono">{c.tac}</td>
                    <td>{c.mmeName || '—'}</td>
                    <td>{c.selectionReason}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <ShapeDrawMap
        open={previewMapOpen}
        onClose={() => setPreviewMapOpen(false)}
        onShapesChange={setPreviewArea}
        title="Draw Selection Preview Area"
      />

      {addOpen && (
        <Modal title="Add Cell" onClose={() => setAddOpen(false)} size="xl">
          <form onSubmit={handleAddCell}>
            <div className="modal-body">
              {createErr && <div className="error-msg">{createErr}</div>}

              <div className="section-title" style={{ marginTop: 0 }}>Identity</div>
              <div className="form-row-3">
                <div className="form-group">
                  <label className="form-label">MCC</label>
                  <input className="input mono" value={form.mcc} onChange={e => setF('mcc', e.target.value)} placeholder="311" required />
                </div>
                <div className="form-group">
                  <label className="form-label">MNC</label>
                  <input className="input mono" value={form.mnc} onChange={e => setF('mnc', e.target.value)} placeholder="435" required />
                </div>
                <div className="form-group">
                  <label className="form-label">MNC Length</label>
                  <select className="select" value={form.mncLength} onChange={e => setF('mncLength', e.target.value)}>
                    <option value={2}>2</option>
                    <option value={3}>3</option>
                  </select>
                </div>
              </div>
              <div className="form-row-3">
                <div className="form-group">
                  <label className="form-label">eNB ID</label>
                  <input className="input mono" type="number" min="0" max="1048575" value={form.enbId} onChange={e => setF('enbId', e.target.value)} required />
                </div>
                <div className="form-group">
                  <label className="form-label">Local Cell ID</label>
                  <input className="input mono" type="number" min="0" max="255" value={form.localCellId} onChange={e => setF('localCellId', e.target.value)} required />
                </div>
                <div className="form-group">
                  <label className="form-label">ECI (computed)</label>
                  <input className="input mono" value={eci ?? ''} readOnly disabled />
                </div>
              </div>
              <div className="form-row-3">
                <div className="form-group">
                  <label className="form-label">TAC</label>
                  <input className="input mono" type="number" min="0" max="65535" value={form.tac} onChange={e => setF('tac', e.target.value)} required />
                </div>
                <div className="form-group">
                  <label className="form-label">Cell Name</label>
                  <input className="input" value={form.cellName} onChange={e => setF('cellName', e.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label">MME Name</label>
                  <input className="input" value={form.mmeName} onChange={e => setF('mmeName', e.target.value)} />
                </div>
              </div>

              <div className="section-title">Location &amp; Coverage</div>
              <div className="form-row-3">
                <div className="form-group">
                  <label className="form-label">Latitude</label>
                  <input className="input mono" type="number" step="any" value={form.latitude} onChange={e => setF('latitude', e.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label">Longitude</label>
                  <input className="input mono" type="number" step="any" value={form.longitude} onChange={e => setF('longitude', e.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label">Nominal Radius (m)</label>
                  <input className="input mono" type="number" step="any" min="0" value={form.nominalRadiusM} onChange={e => setF('nominalRadiusM', e.target.value)} />
                </div>
              </div>
              <div className="form-row-3">
                <div className="form-group">
                  <label className="form-label">Azimuth (deg)</label>
                  <input className="input mono" type="number" step="any" min="0" max="359.999" value={form.azimuthDeg} onChange={e => setF('azimuthDeg', e.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label">Beamwidth (deg)</label>
                  <input className="input mono" type="number" step="any" min="0" max="360" value={form.beamwidthDeg} onChange={e => setF('beamwidthDeg', e.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label">Geometry Quality</label>
                  <select className="select" value={form.geometryQuality} onChange={e => setF('geometryQuality', e.target.value)} required>
                    {GEOMETRY_QUALITIES.map(q => <option key={q} value={q}>{q}</option>)}
                  </select>
                </div>
              </div>
              <div className="form-group">
                <label className="form-label">Coverage Area <span style={{ fontWeight: 400, textTransform: 'none' }}>— draw on a map, or leave empty for a point-only cell. Omnidirectional antennas: draw a circle.</span></label>
                <div className="flex gap-8 items-center">
                  <span className="text-sm text-muted">{coverageArea ? 'Coverage area drawn' : 'No coverage area drawn'}</span>
                  <button type="button" className="btn" onClick={() => setCoverageMapOpen(true)}>Draw Coverage Area</button>
                  {coverageArea && <button type="button" className="btn" onClick={() => setCoverageArea(null)}>Clear</button>}
                </div>
                <div className="text-sm text-muted mt-8">Clicking the map (outside the draw tools) also fills in Latitude/Longitude above.</div>
              </div>

              <div className="section-title">Source &amp; Status</div>
              <div className="form-row-3">
                <div className="form-group">
                  <label className="form-label">Source</label>
                  <input className="input" value={form.source} onChange={e => setF('source', e.target.value)} placeholder="manual" />
                </div>
                <div className="form-group">
                  <label className="form-label">Source Record ID</label>
                  <input className="input" value={form.sourceRecordId} onChange={e => setF('sourceRecordId', e.target.value)} />
                </div>
                <div className="form-group">
                  <label className="form-label">Source Version</label>
                  <input className="input" value={form.sourceVersion} onChange={e => setF('sourceVersion', e.target.value)} />
                </div>
              </div>
              <div className="form-group">
                <label className="form-label">Active</label>
                <select className="select" value={form.active ? 'true' : 'false'} onChange={e => setF('active', e.target.value === 'true')}>
                  <option value="true">true</option>
                  <option value="false">false</option>
                </select>
              </div>
            </div>
            <div className="modal-footer">
              <button type="button" className="btn" onClick={() => setAddOpen(false)}>Cancel</button>
              <button type="submit" className="btn btn-primary" disabled={creating}>{creating ? 'Creating…' : 'Create Cell'}</button>
            </div>
          </form>
        </Modal>
      )}

      <ShapeDrawMap
        ref={coverageMapRef}
        open={coverageMapOpen}
        onClose={() => setCoverageMapOpen(false)}
        onShapesChange={setCoverageArea}
        onMapClick={handleMapClick}
        title="Draw Cell Coverage Area"
      />
    </div>
  )
}
