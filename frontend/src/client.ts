/**
 * DiagClient handles all communication with the diagnostic server.
 * Uses WebSocket as primary transport with HTTP fallback.
 */

import type { WSEnvelope, LatencyResult, ThroughputResult, ProgressPayload, BusyPayload } from './types'

export type OnProgress = (msg: string) => void

const WS_TIMEOUT_MS = 5000
const PING_COUNT = 20
const THROUGHPUT_DURATION = 10 // seconds

export class DiagClient {
  private ws: WebSocket | null = null
  private wsAvailable = true
  private seqCounter = 0
  private pendingPongs = new Map<number, { resolve: (rtMs: number) => void, reject: (e: Error) => void, sent: number }>()

  constructor(private baseUrl: string = '') {}

  async connect(): Promise<boolean> {
    const wsUrl = this.buildWsUrl()
    return new Promise(resolve => {
      try {
        const ws = new WebSocket(wsUrl)
        const timer = setTimeout(() => {
          ws.close()
          this.wsAvailable = false
          resolve(false)
        }, WS_TIMEOUT_MS)

        ws.onopen = () => {
          clearTimeout(timer)
          this.ws = ws
          this.wsAvailable = true
          this.setupMessageHandler(ws)
          resolve(true)
        }
        ws.onerror = () => {
          clearTimeout(timer)
          this.wsAvailable = false
          resolve(false)
        }
      } catch {
        this.wsAvailable = false
        resolve(false)
      }
    })
  }

  disconnect() {
    this.ws?.close()
    this.ws = null
  }

  /** Measure LAN latency via WebSocket pings or HTTP fallback. */
  async measureLatency(onProgress?: OnProgress): Promise<LatencyResult> {
    if (this.wsAvailable && this.ws?.readyState === WebSocket.OPEN) {
      return this.measureLatencyWS(onProgress)
    }
    return this.measureLatencyHTTP(onProgress)
  }

  private async measureLatencyWS(onProgress?: OnProgress): Promise<LatencyResult> {
    const samples: number[] = []
    let sent = 0

    for (let i = 0; i < PING_COUNT; i++) {
      const seq = ++this.seqCounter
      sent++
      try {
        const rtMs = await this.sendPing(seq)
        samples.push(rtMs)
        onProgress?.(`Latency: ${rtMs.toFixed(1)} ms`)
      } catch {
        onProgress?.(`Ping ${i + 1} timed out`)
      }
      await sleep(100)
    }
    return computeLatencyStats(samples, sent)
  }

  private async measureLatencyHTTP(onProgress?: OnProgress): Promise<LatencyResult> {
    const samples: number[] = []
    let sent = 0

    for (let i = 0; i < PING_COUNT; i++) {
      sent++
      const t0 = performance.now()
      try {
        await fetch(`${this.baseUrl}/ping`, { cache: 'no-store' })
        const rtMs = performance.now() - t0
        samples.push(rtMs)
        onProgress?.(`Latency (HTTP): ${rtMs.toFixed(1)} ms`)
      } catch {
        onProgress?.(`HTTP ping ${i + 1} failed`)
      }
      await sleep(150)
    }
    return computeLatencyStats(samples, sent)
  }

  /** Measure internet download speed via Cloudflare speed test CDN. */
  async measureInternetDownload(onProgress?: OnProgress): Promise<ThroughputResult | null> {
    const DURATION_MS = 10_000
    const WARMUP_MS = 2_000
    const PARALLEL = 4
    // 100 MB per stream — large enough that no stream finishes before the test ends at 250+ Mbps
    const url = 'https://speed.cloudflare.com/__down?bytes=104857600'

    const startT = performance.now()
    let warmupDone = false
    let measuredBytes = 0
    let measuredStart = 0
    let peakMbps = 0
    const abort = new AbortController()

    const runStream = async () => {
      try {
        const resp = await fetch(url, { cache: 'no-store', signal: abort.signal })
        if (!resp.body) return
        const reader = resp.body.getReader()
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          const now = performance.now()
          if (!warmupDone && now - startT >= WARMUP_MS) {
            warmupDone = true
            measuredStart = now
            measuredBytes = 0
          }
          if (warmupDone) {
            measuredBytes += value.byteLength
            const dur = (now - measuredStart) / 1000
            if (dur > 0) {
              const mbps = (measuredBytes * 8) / dur / 1e6
              if (dur >= 1 && mbps > peakMbps) peakMbps = mbps
              onProgress?.(`Internet download: ${mbps.toFixed(1)} Mbps`)
            }
          }
          if (now - startT >= DURATION_MS) { abort.abort(); break }
        }
      } catch { /* AbortError or network error — partial result is still used */ }
    }

