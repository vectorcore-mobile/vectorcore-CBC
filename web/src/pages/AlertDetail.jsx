import React, { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { XCircle } from 'lucide-react'
import { getAlert, getCBSPlan, getAudit } from '../api/client.js'
import Spinner from '../components/Spinner.jsx'
import Badge from '../components/Badge.jsx'

export default function AlertDetail() {
  const { id } = useParams()
  const [record, setRecord] = useState(null)
  const [plan, setPlan] = useState(null)
  const [audit, setAudit] = useState([])
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let mounted = true
    setLoading(true)
    Promise.all([
      getAlert(id),
      getCBSPlan(id).catch(() => null),
      getAudit().catch(() => []),
    ]).then(([r, p, a]) => {
      if (!mounted) return
      setRecord(r)
      setPlan(p)
      setAudit((a || []).filter(e => e.alert_id === id))
      setError(null)
    }).catch(err => {
      if (mounted) setError(err.message || String(err))
    }).finally(() => {
      if (mounted) setLoading(false)
    })
    return () => { mounted = false }
  }, [id])

  if (loading) return <Spinner />
  if (error) {
    return (
      <div className="error-state">
        <XCircle size={32} className="error-icon" />
        <div>{error}</div>
      </div>
    )
  }
  if (!record) return null

  const info = record.alert.Info && record.alert.Info.length > 0 ? record.alert.Info[0] : {}

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="page-title mono">{record.alert.Identifier}</div>
          <div className="page-subtitle">{info.headline || info.event || 'Alert detail'}</div>
        </div>
        <Link to="/alerts" className="btn btn-ghost">&larr; Back to alerts</Link>
      </div>

      <div className="detail-grid">
        <div className="detail-row"><span className="detail-label">State</span><span className="detail-value"><Badge state={record.state} /></span></div>
        <div className="detail-row"><span className="detail-label">Severity</span><span className="detail-value"><Badge state={info.severity} /></span></div>
        <div className="detail-row"><span className="detail-label">Event</span><span className="detail-value">{info.event || '—'}</span></div>
        <div className="detail-row"><span className="detail-label">Urgency</span><span className="detail-value">{info.urgency || '—'}</span></div>
        <div className="detail-row"><span className="detail-label">Certainty</span><span className="detail-value">{info.certainty || '—'}</span></div>
        <div className="detail-row"><span className="detail-label">Sender</span><span className="detail-value">{record.alert.Sender}</span></div>
        <div className="detail-row"><span className="detail-label">Sent</span><span className="detail-value mono">{record.alert.Sent}</span></div>
        <div className="detail-row"><span className="detail-label">Expires</span><span className="detail-value mono">{info.expires || '—'}</span></div>
        <div className="detail-row"><span className="detail-label">Received</span><span className="detail-value mono">{record.received_at}</span></div>
      </div>

      {info.description && <p className="mt-16">{info.description}</p>}
      {info.instruction && <p><em>{info.instruction}</em></p>}

      {info.areas && info.areas.length > 0 && (
        <>
          <div className="section-title mt-20">Target Area</div>
          {info.areas.map((a, i) => (
            <p key={i}>{a.description}</p>
          ))}
        </>
      )}

      <div className="section-title mt-20">Prepared CBS Plan</div>
      {plan ? (
        <pre className="code-block">{JSON.stringify(plan, null, 2)}</pre>
      ) : (
        <div className="empty-state">No CBS plan available.</div>
      )}

      <div className="section-title mt-20">Audit Trail</div>
      {audit.length === 0 ? (
        <div className="empty-state">No audit events</div>
      ) : (
        <div className="table-container">
          <table>
            <thead><tr><th>At</th><th>Type</th><th>Detail</th></tr></thead>
            <tbody>
              {audit.map(e => (
                <tr key={e.id}>
                  <td className="mono">{e.at}</td>
                  <td>{e.type}</td>
                  <td>{e.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
