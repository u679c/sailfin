# 帆鳍（Sailfin）

Go + SQLite + React 的单机 Web 采集与分析平台。所有管理视图调用真实 API；初始数据库没有示例项目或统计数据。

本机发布包的生成、部署与数据保留流程见 [单机打包与部署](docs/deployment.md)。完整范围和待验证项见 [交付状态](docs/status.md)，原需求保存在 [原始 PRD](docs/original-requirements.md)。

## 开始开发

需要 **Go ≥1.22、Node ≥20.19、npm、make、C 编译器**（SQLite 使用 CGO）。macOS 使用 Xcode Command Line Tools；Linux 使用系统编译工具链。Docker 可选，启动 Core 不需要 Docker。

```sh
make setup
make dev
```

默认管理界面：[http://localhost:5173](http://localhost:5173)，Core：[http://127.0.0.1:8080/api/v1/health](http://127.0.0.1:8080/api/v1/health)。Core 首次启动自动创建数据库、迁移与随机管理员密码。

查看 **`var/bootstrap-admin.txt`** 获取登录凭据。文件权限 0600，不是公开默认密码。不要提交、分享此文件。登录后可在“系统设置 → 运行设置”修改密码。可在首次启动前设置 `SAILFIN_ADMIN_NAME` / `SAILFIN_ADMIN_PASSWORD`（至少 12 字符）。已有数据库不会被这些变量重置。

`make dev` 同时启动：

- Core 和内置本机探针（Go、SQL 或内置 SDK 变更后重启）。
- React UI（Vite Fast Refresh）。

Ctrl+C 只终止此次编排启动的进程组。脚本不调用 Docker CLI、不改动其他服务或已有容器。Core 热更新时会一并重启探针。

## 配置

命名统一为：英文与代码 Sailfin，中文界面“帆鳍”。环境变量使用 `SAILFIN_`，采集路由使用 `/__sailfin/`，SDK 全局对象为 `Sailfin`。

升级兼容：已有 `TRACELITE_` 环境变量仍可读取，显式的 `SAILFIN_` 配置优先；没有新数据库时继续使用已有 `var/tracelite.db`，不移动正在使用的 SQLite 文件。已部署的旧 Nginx 采集路由、同意事件、匿名访客存储和元素标记保留兼容。新接入与示例均使用新名称。管理员 Cookie 已改名，升级后需重新登录。

```sh
cp .env.example .env
# 编辑 .env 后重新运行 make dev
```

`.env` 由 `make dev` 读取，已有 shell 环境变量优先；不会执行变量插值或 Shell 命令。直接运行 Core 二进制时使用进程环境变量。

更换 Core 端口时同步配置 `SAILFIN_ADDR` 和 `SAILFIN_CORE_URL`；更换 Web 端口时同步 `SAILFIN_WEB_PORT` 和 `SAILFIN_PUBLIC_URL`。管理浏览器地址应匹配 PUBLIC_URL Origin，同协议同端口的本机回环地址可以互换。使用自定义域名或代理端口时，可运行 `npm run dev -- --public-url https://实际后台域名`。

数据位置：Core 默认 `var/sailfin.db`（`SAILFIN_DB`）；探针状态默认位于数据库同级的 `local-probe` 目录（`SAILFIN_PROBE_STATE`）。日志偏移、待发队列和暂停状态均持久化。

## 接入站点

在 macOS 或 Linux 宿主机启动 Core 时会自动启动本机探针。在「探针管理 → 探针管理」可以查看心跳、配置站点数及暂停或恢复采集。探针支持读取授权的 Nginx 日志，以及接收 Nginx 注入的浏览器事件。两种来源使用同一站点 ID 时归入同一站点。

后台收到首批有效数据后自动显示站点。首次使用时站点列表为空属于正常情况。完整配置见 [本机探针与 Nginx 接入](docs/host-nginx.md)。

本地开发时启动 `make dev`，登录后打开 **探针管理 → 站点接入**，页面自动扫描本机正在运行的 Nginx 站点。查看预览后点击「确认接入」；需要更新结果时点击「重新扫描」。接入后仍使用原网址，无需逐个填写站点或环境变量。现有手动命令保留给特殊配置。[本机 Nginx 接入说明](docs/host-nginx.md)

管理界面统一使用顶部导航和左右分栏：站点左侧搜索、选择站点，右侧查看概览、页面、点击行为、访问轨迹、事件记录和设备；探针管理左侧为站点接入和探针管理；系统设置左侧为运行设置和安全审计。右侧数据列表默认每页 20 条，左侧站点列表不分页。

当前 `serve` 使用本机内置探针，不需要创建项目或注册远程 Agent。旧版项目与远程 Agent 接口保留在兼容模式，见 [API](docs/api.md)。

## 单机打包与部署

在目标系统和架构对应的构建机上执行 `npm ci` 和 `./scripts/package.sh 0.1.0`。脚本在 `dist/` 对应的版本目录生成单个 `Sailfin-installer-<md5>.bin`，其中内嵌 Docker 镜像和宿主机探针。安装需要 Docker。宿主机 Nginx 用于自动注入，容器里的 OpenResty 可手动指定宿主机日志路径。进入安装目录执行 `./aqua start`，Core 和管理端在 Sailfin 专用 Docker 容器中运行，探针在宿主机运行。用 `./aqua status`、`./aqua stop` 和 `./aqua restart` 管理本次安装。 [部署步骤](docs/deployment.md)

### 部署后续工作

- [ ] 分布式部署留待后续，届时探针可以部署在与管理节点不同的设备。

## 测试、检查与主动构建

```sh
make test    # Go + SQLite 集成测试，JS SDK 策略测试
make check   # TypeScript 无产物检查 + 编排脚本语法检查
make build   # 生成包含探针和 SDK 源码的 Core 二进制及 Web 静态文件
```

`make build` 输出 `bin/sailfin-core` 和 `web/dist`，需要本机 C 工具链。`make package VERSION=0.1.0` 生成自解压安装文件，Linux 安装包的宿主机程序禁用 CGO，Docker 镜像内的 Core 保留 SQLite 支持。

用户自行构建后，可以不运行 Node 服务：

```sh
./bin/sailfin-core serve --web-dir web/dist --public-url http://localhost:8080
```

## 数据与安全

- 密码 Argon2id；管理员会话 HttpOnly/SameSite/CSRF；HTTPS Origin 启用 Secure。
- 管理 API 可暂停或恢复本机采集。开发模式下探针和 Core 同进程通信，安装模式下宿主机探针使用安装密钥连接容器。
- 浏览器通过站点同源 Nginx 路由上报，本机中继检查 Origin、批次大小和并发，Core 校验事件、去重并执行写入配额；不向浏览器发放管理员或探针凭据。公开采集入口仍需部署层防滥用策略。
- 日志目录通过主机配置显式授权。浏览器事件入口由同机 Nginx 转发至探针的回环端口。
- SQLite WAL 自动迁移；每小时数据保留清理；分页与查询范围上限。
- 「系统设置 → 运行设置」提供一致性在线数据库备份，不直接复制活跃 SQLite 主文件。

更多：[API](docs/api.md) · [架构与顺序](docs/architecture.md) · [安全与恢复](docs/security.md) · [验收报告](docs/testing.md) · [交付状态](docs/status.md)。
