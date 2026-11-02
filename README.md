# cdt-manager · 阿里云 CDT 多实例管理平台

基于 Go 原生自研实现的阿里云 ECS / CDT / BSS 接口与多实例保活、联合轮换、DDNS 动态解析管理系统。

- **零依赖与高性能**：核心业务与阿里云 RPC V1 签名（HMAC-SHA1）、协议构造、错误解析全部由 Go 原生自主实现，不依赖任何第三方云厂商 SDK。
- **双存储引擎支持**：支持纯文件 **JSON** 与关系型 **SQLite** 双持久化存储，无需部署独立数据库服务。
- **现代化响应式 Web 控制台**：基于 React 19、HeroUI 与 TailwindCSS 构建，支持深色/浅色模式无缝切换与移动端自适应。
- **安全可靠**：敏感云凭证采用 **AES-256-GCM** 加密落盘（由独立 `master.key` 保护），管理员账户使用 **PBKDF2-SHA256**（260,000 轮）高强度加盐哈希存储与常量时间防时序攻击校验。
- **多平台与容器支持**：原生无 CGO 依赖，支持静态单文件分发，官方提供多架构 Docker 镜像（支持 AMD64 / ARM64）。

---

## 核心功能矩阵

| 功能模块 | 说明 |
|---|---|
| **多实例仪表盘** | 实时汇总各实例运行状态、当月 CDT 互联网流量、账户可用余额、本月账单，支持实例启停（节省停机自动降级）与明细刷新 |
| **单机保活引擎** | 目标实例发生意外停机时自动尝试重新启动，提供指数递增与里程碑进度追踪，具备防冲突让位机制 |
| **联合换班组** | 支持多台实例按小时轮换或按月排班，内置**流量超限自动转移**、**起不来故障转移**、**全员熔断保护**及**限额关机** |
| **Cloudflare DDNS** | 域名解析动态同步，支持按实例或换班组绑定，智能判定 IP 变化并进行免请求短路，具备失败冷却与告警去重 |
| **多通道通知告警** | 支持 Telegram（可配置反代/代理）、SMTP 邮件及通用 Webhook，针对换班、超限、故障、熔断及每日定时报告实时推送 |
| **流量趋势与采样** | 周期性自动落盘采样点，提供过去 7 天连续流量趋势曲线图（纯 SVG 零依赖绘制） |
| **身份与权限隔离** | 支持引导式管理员初始化向导，提供安全 Cookie 会话管理、改密失效与 API Bearer Token 访问控制 |

---

## 快速上手

### 1. 使用 Docker Compose 一键启动（推荐）

创建 `docker-compose.yml` 文件：

```yaml
services:
  cdt-manager:
    image: ghcr.io/alex950808/cdt-manager:latest
    container_name: cdt-manager
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      - TZ=Asia/Shanghai
      # 可选：API 鉴权 Token
      # - CDTD_TOKEN=your_secret_token
      # 可选：首次启动自动初始化管理员账户（密码不为空）
      # - CDTD_ADMIN_USERNAME=admin
      # - CDTD_ADMIN_PASSWORD=your_secure_password
```

启动容器：
```bash
docker compose up -d
```
打开浏览器访问 `http://你的服务器IP:8080` 即可进入管理后台。首次启动时会弹出管理员初始化向导。

---

### 2. 使用 Docker CLI 直接运行

```bash
docker run -d --name cdt-manager \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  -e TZ=Asia/Shanghai \
  --restart unless-stopped \
  ghcr.io/alex950808/cdt-manager:latest
```

---

### 3. 本地编译与运行

#### 环境要求
- Go 1.22 或更高版本
- Node.js 20+（仅需重新编译前端时用到，发布二进制已内嵌构建产物）

#### 编译与启动
```bash
# 1. 编译前端（产物输出至 cmd/cdtd/web/dist）
npm --prefix web-src install
npm --prefix web-src run build

# 2. 编译后端（静态嵌入前端资源）
go build -o bin/cdtd ./cmd/cdtd

# 3. 启动管理服务（默认使用 SQLite 数据库存储）
./bin/cdtd -addr 0.0.0.0:8080 -data cdtd-data.db

# 亦可指定使用传统 JSON 存储：
# ./bin/cdtd -addr 0.0.0.0:8080 -data cdtd-data.json
```

