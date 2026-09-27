# Wi-Fi Diagnostic Application — Project Specification

## 1. Purpose

Build a lightweight, browser-based network and Wi-Fi diagnostic application for troubleshooting connectivity problems from the user's actual device.

The application must work from a normal web browser on:

* Windows PCs
* macOS
* iPhone/iPad
* Android devices

The user should only need to open a URL. No browser extension, native application, special browser permissions, or client-side installation should be required.

The application will combine:

1. **Measurements performed by browser code**
2. **Client and Wi-Fi information obtained from the UniFi Network Controller via the diagnostic server**

The diagnostic server runs as a **Docker container** on a **Synology NAS** and is deployed on the user's local network infrastructure.

The diagnostic server can be on a different VLAN from clients. The client VLAN and diagnostic-server VLAN are routed, and **there is no NAT between clients and the diagnostic server**.

---

# 2. Deployment

## 2.1 Docker container

The diagnostic server must run as a **self-contained Docker container** on a Synology NAS running DSM 7.x with Container Manager.

The container should:

* contain the Go backend
* serve the compiled browser frontend
* expose the diagnostic HTTP service
* communicate with the UniFi Network Controller over the network
* require no host-side application installation beyond Docker/Container Manager
* have no dependency on a particular Linux distribution
* run without privileged container access
* run with a non-root user where practical

The application should be lightweight enough to run comfortably on a Synology NAS alongside other containers. Synology NAS units typically have 2–8 GB RAM and mobile-class CPUs; the application must be conservative with memory and CPU, particularly during throughput tests.

### 2.1.1 Target architecture

The Docker image must target **`linux/amd64`** at minimum, as most Docker-capable Synology NAS units (Plus-series, Value-series) use Intel/AMD x86_64 processors.

An optional `linux/arm64` variant may be provided for newer ARMv8 Synology models that support Container Manager, using `docker buildx` for multi-platform builds.

---

## 2.2 Container configuration

Configuration should be supplied through environment variables and/or a mounted configuration file.

At minimum, configuration should support:

```text
UNIFI_URL
UNIFI_API_KEY
UNIFI_VERIFY_TLS
SERVER_PORT
LOG_LEVEL
DATA_RETENTION_DAYS
```

The exact UniFi authentication configuration must be determined when the current UniFi API is verified.

Secrets such as the UniFi API credential must **not** be baked into the Docker image.

A `.env.example` should document the available configuration without containing real credentials.

---

## 2.3 Network: macvlan deployment

### 2.3.1 Why macvlan is required

The diagnostic server must see the **real client source IP** on every inbound connection in order to correlate clients with UniFi.

With Synology's default Docker bridge networking and port mapping, the container sees the Docker bridge gateway IP (e.g., `172.17.0.1`) as the source for all connections. **This breaks UniFi correlation entirely.**

The solution is **macvlan networking**, which gives the container its own MAC address and IP address directly on the LAN. There is no NAT or port mapping — clients connect to the container's LAN IP, and the container sees their real source addresses.

This is the established pattern on Synology for services that require real client IPs (Pi-hole, reverse proxies, etc.).

### 2.3.2 macvlan network creation

The macvlan Docker network is created once via SSH on the Synology NAS. It persists across reboots — Docker retains network definitions.

**Identify the parent interface:**

```bash
ip addr show
```

Use the interface that holds the NAS LAN IP. On this deployment:

* **`eth0`** — primary LAN interface at `192.168.1.2/24`
* No Open vSwitch / `ovs_eth0` — Virtual Machine Manager is not installed

If Open vSwitch is active (e.g., if Virtual Machine Manager is installed), the parent interface will be `ovs_eth0` instead of `eth0`.

**Create the network:**

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

This allocates:

| Address | Purpose |
|---|---|
| `192.168.1.250` | Diagnostic container |
| `192.168.1.251` | Reserved (aux-address) |

Both addresses must be **excluded from the DHCP pool** on the UniFi router.