    await Promise.all(Array.from({ length: PARALLEL }, runStream))

    const elapsed = (performance.now() - startT) / 1000
    if (!warmupDone) return null

    const measuredDur = Math.max(0.1, (performance.now() - measuredStart) / 1000)
    const avgMbps = (measuredBytes * 8) / measuredDur / 1e6
    if (peakMbps === 0) peakMbps = avgMbps

    return {
      mbps_avg: round2(avgMbps),
      mbps_peak: round2(peakMbps),
      bytes_total: measuredBytes,
      duration_secs: round2(elapsed),
      warmup_ms: WARMUP_MS,
    }
  }

  /** Measure internet upload speed via Cloudflare speed test CDN. */
  async measureInternetUpload(onProgress?: OnProgress): Promise<ThroughputResult | null> {
    const DURATION_MS = 10_000
    const WARMUP_MS = 2_000
    const STREAMS = 6
    const CHUNK_BYTES = 1_000_000

    const startT = performance.now()
    let warmupDone = false
    let measuredBytes = 0
    let measuredStart = 0
    let peakMbps = 0

    const recordChunk = () => {
      const now = performance.now()
      if (!warmupDone && now - startT >= WARMUP_MS) {
        warmupDone = true
        measuredStart = now
        measuredBytes = 0
      }
      if (warmupDone) {
        measuredBytes += CHUNK_BYTES
        const dur = (now - measuredStart) / 1000
        if (dur > 0) {
          const mbps = (measuredBytes * 8) / dur / 1e6
          if (dur >= 1 && mbps > peakMbps) peakMbps = mbps
          onProgress?.(`Internet upload: ${mbps.toFixed(1)} Mbps`)
        }
      }
    }

    const chunk = new Uint8Array(CHUNK_BYTES)
    const runStream = async () => {
      while (performance.now() - startT < DURATION_MS) {
        try {
          await fetch('https://speed.cloudflare.com/__up', {
            method: 'POST',
            body: chunk,
            cache: 'no-store',
          })
          recordChunk()
        } catch { break }
      }
    }

    await Promise.all(Array.from({ length: STREAMS }, runStream))

    const elapsed = (performance.now() - startT) / 1000
    if (!warmupDone) return null

    const measuredDur = Math.max(0.1, (performance.now() - measuredStart) / 1000)
    const avgMbps = (measuredBytes * 8) / measuredDur / 1e6
    if (peakMbps === 0) peakMbps = avgMbps

    return {
      mbps_avg: round2(avgMbps),
      mbps_peak: round2(peakMbps),
      bytes_total: measuredBytes,
      duration_secs: round2(elapsed),
      warmup_ms: WARMUP_MS,
    }
  }

  /** Measure internet latency via a configurable HTTP endpoint. */
  async measureInternetLatency(testUrl: string, onProgress?: OnProgress): Promise<LatencyResult> {
    const samples: number[] = []
    let sent = 0
    const count = 5

    for (let i = 0; i < count; i++) {
      sent++
      const t0 = performance.now()
      try {
        await fetch(testUrl, { cache: 'no-store', mode: 'no-cors' })
        const rtMs = performance.now() - t0
        samples.push(rtMs)
        onProgress?.(`Internet latency: ${rtMs.toFixed(1)} ms`)
      } catch {
        onProgress?.(`Internet ping ${i + 1} failed`)
      }
      await sleep(200)
    }
    return computeLatencyStats(samples, sent)
  }

  /** Measure download throughput. Returns null if slot is busy. */
  async measureDownload(
    onProgress?: OnProgress
  ): Promise<ThroughputResult | null> {
    if (this.wsAvailable && this.ws?.readyState === WebSocket.OPEN) {
      return this.measureDownloadWS(onProgress)
    }
    return this.measureDownloadHTTP(onProgress)
  }

  private measureDownloadWS(onProgress?: OnProgress): Promise<ThroughputResult | null> {
    return new Promise((resolve, reject) => {
      const ws = this.ws!
      const seq = ++this.seqCounter

      const handler = (event: MessageEvent) => {
        const env = parseEnvelope(event.data)
        if (!env || env.seq !== seq) return

        if (env.type === 'progress') {
          const p = env.payload as ProgressPayload
          onProgress?.(`Download: ${p.mbps.toFixed(1)} Mbps`)
        } else if (env.type === 'result') {
          ws.removeEventListener('message', handler)
          resolve(env.payload as ThroughputResult)
        } else if (env.type === 'busy') {
          ws.removeEventListener('message', handler)
          const b = env.payload as BusyPayload
          onProgress?.(`Download slot busy (position ${b.position}) — skipping`)
          resolve(null)
        } else if (env.type === 'error') {
          ws.removeEventListener('message', handler)
          reject(new Error('server error during download'))
        }
      }

      ws.addEventListener('message', handler)
      ws.send(JSON.stringify({ type: 'start_download', seq, payload: { duration_secs: THROUGHPUT_DURATION } }))
    })
  }

  private async measureDownloadHTTP(onProgress?: OnProgress): Promise<ThroughputResult | null> {
    const t0 = performance.now()
    let totalBytes = 0
    let peakMbps = 0
    const warmupMs = 2000

    try {
      const resp = await fetch(`${this.baseUrl}/download`, { cache: 'no-store' })
      if (!resp.body) return null
      const reader = resp.body.getReader()
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        totalBytes += value.byteLength
        const elapsed = performance.now() - t0
        const mbps = (totalBytes * 8) / elapsed / 1000
        if (mbps > peakMbps) peakMbps = mbps
        onProgress?.(`Download (HTTP): ${mbps.toFixed(1)} Mbps`)
      }
    } catch {
      // partial result is still useful
    }

    const elapsed = (performance.now() - t0) / 1000
    const measuredBytes = Math.max(0, totalBytes - Math.floor(totalBytes * warmupMs / (elapsed * 1000)))
    const measuredDur = Math.max(0.1, elapsed - warmupMs / 1000)
    const avgMbps = (measuredBytes * 8) / measuredDur / 1e6

    return {
      mbps_avg: round2(avgMbps),
      mbps_peak: round2(peakMbps),
      bytes_total: totalBytes,
      duration_secs: round2(elapsed),
      warmup_ms: warmupMs,
    }
  }

  /** Measure upload throughput. Returns null if slot is busy. */
  async measureUpload(onProgress?: OnProgress): Promise<ThroughputResult | null> {
    if (!this.wsAvailable || this.ws?.readyState !== WebSocket.OPEN) {
      onProgress?.('Upload test requires WebSocket — skipping (HTTP fallback not available for upload)')
      return null
    }

    const ws = this.ws!
    const seq = ++this.seqCounter

    return new Promise((resolve, reject) => {
      let uploading = false
      let uploadTimer: ReturnType<typeof setInterval> | null = null

      const cleanup = () => {
        if (uploadTimer) { clearInterval(uploadTimer); uploadTimer = null }
        ws.removeEventListener('message', handler)
      }

      const handler = (event: MessageEvent) => {
        const env = parseEnvelope(event.data)
        if (!env || env.seq !== seq) return

        if (env.type === 'progress' && !uploading) {
          const p = env.payload as { ready?: boolean; duration_secs?: number }
          if (p.ready) {
            uploading = true
            const durationMs = (p.duration_secs ?? THROUGHPUT_DURATION) * 1000
            const deadline = Date.now() + durationMs
            const chunkSize = 64 * 1024
            const chunk = new Uint8Array(chunkSize)

            const sendChunks = () => {
              if (ws.readyState !== WebSocket.OPEN) {
                cleanup()
                resolve(null)
                return
              }
              if (Date.now() >= deadline) {
                try { ws.send(JSON.stringify({ type: 'upload_done', seq })) } catch { /* ignore */ }
                cleanup()
                return
              }
              try {
                ws.send(chunk.buffer)
              } catch {
                cleanup()
                resolve(null)
              }
            }
            uploadTimer = setInterval(sendChunks, 10)
          }
        } else if (env.type === 'progress' && uploading) {
          const p = env.payload as ProgressPayload
          onProgress?.(`Upload: ${p.mbps.toFixed(1)} Mbps`)
        } else if (env.type === 'result') {
          cleanup()
          resolve(env.payload as ThroughputResult)
        } else if (env.type === 'busy') {
          cleanup()
          const b = env.payload as BusyPayload
          onProgress?.(`Upload slot busy (position ${b.position}) — skipping`)
          resolve(null)
        } else if (env.type === 'error') {
          cleanup()
          reject(new Error('server error during upload'))
        }
      }

      ws.addEventListener('message', handler)
      ws.send(JSON.stringify({ type: 'start_upload', seq, payload: { duration_secs: THROUGHPUT_DURATION } }))
    })
  }

  private sendPing(seq: number): Promise<number> {
    return new Promise((resolve, reject) => {
      const sent = performance.now()
      const timeout = setTimeout(() => {
        this.pendingPongs.delete(seq)
        reject(new Error('ping timeout'))
      }, 3000)

      this.pendingPongs.set(seq, {
        resolve: (rtMs) => { clearTimeout(timeout); resolve(rtMs) },
        reject,
        sent,
      })
      this.ws!.send(JSON.stringify({ type: 'ping', seq }))
    })
  }

  private setupMessageHandler(ws: WebSocket) {
    ws.onmessage = (event: MessageEvent) => {
      const env = parseEnvelope(event.data)
      if (!env) return
      if (env.type === 'pong' && env.seq !== undefined) {
        const pending = this.pendingPongs.get(env.seq)
        if (pending) {
          this.pendingPongs.delete(env.seq)
          pending.resolve(performance.now() - pending.sent)
        }
      }
      // Other message types are handled by per-operation listeners.
    }

    ws.onclose = () => {
      this.ws = null
      this.wsAvailable = false
      // Reject all pending pings.
      for (const [, pending] of this.pendingPongs) {
        pending.reject(new Error('websocket closed'))
      }
      this.pendingPongs.clear()
    }
  }

  private buildWsUrl(): string {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = this.baseUrl ? new URL(this.baseUrl).host : location.host
    return `${proto}//${host}/ws`
  }
}

