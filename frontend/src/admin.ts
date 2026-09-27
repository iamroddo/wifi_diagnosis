import './style.css'
import type { DiagSession } from './types'

function el<T extends HTMLElement = HTMLElement>(id: string): T {
  return document.getElementById(id) as T
}

async function loadSessions() {
  el('sessions-container').innerHTML = '<p style="color:var(--text-muted)">Loading…</p>'
  try {
    const resp = await fetch('/api/admin/sessions')
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
    const sessions = await resp.json() as DiagSession[]

    if (sessions.length === 0) {
      el('sessions-container').innerHTML = '<p style="color:var(--text-muted)">No sessions recorded yet.</p>'
      return
    }

    el('sessions-container').innerHTML = sessions.map(renderSession).join('')
  } catch (err) {
    el('sessions-container').innerHTML = `<p style="color:var(--critical)">Error loading sessions: ${(err as Error).message}</p>`
  }
}

function renderSession(s: DiagSession): string {
  const ts = s.created_at ? new Date(s.created_at).toLocaleString() : '—'
  const corr = s.correlation_status ?? '—'

  const clientSection = `
    <div class="session-section">
      <h4>Client</h4>
      <dl>
        <dt>IP</dt><dd>${s.client_ip ?? '—'}</dd>
        <dt>MAC</dt><dd>${s.client_mac ?? '—'}</dd>
        <dt>Name</dt><dd>${s.client_name || s.client_hostname || '—'}</dd>
        <dt>Correlation</dt><dd>${corr}</dd>
      </dl>
    </div>`

  const wifiSection = s.ssid ? `
    <div class="session-section">
      <h4>Wi-Fi</h4>
      <dl>
        <dt>SSID</dt><dd>${s.ssid}</dd>
        <dt>Band</dt><dd>${s.band ?? '—'}</dd>
        <dt>Channel</dt><dd>${s.channel ?? '—'}</dd>
        <dt>UniFi RSSI</dt><dd>${s.rssi !== undefined ? s.rssi + ' dBm' : '—'}</dd>
        <dt>Retries</dt><dd>${s.retries !== undefined ? s.retries + '%' : '—'}</dd>
      </dl>
    </div>` : ''

  const perfSection = (s.lan_latency || s.download || s.upload) ? `
    <div class="session-section">
      <h4>Performance</h4>
      <dl>
        ${s.lan_latency?.avg_ms !== undefined ? `<dt>LAN Avg</dt><dd>${s.lan_latency.avg_ms} ms</dd>` : ''}
        ${s.lan_latency?.loss_pct !== undefined ? `<dt>Loss</dt><dd>${s.lan_latency.loss_pct}%</dd>` : ''}
        ${s.download?.mbps_avg !== undefined ? `<dt>Download</dt><dd>${s.download.mbps_avg} Mbps</dd>` : ''}
        ${s.upload?.mbps_avg !== undefined ? `<dt>Upload</dt><dd>${s.upload.mbps_avg} Mbps</dd>` : ''}
      </dl>
    </div>` : ''

  return `
    <div class="session-card">
      <h3>${s.id}</h3>
      <div class="session-meta">${ts} · ${s.client_ip}</div>
      <div class="session-grid">
        ${clientSection}
        ${wifiSection}
        ${perfSection}
      </div>
    </div>`
}

el('btn-refresh').addEventListener('click', loadSessions)
loadSessions()

fetch('/api/config')
  .then(r => r.json())
  .then((cfg: { title?: string; contact_name?: string; contact_email?: string }) => {
    if (cfg.title) {
      document.title = `${cfg.title} — Admin`
      const h1 = document.querySelector('h1')
      if (h1) h1.textContent = `${cfg.title} — Admin`
    }
    const contactEl = document.getElementById('contact-info')
    if (contactEl && (cfg.contact_name || cfg.contact_email)) {
      const name = cfg.contact_name || ''
      const email = cfg.contact_email
      contactEl.innerHTML = 'Need help? Contact ' +
        (email ? `<a href="mailto:${email}">${name || email}</a>` : name)
      contactEl.classList.remove('hidden')
    }
  })
  .catch(() => { /* use default title on failure */ })
