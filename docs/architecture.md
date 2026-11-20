# 本地优先架构与实施顺序

本次调整覆盖原 PRD 的安装与发布优先级。当前只交付持续开发环境和真实功能源码。

1. Core + SQLite + 认证 + 项目管理。
2. Host Agent 注册、心跳、配置版本与安全目录授权。
3. 访问日志采集、偏移、持久化待发送队列、批量去重与写入配额。
4. 分析 API 与管理工作台。
5. Browser SDK、匿名会话、真实事件时间线。
6. Docker 只读发现和 Host Collector 授权日志关联。
7. 集成验证、安全检查和交付边界记录。
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
- `browser-agent`：可独立构建的 JS SDK；本地 watch 更新产物。
- `scripts/dev.mjs`：开发进程编排、Go 文件监听、只清理自己启动的进程组。
- `tests` 与各模块 `_test.go`：真实数据库集成测试及策略测试。

## 通信和存储

Core 对 Agent 仅暴露版本化 HTTP API，Agent 不访问数据库。服务地址、Agent 身份路径、数据路径都由环境变量配置。远程 Core 默认要求 TLS；本地默认绑定回环地址。
SQLite 单连接串行写入，WAL 和 busy timeout 保护并发；无需外部数据库或队列。事件原始数据是事实来源，分析使用受范围约束的 SQL 聚合。会话从真实 Browser 事件按项目聚合，不生成伪造会话。

身份令牌在 Core 仅存 SHA-256 摘要；管理员密码使用独立盐 Argon2id；管理会话使用 HttpOnly/SameSite Cookie 与 CSRF。当前为单管理员角色，所有管理路由都认证；细粒度多用户 RBAC 留待后续。

## 采集行为

日志目录授权由本机启动参数决定，Core 配置不能越权。偏移与待发事件写入同一私有状态文件，原子替换；单实例锁避免重复进程并行采集；最多 1000 待发事件，到达上限暂停读取，通过稳定 eventId 实现重试去重。
文件 inode 变化或截断后重新读取新文件。轮转前尚未读取的旧文件末尾不保证被补读；copytruncate 同 inode 且长度迅速恢复的情况需额外策略，详见功能边界。

Docker Socket 仅在 Host Agent 本机显式配置。发现使用 GET；日志读取还要求完整容器 ID 位于本机允许名单。Web 只配置 `dockerTargets`；没有任意命令、容器创建、停止、删除入口。当前选择 Host Collector 模式，无 Sidecar 生命周期操作。

Browser SDK 在用户同意后由网站调用 `init`，Core 项目还需开启浏览器采集与精确 Origin 白名单。浏览器没有管理员或 Agent 长期密钥。事件入口同时具有限流、配额、白名单、大小、类型与时间限制；CORS 不被视为认证或完整防刷手段。

## 生命周期与容量

每小时按项目 retention_days 删除过期事件，清理会话令牌、注册令牌、历史计数与审计。心跳只保存最新状态，错误日志与审计具有全局条数上限。查询默认 50、最多 200 条；项目/Agent 列表最多 500 条；分析时间范围最多 366 天。
安全中心读取磁盘可用空间，低于 256 MiB 提示；长期容量趋势与聚合预计算尚待目标机压力测试决定。在线备份使用 `VACUUM INTO` 一致性快照，下载后移除临时文件；禁止把活跃 SQLite 主文件复制当成完整备份。

## 本机开发与独立运行

`make dev` 会编译并运行 Go 开发进程，但不执行 `make build` 或发布流程。用户主动 `make build` 才生成二进制、Web 和 SDK 静态产物；Core 可通过 TRACELITE_WEB_DIR 服务静态前端。此构建入口不是安装器或正式发布流水线。
所有开发默认值都是可覆盖的便利配置，代码不依赖某台固定主机或特定容器名称。
