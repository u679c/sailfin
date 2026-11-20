# API v1

当前部署为单机模式，Core 随启动初始化内置探针。部署版公开站点、统计与本机管理接口。旧版远程 Agent、注册令牌和直连 Browser 采集路由仅保留在兼容测试模式。

- `GET /sites` 列出已经收到数据的站点。
- `GET /system/probe` 查看本机探针状态及心跳。
- `PATCH /system/probe` 接收 `{enabled: boolean}`，暂停或恢复采集，需要管理员会话和 CSRF。
- Nginx 的浏览器上报经本机探针进入 Core。探针内部的首批事件和站点记录在同一事务提交。

所有路径以 `/api/v1` 开头。JSON 成功响应；失败为 `{ "error": "..." }`，HTTP 400/401/403/404/429/500/503。管理 API 用会话 Cookie；修改操作需 `X-CSRF-Token`。登录响应或 `GET /auth/me` 获取 CSRF 值；浏览器在同源调用时自动携带 Cookie。

| 方法和路径 | 用途 |
|---|---|
| GET /health | Core + SQLite 健康 |
| POST /auth/login | `{name,password}` |
| POST /auth/password | `{currentPassword,newPassword}`，撤销其他会话 |
| GET /auth/me | 当前用户和 CSRF（数组） |
| POST /auth/logout | 撤销会话 |
| GET /projects[/{id}] | 项目列表/记录数组 |
| POST /projects | 新建项目 |
| PATCH /projects/{id} | 保存完整项目配置 |
| DELETE /projects/{id} | `{confirmName}`，删除项目内数据、Agent 与配置 |
| GET /projects/{id}/config | 浏览器采集策略历史；载入旧值后 PATCH 保存新版本 |
| DELETE /agents/{id} | `{confirmName}`，删除身份及配置，保留历史事件 |
| GET /agents[/{id}] | Agent 列表/记录数组，支持 projectId |
| POST /agents/enrollment | `{projectId,name}` → `{token,expiresAt}`，10 分钟单次令牌 |
| PATCH /agents/{id} | `{enabled,revoke?}`，revoke 永久撤销现有凭据 |
| GET /agents/{id}/config | 最近 20 个配置版本 |
| PUT /agents/{id}/config | 保存配置；Core 分配递增 version |
| GET /agents/{id}/metrics | 最新真实指标 |
| GET /agents/{id}/logs | 最近 100 个上报错误 |
| GET /events | 分页事件 |
| GET /sessions | 按项目和匿名 ID 聚合会话 |
| GET /sessions/{id}?projectId=... | 必须带项目 ID，分页时间线 |
| GET /stats/overview | PV/UV、HTTP 请求、事件、会话、错误和可观测停留 |
| GET /stats/trends | 按 UTC 小时分组 |
| GET /stats/devices | 设备/浏览器/OS 分组 |
| GET /stats/pages | 路径分组 |
| GET /security/summary | 限流/拒绝计数、UTC 今日配额、管理审计 |
| GET /system/status | 服务与数据库状态 |
| POST /system/backup | 下载 SQLite 一致性快照 |

项目写入字段：`name, description, domains: string[], status: active|paused|archived, retentionDays, dailyQuota, browserEnabled, collection`。PATCH 当前按完整表单保存，不能仅传一个字段。域名列表实际为精确 Origin，含 scheme 与可选端口，不带尾斜线/路径。暂停和归档均拒绝新采集，已有数据保留至保留期。

`collection` 支持：`eventTypes:string[], sampleRate:0..1, excludePaths:string[], excludeElements:string[], batchSize:1..100, intervalSeconds:1..100`。

Host 配置：
```json
{"enabled":true,"sources":[{"id":"access","path":"/absolute/logs/access.log","parser":"nginx"}],"dockerTargets":[],"batchSize":50,"intervalSeconds":5}
```

Agent API 独立使用 `Authorization: Bearer <运行凭据>`；`POST /agent/enroll` 仅使用 `{token}` 引导，响应为 `{agentId,projectId,token}`。运行凭据仅成功注册时返回一次。

- POST /agent/heartbeat、POST /agent/metrics：hostname/os/arch/version/containers/metrics/error。
- GET /agent/config：当前版本化配置。
- POST /agent/events/batch：`{events:[...]}`；只允许 http_request，禁止为日志编造 visitorId/sessionId。

Browser API：POST `/collect/browser?projectId=...`，`{events:[...]}`；GET `/collection/config?projectId=...`。独立于管理登录，需要开启项目及精确 Origin，支持受限 CORS。

事件字段：`eventId, projectId, sessionId, visitorId, eventType, timestamp(Unix ms), path, properties, ua?`。Browser UA 使用 HTTP Header 覆盖，Host UA 来自访问日志。服务端维护 received_at/source_type/agent_id，不接受客户端声明。

事件类型与属性白名单见 `internal/core/events.go`。每批 1–100，HTTP Body 256 KiB，单事件 8 KiB，禁止嵌套属性、查询参数与任意字段。eventId 按项目去重。接受最多 31 天前至未来 5 分钟事件；统计只纳入查询范围。

查询支持 `projectId,agentId,eventType,from,to,page,limit`，from/to 为 Unix ms；默认最近 24 小时、最多 366 天，page 从 1 开始、limit 默认 50 最大 200。会话详情仅按项目和会话标识分页，不套用列表的时间过滤。

PV 只计 browser page_view/route_change；日志 HTTP 请求不会提升 PV；UV 是项目内匿名 visitorId 去重；活跃会话是最近 30 分钟且在查询范围内观测到活动的会话。均不表示真实自然人数。