function parseEnvelope(data: unknown): WSEnvelope | null {
  if (typeof data !== 'string') return null
  try {
    return JSON.parse(data) as WSEnvelope
  } catch {
    return null
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms))
}

function round2(v: number): number {
  return Math.round(v * 100) / 100
}

function computeLatencyStats(samples: number[], sent: number): LatencyResult {
  if (samples.length === 0) {
    return {
      samples_ms: [], min_ms: 0, avg_ms: 0, median_ms: 0,
      p95_ms: 0, max_ms: 0, jitter_ms: 0,
      loss_pct: sent > 0 ? 100 : 0, count: 0, sent,
    }
  }

  const sorted = [...samples].sort((a, b) => a - b)
  const sum = sorted.reduce((a, b) => a + b, 0)
  const avg = sum / sorted.length

  let jitterSum = 0
  for (let i = 1; i < samples.length; i++) {
    jitterSum += Math.abs(samples[i] - samples[i - 1])
  }
  const jitter = samples.length > 1 ? jitterSum / (samples.length - 1) : 0

  return {
    samples_ms: samples.map(v => round2(v)),
    min_ms: round2(sorted[0]),
    avg_ms: round2(avg),
    median_ms: percentile(sorted, 50),
    p95_ms: percentile(sorted, 95),
    max_ms: round2(sorted[sorted.length - 1]),
    jitter_ms: round2(jitter),
    loss_pct: sent > 0 ? round2((sent - samples.length) / sent * 100) : 0,
    count: samples.length,
    sent,
  }
}

function percentile(sorted: number[], p: number): number {
  const idx = (p / 100) * (sorted.length - 1)
  const lo = Math.floor(idx)
  const hi = Math.ceil(idx)
  if (lo === hi) return round2(sorted[lo])
  return round2(sorted[lo] * (1 - (idx - lo)) + sorted[hi] * (idx - lo))
}
