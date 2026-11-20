# 在 macOS 或 Linux 接入 Nginx

Sailfin 采用单机部署。Core 启动时自动初始化并运行本机探针。在「系统状态 → 本机探针」可以查看运行状态、心跳、已配置站点数，或者暂停和恢复采集。站点列表在 Core 收到首批有效数据后更新。

## 从 Web 批量启用现有站点

运行 `make dev`，登录后台，在 **系统状态 → Nginx 采集** 点「启用 Nginx 采集」。扫描只查看运行中的 Nginx 和实际加载的配置。预览会标记可接入、已配置和已跳过的站点。确认后才写入：保存备份，验证所有受影响实例，再请求重载。校验或发送重载失败时尝试恢复原配置。

站点继续使用原域名和端口。默认站点按实际访问的主机名归类，非默认端口也参与站点识别。正常页面加载同源 SDK 后才会产生浏览器数据。回环测试站点可直接采集，对外站点仍需取得同意。点击「暂停采集」可以停止探针入库，已经应用到 Nginx 的配置仍保留。

本地开发版支持与 Core 同一用户运行的现有 Nginx，需要当前用户有站点配置目录写入权限和重载权限。其他用户启动的 Nginx、无法验证的实例、共享配置文件和有继承冲突的路由会列出跳过原因。自动模式优先接入常见的代理和静态 HTML 路由，压缩、CSP 或自定义响应替换可能需要手动处理。

## 自动为现有站点加 JS 注入

使用已有站点的原域名和端口。选定 Nginx 配置里直接声明的 `server` 和提供 HTML 的 `location`。例如站点 `https://example.com`：

```sh
./bin/sailfin-core nginx-inject \
  --site-file /path/to/nginx/conf.d/example.conf \
  --server-name example.com \
  --listen 443 \
  --origin https://example.com \
  --site-id example-com \
  --sites-file "$PWD/var/sites.json"
```

