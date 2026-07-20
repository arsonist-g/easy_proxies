# Easy Proxies API 文档

所有管理接口位于 `/api/v1` 前缀下。订阅代理列表 `GET /sub/{token}` 是独立路径，不在 `/api/v1` 下。

## 基础 URL

```
http://<host>:<management.listen 端口>
```

端口即 `config.yaml` 中 `management.listen`（默认 `9090`，可自行修改）。

---

## 全局约定

### 统一错误响应

所有错误响应为 JSON：

```json
{
  "code": "validation_error",
  "message": "具体错误原因",
  "details": {},
  "traceId": "请求 ID"
}
```

| `code` | HTTP | 含义 |
|--------|------|------|
| `invalid_request` | 400 | 请求格式错误（JSON 解析失败等） |
| `unauthorized` | 401 | 未授权 / 凭证无效 |
| `forbidden` | 403 | 权限不足（API Key 缺少所需 scope） |
| `not_found` | 404 | 资源不存在 |
| `conflict` | 409 | 资源冲突（名称/端口占用） |
| `validation_error` | 422 | 业务校验失败，`message` 带具体原因 |
| `internal_error` | 500 | 内部错误；**依赖未注入的端点返回 501 也用此 code** |

### 鉴权方式

除 `POST /api/v1/auth/login` 与 `GET /sub/{token}` 外，所有端点需鉴权。三种方式任一通过即可：

1. **Session Cookie**（WebUI）：登录后写入 `ep_session` cookie（`HttpOnly`、`SameSite=Strict`、有效期 7 天）。
2. **Bearer Token**：`Authorization: Bearer <session-token>`（token 值同 cookie 值）。
3. **API Key**：`X-API-Key: <明文 key>`（服务端 sha256 反查，吊销/不存在均失败）。

> **无密码模式**：当 `management.password` 为空时，所有端点免鉴权直接放行。

#### 鉴权覆盖表

| 端点 | 鉴权方式 | Scope 要求 |
|------|----------|-----------|
| `POST /api/v1/auth/login` | 公开（无鉴权） | — |
| `GET /sub/{token}` | **Subscribe Token**（token 作路径参数） | — |
| 其余全部 `/api/v1/*`（stats / nodes / subscriptions / virtual-pools / alerts / export / settings 等） | Session Cookie / Bearer Token / API Key（任一即可） | — |
| `/api-keys`、`/subscribe-tokens` 的 list / create / plain / delete | Session Cookie / Bearer Token / API Key（任一即可） | **API Key 必须含 `manage_credentials`**（Session 登录与无密码模式不受限） |

> Session 登录与无密码模式拥有全部权限，上表「Scope 要求」列仅约束 API Key。

### 权限作用域（scope）

API Key 创建时可分配 scope；Session 登录与无密码模式拥有全部权限。

| scope | 含义 |
|-------|------|
| `manage_nodes` | 节点 CRUD |
| `manage_subscriptions` | 订阅 CRUD |
| `manage_pools` | 虚拟池 CRUD |
| `manage_credentials` | 凭证管理（API Key / Subscribe Token） |
| `read_only` | 只读 |

> 路由层仅对**凭证管理**端点强制 `manage_credentials`；节点/订阅/虚拟池的写操作只要通过鉴权即可。用 API Key 访问凭证端点时，该 Key 必须含 `manage_credentials`，否则 403。

### 分页

列表端点支持 query 参数 `page`（从 1 起，默认 1）与 `page_size`（默认 20，上限 100）。分页响应结构：

```json
{
  "items": [],
  "total": 123,
  "page": 1,
  "page_size": 20
}
```

---

## 认证

### 登录

`POST /api/v1/auth/login` · 公开

**请求体：**

```json
{ "password": "your_password" }
```

**响应（有密码且正确，200）：** 设置 `ep_session` cookie

```json
{ "message": "登录成功", "token": "<session-token>" }
```

**响应（未配置密码，200）：** 不设 cookie

