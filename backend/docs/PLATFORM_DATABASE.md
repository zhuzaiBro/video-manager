# 业务平台 API 与自建 PostgreSQL

`/api/platform/*` 是 zood.work 的业务 API，跑在现有 video-manager 进程里。浏览器不再直接对业务表发 PostgREST 查询。

## 数据源

默认 `PLATFORM_DATA_SOURCE=supabase`。后端使用调用者自己的 Supabase access token 调用 Data API，anon key 只放在服务器上。服务角色密钥会被拒绝启动，不能用来绕过 RLS。

自建库是独立的第一阶段，不会搬迁 Supabase Auth、Storage 或现有数据，也不会自动切换生产库。

| 变量 | 作用 | 默认 |
| --- | --- | --- |
| `SUPABASE_URL` | 项目 URL，仅服务器 | 空则不启用平台 API |
| `SUPABASE_ANON_KEY` | 公开 anon / publishable key，仅服务器 | 空则不启用 |
| `SUPABASE_JWT_SECRET` | 校验浏览器带来的用户 JWT | 已有配置 |
| `PLATFORM_DATA_SOURCE` | `supabase` 或 `postgres` | `supabase` |
| `PLATFORM_DATABASE_URL` | 自建库连接串，仅在 `postgres` 时使用 | 空 |

不要把这些值写进仓库，也不要加 `NEXT_PUBLIC_` 前缀。`PLATFORM_DATABASE_URL` 应使用非超级用户 `platform_app`。

## 启用自建库

1. 在后端机器上安装 PostgreSQL。这一步由服务器协调方处理，不在应用启动时连接生产。
2. 创建空数据库，执行 `backend/migrations/003_platform_independent.sql` 之前，先建立与当前业务表一致的结构。本迁移只加访问策略，不复制 Supabase 数据。
3. 确认应用仍使用 `PLATFORM_DATA_SOURCE=supabase`。
4. 只有在明确验收之后，才把 `PLATFORM_DATA_SOURCE` 改成 `postgres` 并填写 `PLATFORM_DATABASE_URL`。改完需要正常发布，不要为了这份改动重启当前生产进程。

`GET /api/platform/health` 会返回当前 `dataSource`，不返回连接串或密钥。

## 仍然留在 Supabase 的能力

登录、注册、Web3 登录、会话刷新和退出仍由 Supabase Auth 处理。实时客服只把 Realtime 当作刷新信号，消息正文从平台 API 读取。邀请奖励、Telegram 客服映射和 Auth 用户补齐仍依赖服务角色或 Auth Admin API，没有迁到这里。
