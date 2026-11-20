# 业务平台 API、发布位置与自建 PostgreSQL

`/api/platform/*` 加在现有 Go 服务 `video-manager` 里，和视频上传、播放接口同一个进程。zood.work 通过 `lib/videoApi.ts` 和 `lib/platform/http.ts` 访问它。两个文件的默认地址都是 `https://video-api.zood.work`，可以用 `NEXT_PUBLIC_PLATFORM_API_BASE_URL` 或 `NEXT_PUBLIC_VIDEO_API_BASE_URL` 分开覆盖。

## 这段代码现在不会自动发布

`.github/workflows/deploy.yml` 只在推送到 `main` 时执行。它用 GitHub secret 里的 `SSH_HOST`、`SSH_USER`、`SSH_PRIVATE_KEY` 和 `DEPLOY_PATH` 登录，在目标目录执行 `git fetch && git reset --hard origin/main && ./app.sh redeploy`。仓库没有把 `biz.zood.work` 写成 HTTP 服务地址。`biz.zood.work` 是一台跑多个站点的宝塔 Nginx 主机，不能据此认为该域名已经提供 `/api/platform`。当前 PR 没有合并到 `main`，因此生产上的 video-api 还没有这些路由。

## 数据访问边界

平台读写只有一条路径：现有 `/api/platform/*` handler 和 `platform.Service`，底下是 `internal/platform/postgres`。进程不再创建 Supabase HTTP 客户端。视频上传和播放仍用原来的 `DATABASE_URL` 连接池，平台用 `PLATFORM_DATABASE_URL`，两个地址相同则平台接口不启动。

调用者身份仍由现有 JWT 校验完成。数据库事务里只把已验证的 `sub` 和 `role` 写进 `request.jwt.claims`，并 `SET LOCAL ROLE` 为 `authenticated` 或 `anon`，让恢复库里原有的 `auth.uid()` 生效。连接账号不能是超级用户，也不能 `BYPASSRLS`。

跨用户会员、邀请、资料补齐和学习统计仍走原来的 `Privileged` 接口，并且仍然先做管理员或本人检查。Supabase 管理客户端已删除，这些接口在本地库上目前返回 503，没有改成信任客户端传来的 `user_id`。

| 变量 | 作用 |
| --- | --- |
| `PLATFORM_DATA_SOURCE` | 必须是 `postgres`，其他值不启用平台 API |
| `PLATFORM_DATABASE_URL` | 本地业务库，不能等于视频库 |
| `SUPABASE_JWT_SECRET` | 只在本地校验会话 JWT，不访问 Supabase |
| `DATABASE_URL` | 只给现有视频库 |

`backend/migrations/003_platform_independent.sql` 使用 `app.user_id`，不要应用到已经有 `auth.uid()` 策略的恢复库。

登录、OAuth、Web3、Storage 上传和客服 Realtime 仍由现有前端 Supabase SDK 触发。它们不是这次数据查询边界的一部分，见发布说明，不在这里再做一套认证框架。
