const BASE = '/v1'

async function request(method, path, body) {
  const opts = { method, headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(`${BASE}${path}`, opts)
  if (res.status === 204) return null
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const data = await res.json()
      msg = data.detail || data.message || data.error || msg
      if (data.errors && data.errors.length > 0) {
        msg += ': ' + data.errors.map(e => `${e.path || e.location || '?'} — ${e.message}${e.value !== undefined ? ` (got ${JSON.stringify(e.value)})` : ''}`).join('; ')
      }
    } catch {}
    const err = new Error(msg)
    err.status = res.status
    throw err
  }
  return res.json()
}

// Operational (top-level, not under /v1)
export async function getMetrics() {
  const res = await fetch('/metrics')
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}
export async function getHealth() {
  const res = await fetch('/healthz')
  return res.ok
}
export async function getReady() {
  const res = await fetch('/readyz')
  return res.ok
}
export const getVersion = () => request('GET', '/version')

// Alerts
export const getAlerts = () => request('GET', '/alerts')
export const getAlert = (id) => request('GET', `/alerts/${encodeURIComponent(id)}`)
export const getCBSPlan = (id) => request('GET', `/alerts/${encodeURIComponent(id)}/cbs`)
export const getAudit = () => request('GET', '/audit')

// Cell Inventory
export const getCells = ({ limit = 0, offset = 0 } = {}) => {
  const p = new URLSearchParams()
  if (limit > 0) { p.set('limit', String(limit)); p.set('offset', String(offset)) }
  const qs = p.toString()
  return request('GET', `/cell-inventory/cells${qs ? `?${qs}` : ''}`)
}
export const getCell = (cellID) => request('GET', `/cell-inventory/cells/${encodeURIComponent(cellID)}`)
export const createCell = (body) => request('POST', '/cell-inventory/cells', body)
export const deleteCell = (cellID) => request('DELETE', `/cell-inventory/cells/${encodeURIComponent(cellID)}`)
export const getImport = (importID) => request('GET', `/cell-inventory/imports/${encodeURIComponent(importID)}`)
export const getImportErrors = (importID) => request('GET', `/cell-inventory/imports/${encodeURIComponent(importID)}/errors`)
export const previewSelection = (body) => request('POST', '/cell-inventory/selection-preview', body)

export async function importCellInventory(file, mode) {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch(`${BASE}/cell-inventory/imports?mode=${encodeURIComponent(mode)}`, { method: 'POST', body: form })
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    const err = new Error(data?.detail || data?.message || `HTTP ${res.status}`)
    err.status = res.status
    throw err
  }
  return data
}

export async function exportCellInventory(format = 'csv') {
  const res = await fetch(`${BASE}/cell-inventory/export?format=${encodeURIComponent(format)}`)
  if (!res.ok) {
    const err = new Error(`HTTP ${res.status}`)
    err.status = res.status
    throw err
  }
  const blob = await res.blob()
  const disposition = res.headers.get('Content-Disposition') || ''
  const match = disposition.match(/filename="?([^"]+)"?/)
  return { blob, filename: match ? match[1] : `cell-inventory.${format}` }
}

// Geo Codes registry - operator-curated (type, code, description) entries,
// independent of any cell. Populates the Cell Mappings dropdown below.
export const listGeocodeRegistry = () => request('GET', '/geocode-registry')
export const createGeocodeRegistryEntry = (body) => request('POST', '/geocode-registry', body)
export const deleteGeocodeRegistryEntry = (id) => request('DELETE', `/geocode-registry/${encodeURIComponent(id)}`)

// Cell Mappings (geo code -> cell, any registered type)
export const listGeocodes = ({ codeType = '', code = '', cellId = '', limit = 200, offset = 0 } = {}) => {
  const p = new URLSearchParams()
  if (codeType) p.set('codeType', codeType)
  if (code) p.set('code', code)
  if (cellId) p.set('cellId', String(cellId))
  p.set('limit', String(limit))
  p.set('offset', String(offset))
  return request('GET', `/geocodes?${p.toString()}`)
}
export const createGeocode = (body) => request('POST', '/geocodes', body)
export const deleteGeocode = (id) => request('DELETE', `/geocodes/${encodeURIComponent(id)}`)
export const resolveGeocode = (codeType, code) => request('POST', '/geocodes/resolve', { codeType, code })

export async function importGeocodes(file, mode) {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch(`${BASE}/geocodes/import?mode=${encodeURIComponent(mode)}`, { method: 'POST', body: form })
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    const err = new Error(data?.detail || data?.message || `HTTP ${res.status}`)
    err.status = res.status
    throw err
  }
  return data
}

export async function exportGeocodes(format = 'csv') {
  const res = await fetch(`${BASE}/geocodes/export?format=${encodeURIComponent(format)}`)
  if (!res.ok) {
    const err = new Error(`HTTP ${res.status}`)
    err.status = res.status
    throw err
  }
  const blob = await res.blob()
  const disposition = res.headers.get('Content-Disposition') || ''
  const match = disposition.match(/filename="?([^"]+)"?/)
  return { blob, filename: match ? match[1] : `cell-geocodes.${format}` }
}
