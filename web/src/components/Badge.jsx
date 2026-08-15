import React from 'react'

const STATE_MAP = {
  enabled: { cls: 'badge-open', label: 'enabled' },
  disabled: { cls: 'badge-disabled', label: 'disabled' },
  connected: { cls: 'badge-open', label: 'connected' },
  disconnected: { cls: 'badge-closed', label: 'disconnected' },
  // CAP alert lifecycle state (internal/service.Record.State)
  active: { cls: 'badge-open', label: 'active' },
  cancelled: { cls: 'badge-closed', label: 'cancelled' },
  superseded: { cls: 'badge-disabled', label: 'superseded' },
  expired: { cls: 'badge-disabled', label: 'expired' },
  // CAP severity (internal/cap.Info.Severity)
  Extreme: { cls: 'badge-closed', label: 'Extreme' },
  Severe: { cls: 'badge-closed', label: 'Severe' },
  Moderate: { cls: 'badge-info', label: 'Moderate' },
  Minor: { cls: 'badge-disabled', label: 'Minor' },
}

export default function Badge({ state, label: labelOverride }) {
  if (!state) return null
  const entry = STATE_MAP[state] || { cls: 'badge-disabled', label: state }
  return (
    <span className={`badge ${entry.cls}`}>
      {labelOverride || entry.label}
    </span>
  )
}