### 2.3.3 Host-to-container communication

With macvlan, the Synology NAS host **cannot directly reach the container** due to a Linux kernel limitation. This is acceptable — the NAS does not need to communicate with the diagnostic container.

All LAN clients (Wi-Fi devices, wired devices, devices on other routed VLANs) can reach the container directly at `192.168.1.250`.

If NAS-to-container communication is needed in the future, a macvlan shim interface can be added on the host.

### 2.3.4 DSM firewall

If the Synology DSM firewall is enabled, a rule must allow inbound traffic to `192.168.1.250`. The DSM firewall operates at the host level and can block traffic destined for macvlan IPs.

---

## 2.4 Docker Compose

Provide a `docker-compose.yml` for deployment. Because the macvlan network is created manually, it is referenced as an **external** network in Compose.

```yaml
services:
  wifi-diagnostics:
    image: wifi-diagnostics:latest
    container_name: wifi-diagnostics
    restart: unless-stopped
    networks:
      wifi-diag-net:
        ipv4_address: 192.168.1.250
    environment:
      UNIFI_URL: ${UNIFI_URL}
      UNIFI_API_KEY: ${UNIFI_API_KEY}
      SERVER_PORT: 8080
    volumes:
      - /volume1/docker/wifi-diagnostics/data:/app/data

networks:
  wifi-diag-net:
    external: true
```

Key differences from a standard bridge-mode deployment:

* **No `ports:` section.** macvlan containers do not use port mapping. The container is reachable directly at `192.168.1.250:8080`.
* **`external: true`** because the network was created manually via `docker network create`.
* **Volume mount** follows Synology conventions (`/volume1/docker/...`).

The application should also be usable behind an existing reverse proxy such as:

* Caddy
* Traefik
* Nginx
* an existing UniFi/network reverse proxy arrangement

The application itself should not require a reverse proxy for basic operation.

---

## 2.5 Network requirements and traffic flow

```text
Wi-Fi Client (e.g. 192.168.20.47)
     │
     │  routed (no NAT)
     ▼
Synology NAS (192.168.1.2) — eth0
     │
     │  macvlan — container has its own MAC on the LAN
     ▼
Wi-Fi Diagnostic Container (192.168.1.250)
     │
     │  r.RemoteAddr = "192.168.20.47:xxxxx"  ← real client IP
     │
     │  UniFi API lookup: find client with IP 192.168.20.47
     ▼
UniFi Controller
     │
     └── MAC, AP, SSID, RSSI, channel, rates, retries
```

No NAT anywhere in the path. The container sees the real source IP. UniFi correlation works.

The Docker container requires network connectivity to:

1. **Diagnostic clients** — LAN or routed VLAN clients connecting to `192.168.1.250`
2. **The UniFi Network Controller/API** — reachable from the container's network position

### 2.5.1 Reverse proxy and trusted headers

If a reverse proxy is placed in front of the diagnostic server, the application must support trusted proxy configuration so that the original client IP can be recovered from `Forwarded` or `X-Forwarded-For` headers.

The application must **not blindly trust arbitrary forwarded-IP headers**. Only explicitly configured trusted proxies may supply the client IP.

### 2.5.2 Tailscale and VPN access

If the NAS is accessible via Tailscale or another VPN overlay, clients connecting over the VPN will present their VPN IP as the source address. UniFi correlation will not work for these clients because they are not traversing the Wi-Fi network.

The application should handle this gracefully: correlation fails, browser diagnostics still run. This is correct behaviour — there is no Wi-Fi path to diagnose for a VPN client.

### 2.5.3 IP preservation validation

Before building the application, verify that the container sees real client IPs:

```bash
docker run --rm --network wifi-diag-net --ip 192.168.1.250 nginx:alpine
```

From a Wi-Fi client on the LAN, open `http://192.168.1.250/`. Check the nginx access log inside the container — it should show the client's real LAN IP, not a Docker bridge address.

