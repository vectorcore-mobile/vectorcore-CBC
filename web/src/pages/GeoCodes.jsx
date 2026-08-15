import React, { useState, useRef, useEffect } from 'react'
import { Upload, Download, XCircle, Trash2 } from 'lucide-react'
import { usePoller } from '../hooks/usePoller.js'
import {
  getCells, listGeocodes, createGeocode, deleteGeocode, resolveGeocode, importGeocodes, exportGeocodes,
  listGeocodeRegistry, createGeocodeRegistryEntry, deleteGeocodeRegistryEntry,
} from '../api/client.js'
import { useToast } from '../components/Toast.jsx'
import Spinner from '../components/Spinner.jsx'

export default function GeoCodes() {
  const toast = useToast()
  const fileInput = useRef(null)
  const [mode, setMode] = useState('merge')
  const [importing, setImporting] = useState(false)
  const [cells, setCells] = useState([])
  const [selectedCellId, setSelectedCellId] = useState('')
  const [selectedRegistryId, setSelectedRegistryId] = useState('')
  const [adding, setAdding] = useState(false)

  const [regType, setRegType] = useState('')
  const [regCode, setRegCode] = useState('')
  const [regDesc, setRegDesc] = useState('')
  const [savingCode, setSavingCode] = useState(false)

  const [testCodeType, setTestCodeType] = useState('')
  const [testCode, setTestCode] = useState('')
  const [testResult, setTestResult] = useState(null)
  const [testErr, setTestErr] = useState(null)

  const { data: geoData, error, errorStatus, loading, refresh } = usePoller(() => listGeocodes(), 15000)
  const { data: registryData, error: registryError, loading: registryLoading, refresh: refreshRegistry } = usePoller(() => listGeocodeRegistry(), 15000)

  useEffect(() => {
    getCells({ limit: 500 }).then(r => setCells(r.cells || [])).catch(() => {})
  }, [])

  const registryEntries = registryData?.codes || []

  async function handleAddCode(e) {
    e.preventDefault()
    setSavingCode(true)
    try {
      await createGeocodeRegistryEntry({ type: regType.toUpperCase(), code: regCode, description: regDesc })
      toast.success('Geo code added', `${regType.toUpperCase()} ${regCode}`)
      setRegCode('')
      setRegDesc('')
      refreshRegistry()
    } catch (err) {
      toast.error('Add failed', err.message)
    } finally {
      setSavingCode(false)
    }
  }

  async function handleDeleteCode(id) {
    try {
      await deleteGeocodeRegistryEntry(id)
      refreshRegistry()
    } catch (err) {
      toast.error('Delete failed', err.message)
    }
  }

  async function handleAdd(e) {
    e.preventDefault()
    const cell = cells.find(c => String(c.id) === String(selectedCellId))
    const reg = registryEntries.find(r => String(r.id) === String(selectedRegistryId))
    if (!cell || !reg) return
    setAdding(true)
    try {
      await createGeocode({ mcc: cell.plmn.mcc, mnc: cell.plmn.mnc, mncLength: cell.plmn.mncLength, eci: cell.eci, codeType: reg.type, code: reg.code })
      toast.success('Cell mapping added', `${reg.type} ${reg.code} -> ECI ${cell.eci}`)
      refresh()
    } catch (err) {
      toast.error('Add failed', err.message)
    } finally {
      setAdding(false)
    }
  }

  async function handleDelete(id) {
    try {
      await deleteGeocode(id)
      refresh()
    } catch (err) {
      toast.error('Delete failed', err.message)
    }
  }

  async function handleImport(e) {
    const file = e.target.files?.[0]
    if (!file) return
    setImporting(true)
    try {
      const result = await importGeocodes(file, mode)
      toast.success('Import complete', `${result.inserted || 0} inserted, ${result.rowsRejected || 0} rejected`)
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
      const { blob, filename } = await exportGeocodes('csv')
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

  async function handleTest(e) {
    e.preventDefault()
    setTestErr(null)
    setTestResult(null)
    try {
      const result = await resolveGeocode(testCodeType.toUpperCase(), testCode)
      setTestResult(result)
    } catch (err) {
      setTestErr(err.message || String(err))
    }
  }

  const entries = geoData?.entries || []

  if (errorStatus === 404) {
    return (
      <div>
        <div className="page-header">
          <div>
            <div className="page-title">Geo Codes</div>
          </div>
        </div>
        <div className="empty-state">
          Geo codes are not enabled on this CBC.
          Set <code>cell_inventory.enabled: true</code> in the server config to use this page.
        </div>
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="page-title">Geo Codes</div>
          <div className="page-subtitle">{registryData ? `${registryEntries.length} codes` : ''}</div>
        </div>
      </div>

      <div className="section-title">Add a geo code</div>
      <form onSubmit={handleAddCode}>
        <div className="form-row-3">
          <div className="form-group">
            <label className="form-label">Type</label>
            <input className="input mono" value={regType} onChange={e => setRegType(e.target.value.toUpperCase())}
              placeholder="SAME" style={{ textTransform: 'uppercase' }} required />
          </div>
          <div className="form-group">
            <label className="form-label">Code</label>
            <input className="input mono" value={regCode} onChange={e => setRegCode(e.target.value)}
              placeholder="001101" required />
          </div>
          <div className="form-group">
            <label className="form-label">Description</label>
            <input className="input" value={regDesc} onChange={e => setRegDesc(e.target.value)}
              placeholder="Jefferson County, AL" />
          </div>
        </div>
        <button className="btn btn-primary mt-12" type="submit" disabled={savingCode || !regType.trim() || !regCode.trim()}>Add</button>
      </form>

      {registryLoading && !registryData && <Spinner />}
      {registryError && (
        <div className="error-state">
          <XCircle size={32} className="error-icon" />
          <div>{registryError}</div>
        </div>
      )}

      {registryData && (
        registryEntries.length === 0 ? (
          <div className="empty-state">No geo codes defined yet</div>
        ) : (
          <div className="table-container">
            <table>
              <thead>
                <tr>
                  <th>Type</th><th>Code</th><th>Description</th><th>Created</th><th></th>
                </tr>
              </thead>
              <tbody>
                {registryEntries.map(c => (
                  <tr key={c.id}>
                    <td className="mono">{c.type}</td>
                    <td className="mono">{c.code}</td>
                    <td>{c.description || '—'}</td>
                    <td>{new Date(c.createdAt).toLocaleString()}</td>
                    <td>
                      <button className="btn-icon danger" onClick={() => handleDeleteCode(c.id)} title="Delete">
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

      <div className="page-header mt-20">
        <div>
          <div className="page-title">Cell Mappings</div>
          <div className="page-subtitle">{geoData ? `${geoData.total} mappings` : ''}</div>
        </div>
        <div className="flex gap-8 items-center">
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

      <div className="section-title">Map a geo code to a cell</div>
      <form onSubmit={handleAdd}>
        <div className="form-row-3">
          <div className="form-group">
            <label className="form-label">Cell</label>
            <select className="select" value={selectedCellId} onChange={e => setSelectedCellId(e.target.value)} required>
              <option value="" disabled>Select a cell…</option>
              {cells.map(c => (
                <option key={c.id} value={c.id}>
                  ECI {c.eci} {c.cellName ? `— ${c.cellName}` : ''} ({c.plmn.mcc}-{c.plmn.mnc})
                </option>
              ))}
            </select>
          </div>
          <div className="form-group" style={{ gridColumn: 'span 2' }}>
            <label className="form-label">Geo Code</label>
            <select className="select" value={selectedRegistryId} onChange={e => setSelectedRegistryId(e.target.value)} required>
              <option value="" disabled>Select a geo code…</option>
              {registryEntries.map(r => (
                <option key={r.id} value={r.id}>
                  {r.type} — {r.code}{r.description ? ` — ${r.description}` : ''}
                </option>
              ))}
            </select>
          </div>
        </div>
        <button className="btn btn-primary mt-12" type="submit" disabled={adding || !selectedCellId || !selectedRegistryId}>Add</button>
      </form>

      {loading && !geoData && <Spinner />}
      {error && (
        <div className="error-state">
          <XCircle size={32} className="error-icon" />
          <div>{error}</div>
        </div>
      )}

      {geoData && (
        entries.length === 0 ? (
          <div className="empty-state">No geo codes mapped yet</div>
        ) : (
          <div className="table-container">
            <table>
              <thead>
                <tr>
                  <th>ECI</th><th>Cell Name</th><th>Code Type</th><th>Code</th><th>Created</th><th></th>
                </tr>
              </thead>
              <tbody>
                {entries.map(e => (
                  <tr key={e.id}>
                    <td className="mono">{e.eci}</td>
                    <td>{e.cellName || '—'}</td>
                    <td className="mono">{e.codeType}</td>
                    <td className="mono">{e.code}</td>
                    <td>{new Date(e.createdAt).toLocaleString()}</td>
                    <td>
                      <button className="btn-icon danger" onClick={() => handleDelete(e.id)} title="Delete">
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

      <div className="section-title mt-20">Test a code</div>
      <form onSubmit={handleTest}>
        <div className="form-row-3">
          <div className="form-group">
            <label className="form-label">Code Type</label>
            <input className="input mono" value={testCodeType} onChange={e => setTestCodeType(e.target.value.toUpperCase())}
              placeholder="SAME" style={{ textTransform: 'uppercase' }} required />
          </div>
          <div className="form-group">
            <label className="form-label">Code</label>
            <input className="input mono" value={testCode} onChange={e => setTestCode(e.target.value)} placeholder="001101" required />
          </div>
        </div>
        <button className="btn btn-primary mt-12" type="submit">Resolve</button>
      </form>

      {testErr && (
        <div className="error-state">
          <XCircle size={32} className="error-icon" />
          <div>{testErr}</div>
        </div>
      )}
      {testResult && (
        <div className="mt-16">
          {(testResult.cells || []).length === 0 ? (
            <div className="empty-state">This code does not match any cell in your inventory.</div>
          ) : (
            <p>Resolves to ECIs: <span className="mono">{testResult.cells.join(', ')}</span></p>
          )}
        </div>
      )}
    </div>
  )
}
