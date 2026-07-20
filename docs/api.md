# Easy Proxies API Documentation

All management endpoints live under the `/api/v1` prefix. The subscription proxy list `GET /sub/{token}` is a standalone path, not under `/api/v1`.

## Base URL

```
http://<host>:<management.listen port>
```

The port is `management.listen` in `config.yaml` (default `9090`, configurable).

---

## Conventions

### Error Responses

All errors are JSON:

```json
{
  "code": "validation_error",
  "message": "specific reason",
  "details": {},
  "traceId": "request id"
}
```

| `code` | HTTP | Meaning |
|--------|------|---------|
| `invalid_request` | 400 | Malformed request (JSON parse failure, etc.) |
| `unauthorized` | 401 | Not authenticated / invalid credential |
| `forbidden` | 403 | Insufficient scope (API Key missing required scope) |
| `not_found` | 404 | Resource not found |
| `conflict` | 409 | Resource conflict (name/port in use) |
| `validation_error` | 422 | Business validation failure, `message` carries the reason |
| `internal_error` | 500 | Internal error; **endpoints whose dependency is not injected return 501 with this code** |

### Authentication

All endpoints except `POST /api/v1/auth/login` and `GET /sub/{token}` require auth. Any one of these passes:

1. **Session Cookie** (WebUI): sets the `ep_session` cookie after login (`HttpOnly`, `SameSite=Strict`, 7-day validity).
2. **Bearer Token**: `Authorization: Bearer <session-token>` (same value as the cookie).
3. **API Key**: `X-API-Key: <plaintext key>` (looked up via sha256; revoked/missing keys fail).

> **No-password mode**: when `management.password` is empty, all endpoints are accessible without auth.

#### Auth coverage matrix

| Endpoints | Auth method | Scope requirement |
|-----------|-------------|--------------------|
| `POST /api/v1/auth/login` | Public (no auth) | — |
| `GET /sub/{token}` | **Subscribe Token** (token as path param) | — |
| All other `/api/v1/*` (stats / nodes / subscriptions / virtual-pools / alerts / export / settings / …) | Session Cookie / Bearer Token / API Key (any one) | — |
| `/api-keys`, `/subscribe-tokens` list / create / plain / delete | Session Cookie / Bearer Token / API Key (any one) | **API Key must carry `manage_credentials`** (Session login & no-password mode are unrestricted) |

> Session login and no-password mode have full access; the "Scope requirement" column only constrains API Keys.

### Scopes

API Keys can be assigned scopes; Session login and no-password mode have full access.

| Scope | Meaning |
|-------|---------|
| `manage_nodes` | Node CRUD |
| `manage_subscriptions` | Subscription CRUD |
| `manage_pools` | Virtual pool CRUD |
| `manage_credentials` | Credential management (API Key / Subscribe Token) |
| `read_only` | Read only |

> Only the **credential** endpoints enforce `manage_credentials` at the route layer. Accessing credential endpoints with an API Key requires that Key to carry `manage_credentials`, otherwise 403.

### Pagination

List endpoints accept `page` (from 1, default 1) and `page_size` (default 20, max 100). Response shape:

```json
{
  "items": [],
  "total": 123,
  "page": 1,
  "page_size": 20
}
```

---

## Authentication

### Login

`POST /api/v1/auth/login` · Public

**Request body:**

```json
{ "password": "your_password" }
```

**Response (password set and correct, 200):** sets the `ep_session` cookie

```json
{ "message": "登录成功", "token": "<session-token>" }
```

**Response (no password configured, 200):** no cookie is set

```json
{ "message": "无需密码", "no_password": true }
```

**Errors:** wrong password 401 `unauthorized`; JSON parse failure 400.

### Logout

`POST /api/v1/auth/logout` · Auth required

```json
{ "message": "已登出" }
```

Clears the `ep_session` cookie.

### Auth Status

`GET /api/v1/auth/status` · Auth required

```json
{ "auth_method": "session", "has_password": true }
```

`auth_method`: `session` / `apikey` / `subtoken` / `none` / `unknown`.

---

## Stats & Alerts

### Global Stats

`GET /api/v1/stats` · Auth required

Real-time aggregated runtime stats:

