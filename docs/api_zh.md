# Easy Proxies API 文档

## 基础 URL

```
http://localhost:9090
```

## 身份认证

如果在 `config.yaml` 中配置了密码，除 `/api/auth` 外的所有 API 端点都需要身份认证。

### 认证方式

1. **基于 Cookie**（推荐用于 Web UI）
   - 登录后，session token 会被存储在名为 `session_token` 的 cookie 中
   - Cookie 有效期：7 天

2. **Bearer Token**（推荐用于 API 客户端）
   - 在 `Authorization` 请求头中包含 token
   - 格式：`Authorization: Bearer {token}`

---

## 端点列表

### 身份认证

#### 登录

POST `/api/auth`

验证用户身份并返回 session token。

**请求体：**

```json
{
  "password": "your_password"
}
```

**响应 (200 OK):**

```json
{
  "message": "登录成功",
  "token": "hex_encoded_token_here"
}
```

**响应 (200 OK - 无密码):**

```json
{
  "message": "无需密码",
  "no_password": true
}
```

**响应 (401 Unauthorized):**

```json
{
  "error": "密码错误"
}
```

---

### 设置管理

#### 获取设置

GET `/api/settings`

获取当前动态设置。

**响应 (200 OK):**

```json
{
  "external_ip": "1.2.3.4",
  "probe_target": "www.apple.com:80",
  "skip_cert_verify": false
}
```

#### 更新设置

PUT `/api/settings`

更新动态设置并持久化到 `config.yaml`。

**请求体：**

```json
{
  "external_ip": "1.2.3.4",
  "probe_target": "www.apple.com:80",
  "skip_cert_verify": false
}
```

**响应 (200 OK):**

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

### 节点监控

#### 列出节点（运行时状态）

GET `/api/nodes`

返回所有可用节点的运行时状态（仅包含初始健康检查通过的节点）。

**响应 (200 OK):**

```json
{
  "nodes": [
    {
      "tag": "node-1",
      "name": "台湾节点",
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

#### 探测单个节点

POST `/api/nodes/{tag}/probe`

手动触发特定节点的延迟探测。

**路径参数：**
- `tag` - 节点标签（如需 URL 编码）

**响应 (200 OK):**

```json
{
  "message": "探测成功",
  "latency_ms": 150
}
```

**响应 (500 Internal Server Error):**

```json
{
  "error": "连接超时"
}
```

#### 解除节点拉黑

POST `/api/nodes/{tag}/release`

立即从黑名单中移除节点。

**路径参数：**
- `tag` - 节点标签（如需 URL 编码）

**响应 (200 OK):**

```json
{
  "message": "已解除拉黑"
}
```

**响应 (500 Internal Server Error):**

```json
{
  "error": "节点不存在"
}
```

#### 探测所有节点 (SSE)

POST `/api/nodes/probe-all`

并发触发所有节点的延迟探测。通过服务器发送事件 (SSE) 返回结果。

**事件类型：**

1. **start** - 探测开始
   ```json
   {
     "type": "start",
     "total": 10
   }
   ```

2. **progress** - 单个节点完成
   ```json
   {
     "type": "progress",
     "tag": "node-1",
     "name": "台湾节点",
     "latency": 150,
     "error": "",
     "current": 3,
     "total": 10,
     "progress": 30.0
   }
   ```

3. **complete** - 所有探测完成
   ```json
   {
     "type": "complete",
     "total": 10,
     "success": 8,
     "failed": 2
   }
   ```

**使用 curl 示例：**

```bash
curl -N http://localhost:9090/api/nodes/probe-all \
  -H "Authorization: Bearer your_token"
