# Easy Proxies

[![Docker Build](https://github.com/arsonist-g/easy_proxies/actions/workflows/docker-build.yaml/badge.svg)](https://github.com/arsonist-g/easy_proxies/actions/workflows/docker-build.yaml)
[![Docker Image](https://img.shields.io/badge/docker-ghcr.io-2496ED?logo=docker&logoColor=white)](https://github.com/arsonist-g/easy_proxies/pkgs/container/easy_proxies)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![sing-box](https://img.shields.io/badge/sing--box-1.12-FF4F8B)](https://github.com/SagerNet/sing-box)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

English | [简体中文](README_ZH.md)

A proxy node pool management tool based on [sing-box](https://github.com/SagerNet/sing-box), supporting multiple protocols, automatic failover, load balancing, with a built-in web dashboard, credential management, and GeoIP probing.

## Documentation

- [API Documentation](docs/api.md) / [中文 API 文档](docs/api_zh.md) — complete REST API endpoints, request/response fields, and error codes

## Features

- **Multi-Protocol**: VMess, VLESS, Hysteria2, Shadowsocks, Trojan
- **Multiple Transports**: TCP, WebSocket, HTTP/2, gRPC, HTTPUpgrade
- **Node URI Validation**: Validates protocol support and required fields on add/edit (pure parsing, no connectivity check, does not affect the running instance)
- **Subscription Support**: Auto-fetch from Base64, Clash YAML, plain text, sing-box formats
- **Auto-Refresh**: Periodic subscription refresh with manual trigger (⚠️ restarts the sing-box core and interrupts connections)
- **Pool Mode**: Automatic failover and load balancing (sequential / random / least-connections)
- **Multi-Port Mode**: Each node on an independent port
- **Hybrid Mode**: Pool + Multi-Port with shared state
- **Virtual Pools**: Filter nodes by regex into independent load-balanced entries; **CRUD via WebUI/API takes effect immediately, no restart needed**
- **Unified Probing**: Probing goes through Cloudflare `cdn-cgi/trace` to obtain availability + latency + exit IP + country + ASN in one request
- **GeoIP / ASN**: Optional MaxMind GeoLite2-ASN integration with auto-download and periodic updates
- **Credential Management**: API Keys (for third-party auth) and Subscribe Tokens (proxy-list subscription links), stored as AES-256-GCM ciphertext, decrypted server-side on copy
- **Three-Layer Auth**: Session (WebUI) / Bearer Token / API Key, with a `manage_credentials` scope guarding credential operations
- **Web Dashboard**: Real-time node status, sortable column headers, pagination, latency probes, one-click export/copy of proxy links
- **Localized Flag Fonts**: Flag emoji fonts inlined for correct rendering on Windows; country full names shown in node table, charts, and filters
- **Health Check**: Auto-detect node availability on startup, periodic rechecks every 5 min
- **Smart Filtering**: Unavailable nodes auto-hidden from dashboard and export
- **Node Management**: CRUD via Web UI or API
- **Port Preservation**: Existing nodes keep their ports across config reloads

## Quick Start

### 1. Configuration

```bash
cp config.example.yaml config.yaml
cp nodes.example nodes.txt   # optional: use a node file instead of subscriptions
```

Edit `config.yaml` for settings; `nodes.txt` holds one proxy URI per line.

### 2. Run

**Docker (Recommended):**

```bash
./start.sh
# or
docker compose up -d
```

**Local Build:**

```bash
go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor" -o easy-proxies ./cmd/easy_proxies
./easy-proxies --config config.yaml
```

> Full features require all build tags (see [Building](#building)).

## Configuration

See [`config.example.yaml`](config.example.yaml) for a complete example. Key options below.

### Basic Config

```yaml
mode: pool                    # pool / multi-port / hybrid
log_level: info               # debug / info / warn / error
singbox_log_level: warn       # sing-box core log level (default warn)
external_ip: ""               # public IP used to replace 0.0.0.0 in exported URIs
skip_cert_verify: false       # skip node TLS cert verification globally

# Subscriptions (optional; format "name:url", name optional)
subscriptions:
  - "MySub:https://example.com/subscribe"
  - "https://example.com/subscribe/clash"

# Management interface
management:
  enabled: true
  listen: 0.0.0.0:9090        # Web dashboard address (port is configurable)
  probe_target: www.apple.com:80
  password: ""                # optional WebUI password
  path_pwd: ""                # optional path password (visit /path_pwd to reach the dashboard)

# Multi-port / Hybrid entry
multi_port:
  address: 0.0.0.0
  base_port: 24000            # starting port, increments per node
  username: mpuser
  password: mppass

# Pool config (selection strategy + pool/hybrid proxy entry)
pool:
  address: 0.0.0.0
  port: 2323
  username: user
  password: pass
  mode: sequential            # sequential / random / balance
  failure_threshold: 3
  blacklist_duration: 24h
```

### GeoIP / ASN (Optional)

When configured, the ASN and organization name are backfilled from the node's exit IP. Probing is unified through Cloudflare `cdn-cgi/trace` (availability + latency + exit IP + country + ASN in one request).

```yaml
geoip:
  asn_database: ""            # local GeoLite2-ASN.mmdb path; empty = disable ASN lookup
  account_id: ""              # MaxMind Account ID (required for auto-update)
  license_key: ""             # MaxMind License Key
  edition_id: "GeoLite2-ASN"
  update_interval: 24h        # auto-update check interval
```

Setting `license_key` enables auto-update: downloads on first start if missing, otherwise checks periodically via `If-Modified-Since` (MaxMind publishes on Tuesdays).

### Operating Modes

#### Pool Mode

Single entry point for all nodes with auto-selection.

**Use:** `http://user:pass@localhost:2323`

#### Multi-Port Mode

Each node on its own port for precise control. Ports increment from `base_port` (default 24000).

#### Hybrid Mode

Both Pool and Multi-Port enabled, sharing node state.

- Pool entry: `http://user:pass@0.0.0.0:2323`
- Multi-port entries: `http://mpuser:mppass@0.0.0.0:24000+`

### Virtual Pools

Virtual pools filter nodes by regex to create independent load-balanced entries, useful for grouping by region or type.

**Two ways to configure, both manageable at runtime:**

1. **Static YAML** (loaded at startup):

```yaml
virtual_pool:
  base_port: 30000            # starting port for auto-allocation (required)

virtual_pools:
  - name: "US_Pool"
    regular: ".*US.*"          # regex to match node names
    address: 0.0.0.0
    port: 3001                # leave empty to auto-allocate from base_port
    username: ususer          # optional auth
    password: uspass
    strategy: sequential      # sequential / random / balance

  - name: "Fast_Pool"
    regular: ".*"
    address: 0.0.0.0
    port: 3002
    strategy: balance
    max_latency_ms: 200       # only select nodes with latency < 200ms
```

2. **WebUI / API** (recommended):

Manage virtual pools from the dashboard's "Virtual Pools" page or via `POST /api/v1/virtual-pools`.

> **Virtual pools are an independent proxy layer in front of sing-box.** CRUD (create/update/delete) **takes effect immediately** — by the time the API returns success, the corresponding TCP listener has already been opened/closed. **No process or container restart, and no reload endpoint is needed.** Changes are also persisted to the `virtual_pools` section of `config.yaml` (so they remain visible for manual editing).

**Usage:** `http://ususer:uspass@localhost:3001`

**Load Balancing Strategies:**
- `sequential`: Round-robin (default)
- `random`: Random selection
- `balance`: Least connections first

### Node Configuration

**Method 1: Subscription Links**

```yaml
subscriptions:
  - "https://example.com/subscribe/v2ray"
  - "https://example.com/subscribe/clash"
```

**Method 2: Node File**

```yaml
nodes_file: nodes.txt
```

`nodes.txt` format (one URI per line):

```
vless://uuid@server:443?security=reality&sni=example.com#NodeName
hysteria2://password@server:443?sni=example.com#HY2Node
ss://base64@server:8388#SSNode
```

**Method 3: Inline Nodes**

```yaml
nodes:
  - uri: "vless://uuid@server:443#Node1"
  - name: custom-name
    uri: "ss://base64@server:8388"
    port: 24001  # optional, manual port
```

## Supported Protocols

| Protocol | URI Format | Features |
|----------|------------|----------|
| VMess | `vmess://` | WebSocket, HTTP/2, gRPC, TLS |
| VLESS | `vless://` | Reality, XTLS-Vision, multiple transports |
| Hysteria2 | `hysteria2://` | Bandwidth control, obfuscation |
| Shadowsocks | `ss://` | Multiple ciphers |
| Trojan | `trojan://` | TLS, multiple transports |

### Protocol Details

**VMess**

```
vmess://uuid@server:port?encryption=auto&security=tls&sni=example.com&type=ws&host=example.com&path=/path#Name
```

Params: `net/type` (tcp, ws, h2, grpc), `tls/security` (tls or empty), `scy/encryption` (auto, aes-128-gcm, chacha20-poly1305)

**VLESS**

```
vless://uuid@server:port?encryption=none&security=reality&sni=example.com&fp=chrome&pbk=xxx&sid=xxx&type=tcp&flow=xtls-rprx-vision#Name
```

Params: `security` (none, tls, reality), `type` (tcp, ws, http, grpc, httpupgrade), `flow` (xtls-rprx-vision, TCP only), `fp` (chrome, firefox, safari)

**Hysteria2**

```
hysteria2://password@server:port?sni=example.com&obfs=salamander&obfs-password=xxx#Name
```

Params: `upMbps`/`downMbps` (bandwidth limits), `obfs` (obfuscation type)

> When adding a node, the protocol is checked against the supported set above, and required fields (e.g. VLESS uuid) are validated. An unsupported scheme or missing field returns `422 validation_error` with a specific reason (e.g. `unsupported scheme "ssr"`, `vless uri missing uuid`), **is not saved, and does not affect the running proxy instance**.

## Web Dashboard

Open the dashboard (default `http://localhost:9090`, the port from `management.listen`):

- Node status (Healthy / Warning / Error / Blacklisted), latency, availability, country, ASN
- Sortable column headers (node/country/protocol/port/latency/availability), pagination
- Manual single-node / all-node probing, release blacklisted nodes
- **One-click export** of available node URIs, **one-click copy** of node/virtual-pool proxy links
- Country flag + full name, filter by country/protocol/status/name regex
- Virtual pool management (CRUD, real-time)
- Node CRUD (with protocol validation)
- Subscription management and refresh
- Credential management (API Keys / Subscribe Tokens)
- Settings: `external_ip`, `probe_target`, alert toggle, etc.

### Password Protection

Set in `config.yaml`:

```yaml
management:
  password: "your_secure_password"
  path_pwd: "secret"          # optional: visit /secret to reach the dashboard
```

### Credential Management

The dashboard's "Credentials" page manages two credential types, both stored as **AES-256-GCM ciphertext** in the database. The encryption key lives in `config.yaml` under `credential_key` (auto-generated on first start), kept separate from the database:

- **API Key**: For third-party programs to call management APIs via the `X-API-Key` header; can be assigned permission scopes.
- **Subscribe Token**: Generates an independent proxy-list subscription link `http://<host>/sub/<token>` for third-party platforms to pull node lists, with optional country-based filtering.

The list returns only the Key/Token **prefix**. Clicking "Copy" calls `GET /api/v1/api-keys/{id}/plain` (or `subscribe-tokens/{id}/plain`); the backend decrypts the ciphertext and returns the plaintext, which the frontend copies to the clipboard. Switching browsers/devices only requires logging in — no local cache involved.

## API

All endpoints live under `/api/v1`. Except for `POST /api/v1/auth/login`, all require authentication.

### Authentication

1. **Session Cookie** (WebUI): sets the `ep_session` cookie after login.
2. **Bearer Token**: `Authorization: Bearer <token>`.
3. **API Key**: `X-API-Key: <key>` (limited by its scope).

### Quick Examples

```bash
# Login to get a token
TOKEN=$(curl -s -X POST http://localhost:9090/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' | jq -r '.token')

# List nodes (sort/pagination/filter supported)
curl "http://localhost:9090/api/v1/nodes?sort=-latency&page=1&page_size=20" \
  -H "Authorization: Bearer $TOKEN"

# Add a node (with protocol validation)
curl -X POST http://localhost:9090/api/v1/nodes \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"New","uri":"vless://uuid@server:443#New"}'

# Export proxy URIs
curl http://localhost:9090/api/v1/export -H "Authorization: Bearer $TOKEN"
```

For the complete endpoint list, request/response fields, pagination, and error codes, see the **[API Documentation](docs/api.md)**.

## Health Check

- **Unified Probing**: obtains availability, latency, exit IP, country, and ASN in one request via Cloudflare `cdn-cgi/trace`
- **Initial Check**: Tests all nodes on startup
- **Periodic Check**: Every 5 minutes
- **Smart Filtering**: Hides unavailable nodes from dashboard and export
- **Configurable Target**: `management.probe_target` (default `www.apple.com:80`)

## Subscription Auto-Refresh

```yaml
subscription_refresh:
  enabled: false              # disabled by default
  interval: 1h                # refresh interval
  timeout: 30s                # fetch timeout
  health_check_timeout: 60s   # new-node health check timeout
  drain_timeout: 30s          # old-instance drain timeout
  min_available_nodes: 1      # minimum available nodes; do not switch below this
```

> ⚠️ **Refresh restarts the sing-box core and interrupts all connections**: refresh closes the old instance and rebuilds/reloads new node config, dropping all active connections. Set a longer interval (e.g. `1h` or more), avoid manual refresh during peak usage, or disable (`enabled: false`) if stability is critical.

> The refresher is always initialized, so manual refresh (all/single) and "refresh-on-add" always work, **unaffected by `subscription_refresh.enabled`**. That flag only controls **periodic** auto-refresh — when off, the timer doesn't run, but manually triggered and add-subscription-triggered refreshes still work. Adding a subscription auto-triggers a background refresh (restarts the core, brief connection interruption); nodes appear within seconds.

## Ports

| Port | Purpose |
|------|---------|
| 2323 | Pool / Hybrid entry |
| 9090 | Web dashboard (i.e. `management.listen`, configurable) |
| 24000+ | Multi-port / Hybrid nodes (incrementing from `base_port`) |
| 30000+ | Virtual pool entries (auto-allocated from `virtual_pool.base_port`) |

## Docker Deployment

### Host Network (Recommended)

```yaml
services:
  easy-proxies:
    image: ghcr.io/arsonist-g/easy_proxies:latest
    container_name: easy-proxies
    restart: unless-stopped
    network_mode: host
    volumes:
      - ./config.yaml:/etc/easy-proxies/config.yaml
      - ./nodes.txt:/etc/easy-proxies/nodes.txt
```

> Config files need write permission for dashboard settings to persist. If you hit permission issues: `chmod 666 config.yaml nodes.txt`

### Port Mapping

```yaml
services:
  easy-proxies:
    image: ghcr.io/arsonist-g/easy_proxies:latest
    container_name: easy-proxies
    restart: unless-stopped
    ports:
      - "2323:2323"
      - "9090:9090"
      - "24000-24200:24000-24200"
    volumes:
      - ./config.yaml:/etc/easy-proxies/config.yaml
      - ./nodes.txt:/etc/easy-proxies/nodes.txt
```

## Building

```bash
# Full features (recommended; all build tags required)
go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor" -o easy-proxies.exe ./cmd/easy_proxies

# Basic build (without QUIC/WireGuard/gVisor optional features)
go build -o easy-proxies ./cmd/easy_proxies
```

## License

MIT License