| Field | Type | Meaning |
|-------|------|---------|
| `total_nodes` | int | Total nodes |
| `available_nodes` | int | Available nodes |
| `duplicate_nodes` | int | Duplicate nodes |
| `active_subscriptions` | int | Active subscriptions |
| `availability_distribution` | object | `{"high":n,"medium":n,"low":n}` (high≥0.9 / medium 0.5~0.9 / low<0.5) |
| `latency_distribution` | object | `{"lt100":n,"100_300":n,"300_500":n,"gt500":n,"unknown":n}` |
| `country_distribution` | array | `[{country_code, country_name, count}]`, by count desc |
| `top_called_nodes` | array | Top 10 by `call_total` |
| `subscription_health` | object | `{total, failed, failed_names:[]}` |
| `alert_summary` | object | `{"alert_count":n,"alert_critical":n,"empty_password_count":n}`, same scope as `/alerts` (node empty passwords + virtual-pool auth); total / critical / node-empty-password breakdown |

### Alerts

`GET /api/v1/alerts` · Auth required

Scans nodes and virtual pools for security alerts (empty passwords, etc.).

```json
{ "alerts": [ { "level": "warning", "code": "empty_node_password", "message": "...", "ref": "<stable_id>" } ], "count": 1, "enabled": true }
```

`level`: `warning` / `critical`; `code`: `empty_node_password` / `empty_pool_auth` / `weak_pool_auth`. Node passwords come from a single shared entry config (`multi_port`/`listener`), so `empty_node_password` is emitted **once as a summary** (not once per node). When `alert_enabled` is off, returns `enabled:false` with an empty `alerts`.

---

## Nodes

### List Nodes

`GET /api/v1/nodes` · Auth required

**Query params (all optional):**

| Param | Meaning |
|-------|---------|
| `page` / `page_size` | Pagination |
| `sort` | Sort field, `-` prefix for desc. One of `name`/`country`/`exit_ip`/`protocol`/`port`/`latency`/`availability`; default `latency` asc, unknown latency always sinks to bottom |
| `country` | Filter by country code (case-insensitive) |
| `protocol` | Filter by protocol (case-insensitive, matches `mode`) |
| `available` | `true`/`1`: only checked-and-available nodes |
| `duplicate` | `true`/`1`: include duplicate nodes; **duplicates are hidden by default** |
| `name_regex` | Filter by node-name regex (invalid regex applies no filter) |

**Response:** 200, paginated; `items` is an array of `Snapshot`.

### Snapshot Object

Used by `GET /nodes`, `GET /nodes/{stable_id}`, and the `node` field of `probe`.

