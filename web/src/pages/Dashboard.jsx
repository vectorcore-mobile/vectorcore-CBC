import React from 'react'
import { Siren, CheckCircle, Radio, XCircle, RotateCcw, AlertTriangle, Send } from 'lucide-react'
import { usePoller } from '../hooks/usePoller.js'
import { getMetrics } from '../api/client.js'
import StatCard from '../components/StatCard.jsx'
import Spinner from '../components/Spinner.jsx'
import Badge from '../components/Badge.jsx'

export default function Dashboard() {
  const { data: metrics, error, loading } = usePoller(getMetrics, 5000)

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="page-title">Dashboard</div>
          <div className="page-subtitle">CBC operational status</div>
        </div>
        <div className="connection-indicator">
          <span>CBE session:</span>
          <Badge state={metrics?.Connected ? 'connected' : 'disconnected'} />
        </div>
      </div>

      {loading && !metrics && <Spinner />}
      {error && (
        <div className="error-state">
          <XCircle size={32} className="error-icon" />
          <div>{error}</div>
        </div>
      )}

      {metrics && (
        <>
          <div className="stats-grid">
            <StatCard title="Total Alerts" value={metrics.Alerts} icon={<Siren size={18} />} />
            <StatCard title="Active" value={metrics.Active} icon={<CheckCircle size={18} />} color="var(--success)" />
            <StatCard title="Ingested" value={metrics.Ingested} icon={<Send size={18} />} />
            <StatCard title="Rejected" value={metrics.Rejected} icon={<XCircle size={18} />} color="var(--danger)" />
          </div>

          <div className="section-title mt-20">eNB Restart Handling</div>
          <div className="stats-grid">
            <StatCard title="Restart Indications" value={metrics.RestartIndications} icon={<RotateCcw size={18} />} />
            <StatCard title="Failure Indications" value={metrics.FailureIndications} icon={<AlertTriangle size={18} />} color="var(--warning)" />
            <StatCard title="Restart Rebroadcasts" value={metrics.RestartRebroadcasts} icon={<Radio size={18} />} />
          </div>
        </>
      )}
    </div>
  )
}
