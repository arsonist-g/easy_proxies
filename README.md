# Easy Proxies

English | [简体中文](README_ZH.md)

A proxy node pool management tool based on [sing-box](https://github.com/SagerNet/sing-box), supporting multiple protocols, automatic failover, and load balancing.

## Documentation

- [API Documentation](docs/api.md) - Complete REST API reference
- [中文 API 文档](docs/api_zh.md) - API 文档（中文）

## Features

- **Multi-Protocol**: VMess, VLESS, Hysteria2, Shadowsocks, Trojan
- **Multiple Transports**: TCP, WebSocket, HTTP/2, gRPC, HTTPUpgrade
- **Subscription Support**: Auto-fetch from Base64, Clash YAML, etc.
- **Auto-Refresh**: Periodic subscription refresh with manual trigger (⚠️ interrupts connections)
- **Pool Mode**: Automatic failover and load balancing
- **Multi-Port Mode**: Each node on independent port
- **Hybrid Mode**: Pool + Multi-Port with shared state
- **Virtual Pools**: Filter nodes by regex, create multiple independent load-balanced entries
- **Web Dashboard**: Real-time monitoring, latency probes, one-click export
- **Health Check**: Auto-detect node availability, periodic checks every 5 min
- **Smart Filtering**: Auto-hide unavailable nodes, sort by latency
- **Node Management**: CRUD operations via Web UI or API
- **Port Preservation**: Keep assigned ports when reloading

## Quick Start

### 1. Configuration

```bash
cp config.example.yaml config.yaml
cp nodes.example nodes.txt
```

Edit `config.yaml` for settings, `nodes.txt` for proxy nodes.

### 2. Run

**Docker (Recommended):**

```bash
./start.sh
```

Or:

```bash
docker compose up -d
```

**Local Build:**

```bash
go build -tags "with_utls with_quic with_grpc" -o easy-proxies ./cmd/easy_proxies
./easy-proxies --config config.yaml
```

## Configuration

### Basic Config

```yaml
mode: pool                    # pool, multi-port, or hybrid
log_level: info

# Subscriptions (optional)
subscriptions:
  - "https://example.com/subscribe"

# Management Interface
management:
  enabled: true
  listen: 0.0.0.0:9090        # Web dashboard
  probe_target: www.apple.com:80
  password: ""                # Optional password

# Entry Points
listener:
  address: 0.0.0.0
  port: 2323
  username: user
  password: pass

multi_port:
  address: 0.0.0.0
  base_port: 24000
  username: mpuser
  password: mppass

pool:
  mode: sequential            # sequential, random, or balance
  failure_threshold: 3
  blacklist_duration: 24h
```

### Operating Modes

#### Pool Mode

Single entry point for all nodes with auto-selection.

**Use:** `http://user:pass@localhost:2323`

#### Multi-Port Mode

Each node on its own port for precise control.

Ports auto-increment from `base_port` (default 24000).

#### Hybrid Mode

Both Pool and Multi-Port enabled, sharing node state.

- Pool entry: `http://user:pass@0.0.0.0:2323`
- Multi-port entries: `http://mpuser:mppass@0.0.0.0:24000+`

### Virtual Pools

Virtual pools allow filtering nodes by regex to create multiple independent load-balanced entries. Useful for grouping nodes by region, type, etc.

```yaml
virtual_pools:
  # US nodes pool
  - name: "US_Pool"
    regular: ".*US.*"             # Regex to match node names
    address: 0.0.0.0
    port: 3001
    username: ususer              # Optional auth
    password: uspass
    strategy: sequential          # sequential/random/balance

  # Low latency pool
  - name: "Fast_Pool"
    regular: ".*"                 # Match all nodes
    address: 0.0.0.0
    port: 3002
    strategy: balance
    max_latency_ms: 200           # Only select nodes with latency < 200ms
```

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

`nodes.txt` format (one per line):

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
    port: 24001
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

Parameters:
- `net/type`: tcp, ws, h2, grpc
- `tls/security`: tls or empty
- `scy/encryption`: auto, aes-128-gcm, chacha20-poly1305

**VLESS**

```
vless://uuid@server:port?encryption=none&security=reality&sni=example.com&fp=chrome&pbk=xxx&sid=xxx&type=tcp&flow=xtls-rprx-vision#Name
```

Parameters:
- `security`: none, tls, reality
- `type`: tcp, ws, http, grpc, httpupgrade
- `flow`: xtls-rprx-vision (TCP only)
- `fp`: fingerprint (chrome, firefox, safari)

**Hysteria2**

```
hysteria2://password@server:port?sni=example.com&obfs=salamander&obfs-password=xxx#Name
```

Parameters:
- `upMbps` / `downMbps`: Bandwidth limits
- `obfs`: Obfuscation type

## Web Dashboard

Access `http://localhost:9090` for:

- Real-time node status (Healthy/Warning/Error/Blacklisted)
- Latency and connection stats
- Manual latency probing
- Release blacklisted nodes
- **One-click export** - Export available nodes as proxy URIs
- **One-click copy proxy link** - Each node and virtual pool card has a copy button to copy the complete proxy link to clipboard
- **Country classification and filtering** - Nodes are automatically categorized by country emoji, with keyword filtering support
- **Virtual pool management** - View and manage virtual pool configurations, each filtering nodes by regex
- **Settings** - Modify `external_ip` and `probe_target`
- **Node Management** - Add, edit, delete nodes via UI
- **Subscription status** - View and trigger refresh

### Authentication

Set password in `config.yaml`:

```yaml
management:
  password: "your_secure_password"
```

Login via Web UI or use Bearer token in API requests.

### Settings Management

Click ⚙️ gear icon to modify:

| Setting | Description |
|---------|-------------|
| External IP | IP for exported proxy URIs (replaces `0.0.0.0`) |
| Probe Target | Health check target (format: `host:port`) |

Changes saved immediately to `config.yaml`.

## API Usage

Full API documentation: [docs/api.md](docs/api.md)

### Quick Examples

```bash
# Get available nodes
curl http://localhost:9090/api/nodes

# Export proxy URIs
curl http://localhost:9090/api/export

# Login with password
TOKEN=$(curl -s -X POST http://localhost:9090/api/auth \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' | jq -r '.token')

# Add node
curl -X POST http://localhost:9090/api/nodes/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"uri": "vless://uuid@server:443#New Node"}'

# Reload config
curl -X POST http://localhost:9090/api/reload \
  -H "Authorization: Bearer $TOKEN"
```

### Key Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/nodes` | List runtime nodes |
| GET | `/api/export` | Export proxy URIs |
| GET | `/api/nodes/config` | List configured nodes |
| POST | `/api/nodes/config` | Add node |
| PUT | `/api/nodes/config/:name` | Update node |
| DELETE | `/api/nodes/config/:name` | Delete node |
| POST | `/api/reload` | Reload config |
| GET | `/api/settings` | Get settings |
| PUT | `/api/settings` | Update settings |
| GET | `/api/subscription/status` | Subscription status |
| POST | `/api/subscription/refresh` | Refresh subscription |
| GET | `/api/virtual_pools/status` | Virtual pools status |
| GET | `/api/virtual_pools/:name/nodes` | Virtual pool node list |

## Health Check

- **Initial Check**: Tests all nodes on startup
- **Periodic Check**: Every 5 minutes
- **Smart Filtering**: Hides unavailable nodes from UI and export
- **Configurable Target**: `management.probe_target` (default `www.apple.com:80`)

## Subscription Auto-Refresh

```yaml
subscription_refresh:
  enabled: true
  interval: 1h
  timeout: 30s
  health_check_timeout: 60s
  drain_timeout: 30s
  min_available_nodes: 1
```

> ⚠️ **Refresh restarts sing-box core and interrupts all connections**
>
> - Set longer intervals (e.g., `1h` or more)
> - Avoid manual refresh during peak usage
> - Disable if stability is critical (`enabled: false`)

## Ports

| Port | Purpose |
|------|---------|
| 2323 | Pool/Hybrid entry |
| 9090 | Web dashboard |
| 24000+ | Multi-port/Hybrid nodes |
| Custom | Virtual pool entries (configured in `virtual_pools`) |

## Docker Deployment

### Host Network (Recommended)

```yaml
services:
  easy-proxies:
    image: ghcr.io/jasonwong1991/easy_proxies:latest
    container_name: easy-proxies
    restart: unless-stopped
    network_mode: host
    volumes:
      - ./config.yaml:/etc/easy-proxies/config.yaml
      - ./nodes.txt:/etc/easy-proxies/nodes.txt
```

**Note**: Config files need write permission: `chmod 666 config.yaml nodes.txt`

### Port Mapping

```yaml
services:
  easy-proxies:
    image: ghcr.io/jasonwong1991/easy_proxies:latest
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
# Basic build
go build -o easy-proxies ./cmd/easy_proxies

# Full features (recommended)
go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor" -o easy-proxies.exe ./cmd/easy_proxies
```

## License

MIT License