```json
{ "message": "无需密码", "no_password": true }
```

**错误：** 密码错误 401 `unauthorized`；JSON 解析失败 400。

### 注销

`POST /api/v1/auth/logout` · 需登录

```json
{ "message": "已登出" }
```

清除 `ep_session` cookie。

### 鉴权状态

`GET /api/v1/auth/status` · 需登录

```json
{ "auth_method": "session", "has_password": true }
```

`auth_method` 取值：`session` / `apikey` / `subtoken` / `none` / `unknown`。

---

## 统计与告警

### 全局统计

`GET /api/v1/stats` · 需登录

实时聚合的运行时统计：

| 字段 | 类型 | 含义 |
|------|------|------|
| `total_nodes` | int | 节点总数 |
| `available_nodes` | int | 可用节点数 |
| `duplicate_nodes` | int | 重复节点数 |
| `active_subscriptions` | int | 活跃订阅数 |
| `availability_distribution` | object | `{"high":n,"medium":n,"low":n}`（high≥0.9 / medium 0.5~0.9 / low<0.5） |
| `latency_distribution` | object | `{"lt100":n,"100_300":n,"300_500":n,"gt500":n,"unknown":n}` |
| `country_distribution` | array | `[{country_code, country_name, count}]`，按 count 降序 |
| `top_called_nodes` | array | 按 `call_total` 降序前 10 |
| `subscription_health` | object | `{total, failed, failed_names:[]}` |
| `alert_summary` | object | `{"alert_count":n,"alert_critical":n,"empty_password_count":n}`，同 `/alerts` 口径（节点空密码 + 虚拟池认证）；三项分别为告警总数 / 严重数 / 节点空密码细分 |

### 告警列表

`GET /api/v1/alerts` · 需登录

扫描节点与虚拟池的安全告警（空密码等）。

```json
{ "alerts": [ { "level": "warning", "code": "empty_node_password", "message": "...", "ref": "<stable_id>" } ], "count": 1, "enabled": true }
```

`level`：`warning` / `critical`；`code`：`empty_node_password` / `empty_pool_auth` / `weak_pool_auth`。节点代理密码来自统一入口配置（`multi_port`/`listener`），故 `empty_node_password` **只汇总报一条**（不再逐节点重复）。`alert_enabled` 关闭时返回 `enabled:false` 且 `alerts` 为空。

---

## 节点

### 节点列表

`GET /api/v1/nodes` · 需登录

**Query 参数（均可选）：**

| 参数 | 含义 |
|------|------|
| `page` / `page_size` | 分页 |
| `sort` | 排序字段，前缀 `-` 表降序。可选 `name`/`country`/`exit_ip`/`protocol`/`port`/`latency`/`availability`；默认 `latency` 升序，延迟未知始终沉底 |
| `country` | 按国家码过滤（大小写不敏感） |
| `protocol` | 按协议过滤（大小写不敏感，匹配 `mode`） |
| `available` | `true`/`1`：仅返回已检查且可用的节点 |
| `duplicate` | `true`/`1`：包含重复节点；**默认隐藏重复节点** |
| `name_regex` | 按节点名正则过滤（正则无效时不过滤） |

**响应：** 200，分页结构，`items` 为 `Snapshot` 数组。

### Snapshot 对象

`GET /nodes`、`GET /nodes/{stable_id}`、`probe` 的 `node` 字段均用此结构。