```

---

### 节点配置管理

#### 列出配置节点

GET `/api/nodes/config`

返回 `config.yaml` 中所有配置的节点。

**响应 (200 OK):**

```json
{
  "nodes": [
    {
      "name": "台湾节点",
      "uri": "vless://uuid@server:443?...#台湾节点",
      "port": 24000,
      "username": "",
      "password": ""
    }
  ]
}
```

#### 添加节点

POST `/api/nodes/config`

向配置中添加新节点。**需要重载配置才能生效。**

**请求体：**

```json
{
  "name": "新节点",
  "uri": "vless://uuid@server:443?...#新节点",
  "port": 24001,
  "username": "",
  "password": ""
}
```

**响应 (200 OK):**

```json
{
  "node": {
    "name": "新节点",
    "uri": "vless://uuid@server:443?...#新节点",
    "port": 24001,
    "username": "",
    "password": ""
  },
  "message": "节点已添加，请点击重载使配置生效"
}
```

**响应 (400 Bad Request):**

```json
{
  "error": "节点名称或端口已存在"
}
```

#### 更新节点

PUT `/api/nodes/config/{name}`

更新现有节点。**需要重载配置才能生效。**

**路径参数：**
- `name` - 节点名称（需 URL 编码）

**请求体：**

```json
{
  "name": "更新的节点",
  "uri": "vless://uuid@server:443?...#更新的节点",
  "port": 24001,
  "username": "",
  "password": ""
}
```

**响应 (200 OK):**

```json
{
  "node": {
    "name": "更新的节点",
    "uri": "vless://uuid@server:443?...#更新的节点",
    "port": 24001,
    "username": "",
    "password": ""
  },
  "message": "节点已更新，请点击重载使配置生效"
}
```

**响应 (404 Not Found):**

```json
{
  "error": "节点不存在"
}
```

#### 删除节点

DELETE `/api/nodes/config/{name}`

从配置中移除节点。**需要重载配置才能生效。**

**路径参数：**
- `name` - 节点名称（需 URL 编码）

**响应 (200 OK):**

```json
{
  "message": "节点已删除，请点击重载使配置生效"
}
```

**响应 (404 Not Found):**

```json
{
  "error": "节点不存在"
}
```

---

### 可用节点选择

**注意：** 这些端点仅在 `multi-port` 或 `hybrid` 模式下可用。

#### 获取可用节点（单个）

GET `/api/nodes/get_available_node`

根据选择策略返回单个可用节点。

**查询参数：**

| 参数 | 类型 | 默认值 | 说明 |
|-----------|------|---------|-------------|
| `regular` | string | `""` | 用于筛选节点名称的正则表达式 |
| `strategy` | string | `"sequential"` | 选择策略：`sequential`（顺序）、`random`（随机）、`balance`（均衡）、`weighted`（加权） |
| `latency_weight` | string | `"0.5"` | 延迟权重（用于加权策略） |
| `success_rate_weight` | string | `"0.5"` | 成功率权重（用于加权策略） |
| `weight_mode` | string | `"add"` | 权重计算模式：`add`（加法）、`multiply`（乘法） |
| `weighted_random` | boolean | `false` | 使用加权随机选择（true）或选择最佳节点（false） |

**策略说明：**

- **sequential**：基于时间的轮询选择
- **random**：随机选择
- **balance**：选择活跃连接数最少的节点
- **weighted**：基于延迟和成功率的加权选择

**响应 (200 OK):**

```json
{
  "tag": "node-1",
  "name": "台湾节点",
  "proxy_url": "http://user:pass@1.2.3.4:24000",
  "latency_ms": 150
}
```

**响应 (400 Bad Request):**

```json
{
  "error": "此 API 仅在 multi-port 或 hybrid 模式下可用"
}
```

**示例：**

```bash
# 获取最佳节点（默认顺序策略）
curl "http://localhost:9090/api/nodes/get_available_node"

# 获取通过正则筛选的随机节点
curl "http://localhost:9090/api/nodes/get_available_node?strategy=random&regular=.*台湾.*"

# 获取延迟最低的节点（加权策略）
curl "http://localhost:9090/api/nodes/get_available_node?strategy=weighted&latency_weight=1&success_rate_weight=0"

# 获取成功率最高的节点（加权策略）
curl "http://localhost:9090/api/nodes/get_available_node?strategy=weighted&latency_weight=0&success_rate_weight=1"
```

#### 获取可用节点（多个）

GET `/api/nodes/get_available_nodes`

根据选择策略返回多个可用节点。

**查询参数：**

| 参数 | 类型 | 默认值 | 说明 |
|-----------|------|---------|-------------|
| `count` | integer | `1` | 返回节点数量 (1-100) |
| `regular` | string | `""` | 用于筛选节点名称的正则表达式 |
| `strategy` | string | `"sequential"` | 选择策略（同单节点端点） |
| `latency_weight` | string | `"0.5"` | 延迟权重（用于加权策略） |
| `success_rate_weight` | string | `"0.5"` | 成功率权重（用于加权策略） |
| `weight_mode` | string | `"add"` | 权重计算模式 |
| `weighted_random` | boolean | `false` | 加权随机选择 |

**响应 (200 OK):**

```json
{
  "nodes": [
    {
      "tag": "node-1",
      "name": "台湾节点",
      "proxy_url": "http://user:pass@1.2.3.4:24000",
      "latency_ms": 150
    },
    {
      "tag": "node-2",
      "name": "香港节点",
      "proxy_url": "http://user:pass@1.2.3.4:24001",
      "latency_ms": 120
    }
  ]
}
```

**示例：**

```bash
# 获取 3 个最佳节点
curl "http://localhost:9090/api/nodes/get_available_nodes?count=3"

