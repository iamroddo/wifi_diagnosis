# Wi-Fi Diagnostic Application — Implementation Plan

## Context

Greenfield build. The project directory contains only `prompt.md` (the full spec). The goal is a
browser-based Wi-Fi/network diagnostic tool: the user opens a URL, the browser measures what it
actually experiences (latency, jitter, loss, throughput, stability), and a Go server correlates that
by **real client source IP** with the client's Wi-Fi info pulled from a self-hosted UniFi controller.
The server runs as a Docker container on a Synology NAS using **macvlan** networking so it sees the
real client IP (no NAT). Neither the browser nor UniFi alone is sufficient; the server fuses both into
one diagnostic session (spec §17).

**Confirmed decisions (override the spec where noted):**
- UniFi is a **classic self-hosted Network Application in Docker** at `unifi.roddo.net`, reachable from
  the diagnostic server (verified: `https://unifi.roddo.net` returns 302).
- Auth is **username/password**, not an API key. Config uses `UNIFI_USERNAME` / `UNIFI_PASSWORD`
  (replaces the spec's `UNIFI_API_KEY` in §2.2/§13).
- Classic controller → bare `/api/s/{site}/...` paths (no `/proxy/network` prefix); default site `default`.
- Frontend: **Vanilla TypeScript + Vite**. Backend WS: **coder/websocket**. First increment: **full vertical slice**.

**UniFi API facts to build against (verified via open-source clients — treat field names as
best-effort and map defensively; verify live against the controller early, per spec §13):**
- Login: `POST /api/login` with `{username, password}` → session cookie; capture `x-csrf-token`
  header (needed only for writes; our usage is read-only).
- Active clients: `GET /api/s/default/stat/sta` returns all clients; **filter by `ip` client-side**
  (no server-side IP query).
- Fields: `mac`, `ip`, `hostname`, `name`/`device_name`, `essid`, `ap_mac`, `band`, `channel`,
  `channel_width`, `rssi`, `tx_rate`, `rx_rate`, `retries`, `satisfaction`, `is_wired`.

## Hard prerequisite (spec §2.5.3, §17)

**macvlan IP-preservation must be proven before UniFi work is trusted.** This runs on the NAS, not
this dev machine, and is a deploy-time gate. The build proceeds locally in parallel, but UniFi
correlation is only *validated* once the container sees real client IPs on the NAS. Document the
`docker run --rm --network wifi-diag-net --ip 192.168.1.250 nginx:alpine` check in the README as a
required step before first real use.

## Architecture

```
frontend/ (Vite + TS)  --HTTP/WS-->  Go server (embeds built frontend)
                                         |-- session manager (SQLite)
                                         |-- UniFi client (login + stat/sta, TTL cache, rate limit)
                                         |-- WS diagnostics (latency echo, download, upload)
                                         |-- HTTP fallback endpoints
                                         |-- /healthz, /admin
```

### Backend layout (Go module `wifi-diagnostics`)
- `cmd/server/main.go` — config load, wiring, graceful shutdown on SIGTERM (flush sessions, close WS, finish DB writes).
- `internal/config/` — env-var config (`UNIFI_URL`, `UNIFI_USERNAME`, `UNIFI_PASSWORD`, `UNIFI_VERIFY_TLS`,
  `SERVER_PORT`, `LOG_LEVEL`, `DATA_RETENTION_DAYS`, `UNIFI_SITE=default`, `INTERNET_TEST_URL`, `TRUSTED_PROXIES`).
- `internal/unifi/` — `UniFiClient` interface (`FindClientByIP`, `GetClientDetails`) per spec §13;
  `real.go` (login+cookie jar+CSRF, `stat/sta` fetch, defensive JSON mapping, 5–10s TTL cache,
  outbound rate limit, graceful degradation when unreachable), `mock.go` (deterministic fake for
  dev/tests). Verify field names against live controller before relying on them.
- `internal/clientip/` — derive client IP from `r.RemoteAddr`; only honor `X-Forwarded-For`/`Forwarded`
  from explicitly configured `TRUSTED_PROXIES` (spec §2.5.1). Never trust arbitrary headers.
- `internal/session/` — session ID, correlation state machine (single match → associate; none → retry
  during session; multiple → ambiguous, never guess; §4), User-Agent validation signal, records to SQLite.
- `internal/store/` — SQLite (modernc.org/sqlite, pure-Go, no cgo → static binary) at `/app/data/`;
  schema per spec §10; retention/max-sessions/no-persistence mode (§11).
- `internal/diag/` — WS handler: latency echo (min/avg/median/p95/max + jitter), download (server
  streams binary frames, warm-up excluded), upload (client sends payload), stability events. Global
  **concurrency limiter** on throughput tests (max 1–2; queue excess with position, else reject with
  clear message; §7). HTTP fallback for latency/throughput when WS unavailable.
- `internal/diagengine/` — deterministic evidence rules combining UniFi + browser + internet results
  (§9). Reports evidence/likely factors, never false certainty. LLM explicitly out of scope for
  generating measurements.
- `internal/httpapi/` — routes: `/` (frontend), `/ws`, HTTP fallbacks, `/api/session`, `/healthz`
  (200 when HTTP server ready; UniFi-down = healthy-but-degraded, does NOT fail the healthcheck),
  `/admin` (combined result view; auth delegated to reverse proxy per §14). Privacy: MAC/hostname
  restricted to `/admin`, not the user page (§11).

### Frontend layout (`frontend/`, Vite + TS)
- User page: explains no install / no permissions / what's measured / approx data volume (§11); runs
  LAN test (vs container) and Internet test (vs `INTERNET_TEST_URL`); live latency/throughput readouts;
  clearly labels **browser-observed** vs **UniFi-reported** (e.g. `UniFi RSSI: -67 dBm`, never
  "Browser Wi-Fi RSSI") (§6). WS-primary with HTTP fallback.
- Admin page (`/admin`): combined session view like spec §14 example, incl. MAC/hostname/UniFi RF data.
- Built assets embedded into the Go binary via `embed.FS` so the runtime image needs no Node.

### Docker & tooling (§15)
- Multi-stage `Dockerfile`: Node (frontend build) → Go build (static, `CGO_ENABLED=0`) → minimal
  runtime (`gcr.io/distroless/static` or `alpine`), non-root user, `HEALTHCHECK` → `/healthz`.
  Target `linux/amd64` (optional `arm64` via buildx).
- `docker-compose.yml` (external macvlan net, no `ports:`, `ipv4_address: 192.168.1.250`, data volume),
  `.dockerignore`, `.env.example` (documents config, no real secrets), `Makefile`
  (`build/test/lint/docker-build/docker-run`).
- CI stub that builds and tests the image.

## Files created (representative)
`go.mod`, `cmd/server/main.go`, `internal/{config,unifi,clientip,session,store,diag,diagengine,httpapi}/*.go`,
`frontend/{package.json,vite.config.ts,index.html,src/*.ts}`, `Dockerfile`, `docker-compose.yml`,
`.dockerignore`, `.env.example`, `Makefile`, `README.md`, `.github/workflows/ci.yml`.

## Verification
1. **Unit/integration:** `make test` — clientip parsing (trusted vs untrusted proxy), correlation
   state machine (single/none/multiple), diagengine rules, UniFi JSON mapping against a captured
   `stat/sta` sample, latency stats math. Uses UniFi **mock**.
2. **Live UniFi check (early, gated):** point config at `unifi.roddo.net` with the read-only account,
   confirm login + `stat/sta` returns and field names match; adjust mapping if the live schema differs.
3. **Local end-to-end:** `make docker-run` (or `go run`), open `http://localhost:PORT/`, run LAN +
   Internet tests, confirm latency/throughput readouts and WS→HTTP fallback. Correlation will be
   "unavailable/uncorrelated" locally (no macvlan) — this is the correct graceful-degradation path.
4. **NAS deploy gate:** run the nginx macvlan test (§2.5.3); confirm access log shows the real client
   LAN IP. Only then is UniFi correlation trusted end-to-end. Confirm `/healthz` stays 200 with UniFi
   up or down; confirm `/admin` shows the fused session.

## Notes / call-outs
- Field names are community-sourced; the real UniFi client maps defensively and is verified live in step 2.
- Upload throughput can be bottlenecked by browser/TCP send buffers rather than the link — label results as indicative.
- macvlan puts the service directly on the LAN with no NAT and no app auth on the user page (by design); acceptable for this LAN, noted in README.
