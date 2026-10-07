# Wi-Fi Diagnostics

**Version:** 2026-10-07-a30ba7b

A browser-based Wi-Fi and network diagnostic tool — open **https://diag.roddo.net/** in any browser, no software installation or browser extensions needed.

The server runs as a Docker container on a Synology NAS using **macvlan** networking so it sees the real client source IP. It uses that IP to correlate your browser session with your device's Wi-Fi stats from the UniFi controller.

## What it measures

**Browser-side:**
- LAN latency (min / avg / median / p95 / max), jitter, packet loss
- LAN download and upload throughput (to the local diagnostic server)
- Internet latency (to a configurable external endpoint)
- Internet download and upload speed (directly to Cloudflare's servers — no server involvement)

**UniFi-side:**
- SSID, AP name, band, channel, channel width
- RSSI, TX/RX rates, retry rate, satisfaction score
- Client MAC address and hostname

Results are combined into a single session with an evidence-based assessment.

**User-facing features:**
- English / German language switcher (preference remembered across visits)
- Optional email report: when `CONTACT_EMAIL` is set, a "Send Report" button appears after the diagnostic with a pre-filled mailto containing session ID, Wi-Fi stats, latency, and speed results

---

## Prerequisites — verify macvlan IP preservation

**Do this before deploying.** The server must see real client IPs for UniFi correlation to work.

On the Synology NAS via SSH, create the macvlan network once (substitute values from your `.env`):

```bash
docker network create \
  --driver macvlan \
  --subnet=$MACVLAN_SUBNET \
  --gateway=$MACVLAN_GATEWAY \
  --ip-range=$CONTAINER_IP/31 \
  --aux-address "host=$MACVLAN_AUX_IP" \
  --opt parent=eth0 \
  wifi-diag-net
```

Verify real client IPs are preserved:

```bash
docker run --rm --network wifi-diag-net --ip $CONTAINER_IP nginx:alpine
```

From a Wi-Fi client on the LAN, open `http://$CONTAINER_IP/`. Check the nginx access log inside the container — it must show the client's real LAN IP, **not** a Docker bridge address (`172.17.x.x`).

Only proceed once this test passes.

---

## Deployment

### 1. Create a read-only UniFi account

In the UniFi controller create a local admin with read-only access. Do not use your main admin account.

### 2. Configure

```bash
cp .env.example .env
# Edit .env — set at minimum:
#   UNIFI_URL, UNIFI_USERNAME, UNIFI_PASSWORD
#   NAS_IP, CONTAINER_IP, MACVLAN_SUBNET, MACVLAN_GATEWAY, MACVLAN_AUX_IP
#   UNIFI_HOSTNAME, UNIFI_INTERNAL_IP
```

### 3. First-time deploy

Builds the image, streams it to the NAS, copies config, and starts the service:

```bash
make deploy
```

### Subsequent deploys

| Command | What it does |
|---|---|
| `make deploy` | Full redeploy — rebuild image, copy config, restart service |
| `make deploy-image` | Build and push the image only |
| `make deploy-files` | Copy `docker-compose.yml` and `.env` to the NAS only |
| `make deploy-restart` | Restart the running service without rebuilding |
| `make deploy-status` | Show container status on the NAS |
| `make deploy-logs` | Tail live logs from the NAS (Ctrl-C to stop) |

The app will be reachable at **https://diag.roddo.net/** (or directly at `http://$CONTAINER_IP:8080`).

### Admin interface

`http://$CONTAINER_IP:8080/admin` — lists recent diagnostic sessions with MAC addresses and hostnames. Protect with a reverse proxy if needed (auth is delegated to the proxy).

### Health check

```bash
curl http://$CONTAINER_IP:8080/healthz
```

Returns `200` when the server is ready. UniFi unavailability is reported as `"unifi_ok": false` but does not cause the health check to fail — browser diagnostics continue without UniFi.

---

## Development

```bash
# Backend only (serves frontend/dist from disk if present)
make build-dev
UNIFI_URL=https://unifi.example.com UNIFI_USERNAME=... UNIFI_PASSWORD=... ./wifi-diagnostics

# Frontend dev server with proxy to backend
make frontend-dev   # http://localhost:5173

# Run tests
make test

# Full production build (frontend embedded in Go binary)
make build
```

---

## Make reference

### Build

| Command | What it does |
|---|---|
| `make build` | Build frontend then compile Go binary with frontend embedded |
| `make build-dev` | Compile Go binary only (no frontend build) |
| `make frontend` | Build frontend assets and stamp version in README |
| `make frontend-dev` | Start Vite dev server (proxies API to backend on :8080) |
| `make test` | Run Go tests |
| `make lint` | Run `go vet` and golangci-lint |
| `make clean` | Remove built binary, frontend/dist, and data directory |

### Docker (local)

| Command | What it does |
|---|---|
| `make docker-build` | Build linux/amd64 image locally |
| `make docker-run` | Run the image locally on port 8080 using `.env` |

### Deploy to Synology

| Command | What it does |
|---|---|
| `make deploy` | Full redeploy — build image, copy config, restart service |
| `make deploy-image` | Build linux/amd64 image and load it onto the NAS |
| `make deploy-files` | Copy `docker-compose.yml` and `.env` to the NAS |
| `make deploy-restart` | Stop and restart the service without rebuilding the image |
| `make deploy-start` | Start the service if it is not running |
| `make deploy-status` | Show container status on the NAS |
| `make deploy-logs` | Tail live logs from the NAS (Ctrl-C to stop) |

All deploy commands read `NAS_IP`, `NAS_USER`, and `NAS_DEPLOY_DIR` from `.env`.

---

## Configuration

All configuration is via environment variables or a `.env` file.

### Application

| Variable | Default | Description |
|---|---|---|
| `UNIFI_URL` | — | UniFi controller URL, e.g. `https://unifi.example.com` |
| `UNIFI_USERNAME` | — | UniFi local admin username |
| `UNIFI_PASSWORD` | — | UniFi local admin password |
| `UNIFI_VERIFY_TLS` | `false` | Verify the UniFi TLS certificate |
| `UNIFI_SITE` | `default` | UniFi site name |
| `SERVER_PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DATA_RETENTION_DAYS` | `30` | Days to keep sessions (0 = unlimited) |
| `MAX_STORED_SESSIONS` | `10000` | Maximum sessions to retain (oldest pruned first) |
| `NO_PERSISTENCE` | `false` | In-memory only — no SQLite writes, data lost on restart |
| `SITE_TITLE` | `Wi-Fi Diagnostics` | Page title shown in the browser and UI |
| `CONTACT_NAME` | — | Contact name shown in the page footer |
| `CONTACT_EMAIL` | — | Contact email shown in the footer; enables the Send Report button |
| `INTERNET_TEST_URL` | `https://one.one.one.one` | Internet latency test target (fetched by the browser) |
| `TRUSTED_PROXIES` | — | Comma-separated proxy IPs to trust for `X-Forwarded-For` |

### Network / deployment

| Variable | Description |
|---|---|
| `NAS_IP` | IP address of the Synology NAS |
| `CONTAINER_IP` | IP address assigned to the container on the macvlan network |
| `MACVLAN_SUBNET` | Subnet for the macvlan network, e.g. `192.168.1.0/24` |
| `MACVLAN_GATEWAY` | Gateway for the macvlan network |
| `MACVLAN_AUX_IP` | Auxiliary host address reserved on the macvlan |
| `UNIFI_HOSTNAME` | Hostname of the UniFi controller (used in `extra_hosts`) |
| `UNIFI_INTERNAL_IP` | Internal Docker IP of the UniFi controller |

---

## Architecture

```
Browser (WebSocket or HTTP fallback)
     │
     ▼
Go server (macvlan $CONTAINER_IP)
     │
     ├── clientip      — real IP from r.RemoteAddr (no NAT)
     ├── session       — UniFi correlation state machine
     ├── unifi         — login, stat/sta lookup, TTL cache
     ├── diag          — WebSocket latency / download / upload handler
     ├── diagengine    — evidence-based assessment rules
     ├── store         — SQLite session persistence
     └── httpapi       — routes, /healthz, /admin

Browser also fetches directly:
     ├── speed.cloudflare.com/__down   (internet download speed)
     └── speed.cloudflare.com/__up    (internet upload speed)
```

### Notes

- Upload throughput can be bottlenecked by browser/TCP send buffers rather than the actual link — treat as indicative.
- The user-facing page has no application-level auth by design (home network). Use a reverse proxy if access control is needed.
- VPN clients (Tailscale etc.) will not correlate with UniFi — they are not on the Wi-Fi path. Browser diagnostics still run.

---

## License

MIT
