# 实际检查与验收报告

日期：2026-11-20。环境：macOS arm64、Go 1.22.2、Node 20.20.2、npm 10.8.2。

## 已执行

| 项目 | 结果 |
|---|---|
| make setup | 成功安装 Go/npm 依赖，生成 go.sum 和 package-lock.json |
| make test（GOCACHE 使用 /private/tmp/sailfin-go-cache） | 通过；Docker 实机用例因未配置 Socket 跳过 |
| make check | TypeScript 无产物检查与开发脚本语法检查通过 |
| gofmt / Prettier | 新增 Go、TS/JS、CSS 文件已格式化 |
| npm 依赖安装审计 | 升级 Vite/React 插件及兼容 TypeScript 后，最近安装报告 0 个已知漏洞 |

Go 测试包含 Core/Agent/Parser/Device 模块；JS 5 个测试通过。测试使用临时目录和真实 SQLite，不创建用户的运行数据库。注册/心跳/事件的 HTTP 测试调用真正的 Core Handler，Agent 端到端测试也使用真正的 Agent 采集逻辑；内存 HTTP 传输替代网络端口，不替代业务响应。

## 覆盖的真实流程

- 管理登录、CSRF、单次令牌重放与过期、无效 Agent 凭据。
- 项目创建、编辑、浏览器策略历史、名称确认删除、跨项目删除隔离。
- Agent 注册、心跳持久化、Online/Offline 判定、禁用/重新启用、凭据撤销与删除身份。
- 真实日志文件 → Host 解析 → Core API → SQLite → 统计结果；URL 查询参数清理。
- Core 重开后的数据保留；Agent 重开后的身份、偏移和待发队列恢复。
- 人为断网时待发数据不丢失，恢复后继续上报。
- Nginx、Apache、Caddy、JSON 字段映射与 Docker JSON 适配器。
- 文件偏移去重、新 inode 轮转、1000 事件缓冲上限、符号链接目录越权拒绝、Agent 状态锁。
- Browser PV/HTTP 请求/UV/总事件区分、匿名会话和时间线隔离。
- 写入配额、重复事件、超大 JSON、非法属性、时间范围、SQL 注入字符串、非授权 Origin。
- 一致性 SQLite 在线备份响应。
- 管理员密码更新与旧密码失效。
- SDK URL/属性隐私策略、采集开关和采样策略；内存 DOM 中的配置启用、真实 SDK SPA 事件流程、会话 ID 连续性与停止后不再发送。
- Docker 未授权日志读取在连接 Socket 前拒绝。

## 发现并修复

测试期间修复了事件 INSERT 参数数量错误、Vite 配置 Node 类型缺失、升级后的插件声明与旧 TypeScript 不兼容，以及 macOS 动态链接器对 Go 1.22 测试程序的兼容问题。Makefile 在 macOS 选择外部链接；没有通过关闭安全检查绕过错误。

## 未执行 / 不宣称通过

- 未运行 make build 或正式发布打包。
- 未启动 make dev 的服务进行手工界面测试；没有打开浏览器、模拟器或真机。
- 当前没有 Docker CLI，未配置实际 Docker Socket，因此本机容器发现/日志读取的实机验收未完成。没有创建、修改、停止或删除任何 Docker 容器。
- 未测目标 Linux 2GB 机器上的 RSS、CPU、吞吐、延迟或长期稳定性；性能测量入口和方法见 performance.md，不能据当前结果宣称资源目标达标。
- 未验证 React/Vue/原生站点的完整真实浏览器矩阵、真实网络/TLS/反向代理、多主机互通、异常断电、磁盘满及高并发 SQLite Busy。
- 未运行 Go 依赖漏洞数据库扫描或第三方渗透审计；npm 审计不等于完整系统安全保证。

## 用户接手的最短路径

1. make dev → 读取 var/bootstrap-admin.txt → 登录并创建项目。
2. 注册 Host Agent，在独立终端 make agent；配置授权日志源。
3. 可选 make site 产生真实 HTTP 请求与浏览器事件；不要预先插入统计数据。
4. 查看 Agent、事件、设备和会话；退出并重启进程确认持久化。
5. 配置本机 Docker Socket 后 make docker-test（只读），再通过允许名单启用具体容器日志。
6. 主要功能、安全与目标环境验收通过后，才单独安排安装和发布阶段。
