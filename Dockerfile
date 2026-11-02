# ==========================================
# Stage 1: 前端构建 (React + HeroUI + Tailwind)
# ==========================================
FROM node:22-alpine AS web-builder
WORKDIR /app/web-src

# 利用 Docker 缓存层预装前端依赖
COPY web-src/package*.json ./
RUN npm ci || npm install

# 复制前端源码并打包至 cmd/cdtd/web/dist
COPY web-src/ ./
RUN npm run build

# ==========================================
# Stage 2: 后端构建 (Go 静态编译 + 资源内嵌)
# ==========================================
FROM golang:1.24-alpine AS go-builder
WORKDIR /app

# 预拉取 Go 模块缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制后端源码
COPY . .

# 将前端构建产物拷入内嵌目录（确保 //go:embed web/dist 完整可用）
COPY --from=web-builder /app/cmd/cdtd/web/dist ./cmd/cdtd/web/dist

# 静态编译（CGO_ENABLED=0，内置现代纯 Go SQLite 驱动，无 libc/gcc 依赖）
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/cdtd ./cmd/cdtd

# ==========================================
# Stage 3: 最小化生产运行镜像
# ==========================================
FROM alpine:3.21

# 安装 CA 根证书（调用阿里云/Cloudflare API 必需）与时区数据
RUN apk add --no-cache ca-certificates tzdata

# 默认设置时区为亚洲/上海（UTC+8，对齐换班组日/月账单统计逻辑）
ENV TZ=Asia/Shanghai

WORKDIR /app

# 从构建镜像拷贝单个独立可执行程序
COPY --from=go-builder /app/bin/cdtd /app/cdtd

# 数据持久化目录（可挂载外部卷存放 .db 或 .json 数据文件及 master.key）
VOLUME ["/data"]

# 默认暴露管理后台端口
EXPOSE 8080

# 默认监听 0.0.0.0:8080，使用 SQLite 数据库存储在持久化目录 /data
ENTRYPOINT ["/app/cdtd"]
CMD ["-addr", "0.0.0.0:8080", "-data", "/data/cdtd-data.db"]