# 获取 5 个随机的台湾节点
curl "http://localhost:9090/api/nodes/get_available_nodes?count=5&strategy=random&regular=.*台湾.*"
```

---

### 导出

#### 导出代理 URI

GET `/api/export`

导出所有可用节点为 HTTP 代理 URI（每行一个）。

**响应 (200 OK, text/plain):**

```
http://user:pass@1.2.3.4:24000
http://user:pass@1.2.3.4:24001
http://user:pass@1.2.3.4:24002
```

**响应头：**
- `Content-Type: text/plain; charset=utf-8`
- `Content-Disposition: attachment; filename=proxy_pool.txt`

---

### 订阅管理

#### 获取订阅状态

GET `/api/subscription/status`

返回当前订阅刷新状态。

**响应 (200 OK):**

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

**响应 (200 OK - 未启用):**

```json
{
  "enabled": false,
  "message": "订阅刷新未启用"
}
```

#### 刷新订阅

POST `/api/subscription/refresh`

立即触发订阅刷新。**⚠️ 这会重启 sing-box 内核并中断所有连接。**

**响应 (200 OK):**

```json
{
  "message": "刷新成功",
  "node_count": 10
}
```

**响应 (500 Internal Server Error):**

```json
{
  "error": "获取订阅失败: timeout"
}
```

---

### 虚拟池管理

#### 获取虚拟池状态

GET `/api/virtual_pools/status`

返回所有虚拟池的状态信息。

**响应 (200 OK):**

```json
{
  "pools": [
    {
      "name": "US_Pool",
      "regular": ".*美国.*",
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

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 虚拟池名称 |
| `regular` | string | 节点名称匹配的正则表达式 |
| `address` | string | 监听地址 |
| `port` | uint16 | 监听端口 |
| `username` | string | 代理认证用户名（可选） |
| `password` | string | 代理认证密码（可选） |
| `strategy` | string | 负载均衡策略（sequential/random/balance） |
| `max_latency_ms` | int | 最大延迟阈值（毫秒），0 表示无限制 |
| `node_count` | int | 匹配的节点数量 |
| `running` | bool | 虚拟池是否正在运行 |

#### 获取虚拟池节点列表

GET `/api/virtual_pools/{name}/nodes`

返回指定虚拟池匹配的节点列表。

**路径参数：**
- `name` - 虚拟池名称（需 URL 编码）

**响应 (200 OK):**

```json
{
  "pool_name": "US_Pool",
  "nodes": [
    {
      "tag": "node-1",
      "name": "美国节点1",
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

**响应 (404 Not Found):**

```json
{
  "error": "虚拟池不存在"
}
```

---

### 配置重载

#### 重载配置

POST `/api/reload`

触发配置重载。**⚠️ 这会重启 sing-box 内核并中断所有连接。**

**响应 (200 OK):**

```json
{
  "message": "重载成功，现有连接已被中断"
}
```

**响应 (500 Internal Server Error):**

```json
{
  "error": "配置错误: ..."
}
```

---

## 错误码

| HTTP 状态码 | 说明 |
|-------------|-------------|
| 200 | 成功 |
| 400 | 错误请求（参数无效） |
| 401 | 未授权（缺少或无效的身份认证） |
| 404 | 未找到（资源不存在） |
| 405 | 方法不允许 |
| 500 | 内部服务器错误 |
| 503 | 服务不可用（功能未启用） |

---

## 常见错误响应

```json
{
  "error": "错误信息"
}
```

---

## 示例

### 完整流程：添加节点、探测并导出

```bash
# 1. 登录
TOKEN=$(curl -s -X POST http://localhost:9090/api/auth \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' \
  | jq -r '.token')

# 2. 添加新节点
curl -X POST http://localhost:9090/api/nodes/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "uri": "vless://uuid@server:443?security=reality&sni=example.com#新节点"
  }'

# 3. 重载配置
curl -X POST http://localhost:9090/api/reload \
  -H "Authorization: Bearer $TOKEN"

# 4. 探测新节点
curl -X POST http://localhost:9090/api/nodes/%E6%96%B0%E8%8A%82%E7%82%B9/probe \
  -H "Authorization: Bearer $TOKEN"

# 5. 导出可用节点
curl http://localhost:9090/api/export \
  -H "Authorization: Bearer $TOKEN"
```

### 加权节点选择

```bash
# 选择 3 个延迟最低的节点（延迟权重 = 1，成功率权重 = 0）
curl "http://localhost:9090/api/nodes/get_available_nodes?count=3&strategy=weighted&latency_weight=1&success_rate_weight=0" \
  -H "Authorization: Bearer $TOKEN"

# 选择 3 个成功率最高的节点（延迟权重 = 0，成功率权重 = 1）
curl "http://localhost:9090/api/nodes/get_available_nodes?count=3&strategy=weighted&latency_weight=0&success_rate_weight=1" \
  -H "Authorization: Bearer $TOKEN"

# 加权随机选择（在延迟和成功率之间平衡）
curl "http://localhost:9090/api/nodes/get_available_node?strategy=weighted&weighted_random=true" \
  -H "Authorization: Bearer $TOKEN"
```

---

## 术语说明

| 英文术语 | 中文说明 |
|---------|---------|
| Endpoint | 端点/接口 |
| Payload | 请求体/响应体 |
| Tag | 节点标签（内部标识） |
| Probe | 探测/健康检查 |
| Blacklist | 黑名单/拉黑 |
| Sequential | 顺序轮询 |
| Round-robin | 轮询 |
| Latency | 延迟 |
| Success Rate | 成功率 |
| Weighted | 加权 |
| SSE (Server-Sent Events) | 服务器发送事件 |
