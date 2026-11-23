# 业务平台 API、发布位置与自建 PostgreSQL

`/api/platform/*` 加在现有 Go 服务 `video-manager` 里，和视频上传、播放接口同一个进程。zood.work 通过 `lib/videoApi.ts` 和 `lib/platform/http.ts` 访问它。两个文件的默认地址都是 `https://video-api.zood.work`，可以用 `NEXT_PUBLIC_PLATFORM_API_BASE_URL` 或 `NEXT_PUBLIC_VIDEO_API_BASE_URL` 分开覆盖。

## 这段代码现在不会自动发布

`.github/workflows/deploy.yml` 只在推送到 `main` 时执行。它用 GitHub secret 里的 `SSH_HOST`、`SSH_USER`、`SSH_PRIVATE_KEY` 和 `DEPLOY_PATH` 登录，在目标目录执行 `git fetch && git reset --hard origin/main && ./app.sh redeploy`。仓库没有把 `biz.zood.work` 写成 HTTP 服务地址。`biz.zood.work` 是一台跑多个站点的宝塔 Nginx 主机，不能据此认为该域名已经提供 `/api/platform`。当前 PR 没有合并到 `main`，因此生产上的 video-api 还没有这些路由。

## 数据访问边界

平台读写只有一条路径：现有 `/api/platform/*` handler 和 `platform.Service`，底下是 `internal/platform/postgres`。进程不再创建 Supabase HTTP 客户端。视频上传和播放仍用原来的 `DATABASE_URL` 连接池，平台用 `PLATFORM_DATABASE_URL`，两个地址相同则平台接口不启动。

登录只使用新的 Supabase Auth 项目。后端校验 `iss`、`aud=authenticated` 和签名：非对称密钥来自该项目的 JWKS，旧的共享密钥只接受 HS256。`amnksdnwvwmmmvqoxzyj` 一律拒绝。事务里只把已验证的 `sub` 和 `role` 写进 `request.jwt.claims`。管理员身份只读本地 `user_profiles.is_admin`，不读 `user_metadata`。

跨用户会员、邀请、资料补齐和学习统计仍先做原来的身份检查，再调用数据库函数，函数内部用 `auth.uid()` 复核管理员或本人，并且不会接受客户端提交的 `is_admin`。

| 变量 | 作用 |
| --- | --- |
| `SUPABASE_URL` | 只能是 `https://gmfuorleragagsqbkajh.supabase.co` |
| `SUPABASE_JWKS_URL` | 可选，默认同项目 `.../auth/v1/.well-known/jwks.json` |
| `SUPABASE_JWT_SECRET` | 仅当该项目仍用 HS256 时需要 |
| `PLATFORM_DATA_SOURCE` | 必须是 `postgres` |
| `PLATFORM_DATABASE_URL` | 本地业务库，不能等于视频库 |
| `DATABASE_URL` | 现有视频库，不要改指到空的 zood 库 |

`003_platform_independent.sql` 不要应用到已有 `auth.uid()` 策略的恢复库。Storage、Realtime 和视频库不迁到新 Supabase。