| 字段 | 类型 | 含义 |
|------|------|------|
| `tag` | string | 内部节点标签 |
| `stable_id` | string | 跨刷新稳定主键 |
| `name` | string | 节点名 |
| `uri` | string | 上游代理 URI |
| `mode` | string | 协议 |
| `listen_address` | string | 监听地址 |
| `port` | uint16 | 监听端口 |
| `username` / `password` | string | 代理认证 |
| `available` | bool | 是否可用 |
| `initial_check_done` | bool | 初始检查是否完成 |
| `last_latency_ms` | int64 | 最近延迟（ms，**<0 表示未探测**） |
| `last_probe_latency` | int64 | 最近探测延迟原始值（纳秒），与 `last_latency_ms` 同源 |
| `availability_rate` | float64 | 可用率（0-1） |
| `country_code` / `country_name` | string | 国家码 / 国名 |
| `asn` / `asn_org` | uint / string | ASN 与组织名（需配置 geoip） |
| `exit_ip` | string | 出口真实 IP |
| `failure_count` | int | 连续失败计数 |
| `blacklisted` | bool | 是否黑名单 |
| `blacklisted_until` | time | 黑名单到期 |
| `active_connections` | int32 | 活跃连接数 |
| `last_error` / `last_failure` / `last_success` | - | 最近错误/失败/成功 |
| `duplicate_of` | string | 去重指向（被保留节点的 stable_id） |
| `total_attempts` / `total_success` / `call_total` | int64 | 总尝试/成功/转发调用次数 |

### 节点详情

`GET /api/v1/nodes/{stable_id}` · 需登录 → 单个 `Snapshot`；未找到 404。

### 添加节点

`POST /api/v1/nodes` · 需登录

**请求体（`NodeConfig`）：**

| 字段 | 必填 | 含义 |
|------|------|------|
| `name` | 是 | 节点名 |
| `uri` | 是 | 上游代理 URI（决定协议与校验） |
| `port` | 否 | 端口 |
| `username` / `password` | 否 | 代理认证 |
| `source` | 否 | 来源（`manual`/`inline`/`nodes_file`/`subscription`） |

**响应：** 201，创建后的节点。

**校验：** 添加时校验协议是否支持（VMess/VLESS/Hysteria2/Shadowsocks/Trojan）及必要字段。失败返回 **422 `validation_error`**，`message` 透出具体原因（如 `unsupported scheme "ssr"`、`vless uri missing uuid`）。冲突（名称/端口已存在）返回 409。

### 更新节点

`PATCH /api/v1/nodes/{stable_id}` · 需登录 · 请求体同添加（同样做协议校验） → 更新后的节点。

### 删除节点

`DELETE /api/v1/nodes/{stable_id}` · 需登录

```json
{ "message": "节点已删除", "stable_id": "<stable_id>" }
```

### 探测单节点

`POST /api/v1/nodes/{stable_id}/probe` · 需登录（同步，15s 超时）

```json
{ "stable_id": "...", "latency_ms": 123, "success": true, "node": { /* Snapshot */ } }
```

`node` 字段供前端探测后就地刷新整行。未找到 404。

### 全节点探测

`POST /api/v1/probe/all` · 需登录（异步触发，立即返回 202）

```json
{ "message": "全节点探测已触发", "status": "running" }
```

### 选取单个代理（轮询）

`GET /api/v1/nodes/pick` · 需登录（session / API Key）

从候选节点中按策略选**一个**节点，返回其**直接 multi_port 端口地址**（而非虚拟池入口）。用于批量注册等需要在单次会话内**固定出口 IP** 的场景：每次调用按游标轮询到下一个节点，拿到一组固定的 `host:port + 凭证`，在同一会话内反复使用该地址即可保持同一出口 IP。

> **与虚拟池的区别**：虚拟池是**请求级** IP 轮换（每条连接换 IP），用于绕上游 API-key 的 IP 速率限制；`/nodes/pick` 是**会话级** IP 固定（一次拿一个地址，跨调用轮询），用于注册等会话内 IP 需一致的场景。

**Query 参数（均可选）：**

| 参数 | 含义 |
|------|------|
| `name_regex` | 按节点名正则过滤（正则无效时不过滤） |
| `exit_ip` | 按出口 IP **严格相等**过滤（已知 IP 反查回该节点） |
| `country` | 按国家码过滤，逗号分隔多个（精确匹配，大小写不敏感） |
| `protocol` | 按协议过滤（精确匹配 `mode`，大小写不敏感） |
| `available` | `true`/`1`：仅返回已检查且可用的节点（**默认 `true`**） |
| `strategy` | 选取策略，见下；默认 `round_robin` |