**This test must pass before any UniFi integration work begins.**

---

# 3. High-level architecture

```text
                         ┌──────────────────────┐
                         │   UniFi Controller   │
                         │                      │
                         │ Client information   │
                         │ AP / radio / RSSI    │
                         │ channel / retries    │
                         └──────────┬───────────┘
                                    │
                                    │ UniFi API
                                    │
                                    ▼
┌───────────────┐            ┌─────────────────────────┐
│ Client device │  HTTP/WS   │ Synology NAS             │
│               │◄──────────►│                          │
│ Browser JS    │            │ Wi-Fi Diagnostic         │
│               │            │ Container (Go)           │
│               │            │ macvlan: 192.168.1.250   │
└───────────────┘            └──────────┬───────────────┘
                                        │
                                        │ client IP from
                                        │ r.RemoteAddr
                                        ▼
                                  UniFi client
                                  lookup by IP
```

The browser and UniFi provide complementary information.

### Browser

Measures what the user actually experiences:

* latency
* jitter
* packet loss/failures
* connection stability
* download throughput
* upload throughput
* latency over time
* interruptions
* WebSocket stability

### UniFi

Provides network infrastructure and Wi-Fi information:

* client IP
* MAC address
* hostname/name
* SSID
* associated AP
* AP ID
* radio/band
* channel
* channel width
* RSSI/signal strength
* TX rate
* RX rate
* retries
* other relevant client/AP statistics exposed by the current UniFi API

---

# 4. Client identification and UniFi correlation

The diagnostic server must identify the client using the **client IP address observed on the incoming connection** (`r.RemoteAddr` in Go).

Because the container uses macvlan networking with no NAT in the path, the server sees the actual client source IP.

The client MAC address must **not** be obtained from browser JavaScript.

The browser must **not** be expected to self-report its IP address or any other network-layer identifier. Browsers do not expose reliable network identity information, and any self-reported value would be untrustworthy and potentially spoofable.

The correlation flow is:

```text
Browser
   │
   │ source IP (from TCP connection)
   ▼
Diagnostic container (macvlan, 192.168.1.250)
   │
   │ lookup client by IP
   ▼
UniFi Controller
   │
   │ active client record
   ▼
MAC / hostname / SSID / AP / radio / RSSI / etc.
```

The UniFi client record is the authoritative source of the client's Wi-Fi information and MAC address.

### Correlation procedure

For every diagnostic session:

1. Generate a unique diagnostic session ID.
2. Determine the client's source IP from `r.RemoteAddr` on the incoming connection.
3. Query UniFi for an active client matching that IP.
4. If exactly one client matches, associate that UniFi client with the diagnostic session.
5. Retrieve the client's current UniFi information.
6. Continue the browser diagnostics regardless of whether UniFi correlation succeeds.
7. If no client is found, retry correlation during the session.
8. If multiple clients match, mark the correlation as ambiguous rather than arbitrarily selecting one.
9. Record the correlation status and timestamp.

The system should tolerate a short delay between a client connecting to Wi-Fi and appearing in the UniFi client list.

### User-Agent as a validation signal

The HTTP User-Agent header may be used as a secondary signal to **validate** a correlation match. If UniFi reports the client as an iPhone but the browser User-Agent indicates Windows, this is evidence the correlation may be stale or incorrect. The User-Agent must not be used as a primary correlator.

---

# 5. Browser Wi-Fi information

## 5.1 Explicit non-requirement

The browser must **not** be expected to obtain physical Wi-Fi radio information.

Normal browser JavaScript cannot reliably access:

* RSSI in dBm
* BSSID
* Wi-Fi channel
* channel width
* TX/RX PHY rate
* Wi-Fi retry rate
* radio interference
* noise level
* 2.4/5/6 GHz radio details

The application must therefore **not depend on browser access to these values**.

The Network Information API may be used where available for supplementary information, but it must not be treated as a source of authoritative Wi-Fi radio measurements.

