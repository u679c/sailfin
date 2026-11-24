# 本地优先架构与实施顺序

当前交付包括开发环境、功能源码和本机发布包生成入口。 [单机打包与部署](deployment.md)

1. Core + SQLite + 管理员认证。开发模式启动内置探针，安装模式由 Aqua 启动宿主机探针。
2. 按需扫描宿主机 Nginx 或已配置的 1Panel OpenResty，预览后由管理员选择并确认应用。
3. 授权访问日志与同源浏览器事件采集。站点先登记，启用后进入站点目录，首批事件到达后显示最近上报时间。
4. 浏览器页面、Hash 路由、点击名称与元素标识、前台停留、匿名访客和会话。
5. 页面统计、点击聚合、事件查询和会话时间线，真实 API 分页。
6. 顶部主导航与统一左右分栏工作台，本机探针状态和暂停/恢复管理。
7. 兼容代码保留项目管理、远程 Agent 注册、配置版本和 Docker 只读采集；当前本机启动入口不公开这些兼容路由。
8. 自解压安装包的构建、部署和原位更新。安装包解包前检查系统和 Docker，宿主机 Nginx 检查提供提示。安装目录中的 `aqua` 管理 Core 容器和宿主机探针。跨架构自动发布、systemd 和自动升级回滚单独推进。

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

## 通信和存储

开发模式下 Core 与探针同进程运行，通过内部 HTTP Handler 通信。安装模式下探针从宿主机连接容器的回环发布端口，使用安装级密钥上报数据并领取 Nginx 扫描或接入任务。探针不直接访问数据库。Nginx 将同源 /__sailfin/ 请求转发到探针回环监听地址。服务地址、身份路径和数据路径可配置。兼容模式的独立 Agent 通过版本化 HTTP API 与 Core 通信，远程默认要求 TLS。
SQLite 单连接串行写入，WAL 和 busy timeout 保护并发；无需外部数据库或队列。事件原始数据是事实来源，分析使用受范围约束的 SQL 聚合。会话从真实 Browser 事件按项目聚合，不生成伪造会话。

身份令牌在 Core 仅存 SHA-256 摘要；管理员密码使用独立盐 Argon2id；管理会话使用 HttpOnly/SameSite Cookie 与 CSRF。当前为单管理员角色，所有管理路由都认证；细粒度多用户 RBAC 留待后续。

## 采集行为

日志目录授权由本机启动参数决定，Core 配置不能越权。偏移与待发事件写入同一私有状态文件，原子替换；单实例锁避免重复进程并行采集；最多 1000 待发事件，到达上限暂停读取，通过稳定 eventId 实现重试去重。
文件 inode 变化或截断后重新读取新文件。轮转前尚未读取的旧文件末尾不保证被补读；copytruncate 同 inode 且长度迅速恢复的情况需额外策略，详见功能边界。

Docker 只读采集客户端保留在独立 Host Agent 中，Socket 和容器允许名单需在 Agent 本机显式配置。兼容配置接口支持 dockerTargets。安装目录中的 `aqua` 通过本机 Docker 管理带有当前安装标识的 Core 容器。

Browser SDK 由 Nginx 注入 `sailfin-boot.js` 自动初始化，入口按内容指纹加载模块，配置和事件均走同源中继。新扫描接入的对外站点默认等待同意信号，手动添加和文件导入按各自的站点配置启动。中继检查站点 Origin，自动模式还验证代理元数据密钥；Core 执行大小、类型、时间、去重和配额校验。浏览器不持有管理员或 Agent 长期密钥。手动 init、项目浏览器开关及跨域 Origin 白名单属于兼容模式。

当前本机中继按站点开关返回 `enabled`，下发站点的 `clickSelectors`，并固定使用 `batchSize=20` 和 `intervalSeconds=5`。暂停本机探针会阻止入库，浏览器 SDK 仍可能监听事件。采样和排除规则通过兼容模式的项目策略配置。

## 生命周期与容量

Core 每小时检查事件保留策略和容量上限，并清理会话令牌、注册令牌、历史计数与审计。心跳只保存最新状态，错误日志与审计具有全局条数上限。分页查询默认 20、最多 200 条，返回总数与下一页响应头；界面可选 20/50/100 条。站点目录和兼容项目/Agent 列表最多 500 条；分析时间范围最多 366 天。
「系统设置 → 安全审计」读取磁盘可用空间，低于 256 MiB 提示；长期容量趋势与聚合预计算尚待目标机压力测试决定。「备份管理」通过 `VACUUM INTO` 生成 SQLite 一致性快照。手动和自动备份存放在数据库目录下的 `backups/<数据库文件名>/`，由 Core 根据自动备份策略轮换。

## 本机开发与独立运行

`make dev` 编译并运行 Go 开发进程。`make build` 分别生成包含 SDK 源码的 Core 二进制和 Web 静态产物。`make package` 将包含 Core 和 Web 静态文件的 Linux 镜像、宿主机探针及 `aqua` 打入自解压安装包。安装目录中的 `./aqua start` 导入镜像并启动容器和探针。
所有开发默认值都是可覆盖的便利配置，代码不依赖某台固定主机或特定容器名称。