**选取策略 `strategy`：**

| 值 | 含义 |
|------|------|
| `round_robin` | 游标轮询，每次返回下一个节点。**游标粒度 = 调用方标识（API Key 或 session）+ 过滤规则组合的哈希**；游标仅存内存，进程重启归零。同一调用方 + 同一规则共享一个游标，跨账号注册自然轮询到不同节点 |
| `availability_first` | 确定性最优：可用率降序、延迟升序、名称升序取第一个，无随机、无游标 |
| `weighted` | 按可用率加权随机（复用虚拟池 PickWeighted 语义） |

**响应：** 200，单个节点地址对象；候选集为空返回 404 `not_found`。

```json
{
  "name": "美国节点-01",
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

> `host` 为外部访问地址，`port`/`username`/`password` 为该节点的**直接 multi_port 端口凭证**（与 `GET /sub/{token}` 同口径）；`exit_ip` 为节点真实出口 IP，可直接拿去欺诈评分网站查纯净度。

---

## 订阅

### Subscription 对象

| 字段 | 类型 | 含义 |
|------|------|------|
| `id` | uint64 | 主键 |
| `name` | string | 名称 |
| `url` | string | 订阅 URL |
| `type` | string | 类型：`base64`/`clash`/`plain`/`singbox`（自动检测） |
| `last_refresh_at` | time | 最近刷新时间 |
| `last_refresh_status` | string | `success`/`failed`/`running`/`never` |
| `last_error` | string | 最近错误 |
| `node_count` | int | 节点数 |
| `created_at` / `updated_at` | time | 创建/更新时间 |

### 订阅列表

`GET /api/v1/subscriptions` · 需登录 · 支持 `page`/`page_size`，分页结构，`items` 为 `Subscription` 数组。

### 添加订阅

`POST /api/v1/subscriptions` · 需登录

```json
{ "name": "我的订阅", "url": "https://example.com/sub", "type": "auto" }
```

仅 `url` 必填（空则 422 `"url 不能为空"`）。响应 201，创建的 `Subscription`。

> 添加订阅后自动触发一次后台刷新（重启内核、短暂中断连接），节点几秒后生效。

### 全量刷新

`POST /api/v1/subscriptions/refresh` · 需登录

```json
{ "message": "订阅刷新完成", "status": { "last_refresh": "...", "node_count": 10, "is_refreshing": false, "refresh_count": 5 } }
```

`status` 字段：`last_refresh`/`next_refresh`/`node_count`/`last_error`/`refresh_count`/`is_refreshing`/`nodes_modified`。

> 刷新管理器始终初始化，此端点始终可用，不受 `subscription_refresh.enabled` 影响（该开关仅控制定时自动刷新）。

### 订阅详情 / 更新 / 删除

- `GET /api/v1/subscriptions/{id}` → 单个 `Subscription`（未找到 404）
- `PATCH /api/v1/subscriptions/{id}` · 请求体 `name`/`url`/`type`（非空字段覆盖）
- `DELETE /api/v1/subscriptions/{id}` → `{"message": "订阅已删除"}`

### 单订阅刷新

`POST /api/v1/subscriptions/{id}/refresh` · 需登录

```json
{ "message": "订阅刷新完成", "subscription_id": 1 }
```

> 当前实现实际触发一次**全量**刷新（仅校验 id 存在），并非真正的单订阅增量合并。

---

## 虚拟池

虚拟池 CRUD **实时生效**，无需重启或 reload——接口返回成功时，对应端口的 TCP 监听器已开/关完毕。

### VirtualPoolConfig 对象

| 字段 | 类型 | 含义 |
|------|------|------|
| `id` | uint64 | 稳定标识（路由主键） |
| `name` | string | 名称（唯一） |
| `regular` | string | 匹配节点名的正则 |
| `address` | string | 监听地址（默认 `0.0.0.0`） |
| `port` | uint16 | 监听端口 |
| `username` / `password` | string | 认证（可选） |
| `strategy` | string | `sequential`(默认)/`random`/`balance`/`weighted` |
| `max_latency_ms` | int | 最大延迟阈值（可选过滤） |
| `latency_weight` / `availability_weight` | float64 | weighted 策略权重（>0） |
| `country_codes` / `excluded_country_codes` | []string | 国家码包含/排除过滤 |

列表/详情响应额外含 `node_count`（当前匹配节点数）与 `running`（是否运行中）。

### 虚拟池列表

`GET /api/v1/virtual-pools` · 需登录 · 直接返回数组（非分页）。

### 添加虚拟池

`POST /api/v1/virtual-pools` · 需登录 · 请求体为 `VirtualPoolConfig`（`port` 可留空自动分配） → 201。

校验失败（strategy 非法等）422；名称已存在/端口占用 409。

### 下一个可用端口

`GET /api/v1/virtual-pools/next-port` · 需登录

```json
{ "port": 30001 }
```

### 虚拟池详情 / 更新 / 删除

- `GET /api/v1/virtual-pools/{id}` → `poolView`
- `PATCH /api/v1/virtual-pools/{id}` · 未提供字段保留（合并语义）
- `DELETE /api/v1/virtual-pools/{id}` → `{"message": "虚拟池已删除"}`

### 虚拟池匹配节点

`GET /api/v1/virtual-pools/{id}/nodes` · 需登录 → `Snapshot` 数组（池当前匹配的节点）。

---

## 凭证（需 `manage_credentials` scope）

凭证以 **AES-256-GCM 密文**存储，加密密钥在 `config.yaml` 的 `credential_key`。列表只返回前缀；点复制时调 `/plain` 解密返回明文。明文 key/token 仅在**创建时返回一次**。

### API Key

**列表：** `GET /api/v1/api-keys` · 需 `manage_credentials` · 分页，`items` 为：

| 字段 | 含义 |
|------|------|
| `id` | 主键 |
| `name` | 名称 |
| `key_prefix` | 明文前缀（前 16 字符） |
| `scopes` | scope 列表 |
| `created_at` / `last_used_at` | 创建/最近使用时间 |
| `revoked` | 是否已吊销 |

**创建：** `POST /api/v1/api-keys`

```json
{ "name": "第三方对接", "scopes": ["manage_nodes"] }
```

响应 201，**明文仅此一次返回**：

```json
{
  "id": 1,
  "name": "第三方对接",
  "key": "epk_live_xxxxxxxxxxxxxxxx...",
  "key_prefix": "epk_live_xxxxxxxx",
  "scopes": ["manage_nodes"],
  "message": "API Key 已创建，可在列表点复制随时获取明文"
}
```

**取明文：** `GET /api/v1/api-keys/{id}/plain` → `{"plain": "epk_live_..."}`

**删除：** `DELETE /api/v1/api-keys/{id}` → `{"message": "API Key 已删除", "id": 1}`

### Subscribe Token

**列表：** `GET /api/v1/subscribe-tokens` · 需 `manage_credentials` · 分页，`items` 含 `id`/`name`/`token_prefix`（前 12 字符）/`filters`/`created_at`/`last_used_at`/`revoked`。

`filters` 字段：

| 字段 | 含义 |
|------|------|
| `country_codes` | 国家码白名单 |
| `protocols` | 协议白名单 |
| `name_regex` | 名称正则 |

**创建：** `POST /api/v1/subscribe-tokens`

```json
{ "name": "给平台A", "filters": { "country_codes": ["US", "JP"] } }
```

响应 201，明文 token 仅此一次：

```json
{
  "id": 1,
  "name": "给平台A",
  "token": "eps_xxxxxxxxxxxx...",
  "token_prefix": "eps_xxxxxxxx",
  "filters": { "country_codes": ["US", "JP"] },
  "message": "订阅 Token 已创建，可在列表点复制随时获取订阅链接"
}
```

**取明文：** `GET /api/v1/subscribe-tokens/{id}/plain` → `{"plain": "eps_..."}`

**删除：** `DELETE /api/v1/subscribe-tokens/{id}` → `{"message": "订阅 Token 已删除", "id": 1}`

---

## 导出与设置

### 导出代理 URI

`GET /api/v1/export` · 需登录

支持与 `/nodes` 相同的过滤参数（`country`/`protocol`/`available`/`duplicate`/`name_regex`，不接受排序与分页）。

```json
{ "uris": ["http://user:pass@1.2.3.4:24000", "..."], "count": 42 }
```

### 获取设置

`GET /api/v1/settings` · 需登录

| 字段 | 含义 |
|------|------|
| `external_ip` | 外部 IP（导出/host 替换用） |
| `skip_cert_verify` | 是否全局跳过 SSL 证书验证 |
| `alert_enabled` | 安全告警开关 |
| `mode` | 运行模式（`pool`/`multi-port`/`hybrid`） |
| `listener_port` | pool 端口（pool 模式复制链接用） |
| `proxy_username` / `proxy_password` | 兼容字段：当前生效入口的凭证（hybrid/multi-port 取 `multi_port`，pool 取 `pool`） |
| `pool` | 代理池入口对象 `{address, port, username, password, enabled}`（pool/hybrid 启用） |
| `multi_port` | 多端口入口对象 `{address, base_port, username, password, enabled}`（multi-port/hybrid 启用） |

### 更新设置

`PUT /api/v1/settings` · 需登录

```json
{ "external_ip": "1.2.3.4", "multi_port_username": "mpuser", "multi_port_password": "newpass", "skip_cert_verify": false, "alert_enabled": true }
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `external_ip` | string | 外部 IP（覆盖写——保留原值需把当前值一并提交） |
| `pool_username` / `pool_password` | string\* | 代理池入口（pool/hybrid 的 listener）凭证；指针，**未提供则保留**。改动任一项会**重建 sing-box 内核**（与节点增删改同等，期间代理入口短暂中断） |
| `multi_port_username` / `multi_port_password` | string\* | 多端口入口（multi-port/hybrid，节点端口共享）凭证；指针，**未提供则保留**，改动同样重建内核 |
| `skip_cert_verify` / `alert_enabled` | bool\* | 指针——未提供则保留 |

