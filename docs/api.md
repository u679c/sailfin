# API v1

当前为单机模式。开发时 Core 启动内置探针，安装版由 Aqua 启动宿主机探针。旧版远程 Agent、注册令牌和浏览器直连采集路由保留在兼容模式。

下文 Core API 路径以 `/api/v1` 开头，浏览器同源中继使用 `/__sailfin/`。管理接口通常返回 JSON，备份接口返回 SQLite 文件。Core 业务错误使用 `{ "error": "..." }`。管理接口使用会话 Cookie，已认证的修改操作需要 `X-CSRF-Token`。登录响应或 `GET /auth/me` 提供 CSRF 值。

## 当前本机管理 API

| 方法和路径 | 用途 |
|---|---|
| GET /sites | 已启用的站点目录，按最近收到数据排序，最多 500 条。未收到数据的站点仍会返回 |
| GET /sites/{id}/elements | 读取站点的 `{clickSelectors,clickDescriptions,browserConfigured}` |
| PUT /sites/{id}/elements | 保存 `{clickSelectors,clickDescriptions?,previous?}`，空数组清除补充规则 |
| GET /system/sites | 站点登记、采集来源、启用状态和 SDK 检查结果 |
| GET /system/site-logs | 探针发现的已授权访问日志路径 |
| POST /system/sites | 以 `{id,name,accessLog?,browserOrigin?,requireConsent?}` 新增或编辑站点。新站点默认关闭采集 |
| PATCH /system/sites/{id} | 以 `{enabled}` 启停已接入的站点，路径 ID 取登记响应的 `id` |
| GET /system/probe | 本机探针 ID、名称、状态、主机、版本、心跳和指标 |
| PATCH /system/probe | `{enabled}`，暂停或恢复全部采集 |
| POST /system/nginx/scan | 点击「扫描站点」获取预览，并登记可接入的候选站点 |
| POST /system/nginx/apply | `{id,siteIds}`，其中 `id` 为预览 ID，`siteIds` 为结果中选中的 1–50 个 `siteId`。应用时备份、校验并请求重载 |
| GET /health | Core + SQLite 健康 |
| POST /auth/login | `{name,password}` |
| POST /auth/password | `{currentPassword,newPassword}`，撤销其他会话 |
| GET /auth/me | 当前用户和 CSRF（数组） |
| POST /auth/logout | 撤销会话 |
| GET /events | 分页事件 |
| GET /sessions | 按项目和匿名 ID 聚合会话 |
| GET /sessions/{id}?projectId=... | 必须带项目 ID，分页时间线 |
| GET /stats/overview | PV/UV、HTTP 请求、事件、会话、错误和可观测停留 |
| GET /stats/trends | 按 UTC 小时分组 |
| GET /stats/devices | 设备/浏览器/OS 分组 |
| GET /stats/pages | 页面分组，浏览器优先使用 pagePath（保留 Hash 路由），HTTP 请求使用原始路径 |
| GET /stats/clicks | 按站点、页面和元素聚合点击，按点击次数降序分页 |
| GET /security/summary | 限流/拒绝计数、UTC 今日配额、管理审计 |
| GET /security/audit | 分页管理审计 |
| GET /system/status | 服务与数据库状态、统计时区、网页更新开关 |
| PATCH /system/timezone | 以 `{timezone}` 设置统计时区，如 `UTC` 或 `Asia/Shanghai` |
| POST /system/update/verify-password | 以 `{password}` 验证当前管理员密码 |
| POST /system/update/install | 按 `password`、`package` 的顺序上传 multipart 表单，包最大 256 MiB。验签通过后返回 HTTP 202 和更新任务 |
| GET /system/update/status/{id} | 查询更新任务，返回 `id,name,status,time,message?` |
| GET /system/update/history | 分页更新记录，每个任务一条，返回 `id,created_at,package_name,action` |
| POST /system/backup | 下载 SQLite 一致性快照 |
| GET /system/backups | 服务器备份列表，返回 `name,kind,createdAt,bytes` |
| POST /system/backups | 创建手动备份，返回 HTTP 201 和备份记录 |
| GET /system/backups/{name} | 下载服务器上的备份 |
| DELETE /system/backups/{name} | 删除指定备份 |
| GET /system/backup/schedule | 读取自动备份策略和最近执行结果 |
| PUT /system/backup/schedule | 保存 `{enabled,intervalHours,keep}`，默认关闭，缺省间隔 24 小时及保留 7 份 |
| GET /system/data-retention | 读取事件保留配置、事件条数和估算数据字节数 |
| PUT /system/data-retention | 保存 `{retentionDays,maxEventMiB}`，`null` 天数沿用站点策略，两项均可用 0 停用对应清理 |

元素规则的 `clickSelectors` 为补充自动点击采集的选择器数组，`clickDescriptions` 为以选择器为键的说明映射。`previous` 可携带上次读取的 `{clickSelectors,clickDescriptions}`，规则发生并发变更时返回 HTTP 409。

`/events` 返回的 `click_description` 根据浏览器点击事件的 `ruleSelector` 读取站点当前配置。没有命中自定义规则或该规则未配置说明时返回 `null`。

## 点击统计