---

## 项目组件说明

| 组件 | 位置 | 用途 |
|---|---|---|
| `cdtd` | `cmd/cdtd` | **主管理后台**（Web 界面 + REST API + 调度引擎 + 存储层） |
| `cdtctl` | `cmd/cdtctl` | **命令行运维工具**（支持命令行下直接查询实例、启停、查流量与查账单） |
| `mockaliyun` | `cmd/mockaliyun` | **本地模拟阿里云端点**（用于无凭证离线联调、故障注入与回归测试） |

---

## 命令行工具 cdtctl 使用示例

```bash
export ALIYUN_ACCESS_KEY_ID="你的AccessKeyID"
export ALIYUN_ACCESS_KEY_SECRET="你的AccessKeySecret"
export ALIYUN_REGION="cn-hongkong"
export ALIYUN_INSTANCE_ID="i-xxxxxxxxxx"
export ALIYUN_SITE="china" # china 或 international

# 查询实例状态 / 公网 IP / 当前停机模式
cdtctl status

# 启动实例
cdtctl start

# 停止实例（默认优先节省停机，--plain 为普通停机）
cdtctl stop
cdtctl stop --plain

# 查询当月 CDT 互联网流量 (GB)
cdtctl traffic

# 查询账户可用余额
cdtctl balance

# 查询实例月度账单
cdtctl bill --cycle 2026-11
```

---

## 接口与能力清单

| 产品 | 阿里云 Action | 接入点 | 核心能力 |
|---|---|---|---|
| **ECS** | `DescribeInstances` | `ecs.{region}.aliyuncs.com` | 读取实例运行状态、停机模式、弹性公网 IP 与原生公网 IP |
| **ECS** | `StartInstances` | `ecs.{region}.aliyuncs.com` | 启动实例；支持库存不足（`InsufficientInventory`）等错误类型识别 |
| **ECS** | `StopInstances` | `ecs.{region}.aliyuncs.com` | 停止实例；默认使用强制停止（`ForceStop=true`），优先执行节省停机，若实例规格或账号不支持则智能降级为普通停机并回报实际模式 |
| **CDT** | `ListCdtInternetTraffic` | `cdt.aliyuncs.com` | 获取跨地域互联网流量明细，自动翻页累加并换算为 GB，精确区分空账期与 0 流量 |
| **BSS** | `QueryAccountBalance` | `business.aliyuncs.com` | 查询账户可用余额与币种；针对中国站/国际站 AK 误配支持自动切换端点重试并回报真实站点 |
| **BSS** | `DescribeInstanceBill` | `business.aliyuncs.com` | 月度账单明细汇总，自动处理分页累加税前扣费金额 |

---

## 目录结构

```
.
├── alicloud/        # 自研轻量级阿里云 RPC 客户端（签名算法、ECS/CDT/BSS 接口与错误处理）
├── cloudflare/      # 自研 Cloudflare v4 REST API 客户端（DNS 记录管理）
├── cmd/
│   ├── cdtctl/      # 命令行客户端源码
│   ├── cdtd/        # 管理后台主程序（引擎状态机、REST API 路由、存储后端及内嵌 UI）
│   └── mockaliyun/  # 本地虚拟阿里云端点与故障模拟器
├── ddns/            # 动态 DNS 抽象与同步服务
├── notify/          # Telegram、SMTP 邮件、Webhook 统一告警通知驱动
├── web-src/         # 前端 React 19 + HeroUI + TailwindCSS 项目源码
├── Dockerfile       # 生产级多阶段精简容器构建文件
├── docker-compose.yml # 一键部署编排模板
└── README.md
```

---

## 单元测试与验证

本项目内置完善的自动化测试套件，涵盖签名向量锚定验证、状态机全状态演进测试、网络端点重算比对及各存储后端读写测试：

```bash
go test -v ./...
```