---

## 5.2 No client-side privileged component

The initial application must not require:

* browser extensions
* native helper applications
* operating-system agents
* special browser builds
* elevated permissions
* client certificates
* device management software

This is an intentional design requirement so that the diagnostic can be used simply by opening a URL.

A future native diagnostic agent could potentially provide additional OS-level Wi-Fi information, but this is explicitly outside the scope of the initial browser-based application.

---

# 6. Wi-Fi signal strength

Wi-Fi signal strength should be obtained from **UniFi**, not from the browser.

```text
Browser
   │
   ├── latency
   ├── jitter
   ├── packet loss
   └── throughput
          │
          ▼
Diagnostic Container (macvlan)
          │
          └── UniFi lookup
                 │
                 ├── AP
                 ├── SSID
                 ├── Band
                 ├── Channel
                 ├── RSSI
                 ├── TX/RX
                 └── Retries
```

The application should clearly distinguish between:

* **browser-observed performance**
* **UniFi-reported wireless conditions**

Do not describe UniFi RSSI as if it were directly measured by the browser.

UniFi RSSI represents the wireless signal information available to the UniFi infrastructure and should be labelled accordingly, e.g.:

`UniFi RSSI: -67 dBm`

rather than:

`Browser Wi-Fi RSSI: -67 dBm`

---

# 7. Diagnostic measurements

The browser should perform tests against the diagnostic server using **WebSocket as the primary transport**, with HTTP fallback.

WebSocket is preferred because:

* Lower per-measurement overhead than HTTP request/response cycles
* More representative latency numbers (no TCP handshake or HTTP framing noise per measurement)
* Connection stability monitoring comes for free (disconnect detection)
* Bidirectional communication supports real-time progress updates

At minimum:

### Latency

Measure repeated WebSocket ping/pong or echo messages to the diagnostic server and calculate:

* minimum
* average
* median
* p95
* maximum

### Jitter

Calculate variation in successive latency measurements.

### Packet loss / failed requests

Record failed or timed-out requests and WebSocket disconnects.

### Download

Measure sustained download throughput from the diagnostic server. The server streams data to the client over WebSocket binary frames or HTTP chunked transfer.

### Upload

Measure sustained upload throughput to the client. The browser sends generated payload to the server.

### Throughput test methodology

* Define default payload sizes and test durations, with conservative defaults for mobile/metered connections.
* Exclude an initial warm-up period (e.g., first 2 seconds) from throughput calculations to avoid TCP slow-start bias.
* Stream data rather than buffering entire payloads in memory — the Synology NAS has limited RAM.
* Limit maximum concurrent throughput tests server-wide to prevent mutual interference. Throughput tests saturate bandwidth by design; concurrent tests produce misleading results. Queue or reject excess requests with a clear user-facing message.

### Stability

Record:

* connection failures
* latency spikes
* interruptions
* WebSocket disconnects
* changes in performance during the test

The tests should be configurable so that test duration and transferred data can be limited.

---

# 8. LAN versus Internet testing

The application should distinguish between local-network performance and Internet performance.

At minimum, provide:

### LAN test

```text
Client → Wi-Fi → AP → LAN → Diagnostic Container (192.168.1.250)
```

This isolates much of the Wi-Fi/LAN path.

### Internet test

```text
Client → Wi-Fi → AP → LAN → Router → Internet → Test endpoint
```

This allows the application to distinguish problems affecting the local wireless/network path from problems affecting the WAN/Internet connection.

A poor Internet result combined with a healthy LAN result should not automatically be diagnosed as a Wi-Fi problem.

### Internet test endpoint

The Internet test endpoint should be **configurable** via server configuration (environment variable or config file), with a sensible default such as a well-known DNS-over-HTTPS latency check or a configurable HTTP endpoint.

Document the limitations: external endpoints may impose rate limits, CORS restrictions, or become unavailable. The Internet test is supplementary — the LAN test is the primary diagnostic.

