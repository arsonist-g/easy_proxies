# Easy Proxies API Documentation

## Base URL

```
http://localhost:9090
```

## Authentication

If a password is configured in `config.yaml`, all API endpoints (except `/api/auth`) require authentication.

### Authentication Methods

1. **Cookie-based** (Recommended for Web UI)
   - After login, a session token is stored in a cookie named `session_token`
   - Cookie validity: 7 days

2. **Bearer Token** (Recommended for API clients)
   - Include the token in the `Authorization` header
   - Format: `Authorization: Bearer {token}`

---

## Endpoints

### Authentication

#### Login

POST `/api/auth`

Authenticates the user and returns a session token.

**Request Body:**

```json
{
  "password": "your_password"
}
```

**Response (200 OK):**

```json
{
  "message": "登录成功",
  "token": "hex_encoded_token_here"
}
```

**Response (200 OK - No Password):**

```json
{
  "message": "无需密码",
  "no_password": true
}
```

**Response (401 Unauthorized):**

```json
{
  "error": "密码错误"
}
```

---

### Settings Management

#### Get Settings

GET `/api/settings`

Retrieves current dynamic settings.

**Response (200 OK):**

```json
{
  "external_ip": "1.2.3.4",
  "probe_target": "www.apple.com:80",
  "skip_cert_verify": false
}
```

#### Update Settings

PUT `/api/settings`

Updates dynamic settings and persists to `config.yaml`.

**Request Body:**

```json
{
  "external_ip": "1.2.3.4",
  "probe_target": "www.apple.com:80",
  "skip_cert_verify": false
}
```

**Response (200 OK):**

```json
{
  "message": "设置已保存",
  "external_ip": "1.2.3.4",
  "probe_target": "www.apple.com:80",
  "skip_cert_verify": false,
  "need_reload": true
}
```

---

### Node Monitoring

#### List Nodes (Runtime Status)

GET `/api/nodes`

Returns runtime status of all available nodes (initial health check passed only).

**Response (200 OK):**

```json
{
  "nodes": [
    {
      "tag": "node-1",
      "name": "Taiwan Node",
      "uri": "vless://...",
      "mode": "multi-port",
      "listen_address": "0.0.0.0",
      "port": 24000,
      "username": "mpuser",
      "password": "mppass",
      "failure_count": 0,
      "blacklisted": false,
      "blacklisted_until": null,
      "active_connections": 3,
      "last_error": "",
      "last_failure": null,
      "last_success": "2024-01-01T12:00:00Z",
      "last_probe_latency": 150000000,
      "last_latency_ms": 150,
      "available": true,
      "initial_check_done": true
    }
  ]
}
```

#### Probe Single Node

POST `/api/nodes/{tag}/probe`

Manually triggers latency probe for a specific node.

**Path Parameters:**
- `tag` - Node tag (URL encoded if needed)

**Response (200 OK):**

```json
{
  "message": "探测成功",
  "latency_ms": 150
}
```

**Response (500 Internal Server Error):**

```json
{
  "error": "connection timeout"
}
```

#### Release Node

POST `/api/nodes/{tag}/release`

Removes a node from the blacklist immediately.

**Path Parameters:**
- `tag` - Node tag (URL encoded if needed)

**Response (200 OK):**

```json
{
  "message": "已解除拉黑"
}
```

**Response (500 Internal Server Error):**

```json
{
  "error": "节点不存在"
}
```

#### Probe All Nodes (SSE)

POST `/api/nodes/probe-all`

Triggers latency probe for all nodes concurrently. Returns results via Server-Sent Events (SSE).

**Event Types:**

1. **start** - Probe started
   ```json
   {
     "type": "start",
     "total": 10
   }
   ```

2. **progress** - Single node completed
   ```json
   {
     "type": "progress",
     "tag": "node-1",
     "name": "Taiwan Node",
     "latency": 150,
     "error": "",
     "current": 3,
     "total": 10,
     "progress": 30.0
   }
   ```

