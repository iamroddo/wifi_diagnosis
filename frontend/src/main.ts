import './style.css'
import { DiagClient } from './client'
import { t, getLang, setLang, type Lang } from './i18n'
import type {
  SessionStartResponse,
  SessionResponse,
  LatencyResult,
  ThroughputResult,
  Severity,
  HistorySample,
} from './types'

declare const __APP_VERSION__: string

// ---- DOM helpers ----

function el<T extends HTMLElement = HTMLElement>(id: string): T {
  return document.getElementById(id) as T
}

function setStatus(msg: string, kind: 'idle' | 'running' | 'done' | 'error') {
  const bar = el('status-bar')
  bar.textContent = msg
  bar.className = `status-bar status-${kind}`
}

function showResults() {
  el('results').classList.remove('hidden')
}

function metricHTML(label: string, value: string, unit = '', cls = ''): string {
  return `<div class="metric ${cls}">
    <div class="metric-label">${label}</div>
    <div class="metric-value">${value} <span class="metric-unit">${unit}</span></div>
  </div>`
}

function findingIcon(s: Severity): string {
  switch (s) {
    case 'ok': return '✓'
    case 'info': return 'ℹ'
    case 'warning': return '⚠'
    case 'critical': return '✕'
  }
}

function formatBytes(b: number): string {
  if (b > 1e9) return `${(b / 1e9).toFixed(2)} GB`
  if (b > 1e6) return `${(b / 1e6).toFixed(1)} MB`
  if (b > 1e3) return `${(b / 1e3).toFixed(0)} KB`
  return `${b} B`
}

function formatRateKbps(bps: number): string {
  if (!bps) return '—'
  const mbps = bps / 1e6
  return mbps >= 1 ? `${mbps.toFixed(0)} Mbps` : `${(bps / 1e3).toFixed(0)} Kbps`
}

// ---- State ----

const client = new DiagClient()
let running = false
let abortController: AbortController | null = null
let contactEmail = ''
let contactName = ''

let lastSessionId = ''
let lastLanLatency: LatencyResult | null = null
let lastDownload: ThroughputResult | null = null
let lastUpload: ThroughputResult | null = null
let lastInternetLatency: LatencyResult | null = null
let lastInternetDownload: ThroughputResult | null = null
let lastInternetUpload: ThroughputResult | null = null
let lastWifi: SessionResponse['wifi'] | null = null

// ---- i18n: update all static UI text ----

function applyTranslations() {
  el('subtitle').textContent = t('subtitle')
  el('about-title').textContent = t('aboutTitle')
  el('about-item1').textContent = t('aboutItem1')
  el('about-item2').textContent = t('aboutItem2')
  el('about-item3').textContent = t('aboutItem3')
  el('about-item4').innerHTML = t('aboutItem4')
  el<HTMLButtonElement>('btn-start').textContent = t('btnStart')
  el<HTMLButtonElement>('btn-stop').textContent = t('btnStop')
  el<HTMLButtonElement>('btn-send-report').textContent = t('btnSendReport')
  el('section-connection-info').textContent = t('sectionConnectionInfo')
  el('section-wifi').textContent = t('sectionWifi')
  el('wifi-source-note').textContent = t('wifiSourceNote')
  el('section-wifi-history').textContent = t('sectionWifiHistory')
  el('wifi-history-source-note').textContent = t('wifiHistorySourceNote')
  el('section-lan').textContent = t('sectionLan')
  el('lan-source-note').textContent = t('lanSourceNote')
  el('section-throughput').textContent = t('sectionThroughput')
  el('throughput-source-note').textContent = t('throughputSourceNote')
  el('section-internet').textContent = t('sectionInternet')
  el('internet-source-note').textContent = t('internetSourceNote')
  el('section-internet-speed').textContent = t('sectionInternetSpeed')
  el('internet-speed-source-note').textContent = t('internetSpeedSourceNote')
  el('section-assessment').textContent = t('sectionAssessment')
  el('footer-session-label').textContent = t('footerSessionId')

  // Update active lang button state
  const lang = getLang()
  el('btn-lang-en').classList.toggle('active', lang === 'en')
  el('btn-lang-de').classList.toggle('active', lang === 'de')

  // Re-render status bar only when idle
  const bar = el('status-bar')
  if (bar.classList.contains('status-idle')) {
    bar.textContent = t('statusReady')
  }
}