---

# 9. Diagnostic correlation

The application should correlate browser measurements with UniFi information.

Example:

```text
UniFi:
  RSSI          -73 dBm
  Retries       19%
  Band          5 GHz
  AP            Living Room

Browser:
  LAN latency   35 ms
  Jitter        22 ms
  Loss          4.2%
```

The diagnostic engine can then identify evidence consistent with a wireless problem.

Conversely:

```text
UniFi:
  RSSI          -54 dBm
  Retries       1%

Browser:
  LAN latency   3 ms
  Loss          0%

Internet:
  latency       85 ms
  throughput    12 Mbps
```

This would provide evidence that the problem is more likely beyond the Wi-Fi/LAN portion of the connection.

The diagnostic engine must report **evidence and likely contributing factors**, rather than claiming certainty where the measurements do not establish the cause.

---

# 10. Diagnostic session logging

Every diagnostic session receives a unique session ID.

The server should log, subject to configurable retention:

### Client identity

* session ID
* timestamp
* client IP
* client MAC from UniFi
* hostname/name from UniFi
* UniFi client ID

### Wi-Fi information

* SSID
* AP
* AP ID
* radio/band
* channel
* channel width
* RSSI
* TX rate
* RX rate
* retries
* other available UniFi RF/client metrics

### Browser measurements

* browser/platform information (User-Agent)
* LAN latency statistics
* jitter
* packet loss
* download throughput
* upload throughput
* stability/connection events
* Internet test results

### Correlation metadata

* time of UniFi lookup
* correlation success/failure
* correlation status
* User-Agent validation result (match/mismatch with UniFi device type)
* UniFi data timestamp where available

---

# 11. Privacy

The normal user-facing diagnostic page should not expose unnecessary client-identifying information.

In particular, MAC address and hostname should normally be restricted to the administrator interface.

The application should support configurable:

* diagnostic logging
* retention period
* maximum stored sessions
* optional no-persistence mode

The user-facing page should clearly explain:

* that the diagnostic runs in the browser
* that no software is installed
* that no special browser permissions are required
* what measurements are being performed
* approximately how much data will be transferred

---

# 12. Technology

## Backend

* Go
* standard `net/http` where practical
* WebSocket support (e.g., `gorilla/websocket` or `nhooyr.io/websocket`)
* lightweight dependencies
* structured JSON logging
* Docker with macvlan networking
* non-root container execution where practical
* graceful shutdown on `SIGTERM` (flush in-progress sessions, close WebSocket connections, finish pending database writes)

## Frontend

* TypeScript
* Vite or similarly lightweight build system
* Consider Preact or Lit as lightweight reactive alternatives if vanilla DOM manipulation becomes unwieldy for real-time diagnostic charts and WebSocket state

## Persistence

SQLite for diagnostic session storage. The data model is well-defined (section 10) and session data has clear value for the admin interface; in-memory-only storage would lose all history on container restart.

The SQLite database file should be stored on a mounted volume (`/app/data/`) so it persists independently of the container image.

## Health endpoint

Provide a `/healthz` endpoint that returns HTTP 200 when the server is ready to accept diagnostic sessions. Include a `HEALTHCHECK` instruction in the Dockerfile.

---

# 13. UniFi API implementation

The UniFi integration must be implemented behind an internal interface, for example:

```go
type UniFiClient interface {
    FindClientByIP(ctx context.Context, ip string) (*Client, error)
    GetClientDetails(ctx context.Context, clientID string) (*ClientDetails, error)
}
```

The implementation must use the **current supported UniFi Network API**.

Before implementation, verify:

* authentication mechanism
* client lookup by IP
* current client endpoint
* actual field names
* AP association information
* RSSI availability
* radio/band information
* channel information
* TX/RX rates
* retry statistics
* API version compatibility

Do not invent UniFi endpoints or field names based on older documentation or examples.

A mock UniFi implementation should be provided for development and automated testing.

