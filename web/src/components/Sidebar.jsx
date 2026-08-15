import React from 'react'
import { NavLink } from 'react-router-dom'
import { LayoutDashboard, Siren, Radio, MapPin, Settings } from 'lucide-react'

const NAV_ITEMS = [
  { to: '/dashboard', label: 'Dashboard', icon: <LayoutDashboard size={16} /> },
  { to: '/alerts', label: 'Alerts', icon: <Siren size={16} /> },
  { to: '/cell-inventory', label: 'Cell Inventory', icon: <Radio size={16} /> },
  { to: '/geo-codes', label: 'Geo Codes', icon: <MapPin size={16} /> },
  { to: '/settings', label: 'Settings', icon: <Settings size={16} /> },
]

export default function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="sidebar-header" style={{ textAlign: 'center' }}>
        <div className="sidebar-logo">VectorCore</div>
        <div className="sidebar-logo-sub">Cell Broadcast Centre</div>
      </div>

      <nav className="sidebar-nav" aria-label="Primary navigation">
        {NAV_ITEMS.map(({ to, label, icon }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
          >
            {icon}
            {label}
          </NavLink>
        ))}
      </nav>
    </aside>
  )
}