// ---- Language switcher ----

function switchLang(lang: Lang) {
  setLang(lang)
  document.documentElement.lang = lang
  applyTranslations()
}

el('btn-lang-en').addEventListener('click', () => switchLang('en'))
el('btn-lang-de').addEventListener('click', () => switchLang('de'))

// Apply saved language on load
switchLang(getLang())

// Show build version in footer
const versionEl = document.getElementById('app-version')
if (versionEl) versionEl.textContent = `v${__APP_VERSION__}`

el('btn-start').addEventListener('click', startDiagnostic)
el('btn-stop').addEventListener('click', stopDiagnostic)
el('btn-send-report').addEventListener('click', sendReport)

// Apply title from server config.
fetch('/api/config')
  .then(r => r.json())
  .then((cfg: { title?: string; contact_name?: string; contact_email?: string }) => {
    if (cfg.title) {
      document.title = cfg.title
      const h1 = document.querySelector('h1')
      if (h1) h1.textContent = cfg.title
    }
    const contactEl = el('contact-info')
    if (cfg.contact_name || cfg.contact_email) {
      contactEmail = cfg.contact_email || ''
      contactName = cfg.contact_name || ''
      renderContactInfo()
      contactEl.classList.remove('hidden')
    }
  })
  .catch(() => { /* use default title on failure */ })

function renderContactInfo() {
  const contactEl = el('contact-info')
  const name = contactName
  const email = contactEmail
  contactEl.innerHTML = t('contactNeedHelp') +
    (email ? `<a href="mailto:${email}">${name || email}</a>` : name)
}

async function startDiagnostic() {
  if (running) return
  running = true
  abortController = new AbortController()

  el<HTMLButtonElement>('btn-start').disabled = true
  el<HTMLButtonElement>('btn-stop').disabled = false
  el('btn-send-report').classList.add('hidden')
  showResults()

  try {
    await runDiagnostic(abortController.signal)
    if (contactEmail) el('btn-send-report').classList.remove('hidden')
  } catch (err) {
    if ((err as Error).message !== 'aborted') {
      setStatus(`${t('statusError')}: ${(err as Error).message}`, 'error')
    }
  } finally {
    running = false
    el<HTMLButtonElement>('btn-start').disabled = false
    el<HTMLButtonElement>('btn-stop').disabled = true
    client.disconnect()
  }
}

function stopDiagnostic() {
  abortController?.abort()
  setStatus(t('statusStopped'), 'idle')
}