3. **complete** - All probes completed
   ```json
   {
     "type": "complete",
     "total": 10,
     "success": 8,
     "failed": 2
   }
   ```

**Example using curl:**

```bash
curl -N http://localhost:9090/api/nodes/probe-all \
  -H "Authorization: Bearer your_token"
```

---

### Node Configuration Management

#### List Configured Nodes

GET `/api/nodes/config`

Returns all configured nodes from `config.yaml`.

**Response (200 OK):**

```json
{
  "nodes": [
    {
      "name": "Taiwan Node",
      "uri": "vless://uuid@server:443?...#Taiwan Node",
      "port": 24000,
      "username": "",
      "password": ""
    }
  ]
}
```

#### Add Node

POST `/api/nodes/config`

Adds a new node to configuration. **Requires reload to take effect.**

**Request Body:**

```json
{
  "name": "New Node",
  "uri": "vless://uuid@server:443?...#New Node",
  "port": 24001,
  "username": "",
  "password": ""
}
```

**Response (200 OK):**

```json
{
  "node": {
    "name": "New Node",
    "uri": "vless://uuid@server:443?...#New Node",
    "port": 24001,
    "username": "",
    "password": ""
  },
  "message": "节点已添加，请点击重载使配置生效"
}
```

**Response (400 Bad Request):**

```json
{
  "error": "节点名称或端口已存在"
}
```

#### Update Node

PUT `/api/nodes/config/{name}`

Updates an existing node. **Requires reload to take effect.**

**Path Parameters:**
- `name` - Node name (URL encoded)

**Request Body:**

```json
{
  "name": "Updated Node",
  "uri": "vless://uuid@server:443?...#Updated Node",
  "port": 24001,
  "username": "",
  "password": ""
}
```

**Response (200 OK):**

```json
{
  "node": {
    "name": "Updated Node",
    "uri": "vless://uuid@server:443?...#Updated Node",
    "port": 24001,
    "username": "",
    "password": ""
  },
  "message": "节点已更新，请点击重载使配置生效"
}
```

**Response (404 Not Found):**

```json
{
  "error": "节点不存在"
}
```

#### Delete Node

DELETE `/api/nodes/config/{name}`

Removes a node from configuration. **Requires reload to take effect.**

**Path Parameters:**
- `name` - Node name (URL encoded)

**Response (200 OK):**

```json
{
  "message": "节点已删除，请点击重载使配置生效"
}
```

**Response (404 Not Found):**

```json
{
  "error": "节点不存在"
}
```

---

### Available Node Selection

**Note:** These endpoints are only available in `multi-port` or `hybrid` mode.

#### Get Available Node (Single)

GET `/api/nodes/get_available_node`

Returns a single available node based on selection strategy.

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `regular` | string | `""` | Regex pattern to filter node names |
| `strategy` | string | `"sequential"` | Selection strategy: `sequential`, `random`, `balance`, `weighted` |
| `latency_weight` | string | `"0.5"` | Latency weight (for weighted strategy) |
| `success_rate_weight` | string | `"0.5"` | Success rate weight (for weighted strategy) |
| `weight_mode` | string | `"add"` | Weight calculation mode: `add` (addition), `multiply` (multiplication) |
| `weighted_random` | boolean | `false` | Use weighted random selection (if true) or select best node (if false) |

**Strategies:**

- **sequential**: Round-robin selection based on time
- **random**: Random selection
- **balance**: Select node with least active connections
- **weighted**: Weighted selection based on latency and success rate

**Response (200 OK):**

```json
{
  "tag": "node-1",
  "name": "Taiwan Node",
  "proxy_url": "http://user:pass@1.2.3.4:24000",
  "latency_ms": 150
}
```

**Response (400 Bad Request):**

```json
{
  "error": "此 API 仅在 multi-port 或 hybrid 模式下可用"
}
```

**Examples:**

