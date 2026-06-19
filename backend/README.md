# Video Manager Backend

Go 后端服务，包含 API Gateway 与独立的 `video-worker` 转码服务。

## 架构

```text
Next.js → Go API → PostgreSQL
                  ↓
                Redis → Asynq → video-worker → FFmpeg → COS → CDN
```

## 服务

| 服务 | 说明 |
|------|------|
| `cmd/api` | HTTP API，处理上传、列表、播放授权 |
| `cmd/video-worker` | 独立转码 Worker，消费 Asynq 队列 |

## 快速开始

### 1. 启动依赖

```bash
docker compose up -d
```

### 2. 配置环境变量

```bash
cp backend/.env.example backend/.env
# 编辑 COS / CDN 配置
```

### 3. 安装依赖并运行

```bash
cd backend
go mod tidy

# API 服务
go run ./cmd/api

# 转码 Worker（另开终端）
go run ./cmd/video-worker
```

## API

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/api/admin/videos/upload` | 无 | 上传视频，返回 `videoId` |
| GET | `/api/videos` | 无 | 视频列表 |
| GET | `/api/videos/{id}` | 无 | 视频详情 |
| GET | `/api/videos/{id}/play` | 需要 | 获取 CDN 签名播放地址 |
| POST | `/api/videos/{id}/segments` | 需要 | 上报切片观看（+6秒配额） |

### 认证

开发环境可通过以下方式传递用户 ID：

```http
X-User-ID: 1001
```

或

```http
Authorization: Bearer 1001
```

### 播放授权响应

```json
{
  "playUrl": "https://cdn.xxx.com/videos/1001/index.m3u8?sign=xxxxx&t=1711111111",
  "expireAt": 1711111111
}
```

### 观看配额超限

```http
HTTP/1.1 403 Forbidden

{
  "code": "WATCH_LIMIT_EXCEEDED",
  "message": "Daily watch limit exceeded"
}
```

## 数据库

使用标准 PostgreSQL，初始化脚本位于 `backend/migrations/001_init.sql`。

## Redis 设计

- 观看时长：`video:usage:{date}:{userId}`，TTL 48h
- 在线设备：`video:online:{userId}`，TTL 60s

## 转码流程

1. 上传 MP4 → 创建 `waiting` 记录
2. 推送 `video:transcode` 任务到 Asynq
3. Worker：`ffprobe` 分析 → `ffmpeg -c copy` 生成 HLS fMP4 → 上传 COS → 更新为 `ready`