async function runDiagnostic(signal: AbortSignal) {
  const onAbort = () => { throw new Error('aborted') }
  signal.addEventListener('abort', onAbort)

  // 1. Start session
  setStatus(t('statusStartingSession'), 'running')
  const sessResp = await fetch('/api/session', { method: 'POST' })
  if (!sessResp.ok) throw new Error('Failed to start session')
  const sess = await sessResp.json() as SessionStartResponse

  el('session-id').textContent = sess.session_id
  lastSessionId = sess.session_id
  renderClientInfo(sess.client_ip, 'pending')

  // 2. Connect WebSocket
  setStatus(t('statusConnecting'), 'running')
  const wsOk = await client.connect()
  if (!wsOk) {
    setStatus(t('statusWsFallback'), 'running')
  }

  checkAbort(signal)

  // 3. Latency test
  setStatus(t('statusMeasuringLanLatency'), 'running')
  const lanLatency = await client.measureLatency(msg => setStatus(msg, 'running'))
  lastLanLatency = lanLatency
  renderLatency(lanLatency)

  checkAbort(signal)

  // 4. Download
  setStatus(t('statusMeasuringDownload'), 'running')
  el('throughput-metrics').innerHTML = `<p style="color:var(--text-muted)">${t('statusRunningDownload')}</p>`
  const download = await client.measureDownload(msg => setStatus(msg, 'running'))
  lastDownload = download
  if (download) renderThroughput(download, null)

  checkAbort(signal)

  // 5. Upload
  setStatus(t('statusMeasuringUpload'), 'running')
  const upload = await client.measureUpload(msg => setStatus(msg, 'running'))
  lastUpload = upload
  if (upload || download) renderThroughput(download, upload)

  checkAbort(signal)

  // 6. Internet latency
  setStatus(t('statusMeasuringInternetLatency'), 'running')
  const internetLatency = await client.measureInternetLatency(
    'https://one.one.one.one',
    msg => setStatus(msg, 'running')
  )
  lastInternetLatency = internetLatency
  renderInternetLatency(internetLatency)

  checkAbort(signal)

  // 7. Internet download
  setStatus(t('statusMeasuringInternetDownload'), 'running')
  el('internet-speed-metrics').innerHTML = `<p style="color:var(--text-muted)">${t('statusRunningInternetDownload')}</p>`
  const internetDownload = await client.measureInternetDownload(msg => setStatus(msg, 'running'))
  lastInternetDownload = internetDownload
  renderInternetSpeed(internetDownload, null)

  checkAbort(signal)

  // 8. Internet upload
  setStatus(t('statusMeasuringInternetUpload'), 'running')
  const internetUpload = await client.measureInternetUpload(msg => setStatus(msg, 'running'))
  lastInternetUpload = internetUpload
  renderInternetSpeed(internetDownload, internetUpload)

  checkAbort(signal)

  // 9. Poll for UniFi correlation
  setStatus(t('statusFetchingWifi'), 'running')
  const sessionData = await pollSession(sess.session_id, 12000)
  if (sessionData) {
    renderClientInfo(sess.client_ip, sessionData.correlation_status)
    lastWifi = sessionData.wifi ?? null
    if (sessionData.wifi) renderWifiInfo(sessionData.wifi)
    else el('wifi-info').innerHTML = `<p style="color:var(--text-muted)">${t('wifiUnavailable')}${sessionData.correlation_status})</p>`

    if (sessionData.client_mac) {
      fetchAndRenderHistory(sessionData.client_mac)
    }
  }

  // 10. Local assessment
  runLocalAssessment(lanLatency, internetLatency, sessionData?.wifi ?? null)

  // 11. Submit results to server
  await submitResults(sess.session_id, lanLatency, internetLatency, download, upload, internetDownload, internetUpload)

  setStatus(t('statusComplete'), 'done')
  signal.removeEventListener('abort', onAbort)
}

async function pollSession(sessionId: string, maxMs: number): Promise<SessionResponse | null> {
  const deadline = Date.now() + maxMs
  while (Date.now() < deadline) {
    try {
      const resp = await fetch(`/api/session/${sessionId}`)
      if (resp.ok) {
        const data = await resp.json() as SessionResponse
        if (data.correlation_status !== 'pending') return data
      }
    } catch { /* retry */ }
    await sleep(1500)
  }
  return null
}

// ---- Render helpers ----

function renderClientInfo(ip: string, corrStatus: string) {
  el('client-info').innerHTML =
    metricHTML(t('metricIp'), ip) +
    metricHTML(t('metricCorrelation'), corrStatus)
}

function renderWifiInfo(wifi: SessionResponse['wifi']) {
  if (!wifi) return
  const rssiClass = wifi.rssi_dbm > -65 ? 'ok' : wifi.rssi_dbm > -75 ? 'warning' : 'critical'
  el('wifi-info').innerHTML =
    metricHTML(t('metricSsid'), wifi.ssid || '—') +
    metricHTML(t('metricAp'), wifi.ap_name || wifi.ap_mac || '—') +
    metricHTML(t('metricBand'), wifi.band || '—') +
    metricHTML(t('metricChannel'), wifi.channel ? String(wifi.channel) : '—', wifi.channel_width ? `(${wifi.channel_width} MHz)` : '') +
    metricHTML(t('metricRssi'), wifi.rssi_dbm ? String(wifi.rssi_dbm) : '—', 'dBm', rssiClass) +
    metricHTML(t('metricTxRate'), formatRateKbps(wifi.tx_rate_bps)) +
    metricHTML(t('metricRxRate'), formatRateKbps(wifi.rx_rate_bps)) +
    metricHTML(t('metricRetries'), wifi.retries !== undefined ? `${wifi.retries}%` : '—') +
    metricHTML(t('metricSatisfaction'), wifi.satisfaction !== undefined ? `${wifi.satisfaction}%` : '—') +
    `<p class="rssi-explain source-note">${t('rssiExplain')}</p>`
}