```bash
# Get best node (default sequential)
curl "http://localhost:9090/api/nodes/get_available_node"

# Get random node filtered by regex
curl "http://localhost:9090/api/nodes/get_available_node?strategy=random&regular=.*Taiwan.*"

# Get node with lowest latency (weighted strategy)
curl "http://localhost:9090/api/nodes/get_available_node?strategy=weighted&latency_weight=1&success_rate_weight=0"

# Get node with highest success rate (weighted strategy)
curl "http://localhost:9090/api/nodes/get_available_node?strategy=weighted&latency_weight=0&success_rate_weight=1"
```

#### Get Available Nodes (Multiple)

GET `/api/nodes/get_available_nodes`

Returns multiple available nodes based on selection strategy.

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `count` | integer | `1` | Number of nodes to return (1-100) |
| `regular` | string | `""` | Regex pattern to filter node names |
| `strategy` | string | `"sequential"` | Selection strategy (same as single endpoint) |
| `latency_weight` | string | `"0.5"` | Latency weight (for weighted strategy) |
| `success_rate_weight` | string | `"0.5"` | Success rate weight (for weighted strategy) |
| `weight_mode` | string | `"add"` | Weight calculation mode |
| `weighted_random` | boolean | `false` | Weighted random selection |

**Response (200 OK):**

```json
{
  "nodes": [
    {
      "tag": "node-1",
      "name": "Taiwan Node",
      "proxy_url": "http://user:pass@1.2.3.4:24000",
      "latency_ms": 150
    },
    {
      "tag": "node-2",
      "name": "Hong Kong Node",
      "proxy_url": "http://user:pass@1.2.3.4:24001",
      "latency_ms": 120
    }
  ]
}
```

**Examples:**

```bash
# Get 3 best nodes
curl "http://localhost:9090/api/nodes/get_available_nodes?count=3"

# Get 5 random Taiwan nodes
curl "http://localhost:9090/api/nodes/get_available_nodes?count=5&strategy=random&regular=.*Taiwan.*"
```

---

### Export

#### Export Proxy URIs

GET `/api/export`

Exports all available nodes as HTTP proxy URIs (one per line).

**Response (200 OK, text/plain):**

```
http://user:pass@1.2.3.4:24000
http://user:pass@1.2.3.4:24001
http://user:pass@1.2.3.4:24002
```

**Response Headers:**
- `Content-Type: text/plain; charset=utf-8`
- `Content-Disposition: attachment; filename=proxy_pool.txt`

---

### Subscription Management

#### Get Subscription Status

GET `/api/subscription/status`

Returns current subscription refresh status.

**Response (200 OK):**

```json
{
  "enabled": true,
  "last_refresh": "2024-01-01T12:00:00Z",
  "next_refresh": "2024-01-01T13:00:00Z",
  "node_count": 10,
  "last_error": "",
  "refresh_count": 5,
  "is_refreshing": false
}
```

**Response (200 OK - Disabled):**

```json
{
  "enabled": false,
  "message": "订阅刷新未启用"
}
```

#### Refresh Subscription

POST `/api/subscription/refresh`

Triggers immediate subscription refresh. **⚠️ This restarts the sing-box core and interrupts all connections.**

**Response (200 OK):**

```json
{
  "message": "刷新成功",
  "node_count": 10
}
```

**Response (500 Internal Server Error):**

```json
{
  "error": "获取订阅失败: timeout"
}
```

---

### Virtual Pool Management

#### Get Virtual Pools Status

GET `/api/virtual_pools/status`

Returns status information for all virtual pools.

**Response (200 OK):**

```json
{
  "pools": [
    {
      "name": "US_Pool",
      "regular": ".*US.*",
      "address": "0.0.0.0",
      "port": 3001,
      "username": "ususer",
      "password": "uspass",
      "strategy": "sequential",
      "max_latency_ms": 0,
      "node_count": 5,
      "running": true
    },
    {
      "name": "Fast_Pool",
      "regular": ".*",
      "address": "0.0.0.0",
      "port": 3002,
      "username": "",
      "password": "",
      "strategy": "balance",
      "max_latency_ms": 200,
      "node_count": 8,
      "running": true
    }
  ]
}
```