### UniFi API rate limiting and caching

* Implement a short TTL cache (e.g., 5–10 seconds) for UniFi client lookups to avoid hammering the controller when multiple diagnostics run simultaneously.
* Rate-limit the diagnostic server's outbound UniFi API calls.
* Degrade gracefully when the UniFi controller is unreachable — continue browser diagnostics and clearly indicate that UniFi data is unavailable.

---

# 14. Administration interface

Provide a separate administrator interface, for example:

```text
/admin
```

The admin interface should show the combined diagnostic result.

Example:

```text
Diagnostic Session
────────────────────────────────

Client
  IP:        192.168.20.47
  MAC:       xx:xx:xx:xx:xx:xx
  Hostname:  iPhone-Rod

Wi-Fi
  SSID:      Home
  AP:        Living-Room
  Band:      5 GHz
  Channel:   44
  RSSI:      -68 dBm
  TX rate:   866 Mbps
  RX rate:   780 Mbps
  Retries:   7%

Performance
  LAN latency:    6 ms
  LAN jitter:     2 ms
  LAN loss:       0%
  Download:       310 Mbps
  Upload:         95 Mbps

Internet
  Latency:        29 ms
  Download:       285 Mbps
  Upload:         91 Mbps

Assessment
  ...
```

Administration authentication can initially be delegated to an existing reverse proxy rather than implementing a complete authentication system in the application.

---

# 15. Docker image and development workflow

The project should include:

```text
Dockerfile
docker-compose.yml
.dockerignore
.env.example
Makefile
```

The Dockerfile should use a multi-stage build:

```text
Frontend build (Node.js)
      │
      ▼
Go build
      │
      ▼
Minimal runtime image
```

The final runtime image should contain only what is required to run the application.

The build should be reproducible:

* `go.sum` and frontend lockfile (e.g., `package-lock.json`) must be committed.
* The final runtime container must not contain Node.js, Go, or development tooling.

The Dockerfile should include a `HEALTHCHECK` instruction pointing at `/healthz`.

The project should support:

```text
make build
make test
make lint
make docker-build
make docker-run
```

where appropriate.

CI should build and test the Docker image.

---

# 16. Future possibilities

The architecture should leave room for:

* continuous monitoring
* walk-test mode
* roaming analysis
* AP handoff detection
* historical client performance
* comparison between APs
* correlation of RSSI with latency/retries
* detection of poor backhaul
* detection of channel congestion
* alerts
* optional native diagnostic agent for deeper OS-level Wi-Fi information
* optional LLM-generated explanations based only on the structured diagnostic evidence
* host-side macvlan shim if NAS-to-container communication becomes necessary

An LLM should not be responsible for generating or inventing measurements. Deterministic diagnostic rules should remain the source of the underlying findings.

---

# 17. Core design principle

The application deliberately separates **what the client experiences** from **what the Wi-Fi infrastructure knows**.

```text
                 CLIENT EXPERIENCE
                 ─────────────────
                 Browser
                    │
                    ├── latency
                    ├── jitter
                    ├── loss
                    ├── throughput
                    └── stability
                         │
                         ▼
              Diagnostic Container
              macvlan: 192.168.1.250
                         ▲
                         │
                    UniFi API
                         │
                         ▼
                 NETWORK KNOWLEDGE
                 ─────────────────
                 UniFi
                    │
                    ├── client
                    ├── MAC
                    ├── AP
                    ├── SSID
                    ├── radio
                    ├── RSSI
                    ├── channel
                    ├── rates
                    └── retries
```

Neither source alone is sufficient for comprehensive troubleshooting. The diagnostic server combines both perspectives into a single diagnostic session.

The initial application should remain **browser-only from the user's perspective** while keeping all privileged/network-infrastructure access on the server side.

The key deployment constraint is explicit: **the macvlan container must preserve the real client IP for correlation**. This is validated by the nginx test (section 2.5.3) before any application code is built.