响应 `{"message": "设置已更新"}`。若入口密码已保存但热重载失败，返回 500 并提示 `"入口密码已保存，但热重载失败…，重启后生效"`。

---

## 订阅代理列表（独立鉴权）

### 获取代理列表

`GET /sub/{token}` · 独立鉴权（token 作路径参数，sha256 反查；吊销/不存在/未提供均 401）

无需 session 或 API Key。过滤完全由 token 的 `filters` 决定；默认额外排除重复节点与已检查但不可用的节点。`exit_ip` 为节点真实出口 IP，可直接用于欺诈评分查询。

```json
{
  "updated_at": "2026-07-14T08:00:00Z",
  "count": 42,
  "proxies": [
    {
      "name": "美国节点",
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

## 示例

### 登录并添加节点

```bash
# 登录
TOKEN=$(curl -s -X POST http://localhost:9090/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' | jq -r '.token')

# 添加节点（带协议校验）
curl -X POST http://localhost:9090/api/v1/nodes \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"香港节点","uri":"vless://uuid@server:443#香港节点"}'

# 排序列出节点
curl "http://localhost:9090/api/v1/nodes?sort=-latency&page=1&page_size=20" \
  -H "Authorization: Bearer $TOKEN"
```

### 创建虚拟池（实时生效）

```bash
curl -X POST http://localhost:9090/api/v1/virtual-pools \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"US_Pool","regular":".*美国.*","address":"0.0.0.0","strategy":"sequential"}'
# 返回 201 后，分配的端口立即可连
```

### 用 API Key 访问

```bash
curl http://localhost:9090/api/v1/nodes \
  -H "X-API-Key: epk_live_xxxxxxxx..."
```