**Field Descriptions:**

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Virtual pool name |
| `regular` | string | Regex pattern to match node names |
| `address` | string | Listen address |
| `port` | uint16 | Listen port |
| `username` | string | Proxy authentication username (optional) |
| `password` | string | Proxy authentication password (optional) |
| `strategy` | string | Load balancing strategy (sequential/random/balance) |
| `max_latency_ms` | int | Maximum latency threshold (milliseconds), 0 means unlimited |
| `node_count` | int | Number of matched nodes |
| `running` | bool | Whether the virtual pool is running |

#### Get Virtual Pool Nodes

GET `/api/virtual_pools/{name}/nodes`

Returns the list of nodes matched by the specified virtual pool.

**Path Parameters:**
- `name` - Virtual pool name (URL encoded if needed)

**Response (200 OK):**

```json
{
  "pool_name": "US_Pool",
  "nodes": [
    {
      "tag": "node-1",
      "name": "US Node 1",
      "uri": "vless://...",
      "mode": "multi-port",
      "listen_address": "0.0.0.0",
      "port": 24000,
      "username": "mpuser",
      "password": "mppass",
      "failure_count": 0,
      "blacklisted": false,
      "active_connections": 2,
      "last_latency_ms": 120,
      "available": true
    }
  ]
}
```

**Response (404 Not Found):**

```json
{
  "error": "Virtual pool not found"
}
```

---

### Configuration Reload

#### Reload Configuration

POST `/api/reload`

Triggers configuration reload. **⚠️ This restarts the sing-box core and interrupts all connections.**

**Response (200 OK):**

```json
{
  "message": "重载成功，现有连接已被中断"
}
```

**Response (500 Internal Server Error):**

```json
{
  "error": "配置错误: ..."
}
```

---

## Error Codes

| HTTP Status | Description |
|-------------|-------------|
| 200 | Success |
| 400 | Bad Request (invalid parameters) |
| 401 | Unauthorized (missing or invalid authentication) |
| 404 | Not Found (resource doesn't exist) |
| 405 | Method Not Allowed |
| 500 | Internal Server Error |
| 503 | Service Unavailable (feature not enabled) |

---

## Common Error Responses

```json
{
  "error": "Error message here"
}
```

---

## Examples

### Complete Workflow: Add Node, Probe, and Export

```bash
# 1. Login
TOKEN=$(curl -s -X POST http://localhost:9090/api/auth \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' \
  | jq -r '.token')

# 2. Add a new node
curl -X POST http://localhost:9090/api/nodes/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "uri": "vless://uuid@server:443?security=reality&sni=example.com#New Node"
  }'

# 3. Reload configuration
curl -X POST http://localhost:9090/api/reload \
  -H "Authorization: Bearer $TOKEN"

# 4. Probe the new node
curl -X POST http://localhost:9090/api/nodes/New%20Node/probe \
  -H "Authorization: Bearer $TOKEN"

# 5. Export available nodes
curl http://localhost:9090/api/export \
  -H "Authorization: Bearer $TOKEN"
```

### Weighted Node Selection

```bash
# Select 3 nodes with lowest latency (latency weight = 1, success rate weight = 0)
curl "http://localhost:9090/api/nodes/get_available_nodes?count=3&strategy=weighted&latency_weight=1&success_rate_weight=0" \
  -H "Authorization: Bearer $TOKEN"

# Select 3 nodes with highest success rate (latency weight = 0, success rate weight = 1)
curl "http://localhost:9090/api/nodes/get_available_nodes?count=3&strategy=weighted&latency_weight=0&success_rate_weight=1" \
  -H "Authorization: Bearer $TOKEN"

# Weighted random selection (balanced between latency and success rate)
curl "http://localhost:9090/api/nodes/get_available_node?strategy=weighted&weighted_random=true" \
  -H "Authorization: Bearer $TOKEN"
```
