# 当前交付状态

2026-11-20，本机开发版本 0.1.0-dev。以下区分源码实现、自动测试与实机验收，不把它们合并成“已全部验证”。

## 已实现（当前本机模式）

- Go Core、内置本机探针、Browser SDK、React 管理端的 Monorepo；Core 启动时自动运行本机探针。
- Makefile 提供 setup/dev/test/check/build/package/site/docker-test。独立 Agent 源码入口为 cmd/host-agent，保留在兼容范围。
- `./scripts/package.sh 0.1.0` 生成单个 `Sailfin-installer-<md5>.bin` 并打印 SHA-256。安装包逐项检查系统、Docker 和 Nginx，然后解压内嵌镜像及宿主机探针。 [部署文档](deployment.md)
- 在安装目录执行 `./aqua start|stop|restart|status`，管理当前安装的 Core 容器和宿主机探针。
- make dev 编排 Core 和 Web，Go/SQL/SDK JS 变化后重启 Core，Web 使用 Vite Fast Refresh；SDK 由 Go embed 提供源码。
- SQLite WAL、启动时初始化 schema、持久化、按保留期清理与一致性在线备份。
- 随机管理员凭据、Argon2id、Cookie 会话、CSRF、修改密码与管理审计。
- 探针管理中的站点接入自动扫描本机 Nginx，提供预览、确认应用、备份、语法校验、重载请求及失败时恢复配置。
- 本机探针读取授权站点日志并转发同源浏览器事件；首批事件和站点记录在同一事务提交。
- SDK 自动采集页面和 SPA/Hash 路由、按钮与链接点击、名称/元素标识/区域、前台停留、匿名会话、异常类别及基础性能；支持 DNT、同意/撤回与停用。
- 无 Hash 路由首页的 title 可更新站点名称；页面统计保留 Hash 路由；点击按站点、页面和元素聚合，原始事件仍可查询。
- 页面、点击、设备、事件、会话、时间线和审计列表分页；默认 20 条，界面可选 50/100 条，接口返回总条数。
- 中文界面“帆鳍”、代码 Sailfin，品牌 SVG、统一左右分栏、紧凑横向筛选和共享 Select。站点左侧列表不分页，右侧列表区域固定高度、内容独立滚动。
- 探针列表展示当前本机探针，正常绿色，异常/离线/暂停/未就绪红色；可暂停或恢复全部采集。
- Sailfin 改名兼容旧环境变量、已有数据库路径、旧 Nginx 路由和 SDK 标记；管理员 Cookie 更名后需要重新登录。

## 源码保留的兼容能力

项目增删改、浏览器策略历史、远程 Agent 注册与凭据管理、配置版本和 Docker 只读发现/日志读取仍有源码和测试。相关公开路由只在 Core.Config.LocalOnly=false 时注册；当前 cmd/core serve 固定启用本机模式，管理界面不提供这些流程。

## 验证记录

当前检查范围为 TypeScript 无产物检查、相关 JS 语法、Go 格式及 diff 检查，详见 [testing.md](testing.md)。未完成完整功能测试和界面验收。

## 尚未实机验收

- 用户主动运行 make dev 后的交互与视觉检查；没有打开浏览器或进行界面验收。
- make build 的完整产物验证：遵守用户偏好，未自动运行生产构建。
- 安装包构建、系统预检、`aqua` 进程管理及 macOS/Linux 目标机验收。
- Docker 只读发现与授权日志读取的实机验收。
- React/Vue/原生站点的真实浏览器兼容性矩阵，SDK 生命周期已有内存 DOM 测试，真实浏览器仍需验收。
- Linux 2GB 目标机性能、RSS、磁盘压力、长时间运行、SQLite Busy、异常退出恢复专项。

## 当前能力边界 / 后续功能

这些尚未实现，不能当成正式 V1 全部完成：

- 跨架构自动发布、systemd、自动升级回滚及卸载。
- Docker Sidecar 创建/停止/清理、远程受控部署：当前用 Host Collector，不执行容器生命周期操作。
- 多用户细粒度 RBAC、项目异步清理任务、远程卸载/升级。
- UA 品牌和 Android 平板更细分类、原始 UA 可选保留、独立设备型号统计及设备趋势。
- 热门来源/自定义事件排名、预聚合表和大数据量查询优化、长期容量趋势告警。
- 轮转前旧文件未读尾部跟进、copytruncate 同 inode 快速恢复的完善处理。
- Docker 单次返回超过 256 KiB 时当前报告错误并暂停该轮读取，尚未实现大积压流分页；不静默截掉积压。
- SSE 实时状态、强制立即健康探测，当前依赖 30 秒心跳和手动刷新。

正式发布前仍需完成主要功能、安全、持久化、Docker 和目标环境验收。构建、测试和界面验证按 AGENTS.md 执行。
