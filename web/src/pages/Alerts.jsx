import React from 'react'
import { useNavigate } from 'react-router-dom'
import { XCircle } from 'lucide-react'
import { usePoller } from '../hooks/usePoller.js'
import { useSort } from '../hooks/useSort.js'
import { getAlerts } from '../api/client.js'
import Spinner from '../components/Spinner.jsx'
import Badge from '../components/Badge.jsx'

function firstInfo(record) {
  return record.alert.Info && record.alert.Info.length > 0 ? record.alert.Info[0] : {}
}

export default function Alerts() {
  const navigate = useNavigate()
  const { data: records, error, loading } = usePoller(getAlerts, 5000)
  const rows = (records || []).map(r => ({
    identifier: r.alert.Identifier,
    event: firstInfo(r).event || '—',
    severity: firstInfo(r).severity || '—',
    state: r.state,
    sent: r.alert.Sent,
    expires: firstInfo(r).expires || '—',
  }))
  const { sorted, sortKey, sortDir, handleSort } = useSort(rows, 'sent', 'desc')

  function th(key, label) {
    return (
      <th className="sortable" onClick={() => handleSort(key)}>
        {label}
        {sortKey === key && <span className="sort-icon">{sortDir === 'asc' ? '▲' : '▼'}</span>}
      </th>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="page-title">Alerts</div>
          <div className="page-subtitle">{rows.length} alert{rows.length !== 1 ? 's' : ''}</div>
        </div>
      </div>

      {loading && !records && <Spinner />}
      {error && (
        <div className="error-state">
          <XCircle size={32} className="error-icon" />
          <div>{error}</div>
        </div>
      )}

      {records && (
        sorted.length === 0 ? (
          <div className="empty-state">No alerts</div>
        ) : (
          <div className="table-container">
            <table>
              <thead>
                <tr>
                  {th('identifier', 'Identifier')}
                  {th('event', 'Event')}
                  {th('severity', 'Severity')}
                  {th('state', 'State')}
                  {th('sent', 'Sent')}
                  {th('expires', 'Expires')}
                </tr>
              </thead>
              <tbody>
                {sorted.map(row => (
                  <tr key={row.identifier} onClick={() => navigate(`/alerts/${encodeURIComponent(row.identifier)}`)} style={{ cursor: 'pointer' }}>
                    <td className="mono">{row.identifier}</td>
                    <td>{row.event}</td>
                    <td><Badge state={row.severity} /></td>
                    <td><Badge state={row.state} /></td>
                    <td className="mono">{row.sent}</td>
                    <td className="mono">{row.expires}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}
    </div>
  )
}