function renderLatency(r: LatencyResult) {
  const avgClass = r.avg_ms < 20 ? 'ok' : r.avg_ms < 50 ? 'warning' : 'critical'
  const jitterClass = r.jitter_ms < 10 ? 'ok' : r.jitter_ms < 20 ? 'warning' : 'critical'
  const lossClass = r.loss_pct === 0 ? 'ok' : r.loss_pct < 2 ? 'warning' : 'critical'

  el('lan-metrics').innerHTML =
    metricHTML(t('metricMin'), r.min_ms.toFixed(1), 'ms') +
    metricHTML(t('metricAvg'), r.avg_ms.toFixed(1), 'ms', avgClass) +
    metricHTML(t('metricMedian'), r.median_ms.toFixed(1), 'ms') +
    metricHTML(t('metricP95'), r.p95_ms.toFixed(1), 'ms') +
    metricHTML(t('metricMax'), r.max_ms.toFixed(1), 'ms') +
    metricHTML(t('metricJitter'), r.jitter_ms.toFixed(1), 'ms', jitterClass) +
    metricHTML(t('metricLoss'), r.loss_pct.toFixed(1), '%', lossClass)
}

function renderInternetLatency(r: LatencyResult) {
  const avgClass = r.avg_ms < 50 ? 'ok' : r.avg_ms < 150 ? 'warning' : 'critical'
  const jitterClass = r.jitter_ms < 15 ? 'ok' : r.jitter_ms < 30 ? 'warning' : 'critical'
  const lossClass = r.loss_pct === 0 ? 'ok' : r.loss_pct < 2 ? 'warning' : 'critical'
  el('internet-metrics').innerHTML =
    metricHTML(t('metricMin'), r.min_ms.toFixed(1), 'ms') +
    metricHTML(t('metricAvg'), r.avg_ms.toFixed(1), 'ms', avgClass) +
    metricHTML(t('metricMedian'), r.median_ms.toFixed(1), 'ms') +
    metricHTML(t('metricP95'), r.p95_ms.toFixed(1), 'ms') +
    metricHTML(t('metricMax'), r.max_ms.toFixed(1), 'ms') +
    metricHTML(t('metricJitter'), r.jitter_ms.toFixed(1), 'ms', jitterClass) +
    metricHTML(t('metricLoss'), r.loss_pct.toFixed(1), '%', lossClass)
}

function renderThroughput(dl: ThroughputResult | null, ul: ThroughputResult | null) {
  let html = ''
  if (dl) {
    const cls = dl.mbps_avg > 50 ? 'ok' : dl.mbps_avg > 10 ? 'warning' : 'critical'
    html += metricHTML(t('metricDownload'), dl.mbps_avg.toFixed(1), 'Mbps', cls)
    html += metricHTML(t('metricPeakDl'), dl.mbps_peak.toFixed(1), 'Mbps')
    html += metricHTML(t('metricDlData'), formatBytes(dl.bytes_total))
  }
  if (ul) {
    const cls = ul.mbps_avg > 20 ? 'ok' : ul.mbps_avg > 5 ? 'warning' : 'critical'
    html += metricHTML(t('metricUpload'), ul.mbps_avg.toFixed(1), 'Mbps', cls)
    html += metricHTML(t('metricPeakUl'), ul.mbps_peak.toFixed(1), 'Mbps')
  }
  if (!dl && !ul) html = `<p style="color:var(--text-muted)">${t('throughputUnavailable')}</p>`
  el('throughput-metrics').innerHTML = html
}

