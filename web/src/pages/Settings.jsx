import React from 'react'
import { usePoller } from '../hooks/usePoller.js'
import { getHealth, getReady, getVersion } from '../api/client.js'
import Badge from '../components/Badge.jsx'

export default function Settings() {
  const { data: healthy } = usePoller(getHealth, 10000)
  const { data: ready } = usePoller(getReady, 10000)
  const { data: version } = usePoller(getVersion, 30000)

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="page-title">Settings</div>
          <div className="page-subtitle">Build info and links</div>
        </div>
      </div>

      <div className="detail-grid">
        <div className="detail-row"><span className="detail-label">Version</span><span className="detail-value mono">{version?.version || '—'}</span></div>
        <div className="detail-row"><span className="detail-label">Liveness</span><span className="detail-value"><Badge state={healthy ? 'connected' : 'disconnected'} label={healthy ? 'live' : 'unreachable'} /></span></div>
        <div className="detail-row"><span className="detail-label">Readiness</span><span className="detail-value"><Badge state={ready ? 'connected' : 'disconnected'} label={ready ? 'ready' : 'not ready'} /></span></div>
      </div>

      <div className="section-title mt-20">Links</div>
      <ul>
        <li><a href="/metrics" target="_blank" rel="noreferrer">/metrics</a> — raw operational metrics (JSON)</li>
        <li><a href="/openapi.json" target="_blank" rel="noreferrer">/openapi.json</a> — API schema</li>
      </ul>
    </div>
  )
}
