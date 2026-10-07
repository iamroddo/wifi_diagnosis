// Shared types matching the Go API.

export interface SessionStartResponse {
  session_id: string
  client_ip: string
}

export interface SessionResponse {
  session_id: string
  client_ip: string
  correlation_status: 'ok' | 'not_found' | 'ambiguous' | 'unavailable' | 'pending'
  client_mac?: string
  wifi?: WiFiInfo
}

export interface WiFiInfo {
  ssid: string
  ap_mac: string
  ap_name: string
  band: string
  channel: number
  channel_width: string
  rssi_dbm: number
  tx_rate_bps: number
  rx_rate_bps: number
  retries: number
  satisfaction: number
}

export interface HistorySample {
  time: string          // RFC3339
  signal_dbm: number
  tx_rate_bps: number
  rx_rate_bps: number
  tx_bytes: number
  rx_bytes: number
  satisfaction: number  // 0 = not reported by controller
}

export interface LatencyResult {
  samples_ms: number[]
  min_ms: number
  avg_ms: number
  median_ms: number
  p95_ms: number
  max_ms: number
  jitter_ms: number
  loss_pct: number
  count: number
  sent: number
}

export interface ThroughputResult {
  mbps_avg: number
  mbps_peak: number
  bytes_total: number
  duration_secs: number
  warmup_ms: number
}

export interface Assessment {
  findings: Finding[]
  summary: string
  overall_severity: Severity
}

export interface Finding {
  severity: Severity
  category: string
  description: string
  evidence?: string
}

export type Severity = 'ok' | 'info' | 'warning' | 'critical'

export interface WSEnvelope {
  type: string
  seq?: number
  payload?: unknown
}

export interface ProgressPayload {
  bytes: number
  mbps: number
  elapsed: number
}

export interface BusyPayload {
  message: string
  position: number
}

// DiagSession matches the Go store.DiagSession JSON shape returned by /api/admin/sessions.
export interface DiagSession {
  id: string
  created_at: string
  client_ip: string
  correlation_status: string
  correlation_at?: string
  client_mac?: string
  client_hostname?: string
  client_name?: string
  unifi_client_id?: string
  ua_validation?: string
  ssid?: string
  ap_mac?: string
  band?: string
  channel?: number
  channel_width?: string
  rssi?: number
  tx_rate?: number
  rx_rate?: number
  retries?: number
  satisfaction?: number
  user_agent?: string
  lan_latency?: LatencyResult
  internet_result?: LatencyResult
  download?: ThroughputResult
  upload?: ThroughputResult
  internet_download?: ThroughputResult
  internet_upload?: ThroughputResult
  stability?: unknown
  assessment?: Assessment
  completed_at?: string
}
