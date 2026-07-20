# Easy Proxies

[![Docker 构建](https://github.com/arsonist-g/easy_proxies/actions/workflows/docker-build.yaml/badge.svg)](https://github.com/arsonist-g/easy_proxies/actions/workflows/docker-build.yaml)
[![Docker 镜像](https://img.shields.io/badge/docker-ghcr.io-2496ED?logo=docker&logoColor=white)](https://github.com/arsonist-g/easy_proxies/pkgs/container/easy_proxies)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![sing-box](https://img.shields.io/badge/sing--box-1.12-FF4F8B)](https://github.com/SagerNet/sing-box)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[English](README.md) | 简体中文

基于 [sing-box](https://github.com/SagerNet/sing-box) 的代理节点池管理工具，支持多协议、多节点自动故障转移和负载均衡，内置 Web 管理面板、凭证管理与 GeoIP 探测。

## 文档

- [API 文档（英文）](docs/api.md) / [API 文档（中文）](docs/api_zh.md) — 完整的 REST API 端点、请求/响应字段与错误码参考

## 特性

- **多协议支持**: VMess、VLESS、Hysteria2、Shadowsocks、Trojan
- **多种传输层**: TCP、WebSocket、HTTP/2、gRPC、HTTPUpgrade
- **节点 URI 校验**: 添加/编辑节点时校验协议是否支持、必要字段是否完整（纯解析，不查连通性，不影响正在运行的实例）
- **订阅链接支持**: 自动从订阅链接获取节点，支持 Base64、Clash YAML、纯文本、sing-box 等格式
- **订阅定时刷新**: 自动定时刷新订阅，支持 WebUI/API 手动触发（⚠️ 刷新会重启 sing-box 内核、中断连接）
- **节点池模式**: 自动故障转移、负载均衡（顺序 / 随机 / 最少连接）
- **多端口模式**: 每个节点独立监听端口
- **混合模式**: 同时启用节点池 + 多端口，节点状态共享
- **虚拟池**: 通过正则表达式筛选节点，创建多个独立的负载均衡入口；**支持 WebUI/API 动态增删改，实时生效，无需重启**
- **统一探测**: 探测走 Cloudflare `cdn-cgi/trace`，一次取得可用性 + 延迟 + 出口 IP + 国家 + ASN
- **GeoIP / ASN**: 可选接入 MaxMind GeoLite2-ASN，自动下载与周期更新，节点回填组织名
- **凭证管理**: API Key（第三方程序鉴权）与 Subscribe Token（代理列表订阅链接），AES-256-GCM 加密存储，点复制时后端解密
- **三层鉴权**: Session（WebUI）/ Bearer Token / API Key，凭证类操作受 `manage_credentials` 权限作用域保护
- **Web 管理面板**: 实时节点状态、可点列头排序、分页、延迟探测、一键导出/复制代理链接
- **国旗本地化**: 国旗 emoji 字体内联，Windows 下正常渲染；节点表/图表/筛选下拉均显示国家全名
- **自动健康检查**: 启动时检测所有节点可用性，定期（5 分钟）复查
- **智能节点过滤**: 不可用节点自动从面板和导出中隐藏
- **节点管理**: 通过 Web UI 或 API 增删改查
- **端口保留**: 重载配置后已有节点保持原有端口不变

## 快速开始

### 1. 配置

```bash
cp config.example.yaml config.yaml
cp nodes.example nodes.txt   # 可选：用节点文件而非订阅
```

编辑 `config.yaml` 设置配置，`nodes.txt` 每行一个代理 URI。

### 2. 运行

**Docker 方式（推荐）：**

```bash
./start.sh
# 或
docker compose up -d
```

**本地编译运行：**

```bash
go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor" -o easy-proxies ./cmd/easy_proxies
./easy-proxies --config config.yaml
```

> 完整功能需带齐全部 build tags（见 [构建](#构建)）。

## 配置说明

完整示例见 [`config.example.yaml`](config.example.yaml)。以下为主要配置项。

### 基础配置

```yaml
mode: pool                    # 运行模式: pool / multi-port / hybrid
log_level: info               # debug / info / warn / error
singbox_log_level: warn       # sing-box 核心日志级别（默认 warn）
external_ip: ""               # 导出代理 URI 时替换 0.0.0.0 的公网 IP
skip_cert_verify: false       # 全局跳过节点 TLS 证书验证

# 订阅链接（可选，支持多个；格式「名字:链接」，名字可省略）
subscriptions:
  - "我的订阅:https://example.com/subscribe"
  - "https://example.com/subscribe/clash"

# 管理接口
management:
  enabled: true
  listen: 0.0.0.0:9090        # Web 管理面板地址（端口可自行修改）
  probe_target: www.apple.com:80
  password: ""                # WebUI 访问密码（可选）
  path_pwd: ""                # 路径密码（可选，设置后需访问 /路径密码 才能进入面板）

# 多端口 / 混合模式入口
multi_port:
  address: 0.0.0.0
  base_port: 24000            # 起始端口，节点依次递增
  username: mpuser
  password: mppass

# 节点池配置（选择策略 + pool/hybrid 代理入口）
pool:
  address: 0.0.0.0
  port: 2323
  username: user
  password: pass
  mode: sequential            # sequential / random / balance
  failure_threshold: 3
  blacklist_duration: 24h
```

### GeoIP / ASN（可选）

配置后，节点探测到出口 IP 时回填 ASN 与组织名。探测已统一走 Cloudflare `cdn-cgi/trace`（可用性 + 延迟 + 出口 IP + 国家 + ASN 一次取得）。

```yaml
geoip:
  asn_database: ""            # 本地 GeoLite2-ASN.mmdb 路径；空=禁用 ASN 查询
  account_id: ""              # MaxMind Account ID（启用自动更新需填写）
  license_key: ""             # MaxMind License Key
  edition_id: "GeoLite2-ASN"
  update_interval: 24h        # 自动检查更新间隔
```

填了 `license_key` 即启用自动更新：启动缺库则下载，有库则按 `If-Modified-Since` 周期检查（MaxMind 每周二发版）。

### 运行模式

#### Pool 模式（节点池）

所有节点共享一个入口，自动选择可用节点。

**使用方式：** `http://user:pass@localhost:2323`

#### Multi-Port 模式（多端口）

每个节点独立监听一个端口，精确控制使用哪个节点。端口从 `base_port`（默认 24000）递增。

#### Hybrid 模式（混合）

同时启用节点池和多端口，共享节点状态。

- 节点池入口：`http://user:pass@0.0.0.0:2323`
- 多端口入口：`http://mpuser:mppass@0.0.0.0:24000+`

### 虚拟池（Virtual Pools）

虚拟池通过正则表达式筛选节点，创建独立的负载均衡入口，适用于按地区/类型分组使用节点。

**配置方式有两种，且都支持运行时动态管理：**

1. **YAML 静态配置**（启动时加载）：

```yaml
virtual_pool:
  base_port: 30000            # 新建虚拟池的端口分配起点（必填）

virtual_pools:
  - name: "US_Pool"
    regular: ".*美国.*"        # 正则匹配节点名称
    address: 0.0.0.0
    port: 3001                # 留空则从 base_port 自动分配
    username: ususer          # 可选认证
    password: uspass
    strategy: sequential      # sequential / random / balance

  - name: "Fast_Pool"
    regular: ".*"
    address: 0.0.0.0
    port: 3002
    strategy: balance
    max_latency_ms: 200       # 只选延迟 < 200ms 的节点
```

2. **WebUI / API 动态管理**（推荐）：

在管理面板「虚拟池」页或通过 `POST /api/v1/virtual-pools` 增删改虚拟池。

> **虚拟池是独立于 sing-box 的前置代理层**，CRUD（创建/修改/删除）**实时生效**——接口返回成功时，对应端口的 TCP 监听器已经开/关完毕，**无需重启进程或容器，也无需调用任何 reload 接口**。增删改同时落盘到 `config.yaml` 的 `virtual_pools` 段（便于二次编辑可见）。

**使用方式：** `http://ususer:uspass@localhost:3001`

**负载均衡策略：**
- `sequential`: 顺序轮询（默认）
- `random`: 随机选择
- `balance`: 最少连接数优先

### 节点配置

**方式 1: 使用订阅链接**

```yaml
subscriptions:
  - "https://example.com/subscribe/v2ray"
  - "https://example.com/subscribe/clash"
```

**方式 2: 使用节点文件**

```yaml
nodes_file: nodes.txt
```

`nodes.txt` 格式（每行一个 URI）：

```
vless://uuid@server:443?security=reality&sni=example.com#节点名称
hysteria2://password@server:443?sni=example.com#HY2节点
ss://base64@server:8388#SS节点
```

**方式 3: 直接在配置文件中**

```yaml
nodes:
  - uri: "vless://uuid@server:443#节点1"
  - name: custom-name
    uri: "ss://base64@server:8388"
    port: 24001  # 可选，手动指定端口
```

## 支持的协议

| 协议 | URI 格式 | 特性 |
|------|----------|------|
| VMess | `vmess://` | WebSocket、HTTP/2、gRPC、TLS |
| VLESS | `vless://` | Reality、XTLS-Vision、多传输层 |
| Hysteria2 | `hysteria2://` | 带宽控制、混淆 |
| Shadowsocks | `ss://` | 多加密方式 |
| Trojan | `trojan://` | TLS、多传输层 |

### 协议详解

**VMess**

```
vmess://uuid@server:port?encryption=auto&security=tls&sni=example.com&type=ws&host=example.com&path=/path#名称
```

参数：`net/type`（tcp, ws, h2, grpc）、`tls/security`（tls 或空）、`scy/encryption`（auto, aes-128-gcm, chacha20-poly1305）

**VLESS**

```
vless://uuid@server:port?encryption=none&security=reality&sni=example.com&fp=chrome&pbk=xxx&sid=xxx&type=tcp&flow=xtls-rprx-vision#名称
```

参数：`security`（none, tls, reality）、`type`（tcp, ws, http, grpc, httpupgrade）、`flow`（xtls-rprx-vision，仅 TCP）、`fp`（chrome, firefox, safari）

**Hysteria2**

```
hysteria2://password@server:port?sni=example.com&obfs=salamander&obfs-password=xxx#名称
```

参数：`upMbps`/`downMbps`（带宽限制）、`obfs`（混淆类型）

> 添加节点时会校验协议是否在上述支持范围内、必要字段（如 VLESS 的 uuid）是否完整。不支持协议或字段缺失将返回 `422 validation_error` 并给出具体原因（如 `unsupported scheme "ssr"`、`vless uri missing uuid`），**不会被保存，也不影响正在运行的代理实例**。

## Web 管理面板

访问管理面板（默认 `http://localhost:9090`，端口即 `management.listen`）：

- 节点状态（健康 / 警告 / 异常 / 拉黑）、延迟、可用率、国家、ASN
- 可点列头排序（节点/国家/协议/端口/延迟/可用率），分页浏览
- 手动单节点探测 / 全节点探测、解除拉黑
- **一键导出**可用节点代理 URI、**一键复制**节点/虚拟池代理链接
- 国家国旗 + 全名显示、按国家/协议/状态/名称正则筛选
- 虚拟池管理（增删改，实时生效）
- 节点增删改查（带协议校验）
- 订阅管理与刷新
- 凭证管理（API Key / Subscribe Token）
- 设置：修改 `external_ip`、`probe_target`、告警开关等

### 密码保护

在 `config.yaml` 设置：

```yaml
management:
  password: "your_secure_password"
  path_pwd: "secret"          # 可选：访问 /secret 才能进入面板
```

### 凭证管理

面板「凭证」页管理两类凭证，均以 **AES-256-GCM 密文**存储在数据库中，加密密钥保存在 `config.yaml` 的 `credential_key`（首次启动自动生成），与数据库分离：

- **API Key**：供第三方程序通过 `X-API-Key` 请求头调用管理 API，可分配权限作用域（scope）。
- **Subscribe Token**：生成一个独立的代理列表订阅链接 `http://<host>/sub/<token>`，供第三方平台拉取节点列表，支持按国家等条件过滤。

列表只返回 Key/Token 的**前缀**；点击「复制」时调用 `GET /api/v1/api-keys/{id}/plain`（或 `subscribe-tokens/{id}/plain`），后端解密密文返回明文，前端复制到剪贴板。换浏览器/换设备只需登录即可复制，不依赖本地缓存。

## API 接口

所有接口在 `/api/v1` 下。除 `POST /api/v1/auth/login` 外均需鉴权。

### 鉴权方式

1. **Session Cookie**（WebUI）：登录后写入 `ep_session` cookie。
2. **Bearer Token**：`Authorization: Bearer <token>`。
3. **API Key**：`X-API-Key: <key>`（受其 scope 限制）。

### 快速示例

```bash
# 登录获取 token
TOKEN=$(curl -s -X POST http://localhost:9090/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' | jq -r '.token')

# 列出节点（支持排序/分页/筛选）
curl "http://localhost:9090/api/v1/nodes?sort=-latency&page=1&page_size=20" \
  -H "Authorization: Bearer $TOKEN"

# 添加节点（带协议校验）
curl -X POST http://localhost:9090/api/v1/nodes \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"新节点","uri":"vless://uuid@server:443#新节点"}'

# 导出代理 URI
curl http://localhost:9090/api/v1/export -H "Authorization: Bearer $TOKEN"
```

完整的端点清单、请求/响应字段、分页与错误码规范见 **[API 文档（中文）](docs/api_zh.md)**。

## 健康检查机制

- **统一探测**: 通过 Cloudflare `cdn-cgi/trace` 一次取得可用性、延迟、出口 IP、国家、ASN
- **初始检查**: 启动后立即检测所有节点连通性
- **定期检查**: 每 5 分钟复查
- **智能过滤**: 不可用节点自动从面板和导出隐藏
- **探测目标**: 通过 `management.probe_target` 配置（默认 `www.apple.com:80`）

## 订阅定时刷新

```yaml
subscription_refresh:
  enabled: false              # 默认关闭
  interval: 1h                # 刷新间隔
  timeout: 30s                # 获取订阅超时
  health_check_timeout: 60s   # 新节点健康检查超时
  drain_timeout: 30s          # 旧实例排空超时
  min_available_nodes: 1      # 最少可用节点数，低于此值不切换
```

> ⚠️ **刷新会重启 sing-box 内核，中断所有连接**：刷新时程序会关闭旧实例、重建并加载新节点配置，期间所有现有连接断开。建议将刷新间隔设长（如 `1h` 或更久），避免业务高峰期手动触发；对稳定性要求极高可关闭（`enabled: false`）。

> 刷新管理器始终初始化，手动刷新（全量/单订阅）与「添加订阅后自动刷新」**始终可用，不受 `subscription_refresh.enabled` 影响**。`enabled` 仅控制**定时**自动刷新——关闭时定时器不跑，但手动触发、以及添加订阅时自动触发的刷新照常工作。添加订阅后会自动触发一次后台刷新（重启内核、短暂中断连接），节点几秒后生效。

## 端口说明

| 端口 | 用途 |
|------|------|
| 2323 | 节点池 / 混合模式入口 |
| 9090 | Web 管理面板（即 `management.listen`，可自行修改） |
| 24000+ | 多端口 / 混合模式节点（从 `base_port` 递增） |
| 30000+ | 虚拟池入口（从 `virtual_pool.base_port` 自动分配） |

## Docker 部署

### 主机网络模式（推荐）

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

> 配置文件需可写以支持面板保存设置。如遇权限问题：`chmod 666 config.yaml nodes.txt`

### 端口映射模式

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

## 构建

```bash
# 完整功能构建（推荐，需带齐全部 build tags）
go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor" -o easy-proxies.exe ./cmd/easy_proxies

# 基础构建（无 QUIC/WireGuard/gVisor 等可选特性）
go build -o easy-proxies ./cmd/easy_proxies
```

## 许可证

MIT License
