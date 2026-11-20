# TraceLite

Go + SQLite + React 的单机 Web 采集与分析平台。所有管理视图调用真实 API；初始数据库没有示例项目或统计数据。

当前阶段不提供安装器、systemd、跨架构发布或升级回滚。完整范围和待验证项见 [交付状态](docs/status.md)，原需求保存在 [原始 PRD](docs/original-requirements.md)。

## 开始开发

需要 **Go ≥1.22、Node ≥20.19、npm、make、C 编译器**（SQLite 使用 CGO）。macOS 使用 Xcode Command Line Tools；Linux 使用系统编译工具链。Docker 可选，启动 Core 不需要 Docker。

```sh
make setup
make dev
```

默认管理界面：[http://localhost:5173](http://localhost:5173)，Core：[http://127.0.0.1:8080/api/v1/health](http://127.0.0.1:8080/api/v1/health)。Core 首次启动自动创建数据库、迁移与随机管理员密码。

查看 **`var/bootstrap-admin.txt`** 获取登录凭据。文件权限 0600，不是公开默认密码。不要提交、分享此文件。登录后可在“系统状态”修改密码。可在首次启动前设置 `TRACELITE_ADMIN_NAME` / `TRACELITE_ADMIN_PASSWORD`（至少 12 字符）。已有数据库不会被这些变量重置。

`make dev` 同时启动：

- Core 和内置本机探针（Go、SQL 或内置 SDK 变更后重启）。
- React UI（Vite Fast Refresh）。

Ctrl+C 只终止此次编排启动的进程组。脚本不调用 Docker CLI、不改动其他服务或已有容器。Core 热更新时会一并重启探针。

## 配置

```sh
cp .env.example .env
# 编辑 .env 后重新运行 make dev
```

`.env` 由 `make dev` 读取，已有 shell 环境变量优先；不会执行变量插值或 Shell 命令。直接运行 Core 二进制时使用进程环境变量。

更换 Core 端口时同步配置 `TRACELITE_ADDR` 和 `TRACELITE_CORE_URL`；更换 Web 端口时同步 `TRACELITE_WEB_PORT` 和 `TRACELITE_PUBLIC_URL`。管理浏览器地址应匹配 PUBLIC_URL Origin，同协议同端口的本机回环地址可以互换。使用自定义域名或代理端口时，可运行 `npm run dev -- --public-url https://实际后台域名`。

数据位置：Core 默认 `var/tracelite.db`（`TRACELITE_DB`）；探针状态默认位于数据库同级的 `local-probe` 目录（`TRACELITE_PROBE_STATE`）。日志偏移、待发队列和暂停状态均持久化。

## 接入站点

在 macOS 或 Linux 宿主机启动 Core 时会自动启动本机探针。在「系统状态 → 本机探针」可以查看心跳、配置站点数及暂停或恢复采集。探针支持读取授权的 Nginx 日志，以及接收 Nginx 注入的浏览器事件。两种来源使用同一站点 ID 时归入同一站点。

后台收到首批有效数据后自动显示站点。首次使用时站点列表为空属于正常情况。完整配置见 [本机探针与 Nginx 接入](docs/host-nginx.md)。

本地开发时启动 `make dev`，登录后打开 **系统状态 → Nginx 采集 → 启用 Nginx 采集**。先预览本机正在运行的 Nginx 站点，再确认统一接入。接入后仍使用原网址，无需逐个填写站点或环境变量。现有手动命令保留给特殊配置。[本机 Nginx 接入说明](docs/host-nginx.md)

## TODO：单机安装与发布

- [ ] 为 macOS 和 Linux 生成 `tracelite-<md5>.bin`。检查 Docker 已安装且可连接，缺失时报错。在安装文件所在目录解压项目文件，创建并启动 TraceLite 专用容器。
- [ ] 单机安装时自动部署同机探针。生成 `./u679c start|stop|restart|status`，统一管理管理节点与探针，重启时保留数据库和配置。
- [ ] 打包部署时准备 Nginx 配置与重载权限，站点接入仍在 Web 后台启用。
- [ ] 分布式部署留待后续，届时探针可以部署在与管理节点不同的设备。

## 测试、检查与主动构建

```sh
make test    # Go + SQLite 集成测试，JS SDK 策略测试
make check   # TypeScript 无产物检查 + 编排脚本语法检查
make build   # 生成包含探针和 SDK 的 Core 二进制及 Web 静态文件
```

`make build` 是实际实现的本机生产程序构建命令，不是正式发布打包。输出 `bin/tracelite-core` 和 `web/dist`。macOS 使用外部链接兼容当前系统与 Go 1.22；Linux 使用默认链接。CGO 构建需要本机 C 工具链，跨架构发布后续处理。

用户自行构建后，可以不运行 Node 服务：

```sh
./bin/tracelite-core serve --web-dir web/dist --public-url http://localhost:8080
```

## 数据与安全

- 密码 Argon2id；管理员会话 HttpOnly/SameSite/CSRF；HTTPS Origin 启用 Secure。
- Core 和本机探针在同一进程通信，管理 API 可暂停或恢复本机采集。
- Browser 写入独立于管理认证；Origin 白名单之外还有限流、配额、大小限制、事件白名单和去重。公开采集入口仍需要部署层防滥用策略。
- 日志目录通过主机配置显式授权。浏览器事件入口由同机 Nginx 转发至探针的回环端口。
- SQLite WAL 自动迁移；每小时数据保留清理；分页与查询范围上限。
- 系统状态页面提供一致性在线数据库备份，不直接复制活跃 SQLite 主文件。

更多：[API](docs/api.md) · [架构与顺序](docs/architecture.md) · [安全与恢复](docs/security.md) · [验收报告](docs/testing.md) · [交付状态](docs/status.md)。