`/stats/clicks` 使用 `projectId/from/to` 筛选和 `page/limit` 分页，返回 `project_id, site_name, path, element_key, label, description, region, element_id, selector, clicks, visitors, last_click`。仅统计浏览器 click 事件，页面优先使用 properties.pagePath（包含 Hash 路由），元素优先使用 elementId，其次 selector。历史事件缺少定位信息时按元素类型、名称和区域归并。名称、区域及定位信息取组内最近一次点击。说明根据该次点击的 `ruleSelector` 读取站点当前配置。匿名访客按 visitor_id 去重；不同站点和页面不合并。旧数据可能无法区分同名同类型元素。

## 兼容模式 API

以下接口仅在 Core.Config.LocalOnly=false 时注册。当前 cmd/core serve 固定使用本机模式，不开放这些路由。

| 方法和路径 | 用途 |
|---|---|
| POST /agent/sites/batch | 已注册宿主机提交站点事件批次 |
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

项目写入字段：`name, description, domains: string[], status: active|paused|archived, retentionDays, dailyQuota, browserEnabled, collection`。PATCH 当前按完整表单保存，不能仅传一个字段。域名列表实际为精确 Origin，含 scheme 与可选端口，不带尾斜线/路径。暂停和归档均拒绝新采集，已有数据保留至保留期。

`collection` 支持 `eventTypes:string[], sampleRate:0..1, excludePaths:string[], excludeElements:string[], clickSelectors:string[], clickDescriptions:Record<string,string>, batchSize:1..100, intervalSeconds:1..100`。点击选择器最多 50 条，每条最多 200 UTF-8 字节。

Host 配置：
```json
{"enabled":true,"sources":[{"id":"access","path":"/absolute/logs/access.log","parser":"nginx"}],"dockerTargets":[],"batchSize":50,"intervalSeconds":5}
```

Agent API 独立使用 `Authorization: Bearer <运行凭据>`；`POST /agent/enroll` 仅使用 `{token}` 引导，响应为 `{agentId,projectId,token}`。运行凭据仅成功注册时返回一次。

- POST /agent/heartbeat、POST /agent/metrics：hostname/os/arch/version/containers/metrics/error。
- GET /agent/config：当前版本化配置。
- POST /agent/events/batch：`{events:[...]}`；只允许 http_request，禁止为日志编造 visitorId/sessionId。

Browser API：POST `/collect/browser?projectId=...`，`{events:[...]}`；GET `/collection/config?projectId=...`。独立于管理登录，需要开启项目及精确 Origin，支持受限 CORS。

## 事件与分页

事件字段：`eventId, projectId, sessionId, visitorId, eventType, timestamp(Unix ms), path, properties, ua?`。本机中继将站点 siteId/name/source 与 events 组成 SiteBatch。开发版提交给进程内的 `/api/v1/agent/sites/batch`，安装版通过带安装密钥的 `/_sailfin_host/` 桥接提交。Browser UA 使用 HTTP Header 覆盖，Host UA 来自访问日志。服务端维护 received_at/source_type/agent_id，不接受客户端声明。

事件类型与属性白名单见 `internal/core/events.go`。每批 1–100，HTTP Body 256 KiB，单事件 8 KiB。属性使用白名单内的字符串或数字，点击事件另可包含 `domPath` 字符串数组。顶层 `path` 只接受 pathname。`eventId` 按项目去重。接受最多 31 天前至未来 5 分钟事件，统计只纳入查询范围。

自动浏览器采集在 `properties.pagePath` 保存去查询参数的页面标识（保留 Hash 路由），顶层 `path` 保持 pathname。点击属性为 `element, elementId, label, selector, ruleSelector, region, domPath, pagePath, pageTitle`。label 是自动读取的元素名称，selector 是 DOM 结构定位，ruleSelector 是采集目标命中的站点 CSS 规则，未命中自定义规则时省略。properties.pageTitle 为页面标题，无 Hash 路由首页的 title 可更新站点名（最长 120 UTF-8 字节）。SDK 字符串最多 200 个 UTF-16 单元且不超过 480 UTF-8 字节，适配 Core 的 500 字节限制。本机 Nginx 中继限制每批 20 条、请求体 64 KiB，SDK 同时按数量与字节数分批。

`properties.domPath` 保存浏览器点击事件的传播路径，按实际点击目标到外层祖先的顺序记录元素标签，例如 `["span","button","main","body","html"]`。路径最多保留靠近目标的 32 个元素，每个标签最多 64 个字符。它与其他属性一起存入现有 JSON；旧事件和没有 DOM 上下文的手动上报可省略此字段。

事件、会话列表和统计查询支持 `projectId,agentId,eventType,from,to`。其中 `from/to` 为 Unix 毫秒，也可使用 `fromLocal/toLocal`，格式为 `YYYY-MM-DDTHH:mm`，按系统统计时区解析并覆盖对应的毫秒边界。默认查询最近 24 小时，范围最多 366 天。本机模式省略 `projectId` 时只统计已启用站点。趋势按 UTC 小时分桶，管理端按统计时区显示，写入配额仍按 UTC 日计算。

分页接口使用 `page/limit`，从第 1 页开始，每页默认 20 条，合法上限为 200 条。会话详情按项目和会话标识查找，不套用列表的时间过滤。事件、会话、时间线、页面/设备/点击统计、审计和更新记录返回数组，通过 `X-Total-Count` 和 `X-Has-More` 提供总条数与下一页状态。趋势接口一次返回所选范围内的小时分桶，趋势明细在前端分页。

PV 只计 browser page_view/route_change；日志 HTTP 请求不会提升 PV；UV 是项目内匿名 visitorId 去重；活跃会话是最近 30 分钟且在查询范围内观测到活动的会话。均不表示真实自然人数。