function renderInternetSpeed(dl: ThroughputResult | null, ul: ThroughputResult | null) {
  let html = ''
  if (dl) {
    const cls = dl.mbps_avg > 25 ? 'ok' : dl.mbps_avg > 5 ? 'warning' : 'critical'
    html += metricHTML(t('metricInternetDownload'), dl.mbps_avg.toFixed(1), 'Mbps', cls)
    html += metricHTML(t('metricPeakDl'), dl.mbps_peak.toFixed(1), 'Mbps')
  }
  if (ul) {
    const cls = ul.mbps_avg > 10 ? 'ok' : ul.mbps_avg > 2 ? 'warning' : 'critical'
    html += metricHTML(t('metricInternetUpload'), ul.mbps_avg.toFixed(1), 'Mbps', cls)
    html += metricHTML(t('metricPeakUl'), ul.mbps_peak.toFixed(1), 'Mbps')
  }
  if (!dl && !ul) html = `<p style="color:var(--text-muted)">${t('internetSpeedUnavailable')}</p>`
  el('internet-speed-metrics').innerHTML = html
}

async function submitResults(
  sessionId: string,
  lan: LatencyResult,
  inet: LatencyResult,
  download: ThroughputResult | null,
  upload: ThroughputResult | null,
  internetDownload: ThroughputResult | null,
  internetUpload: ThroughputResult | null,
) {
  try {
    await fetch(`/api/session/${sessionId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        lan_latency: lan,
        internet_result: inet,
        download: download ?? undefined,
        upload: upload ?? undefined,
        internet_download: internetDownload ?? undefined,
        internet_upload: internetUpload ?? undefined,
      }),
    })
  } catch { /* non-fatal — results are still shown in UI */ }
}

function runLocalAssessment(
  lan: LatencyResult,
  inet: LatencyResult,
  wifi: SessionResponse['wifi'] | null
) {
  const findings: { sev: Severity; desc: string; evidence: string; tip?: string }[] = []

  if (lan.avg_ms > 100) {
    findings.push({ sev: 'critical', desc: t('findingVeryHighLan'), evidence: `avg ${lan.avg_ms.toFixed(1)} ms`, tip: t('tipVeryHighLan') })
  } else if (lan.avg_ms > 30) {
    findings.push({ sev: 'warning', desc: t('findingElevatedLan'), evidence: `avg ${lan.avg_ms.toFixed(1)} ms`, tip: t('tipElevatedLan') })
  } else {
    findings.push({ sev: 'ok', desc: t('findingGoodLan'), evidence: `avg ${lan.avg_ms.toFixed(1)} ms` })
  }

  if (lan.jitter_ms > 20) {
    findings.push({ sev: 'warning', desc: t('findingHighJitter'), evidence: `${lan.jitter_ms.toFixed(1)} ms`, tip: t('tipHighJitter') })
  }

  if (lan.loss_pct > 5) {
    findings.push({ sev: 'critical', desc: t('findingSignificantLoss'), evidence: `${lan.loss_pct.toFixed(1)}%`, tip: t('tipSignificantLoss') })
  } else if (lan.loss_pct > 1) {
    findings.push({ sev: 'warning', desc: t('findingSomeLoss'), evidence: `${lan.loss_pct.toFixed(1)}%`, tip: t('tipSomeLoss') })
  }

  if (wifi && wifi.rssi_dbm) {
    if (wifi.rssi_dbm < -85) {
      findings.push({ sev: 'critical', desc: t('findingVeryWeakWifi'), evidence: `UniFi RSSI: ${wifi.rssi_dbm} dBm`, tip: t('tipVeryWeakWifi') })
    } else if (wifi.rssi_dbm < -75) {
      findings.push({ sev: 'warning', desc: t('findingWeakWifi'), evidence: `UniFi RSSI: ${wifi.rssi_dbm} dBm`, tip: t('tipWeakWifi') })
    } else {
      findings.push({ sev: 'ok', desc: t('findingGoodWifi'), evidence: `UniFi RSSI: ${wifi.rssi_dbm} dBm` })
    }
  }

  // Internet latency findings
  if (inet.avg_ms > 150) {
    findings.push({ sev: 'critical', desc: t('findingVeryHighInet'), evidence: `avg ${inet.avg_ms.toFixed(1)} ms`, tip: t('tipVeryHighInet') })
  } else if (inet.avg_ms > 80) {
    findings.push({ sev: 'warning', desc: t('findingElevatedInet'), evidence: `avg ${inet.avg_ms.toFixed(1)} ms`, tip: t('tipElevatedInet') })
  } else {
    findings.push({ sev: 'ok', desc: t('findingGoodInet'), evidence: `avg ${inet.avg_ms.toFixed(1)} ms` })
  }

  if (inet.jitter_ms > 30) {
    findings.push({ sev: 'warning', desc: t('findingHighInetJitter'), evidence: `${inet.jitter_ms.toFixed(1)} ms`, tip: t('tipHighInetJitter') })
  }

  if (inet.avg_ms > 80 && lan.avg_ms < 20) {
    findings.push({ sev: 'info', desc: t('findingHighInetLowLan'), evidence: `internet avg ${inet.avg_ms.toFixed(1)} ms vs LAN avg ${lan.avg_ms.toFixed(1)} ms` })
  }

  // Download/upload speed findings (internet speed)
  if (lastInternetDownload) {
    const dl = lastInternetDownload.mbps_avg
    if (dl < 5) {
      findings.push({ sev: 'critical', desc: t('findingVerySlowDownload'), evidence: `${dl.toFixed(1)} Mbps`, tip: t('tipVerySlowDownload') })
    } else if (dl < 25) {
      findings.push({ sev: 'warning', desc: t('findingSlowDownload'), evidence: `${dl.toFixed(1)} Mbps`, tip: t('tipSlowDownload') })
    } else {
      findings.push({ sev: 'ok', desc: t('findingGoodDownload'), evidence: `${dl.toFixed(1)} Mbps` })
    }
  }

  if (lastInternetUpload) {
    const ul = lastInternetUpload.mbps_avg
    if (ul < 2) {
      findings.push({ sev: 'critical', desc: t('findingVerySlowUpload'), evidence: `${ul.toFixed(1)} Mbps`, tip: t('tipVerySlowUpload') })
    } else if (ul < 10) {
      findings.push({ sev: 'warning', desc: t('findingSlowUpload'), evidence: `${ul.toFixed(1)} Mbps`, tip: t('tipSlowUpload') })
    } else {
      findings.push({ sev: 'ok', desc: t('findingGoodUpload'), evidence: `${ul.toFixed(1)} Mbps` })
    }
  }

  const html = findings.map(f => `
    <div class="finding finding-${f.sev}">
      <span class="finding-icon">${findingIcon(f.sev)}</span>
      <div class="finding-text">
        ${f.desc}
        ${f.evidence ? `<div class="finding-evidence">${f.evidence}</div>` : ''}
        ${f.tip ? `<div class="finding-tip">${f.tip}</div>` : ''}
      </div>
    </div>
  `).join('')

  el('assessment').innerHTML = html || `<p>${t('noFindings')}</p>`
}

function sendReport() {
  const lines: string[] = [
    t('reportTitle'),
    `${t('reportSession')}${lastSessionId}`,
    `${t('reportDate')}${new Date().toLocaleString()}`,
    '',
  ]

  if (lastWifi) {
    lines.push(t('reportWifi'))
    lines.push(`  SSID: ${lastWifi.ssid || '—'}`)
    lines.push(`  AP: ${lastWifi.ap_name || lastWifi.ap_mac || '—'}`)
    lines.push(`  Band: ${lastWifi.band || '—'} | Channel: ${lastWifi.channel || '—'}`)
    lines.push(`  RSSI: ${lastWifi.rssi_dbm} dBm | Satisfaction: ${lastWifi.satisfaction}%`)
    lines.push('')
  }

  if (lastLanLatency) {
    const l = lastLanLatency
    lines.push(t('reportLan'))
    lines.push(`  Avg: ${l.avg_ms.toFixed(1)} ms | Jitter: ${l.jitter_ms.toFixed(1)} ms | Loss: ${l.loss_pct.toFixed(1)}%`)
    lines.push('')
  }

  if (lastDownload || lastUpload) {
    lines.push(t('reportThroughput'))
    if (lastDownload) lines.push(`  Download: ${lastDownload.mbps_avg.toFixed(1)} Mbps`)
    if (lastUpload) lines.push(`  Upload: ${lastUpload.mbps_avg.toFixed(1)} Mbps`)
    lines.push('')
  }

  if (lastInternetDownload || lastInternetUpload) {
    lines.push(t('reportInternetSpeed'))
    if (lastInternetDownload) lines.push(`  Download: ${lastInternetDownload.mbps_avg.toFixed(1)} Mbps`)
    if (lastInternetUpload) lines.push(`  Upload: ${lastInternetUpload.mbps_avg.toFixed(1)} Mbps`)
    lines.push('')
  }

  if (lastInternetLatency) {
    const i = lastInternetLatency
    lines.push(t('reportInternet'))
    lines.push(`  Avg: ${i.avg_ms.toFixed(1)} ms | Median: ${i.median_ms.toFixed(1)} ms | p95: ${i.p95_ms.toFixed(1)} ms`)
    lines.push(`  Jitter: ${i.jitter_ms.toFixed(1)} ms | Loss: ${i.loss_pct.toFixed(1)}%`)
    lines.push('')
  }

  const subject = encodeURIComponent(t('emailSubject'))
  const body = encodeURIComponent(lines.join('\n'))
  window.location.href = `mailto:${contactEmail}?subject=${subject}&body=${body}`
}

function checkAbort(signal: AbortSignal) {
  if (signal.aborted) throw new Error('aborted')
}

// ---- Wi-Fi history / sparklines ----

async function fetchAndRenderHistory(mac: string) {
  try {
    const resp = await fetch(`/api/unifi/history?mac=${encodeURIComponent(mac)}`)
    if (!resp.ok) return
    const samples = await resp.json() as HistorySample[]
    if (!samples || samples.length === 0) {
      el('wifi-history-group').classList.remove('hidden')
      el('wifi-history').innerHTML = `<p style="color:var(--text-muted)">${t('wifiHistoryUnavailable')}</p>`
      return
    }
    el('wifi-history-group').classList.remove('hidden')
    renderHistory(samples)
  } catch { /* non-fatal */ }
}

function sparkline(
  values: number[],
  opts: { color: string; yMin?: number; yMax?: number; height?: number; width?: number; thresholds?: { value: number; color: string }[] }
): string {
  const w = opts.width ?? 600
  const h = opts.height ?? 60
  const pad = 2
  const raw = values.filter(v => v !== 0 && isFinite(v))
  if (raw.length < 2) return ''

  const lo = opts.yMin ?? Math.min(...raw)
  const hi = opts.yMax ?? Math.max(...raw)
  const range = hi - lo || 1

  const pts = values.map((v, i) => {
    const x = pad + (i / (values.length - 1)) * (w - pad * 2)
    const y = pad + (1 - (v - lo) / range) * (h - pad * 2)
    return `${x.toFixed(1)},${y.toFixed(1)}`
  })

  const thresholdLines = (opts.thresholds ?? [])
    .filter(t => t.value >= lo && t.value <= hi)
    .map(t => {
      const y = (pad + (1 - (t.value - lo) / range) * (h - pad * 2)).toFixed(1)
      return `<line x1="${pad}" y1="${y}" x2="${w - pad}" y2="${y}" stroke="${t.color}" stroke-width="1" stroke-dasharray="4 3" opacity="0.6"/>`
    }).join('')

  return `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none"
    style="width:100%;height:${h}px;display:block;overflow:visible"
    xmlns="http://www.w3.org/2000/svg">
    ${thresholdLines}
    <polyline points="${pts.join(' ')}"
      fill="none" stroke="${opts.color}" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>
  </svg>`
}

type HistoryMetricDef = {
  label: string
  description: string
  values: number[]
  times: Date[]
  fmt: (v: number) => string
  color: (v: number) => string
  yMin?: number
  yMax?: number
  thresholds?: { value: number; color: string }[]
}

function historyChart(def: HistoryMetricDef): string {
  if (def.values.every(v => v === 0)) return ''
  const last = [...def.values].reverse().find((v: number) => v !== 0) ?? def.values[def.values.length - 1]
  const lastTime = def.times[def.values.length - 1]
  const timeStr = lastTime ? lastTime.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : ''
  const cls = def.color(last)
  return `<div class="history-chart">
    <div class="history-chart-header">
      <div class="history-chart-label-group">
        <span class="history-chart-label">${def.label}</span>
        <span class="history-chart-desc">${def.description}</span>
      </div>
      <span class="history-chart-value metric ${cls}">${def.fmt(last)}</span>
      <span class="history-chart-time">${timeStr}</span>
    </div>
    ${sparkline(def.values, { color: `var(--${cls === 'ok' ? 'ok' : cls === 'warning' ? 'warning' : 'critical'})`, yMin: def.yMin, yMax: def.yMax, thresholds: def.thresholds })}
  </div>`
}

function renderHistory(samples: HistorySample[]) {
  const times = samples.map(s => new Date(s.time))
  const signals = samples.map(s => s.signal_dbm)
  const txRates = samples.map(s => s.tx_rate_bps)
  const rxRates = samples.map(s => s.rx_rate_bps)
  const satisfactions = samples.map(s => s.satisfaction)

  const hasSatisfaction = satisfactions.some(v => v > 0)

  const signalColor = (v: number) => v > -65 ? 'ok' : v > -75 ? 'warning' : 'critical'
  const rateColor = (v: number) => v > 50e6 ? 'ok' : v > 10e6 ? 'warning' : 'critical'
  const satColor = (v: number) => v >= 80 ? 'ok' : v >= 50 ? 'warning' : 'critical'

  let html = ''
  html += historyChart({
    label: t('wifiHistorySignal'),
    description: t('wifiHistorySignalDesc'),
    values: signals, times,
    fmt: v => `${v} dBm`,
    color: signalColor,
    yMin: Math.min(...signals.filter(Boolean)) - 5,
    yMax: Math.max(...signals.filter(Boolean)) + 5,
    thresholds: [
      { value: -65, color: 'var(--ok)' },
      { value: -75, color: 'var(--warning)' },
    ],
  })
  html += historyChart({
    label: t('wifiHistoryTxRate'),
    description: t('wifiHistoryTxRateDesc'),
    values: txRates, times,
    fmt: v => formatRateKbps(v),
    color: rateColor,
    yMin: 0,
    thresholds: [
      { value: 50e6, color: 'var(--ok)' },
      { value: 10e6, color: 'var(--warning)' },
    ],
  })
  html += historyChart({
    label: t('wifiHistoryRxRate'),
    description: t('wifiHistoryRxRateDesc'),
    values: rxRates, times,
    fmt: v => formatRateKbps(v),
    color: rateColor,
    yMin: 0,
    thresholds: [
      { value: 50e6, color: 'var(--ok)' },
      { value: 10e6, color: 'var(--warning)' },
    ],
  })
  if (hasSatisfaction) {
    html += historyChart({
      label: t('wifiHistorySatisfaction'),
      description: t('wifiHistorySatisfactionDesc'),
      values: satisfactions, times,
      fmt: v => `${v}%`,
      color: satColor,
      yMin: 0, yMax: 100,
      thresholds: [
        { value: 80, color: 'var(--ok)' },
        { value: 50, color: 'var(--warning)' },
      ],
    })
  }

  el('wifi-history').innerHTML = html || `<p style="color:var(--text-muted)">${t('wifiHistoryUnavailable')}</p>`
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}
