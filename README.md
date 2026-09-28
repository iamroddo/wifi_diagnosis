# Wi-Fi Diagnostics

A browser-based Wi-Fi and network diagnostic tool. Open a URL — no software installation, browser extensions, or special permissions needed.

The server runs as a Docker container on a Synology NAS using **macvlan** networking so it sees the real client source IP, which it uses to correlate your session with your device's Wi-Fi information from the UniFi controller.

## What it does

**Browser measures:**
- LAN latency (min/avg/median/p95/max), jitter, packet loss
- Download and upload throughput (to the local diagnostic server)
- Internet latency (min/avg/median/p95/max), jitter, packet loss (to a configurable external endpoint)
- Internet download and upload speed (measured directly to Cloudflare's servers — no server involvement)
- Connection stability

**UniFi provides:**
- SSID, AP, band, channel, channel width
- RSSI (signal strength), TX/RX rates, retry rate, satisfaction
- Client MAC, hostname

The server combines both into a single diagnostic session with an evidence-based assessment.

**User-facing features:**
- English / German language switcher — preference is remembered across visits
- Optional email report button: when `CONTACT_EMAIL` is configured, a "Send Report" button appears after the diagnostic completes, pre-filling a mailto with session ID, Wi-Fi stats, LAN latency, throughput, internet latency, and internet speed values
- Assessment section with evidence-based findings covering LAN latency, jitter, packet loss, Wi-Fi signal, internet latency, and internet speed

---

## Prerequisites — macvlan IP-preservation check

**This must be verified before trusting UniFi correlation.**

On the Synology NAS via SSH, create the macvlan network once:

```bash
docker network create \
  --driver macvlan \
  --subnet=192.168.1.0/24 \
  --gateway=192.168.1.1 \
  --ip-range=192.168.1.250/31 \
  --aux-address 'host=192.168.1.251' \
  --opt parent=eth0 \
  wifi-diag-net
```

Verify real client IPs are preserved:

```bash
docker run --rm --network wifi-diag-net --ip 192.168.1.250 nginx:alpine
```

From a Wi-Fi client on the LAN, open `http://192.168.1.250/`. Check the nginx access log inside the container — it must show the client's real LAN IP, **not** a Docker bridge address (`172.17.0.x`).

Only proceed with deployment once this test passes.

---

## Deployment

### 1. Create a read-only UniFi account

In the UniFi controller, create a local admin account with read-only access. Never use your main admin account.

### 2. Configure

```bash
cp .env.example .env
# Edit .env with your UniFi URL, username, password
```

### 3. Build and deploy

Build the image (targets `linux/amd64` for Synology) and copy it to the NAS:

```bash
docker build --no-cache --platform linux/amd64 --load -t wifi-diagnostics:latest . && \
docker save wifi-diagnostics:latest | gzip | ssh admin@192.168.1.2 "cat > /tmp/wifi-diagnostics.tar.gz"
```

Create the directory on the NAS (SSH in interactively so sudo can prompt for a password):

```bash
ssh admin@192.168.1.2
sudo mkdir -p /volume1/docker/wifi-diagnosis
sudo chown admin:users /volume1/docker/wifi-diagnosis
exit
```

Then copy the compose file and `.env` from your local machine:

```bash
scp -O docker-compose.yml .env admin@192.168.1.2:/volume1/docker/wifi-diagnosis/
```

On the Synology, load the image, create the data directory, and start:

```bash
docker load < /tmp/wifi-diagnostics.tar.gz
cd /volume1/docker/wifi-diagnosis
sudo mkdir -p /volume1/docker/wifi-diagnostics/data
docker compose up -d
```

The container will be reachable at `http://192.168.1.250:8080`.

### Admin interface

`http://192.168.1.250:8080/admin` — shows recent diagnostic sessions including MAC addresses and hostnames. Protect with a reverse proxy if needed (auth is delegated to the proxy).

### Health check

```bash
curl http://192.168.1.250:8080/healthz
```

Returns `200` when the server is ready. UniFi unavailability is reported as `unifi_ok: false` but does not make the health check fail — browser diagnostics continue without UniFi.

---

## Development

```bash
# Run backend (no frontend embed, serves from frontend/dist if present)
make build-dev
UNIFI_URL=https://unifi.roddo.net UNIFI_USERNAME=... UNIFI_PASSWORD=... ./wifi-diagnostics

# Frontend dev server with proxy to backend
make frontend-dev   # http://localhost:5173

# All tests
make test

# Full production build (frontend + embedded Go binary)
make build
```

---

## Configuration

All configuration via environment variables or `.env` file.

| Variable | Default | Description |
|---|---|---|
| `UNIFI_URL` | — | UniFi controller URL, e.g. `https://unifi.roddo.net` |
| `UNIFI_USERNAME` | — | UniFi local admin username |
| `UNIFI_PASSWORD` | — | UniFi local admin password |
| `UNIFI_VERIFY_TLS` | `false` | Verify UniFi TLS certificate |
| `UNIFI_SITE` | `default` | UniFi site name |
| `SERVER_PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DATA_RETENTION_DAYS` | `30` | Days to keep sessions (0 = unlimited) |
| `MAX_STORED_SESSIONS` | `10000` | Maximum sessions to retain |
| `NO_PERSISTENCE` | `false` | In-memory only, no SQLite writes |
| `SITE_TITLE` | `Wi-Fi Diagnostics` | Page title shown in the browser and UI |
| `CONTACT_NAME` | — | Contact name shown in the page footer |
| `CONTACT_EMAIL` | — | Contact email shown in the page footer (linked) |
| `INTERNET_TEST_URL` | `https://one.one.one.one` | Internet latency test target |
| `TRUSTED_PROXIES` | — | Comma-separated proxy IPs to trust for `X-Forwarded-For` |

---

## Architecture

```
Browser (WS or HTTP fallback)
     │
     ▼
Go server (macvlan 192.168.1.250)
     │
     ├── clientip: real IP from r.RemoteAddr (no NAT)
     ├── session: correlation state machine
     ├── unifi: login + stat/sta + TTL cache
     ├── diag: WS latency/download/upload handler
     ├── diagengine: deterministic evidence rules
     ├── store: SQLite session persistence
     └── httpapi: routes + /healthz + /admin

Browser also fetches directly:
     ├── speed.cloudflare.com/__down  (internet download speed)
     └── speed.cloudflare.com/__up   (internet upload speed)
```

### Notes

- Upload throughput numbers can be bottlenecked by browser/TCP send buffers rather than the actual link — treat as indicative.
- The service is directly on the LAN with no application auth on the user-facing page (by design for a home network). Use a reverse proxy if access control is needed.
- VPN clients (Tailscale etc.) will not correlate with UniFi — they are not on the Wi-Fi path. Browser diagnostics still run.

---

## License

MIT