| Field | Type | Meaning |
|-------|------|---------|
| `tag` | string | Internal node tag |
| `stable_id` | string | Stable primary key across refreshes |
| `name` | string | Node name |
| `uri` | string | Upstream proxy URI |
| `mode` | string | Protocol |
| `listen_address` | string | Listen address |
| `port` | uint16 | Listen port |
| `username` / `password` | string | Proxy auth |
| `available` | bool | Available |
| `initial_check_done` | bool | Initial check done |
| `last_latency_ms` | int64 | Last latency (ms, **<0 means not probed**) |
| `last_probe_latency` | int64 | Raw last-probe latency (nanoseconds), same source as `last_latency_ms` |
| `availability_rate` | float64 | Availability (0-1) |
| `country_code` / `country_name` | string | Country code / name |
| `asn` / `asn_org` | uint / string | ASN and org (requires geoip) |
| `exit_ip` | string | Real exit IP |
| `failure_count` | int | Consecutive failures |
| `blacklisted` | bool | Blacklisted |
| `blacklisted_until` | time | Blacklist expiry |
| `active_connections` | int32 | Active connections |
| `last_error` / `last_failure` / `last_success` | - | Last error/failure/success |
| `duplicate_of` | string | Dedup target (retained node's stable_id) |
| `total_attempts` / `total_success` / `call_total` | int64 | Total attempts/successes/forwarded calls |

### Node Detail

`GET /api/v1/nodes/{stable_id}` · Auth required → a single `Snapshot`; not found 404.

### Add Node

`POST /api/v1/nodes` · Auth required

**Request body (`NodeConfig`):**

| Field | Required | Meaning |
|-------|----------|---------|
| `name` | yes | Node name |
| `uri` | yes | Upstream proxy URI (determines protocol & validation) |
| `port` | no | Port |
| `username` / `password` | no | Proxy auth |
| `source` | no | Source (`manual`/`inline`/`nodes_file`/`subscription`) |

**Response:** 201, the created node.

**Validation:** the protocol is checked against the supported set (VMess/VLESS/Hysteria2/Shadowsocks/Trojan) along with required fields. Failure returns **422 `validation_error`** with the specific reason in `message` (e.g. `unsupported scheme "ssr"`, `vless uri missing uuid`). Conflict (name/port in use) returns 409.

### Update Node

`PATCH /api/v1/nodes/{stable_id}` · Auth required · body like add (same protocol validation) → updated node.

### Delete Node

`DELETE /api/v1/nodes/{stable_id}` · Auth required

```json
{ "message": "节点已删除", "stable_id": "<stable_id>" }
```

### Probe Single Node

`POST /api/v1/nodes/{stable_id}/probe` · Auth required (sync, 15s timeout)

```json
{ "stable_id": "...", "latency_ms": 123, "success": true, "node": { /* Snapshot */ } }
```

The `node` field lets the frontend refresh the row in place after probing. Not found 404.

### Probe All Nodes

`POST /api/v1/probe/all` · Auth required (async, returns 202 immediately)

```json
{ "message": "全节点探测已触发", "status": "running" }
```

### Pick a Single Proxy (rotation)

`GET /api/v1/nodes/pick` · Auth required (session / API Key)

Picks **one** node from the candidate pool using a strategy and returns its **direct multi_port port address** (not the virtual-pool entry). Use this for batch-registration and other flows that need a **fixed exit IP within a single session**: each call rotates to the next node via a cursor, returning a fixed `host:port + credentials` tuple you reuse throughout the session to keep the same exit IP.

> **Difference from virtual pools**: a virtual pool does **request-level** IP rotation (a different IP per connection), for dodging upstream API-key IP rate limits; `/nodes/pick` does **session-level** IP pinning (one address per call, rotating across calls), for registration-style flows that need a consistent IP within a session.

**Query params (all optional):**

| Param | Meaning |
|-------|---------|
| `name_regex` | Filter by node-name regex (invalid regex applies no filter) |
| `exit_ip` | Filter by exit IP **exact match** (reverse-lookup a node from a known IP) |
| `country` | Filter by country code, comma-separated for multiple (exact match, case-insensitive) |
| `protocol` | Filter by protocol (exact match on `mode`, case-insensitive) |
| `available` | `true`/`1`: only checked-and-available nodes (**default `true`**) |
| `strategy` | Selection strategy, see below; default `round_robin` |

**Selection strategy `strategy`:**

| Value | Meaning |
|-------|---------|
| `round_robin` | Cursor rotation, returns the next node each call. **Cursor granularity = hash of caller identity (API Key or session) + combined filter rule**; cursor lives in memory only, reset on process restart. Same caller + same rule share one cursor, so cross-account registration naturally rotates to different nodes |
| `availability_first` | Deterministic best: availability desc, latency asc, name asc, take the first; no randomness, no cursor |
| `weighted` | Availability-weighted random (reuses the virtual-pool PickWeighted semantics) |

**Response:** 200, a single node-address object; empty candidate set returns 404 `not_found`.

```json
{
  "name": "US Node 01",
  "exit_ip": "203.0.113.7",
  "host": "1.2.3.4",
  "port": 24001,
  "username": "mpuser",
  "password": "mppass",
  "protocol": "http",
  "country_code": "US",
  "country_name": "United States",
  "latency_ms": 150,
  "availability_rate": 0.98
}
```

> `host` is the external access address; `port`/`username`/`password` are the node's **direct multi_port port credentials** (same shape as `GET /sub/{token}`); `exit_ip` is the node's real exit IP, ready for fraud-score lookups.

---

## Subscriptions

### Subscription Object

| Field | Type | Meaning |
|-------|------|---------|
| `id` | uint64 | Primary key |
| `name` | string | Name |
| `url` | string | Subscription URL |
| `type` | string | Type: `base64`/`clash`/`plain`/`singbox` (auto-detected) |
| `last_refresh_at` | time | Last refresh time |
| `last_refresh_status` | string | `success`/`failed`/`running`/`never` |
| `last_error` | string | Last error |
| `node_count` | int | Node count |
| `created_at` / `updated_at` | time | Created/updated time |

### List Subscriptions

`GET /api/v1/subscriptions` · Auth required · supports `page`/`page_size`; `items` is a `Subscription` array.

### Add Subscription

`POST /api/v1/subscriptions` · Auth required

```json
{ "name": "MySub", "url": "https://example.com/sub", "type": "auto" }
```

Only `url` is required (empty → 422 `"url 不能为空"`). Response 201, the created `Subscription`.

> Adding a subscription auto-triggers a background refresh (restarts the core, brief interruption); nodes appear within seconds.

### Refresh All

`POST /api/v1/subscriptions/refresh` · Auth required

```json
{ "message": "订阅刷新完成", "status": { "last_refresh": "...", "node_count": 10, "is_refreshing": false, "refresh_count": 5 } }
```

`status` fields: `last_refresh`/`next_refresh`/`node_count`/`last_error`/`refresh_count`/`is_refreshing`/`nodes_modified`.

> The refresher is always initialized, so this endpoint always works, unaffected by `subscription_refresh.enabled` (that flag only controls periodic auto-refresh).

### Subscription Detail / Update / Delete

- `GET /api/v1/subscriptions/{id}` → single `Subscription` (not found 404)
- `PATCH /api/v1/subscriptions/{id}` · body `name`/`url`/`type` (non-empty fields override)
- `DELETE /api/v1/subscriptions/{id}` → `{"message": "订阅已删除"}`

### Refresh Single Subscription

`POST /api/v1/subscriptions/{id}/refresh` · Auth required

```json
{ "message": "订阅刷新完成", "subscription_id": 1 }
```

> The current implementation actually triggers a **full** refresh (only validates the id exists), not a true single-subscription incremental merge.

---

## Virtual Pools

Virtual pool CRUD **takes effect immediately** — no restart or reload; by the time the API returns success, the corresponding TCP listener has been opened/closed.

### VirtualPoolConfig Object

| Field | Type | Meaning |
|-------|------|---------|
| `id` | uint64 | Stable id (route key) |
| `name` | string | Name (unique) |
| `regular` | string | Regex matching node names |
| `address` | string | Listen address (default `0.0.0.0`) |
| `port` | uint16 | Listen port |
| `username` / `password` | string | Auth (optional) |
| `strategy` | string | `sequential`(default)/`random`/`balance`/`weighted` |
| `max_latency_ms` | int | Max latency threshold (optional filter) |
| `latency_weight` / `availability_weight` | float64 | weighted weights (>0) |
| `country_codes` / `excluded_country_codes` | []string | Country include/exclude filters |

List/detail responses additionally include `node_count` (currently matched nodes) and `running`.

### List Virtual Pools

`GET /api/v1/virtual-pools` · Auth required · returns an array directly (not paginated).

### Add Virtual Pool

`POST /api/v1/virtual-pools` · Auth required · body is `VirtualPoolConfig` (`port` may be empty for auto-allocation) → 201.

Validation failure (bad strategy, etc.) 422; name exists / port in use 409.

### Next Available Port

`GET /api/v1/virtual-pools/next-port` · Auth required

```json
{ "port": 30001 }
```

### Virtual Pool Detail / Update / Delete

- `GET /api/v1/virtual-pools/{id}` → `poolView`
- `PATCH /api/v1/virtual-pools/{id}` · omitted fields are preserved (merge semantics)
- `DELETE /api/v1/virtual-pools/{id}` → `{"message": "虚拟池已删除"}`

### Virtual Pool Nodes

`GET /api/v1/virtual-pools/{id}/nodes` · Auth required → `Snapshot` array (currently matched nodes).

---

## Credentials (requires `manage_credentials` scope)

Credentials are stored as **AES-256-GCM ciphertext**; the encryption key is `config.yaml`'s `credential_key`. Lists return only the prefix; copy buttons call `/plain` to decrypt on demand. The plaintext key/token is returned **only once at creation**.

### API Keys

**List:** `GET /api/v1/api-keys` · requires `manage_credentials` · paginated; `items`:

| Field | Meaning |
|-------|---------|
| `id` | Primary key |
| `name` | Name |
| `key_prefix` | Plaintext prefix (first 16 chars) |
| `scopes` | Scope list |
| `created_at` / `last_used_at` | Created / last-used time |
| `revoked` | Revoked |

**Create:** `POST /api/v1/api-keys`

```json
{ "name": "Third-party", "scopes": ["manage_nodes"] }
```

Response 201, **plaintext returned once**:

```json
{
  "id": 1,
  "name": "Third-party",
  "key": "epk_live_xxxxxxxxxxxxxxxx...",
  "key_prefix": "epk_live_xxxxxxxx",
  "scopes": ["manage_nodes"],
  "message": "API Key 已创建，可在列表点复制随时获取明文"
}
```

**Get plaintext:** `GET /api/v1/api-keys/{id}/plain` → `{"plain": "epk_live_..."}`

**Delete:** `DELETE /api/v1/api-keys/{id}` → `{"message": "API Key 已删除", "id": 1}`

### Subscribe Tokens

**List:** `GET /api/v1/subscribe-tokens` · requires `manage_credentials` · paginated; `items` contain `id`/`name`/`token_prefix` (first 12 chars)/`filters`/`created_at`/`last_used_at`/`revoked`.

`filters` fields:

| Field | Meaning |
|-------|---------|
| `country_codes` | Country whitelist |
| `protocols` | Protocol whitelist |
| `name_regex` | Name regex |

**Create:** `POST /api/v1/subscribe-tokens`

```json
{ "name": "For Platform A", "filters": { "country_codes": ["US", "JP"] } }
```

Response 201, plaintext token returned once:

```json
{
  "id": 1,
  "name": "For Platform A",
  "token": "eps_xxxxxxxxxxxx...",
  "token_prefix": "eps_xxxxxxxx",
  "filters": { "country_codes": ["US", "JP"] },
  "message": "订阅 Token 已创建，可在列表点复制随时获取订阅链接"
}
```

**Get plaintext:** `GET /api/v1/subscribe-tokens/{id}/plain` → `{"plain": "eps_..."}`

**Delete:** `DELETE /api/v1/subscribe-tokens/{id}` → `{"message": "订阅 Token 已删除", "id": 1}`

---

## Export & Settings

### Export Proxy URIs

`GET /api/v1/export` · Auth required

Supports the same filters as `/nodes` (`country`/`protocol`/`available`/`duplicate`/`name_regex`; no sort/pagination).

```json
{ "uris": ["http://user:pass@1.2.3.4:24000", "..."], "count": 42 }
```

### Get Settings

`GET /api/v1/settings` · Auth required

| Field | Meaning |
|-------|---------|
| `external_ip` | External IP (for export/host replacement) |
| `skip_cert_verify` | Skip SSL cert verification globally |
| `alert_enabled` | Security alert toggle |
| `mode` | Operating mode (`pool`/`multi-port`/`hybrid`) |
| `listener_port` | Pool port (for copy-link in pool mode) |
| `proxy_username` / `proxy_password` | Compat field: credentials of the active entry (hybrid/multi-port use `multi_port`; pool uses `pool`) |
| `pool` | Pool entry object `{address, port, username, password, enabled}` (pool/hybrid enabled) |
| `multi_port` | Multi-port entry object `{address, base_port, username, password, enabled}` (multi-port/hybrid enabled) |

### Update Settings

`PUT /api/v1/settings` · Auth required

```json
{ "external_ip": "1.2.3.4", "multi_port_username": "mpuser", "multi_port_password": "newpass", "skip_cert_verify": false, "alert_enabled": true }
```

| Field | Type | Meaning |
|-------|------|---------|
| `external_ip` | string | External IP (overwrites — send current value to preserve) |
| `pool_username` / `pool_password` | string\* | Pool entry (pool/hybrid listener) credentials; pointer, **omitted preserves**. Changing either **rebuilds the sing-box core** (same as node CRUD, brief connection interruption) |
| `multi_port_username` / `multi_port_password` | string\* | Multi-port entry (multi-port/hybrid, shared by node ports) credentials; pointer, **omitted preserves**, changing also rebuilds the core |
| `skip_cert_verify` / `alert_enabled` | bool\* | Pointer — omitted preserves |

Response `{"message": "设置已更新"}`. If entry credentials were saved but the reload failed, returns 500 with `"入口密码已保存，但热重载失败…，重启后生效"`.

---

## Subscription Proxy List (standalone auth)

### Get Proxy List

`GET /sub/{token}` · Standalone auth (token as path param, looked up via sha256; revoked/missing/absent all return 401)

No session or API Key needed. Filtering is entirely determined by the token's `filters`; duplicates and checked-but-unavailable nodes are excluded by default. `exit_ip` is the node's real exit IP, ready for fraud-score lookups.

```json
{
  "updated_at": "2026-07-14T08:00:00Z",
  "count": 42,
  "proxies": [
    {
      "name": "US Node",
      "country_code": "US",
      "country_name": "United States",
      "host": "1.2.3.4",
      "port": 24000,
      "username": "mpuser",
      "password": "mppass",
      "protocol": "http",
      "latency_ms": 150,
      "availability_rate": 0.98,
      "exit_ip": "203.0.113.7"
    }
  ]
}
```

---

## Examples

### Login and Add a Node

```bash
# Login
TOKEN=$(curl -s -X POST http://localhost:9090/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' | jq -r '.token')

# Add a node (with protocol validation)
curl -X POST http://localhost:9090/api/v1/nodes \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"HK","uri":"vless://uuid@server:443#HK"}'

# List nodes sorted
curl "http://localhost:9090/api/v1/nodes?sort=-latency&page=1&page_size=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Create a Virtual Pool (real-time)

```bash
curl -X POST http://localhost:9090/api/v1/virtual-pools \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"US_Pool","regular":".*US.*","address":"0.0.0.0","strategy":"sequential"}'
# After 201, the allocated port is reachable immediately
```

### Access with an API Key

```bash
curl http://localhost:9090/api/v1/nodes \
  -H "X-API-Key: epk_live_xxxxxxxx..."
```
