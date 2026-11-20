# 业务平台 API、发布位置与自建 PostgreSQL

`/api/platform/*` 加在现有 Go 服务 `video-manager` 里，和视频上传、播放接口同一个进程。zood.work 通过 `lib/videoApi.ts` 和 `lib/platform/http.ts` 访问它。两个文件的默认地址都是 `https://video-api.zood.work`，可以用 `NEXT_PUBLIC_PLATFORM_API_BASE_URL` 或 `NEXT_PUBLIC_VIDEO_API_BASE_URL` 分开覆盖。

## 这段代码现在不会自动发布

`.github/workflows/deploy.yml` 只在推送到 `main` 时执行。它用 GitHub secret 里的 `SSH_HOST`、`SSH_USER`、`SSH_PRIVATE_KEY` 和 `DEPLOY_PATH` 登录，在目标目录执行 `git fetch && git reset --hard origin/main && ./app.sh redeploy`。仓库没有把 `biz.zood.work` 写成 HTTP 服务地址。`biz.zood.work` 是一台跑多个站点的宝塔 Nginx 主机，不能据此认为该域名已经提供 `/api/platform`。当前 PR 没有合并到 `main`，因此生产上的 video-api 还没有这些路由。

## 身份和隔离的管理客户端

普通读写使用调用者自己的 Supabase access token，anon key 只放在服务器。`internal/platform/supabase/client.go` 拒绝 service role。

邀请奖励、跨用户会员、Auth 用户补齐、全站学习统计和 Telegram 客服映射使用另一个类型 `Admin`。它只在 `SUPABASE_SERVICE_ROLE_KEY` 的 JWT role 确认为 `service_role` 时创建，并且没有通用查询方法。业务代码必须先用用户 JWT 确认管理员或本人，Telegram webhook 必须先比对 `TELEGRAM_WEBHOOK_SECRET`，然后才能调用具名方法。未配置时这些接口返回 503，不会退回用用户 JWT 假装成功。

| 变量 | 作用 | 默认 |
| --- | --- | --- |
| `SUPABASE_URL` | 项目 URL，仅服务器 | 空则不启用平台 API |
| `SUPABASE_ANON_KEY` | anon 或 publishable key，仅服务器 | 空则不启用 |
| `SUPABASE_JWT_SECRET` | 校验用户 JWT | 已有配置 |
| `SUPABASE_SERVICE_ROLE_KEY` | 只给隔离管理客户端 | 空则特权接口 503 |
| `TELEGRAM_BOT_TOKEN` | 后端代发客服消息 | 空则只保存站内消息 |
| `TELEGRAM_WEBHOOK_SECRET` | Telegram webhook 共享密钥 | 空则 webhook 401 |
| `TELEGRAM_SETUP_SECRET` | `/start` 绑定客服口令 | 空则不能绑定 |
| `PLATFORM_DATA_SOURCE` | `supabase` 或 `postgres` | `supabase` |
| `PLATFORM_DATABASE_URL` | 自建库，仅 `postgres` 时使用 | 空 |

不要把这些值写进仓库或 `NEXT_PUBLIC_`。本仓库没有读取 `127.0.0.1:55432`，也没有切换数据源。

## 自建库

自建 PostgreSQL 仍然独立存在。不要把 Supabase 备份恢复进生产库，也不要为了这份改动把 `PLATFORM_DATA_SOURCE` 改成 `postgres`。`backend/migrations/003_platform_independent.sql` 只准备访问策略，不复制数据。