这条命令默认预览。确认目标后，在同一条命令末尾添加 `--apply`。应用时需要对配置文件有写权限，并能通知现有 Nginx master reload。应用会检查目标文件已被现有 Nginx 加载，为修改过的文件保留备份，执行 `nginx -t`。验证通过后请求 Nginx 平滑重载，校验或发送重载命令失败时恢复原文件。[Nginx 重载说明](https://nginx.org/en/docs/control.html#reconfiguration)

- `--location` 默认选 `/`。也可以传 `--location '= /'` 或其他实际提供 HTML 的 location 参数。命令会拒绝多义的服务器块、多个域名共用的服务器块、或已存在冲突注入的配置。
- 如果现有 Nginx 通过自定义主配置、前缀或路径运行，传 `--nginx-config`、`--nginx-prefix` 或 `--nginx-bin`。`--listen` 用于区分同名的 HTTP 和 HTTPS 块。
- 需要 Nginx 的 `http_sub_module`，上游要返回可注入的 HTML。对已显式配置代理请求头的目标 location，命令可禁用上游压缩；对继承代理请求头的 location，请确认上游不压缩 HTML。站点 CSP 也要允许加载和连接同源采集路径。
- 默认仍等待业务站点的同意事件。只有回环 Origin 能使用 `--local-test` 跳过这一步。

随后用相同的站点文件启动或重启 Core，各项参数直接跟在启动命令后：

```sh
./bin/sailfin-core serve \
  --addr 127.0.0.1:8080 \
  --public-url http://localhost:8080 \
  --web-dir "$PWD/web/dist" \
  --sites-file "$PWD/var/sites.json"
```

如果注入时传了 `--relay-addr`，Core 启动时也传同一个值。客户端继续访问原网站，在原域名下加载 `/__sailfin/boot.js`。后台收到有效事件后才会出现站点。

## 可选：启动独立的本地测试入口

业务服务已经在本机运行时，可以让 Core 自动配置一个 Nginx 入口。需要在 macOS 或 Linux 安装包含 `http_sub_module` 的 Nginx，并让 `nginx` 可从运行 Core 的 PATH 找到。

自有本机测试示例，业务上游为 8888：

```sh
npm run dev -- --nginx-upstream http://127.0.0.1:8888 --nginx-require-consent 0
```

独立部署时把这些参数传给 `./bin/sailfin-core serve`。访问 `http://localhost:9080`，HTML 将加载 `/__sailfin/boot.js`，随后通过同源地址上报。Core 自动为探针配置这个站点，后台收到有效事件后才会展示。

- `--nginx-port` 默认 9080，指定空闲端口以避开已有服务。
- `--nginx-site-id` 默认 `local-web`。不同网站使用不同 ID，避免合并采集数据。可用 `--nginx-site-name` 指定显示名称。
- 默认等业务站点发出同意事件后采集。只在自有本机测试环境设置 `--nginx-require-consent 0`。
- Core 在临时目录生成配置，先执行 `nginx -t` 再启动独立 Nginx。Core 正常退出时会停止它启动的 Nginx。这种方式只监听回环地址，保留现有站点与 Nginx 配置。如需在已有域名和端口上注入，使用上面的 `nginx-inject`。

## 配置本机采集来源

把 [站点示例](../examples/nginx/host-sites.example.json) 复制到 Core 数据库所在的目录，命名为 `sites.json`。也可以用 `SAILFIN_SITES_FILE` 指定其他配置路径。修改这些配置后重启 Core。

读取日志时，把日志目录放进 `SAILFIN_LOG_ROOTS`，多个目录用英文逗号分隔。开发模式可以写入仓库根目录的 `.env`：

```dotenv
SAILFIN_SITES_FILE=/absolute/path/to/sites.json
SAILFIN_LOG_ROOTS=/absolute/path/to/nginx-logs
SAILFIN_RELAY_ADDR=127.0.0.1:8091
```

探针状态默认写入数据库同级的 `local-probe` 目录。若需要移动位置，设置 `SAILFIN_PROBE_STATE` 并保留原有状态文件，使日志偏移和待发数据能继续使用。

站点 `id` 是这个 Core 内的稳定标识，只能使用小写字母、数字、点、下划线和短横线，首字符必须是字母或数字。日志和 JS 采集使用同一 ID。独立站点使用不同 ID。首次上报时的 `name` 将作为显示名称。

## 方式一：读取 Nginx 访问日志

在该站点的 Nginx `server` 块中配置专用日志，例如：

```nginx
access_log /absolute/path/to/nginx-logs/local-web.access.log combined;
```

`sites.json` 中的 `accessLog` 指向该文件，Agent 运行用户需要有文件读取和目录遍历权限。一个路径只用于一个站点。如果 Nginx 运行在容器里，写入宿主机已授权且可读的挂载目录即可。Agent 记录读取偏移，日志中的请求计入 HTTP 请求量和设备统计。只使用此方式时，省略 `browserOrigin` 和 `requireConsent`，无需配置转发端口。

## 方式二：Nginx 注入 JS 并转发

把 [Nginx 示例](../examples/nginx/site.conf) 中的相关指令合并到**宿主机 Nginx** 的站点配置。示例入口是 `http://localhost:9080`，站点上游是 `http://127.0.0.1:8888`。用 Nginx 入口访问网站，`browserOrigin` 填入口的实际协议、主机和端口，末尾不带斜杠。

- 通过 `sub_filter` 在 HTML 的 `</head>` 前加入同源 SDK。执行 `nginx -V` 检查当前 Nginx 是否包含 `http_sub_module`，该模块需要构建时启用。上游 HTML 要能返回未压缩内容，站点 CSP 需要允许加载和连接同源采集路由。浏览器采集需要 HTTPS 或浏览器认可的本地开发安全上下文。[Nginx sub_filter 文档](https://nginx.org/en/docs/http/ngx_http_sub_module.html)
- `/__sailfin/` 代理到同机 Agent 的回环监听地址，默认 `127.0.0.1:8091`，通过 `SAILFIN_RELAY_ADDR` 修改。Nginx 固定覆盖 `X-Sailfin-Site`，值与站点 ID 一致。浏览器沿同源路径上报，探针把事件在本机进程内交给 Core。日志文件可按需省略。[Nginx proxy_pass 文档](https://nginx.org/en/docs/http/ngx_http_proxy_module.html)
- 示例启用了 `requireConsent`。网站已有同意组件时，可在同意后设置 `window.SAILFIN_CONSENT = true` 并派发 `new Event("sailfin:consent")`，撤回时把该值置为 `false` 并派发 `new Event("sailfin:revoke")`。在自有本机测试环境可以设为 `false`，用于页面打开后直接采集。
- 在手动启用配置前执行 `nginx -t`。根据宿主机原有方式重载 Nginx。若原页面返回错误或没有可匹配的 HTML，先检查业务上游。常规网页继续由业务服务返回。

浏览器事件和 Nginx 日志分别计数。同一个页面可能同时产生 HTTP 请求和浏览器 PV。浏览器转发遇到暂时性错误时由 SDK 的有界队列重试，关页后内存队列会消失。Nginx 日志待发队列保存在 Agent 状态目录。

## 自动行为采集字段

注入 SDK 后，无需修改业务代码即可记录普通按钮、链接、role=button/link、自带按钮类型的 input，以及动态新增元素的点击。点击使用捕获阶段监听，在业务处理器改动文字或切换页面前获取名称和页面位置。

事件继续使用现有的 page_view、route_change、click、page_hidden 类型。所有浏览器事件在 properties.pagePath 中保留页面标识；支持 /#/route 和 /#!/route，去除查询参数和普通锚点。顶层 path 仍是 pathname，兼容现有后台统计。页面隐藏或路由切换时，停留时间记到原页面，只累计前台可见时间。

click 的 properties 新增 label（按钮名称）、selector（结构定位）、region（最近的语义区域），elementId 优先使用现有 data-sailfin-id，否则对页面与 DOM 结构生成自动 ID。相同结构下按钮文字变化不会改变自动 ID；DOM 层级、同类兄弟元素顺序变化仍可能改变它，不能保证跨站点改版稳定。页面归并、元素合并与后台命名后续实现，当前优先保留原始数据。

浏览器事件同时上报 properties.pageTitle。收到无 Hash 路由的首页（/、/index.html、/index.htm）事件时，站点名称自动更新为 HTML title，最长 120 UTF-8 字节。内页标题不改站点名称；标题为空或仅有访问日志时沿用原名称。已有站点也在收到新首页事件后更新，无需迁移数据库。

SDK 按事件数与 UTF-8 字节数分批，每批请求体不超过约 48 KiB，成功后继续发送剩余队列。429、5xx 和网络失败留待下一轮重试。离页采用 fetch keepalive 尽力发送，受浏览器生命周期和总字节额度约束，不保证所有积压事件送达。
