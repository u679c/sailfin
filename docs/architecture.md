# 本地优先架构与实施顺序

本次调整覆盖原 PRD 的安装与发布优先级。当前只交付持续开发环境和真实功能源码。

1. Core + SQLite + 管理员认证，启动时初始化并运行本机探针。
2. 本机 Nginx 扫描、接入预览、确认应用、备份校验与失败恢复。
3. 授权访问日志和同源浏览器事件采集，站点在首批有效事件入库时创建。
4. 浏览器页面、Hash 路由、点击名称与元素标识、前台停留、匿名访客和会话。
5. 页面统计、点击聚合、事件查询和会话时间线，真实 API 分页。
6. 顶部主导航与统一左右分栏工作台，本机探针状态和暂停/恢复管理。
7. 兼容代码保留项目管理、远程 Agent 注册、配置版本和 Docker 只读采集；当前本机启动入口不公开这些兼容路由。
8. 单独后续任务：Linux 安装器、跨架构、systemd、升级回滚、发布。

## Monorepo

- `cmd/core`：Core 进程入口，超时与优雅退出。
- `cmd/host-agent`：独立 Agent 进程入口。
- `internal/core`：HTTP 管理和采集 API、SQLite 查询、认证与生命周期管理。
- `internal/agent`：持久化 Agent、日志读取、Docker 只读客户端。
- `internal/parser`：独立适配器注册表（combined Nginx/Apache、Caddy、JSON、Docker JSON）。
- `internal/device`：成熟 UA 库封装；不可识别型号和品牌保留 Unknown。
- `internal/protocol`：共享协议。
- `web`：React 管理后台；Vite 代理 Core，Fast Refresh。
- `browser-agent`：JS SDK 源码由 Go embed 内嵌，经中继以 ES Module 提供；另有可单独运行的 Vite 库构建命令。
- `scripts/dev.mjs`：编排 Core 和 Web，监听 Go、SQL、SDK JS 变化后重启 Core，只清理自己启动的进程组。
- `tests` 与各模块 `_test.go`：真实数据库集成测试及策略测试。

## 通信和存储

Core 与本机探针同进程运行，通过内部 HTTP Handler 通信，探针不直接访问数据库。Nginx 将同源 /__sailfin/ 请求转发到探针回环监听地址。服务地址、身份路径和数据路径可配置。兼容模式的独立 Agent 通过版本化 HTTP API 与 Core 通信，远程默认要求 TLS。
SQLite 单连接串行写入，WAL 和 busy timeout 保护并发；无需外部数据库或队列。事件原始数据是事实来源，分析使用受范围约束的 SQL 聚合。会话从真实 Browser 事件按项目聚合，不生成伪造会话。

身份令牌在 Core 仅存 SHA-256 摘要；管理员密码使用独立盐 Argon2id；管理会话使用 HttpOnly/SameSite Cookie 与 CSRF。当前为单管理员角色，所有管理路由都认证；细粒度多用户 RBAC 留待后续。

## 采集行为

日志目录授权由本机启动参数决定，Core 配置不能越权。偏移与待发事件写入同一私有状态文件，原子替换；单实例锁避免重复进程并行采集；最多 1000 待发事件，到达上限暂停读取，通过稳定 eventId 实现重试去重。
文件 inode 变化或截断后重新读取新文件。轮转前尚未读取的旧文件末尾不保证被补读；copytruncate 同 inode 且长度迅速恢复的情况需额外策略，详见功能边界。

Docker 只读客户端保留在独立 Host Agent 中，Socket 和容器允许名单需在 Agent 本机显式配置。兼容配置接口支持 dockerTargets；当前管理界面仅显示本机探针，不提供容器配置。没有任意命令、容器创建、停止或删除入口。

Browser SDK 主要由 Nginx 注入 boot.js 自动初始化，配置和事件均走同源中继。对外站点默认等待同意信号，回环测试站点可按配置直接启动。中继检查站点 Origin，自动模式还验证代理元数据密钥；Core 执行大小、类型、时间、去重和配额校验。浏览器不持有管理员或 Agent 长期密钥。手动 init、项目浏览器开关及跨域 Origin 白名单属于兼容模式。

当前本机中继返回固定的 enabled=true、batchSize=20、intervalSeconds=5；暂停探针会阻止入库，不代表 SDK 端已停止监听。SDK 支持的采样和排除能力尚未通过当前管理界面下发。

## 生命周期与容量

每小时按项目 retention_days 删除过期事件，清理会话令牌、注册令牌、历史计数与审计。心跳只保存最新状态，错误日志与审计具有全局条数上限。分页查询默认 20、最多 200 条，返回总数与下一页响应头；界面可选 20/50/100 条。站点目录和兼容项目/Agent 列表最多 500 条；分析时间范围最多 366 天。
「系统设置 → 安全审计」读取磁盘可用空间，低于 256 MiB 提示；长期容量趋势与聚合预计算尚待目标机压力测试决定。在线备份使用 `VACUUM INTO` 一致性快照，下载后移除临时文件；禁止把活跃 SQLite 主文件复制当成完整备份。

## 本机开发与独立运行

`make dev` 会编译并运行 Go 开发进程，但不执行 `make build` 或发布流程。用户主动 `make build` 才生成包含 SDK 源码的 Core 二进制和 Web 静态产物；该 Makefile 目标不单独生成 SDK 的 IIFE/ES 构建产物。Core 可通过 SAILFIN_WEB_DIR 服务静态前端。此构建入口不是安装器或正式发布流水线。
所有开发默认值都是可覆盖的便利配置，代码不依赖某台固定主机或特定容器名称。
