# Easy Proxies

[English](README.md) | 简体中文

基于 [sing-box](https://github.com/SagerNet/sing-box) 的代理节点池管理工具，支持多协议、多节点自动故障转移和负载均衡。

## 文档

- [API 文档（英文）](docs/api.md) - 完整的 REST API 参考文档
- [API 文档（中文）](docs/api_zh.md) - API 参考文档（中文版）

## 特性

- **多协议支持**: VMess、VLESS、Hysteria2、Shadowsocks、Trojan
- **多种传输层**: TCP、WebSocket、HTTP/2、gRPC、HTTPUpgrade
- **订阅链接支持**: 自动从订阅链接获取节点，支持 Base64、Clash YAML 等格式
- **订阅定时刷新**: 自动定时刷新订阅，支持 WebUI 手动触发（⚠️ 刷新会导致连接中断）
- **节点池模式**: 自动故障转移、负载均衡
- **多端口模式**: 每个节点独立监听端口
- **混合模式**: 同时启用节点池 + 多端口，节点状态共享同步
- **虚拟池**: 通过正则表达式筛选节点，创建多个独立的负载均衡入口
- **Web 监控面板**: 实时查看节点状态、延迟探测、一键导出节点
- **自动健康检查**: 启动时自动检测所有节点可用性，定期（5分钟）检查节点状态
- **智能节点过滤**: 自动过滤不可用节点，WebUI 和导出按延迟排序
- **节点管理**: 通过 Web UI 或 API 进行增删改查操作
- **端口保留**: 重载配置后已有节点保持原有端口不变

## 快速开始

### 1. 配置

```bash
cp config.example.yaml config.yaml
cp nodes.example nodes.txt
```

编辑 `config.yaml` 设置配置，编辑 `nodes.txt` 添加代理节点。

### 2. 运行

**Docker 方式（推荐）：**

```bash
./start.sh
```

或手动执行：

```bash
docker compose up -d
```

**本地编译运行：**

```bash
go build -tags "with_utls with_quic with_grpc" -o easy-proxies ./cmd/easy_proxies
./easy-proxies --config config.yaml
```

## 配置说明

### 基础配置

```yaml
mode: pool                    # 运行模式: pool (节点池)、multi-port (多端口) 或 hybrid (混合)
log_level: info

# 订阅链接（可选，支持多个）
subscriptions:
  - "https://example.com/subscribe"

# 管理接口
management:
  enabled: true
  listen: 0.0.0.0:9090        # Web 监控面板地址
  probe_target: www.apple.com:80  # 延迟探测目标
  password: ""                # WebUI 访问密码（可选）

# 入口配置
listener:
  address: 0.0.0.0
  port: 2323
  username: user
  password: pass

multi_port:
  address: 0.0.0.0
  base_port: 24000            # 起始端口，节点依次递增
  username: mpuser
  password: mppass

pool:
  mode: sequential            # sequential (顺序) 或 random (随机)
  failure_threshold: 3        # 失败阈值，超过后拉黑节点
  blacklist_duration: 24h     # 拉黑时长
```

### 运行模式详解

#### Pool 模式（节点池）

所有节点共享一个入口地址，程序自动选择可用节点。

**使用方式：** `http://user:pass@localhost:2323`

#### Multi-Port 模式（多端口）

每个节点独立监听一个端口，精确控制使用哪个节点。

端口从 `base_port`（默认 24000）开始自动递增。

#### Hybrid 模式（混合模式）

同时启用节点池和多端口模式，两者共享节点状态。

- 节点池入口：`http://user:pass@0.0.0.0:2323`
- 多端口入口：`http://mpuser:mppass@0.0.0.0:24000+`

### 虚拟池（Virtual Pools）

虚拟池允许通过正则表达式筛选节点，创建多个独立的负载均衡入口。适用于需要按地区、类型等分组使用节点的场景。

```yaml
virtual_pools:
  # 美国节点池
  - name: "US_Pool"
    regular: ".*美国.*"           # 正则匹配节点名称
    address: 0.0.0.0
    port: 3001
    username: ususer              # 可选认证
    password: uspass
    strategy: sequential          # sequential/random/balance

  # 低延迟节点池
  - name: "Fast_Pool"
    regular: ".*"                 # 匹配所有节点
    address: 0.0.0.0
    port: 3002
    strategy: balance
    max_latency_ms: 200           # 只选择延迟 < 200ms 的节点
```

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

`nodes.txt` 格式（每行一个）：

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

参数说明：
- `net/type`: tcp, ws, h2, grpc
- `tls/security`: tls 或空
- `scy/encryption`: auto, aes-128-gcm, chacha20-poly1305 等

**VLESS**

```
vless://uuid@server:port?encryption=none&security=reality&sni=example.com&fp=chrome&pbk=xxx&sid=xxx&type=tcp&flow=xtls-rprx-vision#名称
```

参数说明：
- `security`: none, tls, reality
- `type`: tcp, ws, http, grpc, httpupgrade
- `flow`: xtls-rprx-vision（仅 TCP）
- `fp`: 指纹（chrome, firefox, safari 等）

**Hysteria2**

```
hysteria2://password@server:port?sni=example.com&obfs=salamander&obfs-password=xxx#名称
```

参数说明：
- `upMbps` / `downMbps`: 带宽限制
- `obfs`: 混淆类型

## Web 监控面板

访问 `http://localhost:9090` 查看：

- 节点状态（健康/警告/异常/拉黑）
- 实时延迟和连接数统计
- 手动延迟探测
- 解除节点拉黑
- **一键导出节点**: 导出所有可用节点的代理池 URI
- **一键复制代理链接**: 每个节点和虚拟池卡片都有复制按钮，点击即可复制完整的代理链接到剪贴板
- **国家分类和筛选**: 节点按国家 emoji 自动分类，支持关键字筛选
- **虚拟池管理**: 查看和管理虚拟池配置，每个虚拟池通过正则表达式筛选节点
- **设置**: 修改 `external_ip` 和 `probe_target`
- **节点管理**: 通过 UI 添加、编辑、删除节点
- **订阅状态**: 查看和触发刷新

### 密码保护

在 `config.yaml` 中设置密码：

```yaml
management:
  password: "your_secure_password"
```

可通过 Web UI 登录或在 API 请求中使用 Bearer token。

### 设置管理

点击页面顶部的 ⚙️ 齿轮图标修改：

| 设置项 | 说明 |
|--------|------|
| 外部 IP 地址 | 导出节点时使用的 IP 地址（替换 `0.0.0.0`） |
| 探测目标 | 健康检查目标地址（格式：`host:port`） |

修改后立即保存到 `config.yaml`。

## API 使用

完整 API 文档：[docs/api_zh.md](docs/api_zh.md)

### 快速示例

```bash
# 获取可用节点
curl http://localhost:9090/api/nodes

# 导出代理 URI
curl http://localhost:9090/api/export

# 使用密码登录
TOKEN=$(curl -s -X POST http://localhost:9090/api/auth \
  -H "Content-Type: application/json" \
  -d '{"password": "your_password"}' | jq -r '.token')

# 添加节点
curl -X POST http://localhost:9090/api/nodes/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"uri": "vless://uuid@server:443#新节点"}'

# 重载配置
curl -X POST http://localhost:9090/api/reload \
  -H "Authorization: Bearer $TOKEN"
```

### 主要端点

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/nodes` | 列出运行时节点 |
| GET | `/api/export` | 导出代理 URI |
| GET | `/api/nodes/config` | 列出配置节点 |
| POST | `/api/nodes/config` | 添加节点 |
| PUT | `/api/nodes/config/:name` | 更新节点 |
| DELETE | `/api/nodes/config/:name` | 删除节点 |
| POST | `/api/reload` | 重载配置 |
| GET | `/api/settings` | 获取设置 |
| PUT | `/api/settings` | 更新设置 |
| GET | `/api/subscription/status` | 订阅状态 |
| POST | `/api/subscription/refresh` | 刷新订阅 |
| GET | `/api/virtual_pools/status` | 虚拟池状态 |
| GET | `/api/virtual_pools/:name/nodes` | 虚拟池节点列表 |

## 健康检查机制

- **初始检查**: 启动后立即检测所有节点的连通性
- **定期检查**: 每 5 分钟检查一次所有节点状态
- **智能过滤**: 不可用节点自动从 WebUI 和导出列表中隐藏
- **探测目标**: 通过 `management.probe_target` 配置（默认 `www.apple.com:80`）

## 订阅定时刷新

```yaml
subscription_refresh:
  enabled: true                 # 启用定时刷新
  interval: 1h                  # 刷新间隔（默认 1 小时）
  timeout: 30s                  # 获取订阅超时
  health_check_timeout: 60s     # 新节点健康检查超时
  drain_timeout: 30s            # 旧实例排空超时
  min_available_nodes: 1        # 最少可用节点数，低于此值不切换
```

> ⚠️ **重要提示：订阅刷新会导致连接中断**
>
> 订阅刷新时，程序会**重启 sing-box 内核**以加载新节点配置。这意味着：
>
> - **所有现有连接将被断开**
> - 正在进行的下载、流媒体播放等会中断
> - 客户端需要重新建立连接
>
> **建议：**
> - 将刷新间隔设置为较长时间（如 `1h` 或更长）
> - 避免在业务高峰期手动触发刷新
> - 如果对连接稳定性要求极高，建议关闭此功能（`enabled: false`）

## 端口说明

| 端口 | 用途 |
|------|------|
| 2323 | 节点池/混合模式入口 |
| 9090 | Web 监控面板 |
| 24000+ | 多端口/混合模式节点 |
| 自定义 | 虚拟池入口（在 `virtual_pools` 中配置） |

## Docker 部署

### 主机网络模式（推荐）

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

> **注意**: 配置文件需要可写权限以支持 WebUI 设置保存。如遇权限问题，请执行 `chmod 666 config.yaml nodes.txt`

### 端口映射模式

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

## 构建

```bash
# 基础构建
go build -o easy-proxies ./cmd/easy_proxies

# 完整功能构建（推荐）
go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor" -o easy-proxies.exe ./cmd/easy_proxies
```

## 许可证

MIT License
