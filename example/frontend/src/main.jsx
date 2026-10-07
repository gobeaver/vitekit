import { StrictMode, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './simple.css'

function App() {
  const [overview, setOverview] = useState(null)
  const [activity, setActivity] = useState([])
  const [loading, setLoading] = useState(true)

  async function loadData() {
    setLoading(true)
    const [overviewResponse, activityResponse] = await Promise.all([
      fetch('/api/overview'),
      fetch('/api/activity'),
    ])
    setOverview(await overviewResponse.json())
    setActivity(await activityResponse.json())
    setLoading(false)
  }

  async function toggleMaintenance() {
    const response = await fetch('/api/maintenance', { method: 'POST' })
    const data = await response.json()
    setOverview((current) => ({ ...current, maintenance: data.maintenance }))
  }

  useEffect(() => {
    loadData().catch(() => setLoading(false))
  }, [])

  return (
    <main className="app-shell">
      <header className="app-header">
        <div>
          <p className="kicker">VITEKIT EXAMPLE</p>
          <h1>Launch Console</h1>
          <p className="subtitle">A small React frontend backed by Go.</p>
        </div>
        <button className="refresh" onClick={loadData}>Refresh</button>
      </header>

      <section className="summary" aria-label="Backend overview">
        <div><span>Mode</span><strong>{overview?.mode ?? 'loading'}</strong></div>
        <div><span>Requests</span><strong>{overview?.requests?.toLocaleString() ?? '-'}</strong></div>
        <div><span>Success</span><strong>{overview ? `${overview.successRate}%` : '-'}</strong></div>
        <div><span>Release</span><strong>{overview?.deploy ?? '-'}</strong></div>
      </section>

      <section className="panel">
        <div className="panel-heading">
          <div><p className="kicker">RECENT ACTIVITY</p><h2>Service events</h2></div>
          <button className={overview?.maintenance ? 'maintenance active' : 'maintenance'} onClick={toggleMaintenance}>
            {overview?.maintenance ? 'Disable maintenance' : 'Enable maintenance'}
          </button>
        </div>
        {loading ? <p className="muted">Loading activity...</p> : (
          <ul className="activity-list">
            {activity.map((item) => <li key={item.id}>
              <span className={item.status === 'warning' ? 'dot warning' : 'dot'} />
              <span><strong>{item.action}</strong><small>{item.service}</small></span>
              <time>{item.duration}</time>
            </li>)}
          </ul>
        )}
      </section>
    </main>
  )
}

createRoot(document.getElementById('app')).render(<StrictMode><App /></StrictMode>)