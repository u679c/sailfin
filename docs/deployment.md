# 单机打包与部署

单机发布包包含 Sailfin Docker 镜像和宿主机探针。Core、管理 API 和管理页面在 Docker 容器中运行，探针在宿主机读取授权日志、接收浏览器事件并执行经管理端确认的 Nginx 接入操作。两者由安装目录中的 `aqua` 管理。

## 生成发布包

在 macOS 或 Linux 的 amd64、arm64 构建机上准备 Go ≥1.22、Node ≥20.19、npm 和能够构建 Linux 镜像的 Docker。macOS 打包还需要 C 工具链和 `codesign`。在与目标机匹配的系统和 CPU 架构上构建宿主机探针。Linux 安装包里的宿主机程序不依赖 glibc，SQLite 在镜像内运行。

```sh
npm ci
./aqua build install 0.1.1
./aqua build update 0.1.2 --from 99806fc
```

以 Linux amd64 为例，新装包是 `dist/sailfin-0.1.1-linux-amd64/Sailfin-installer-<md5>.bin`，更新包是 `dist/sailfin-update-0.1.2-linux-amd64/Sailfin-update-<md5>.bin`。`--from` 可指定起始 Git 提交或标签，打包时把从该引用到当前 HEAD 的提交摘要加入说明。两种包均包含当前工作区构建的程序和镜像。命令会打印 SHA-256，可随发布说明提供给下载者。原有 `./scripts/package.sh 0.1.1` 和 `make package VERSION=0.1.1` 仍可生成新装包。

再次打包时，脚本会在构建前清除 `dist/` 下同版本、同平台的旧产物目录。

## 安装与启动

下载与操作系统和架构匹配的 Sailfin 安装文件。安装前自行准备以下依赖。

- Docker CLI 和已启动的 Docker 服务。安装检查验证当前 context 使用本机 Unix socket，并且支持 Linux 容器。 [Docker 官方下载](https://docs.docker.com/get-docker/)
- Bash、`tar`、`gzip`、`ps` 等系统工具，以及创建 `/data/sailfin` 的权限。

宿主机自动注入需要 `PATH` 中可用的 `nginx`、`http_sub_module` 以及配置写入和重载权限。使用 1Panel OpenResty 或其他容器化 Web 服务时，可手动指定挂载到宿主机的日志文件路径。 [Nginx 模块文档](https://nginx.org/en/docs/http/ngx_http_sub_module.html) · [手动日志接入](host-nginx.md#手动指定容器日志路径)

将下例中的 MD5 替换为下载文件名中的实际值。安装前可用 `sha256sum "$installer"`（macOS 使用 `shasum -a 256 "$installer"`）计算摘要，并与发布说明中的 SHA-256 核对。

```sh
installer=Sailfin-installer-实际MD5.bin
chmod +x "$installer" &&
  "./$installer"
```

安装文件逐项显示预检结果。终端用绿色 `[v]` 表示通过、红色 `[x]` 表示失败、橙色 `[·]` 表示警告。任一必检项失败时，检查完成后显示「系统校验未通过」并以非零状态退出。未找到宿主机 Nginx、缺少注入模块或未发现同一用户运行的 Nginx 主进程时，预检显示警告并继续检查。

安装过程显示三个阶段：系统校验、解压安装文件、验证安装文件。新装固定写入 `/data/sailfin`，完成后打印 `aqua` 启动命令。可运行 `"./$installer" --check` 单独执行系统校验。已有目标目录时校验失败。

所有 `aqua` 命令都在安装目录下执行。`./aqua start` 会把内嵌镜像导入本机 Docker，启动当前安装专用的 Core 容器和宿主机探针。停止和重启仅操作当前安装创建的容器。日志写入安装目录中的 `.aqua/sailfin.log`。

```sh
./aqua start
./aqua status
./aqua restart
./aqua stop
```

容器端口默认发布到宿主机的 `127.0.0.1:8080`，管理地址为 `http://localhost:8080`。启动后可以检查数据库连通状态。

```sh
curl --fail --silent --show-error http://127.0.0.1:8080/api/v1/health
```

首次启动生成的管理员凭据位于数据库同级的 `bootstrap-admin.txt`，默认路径为 `var/bootstrap-admin.txt`。使用该凭据登录后可修改密码。`./aqua restart` 沿用最近成功启动的参数和环境配置。要更改启动配置，可用 `./aqua stop` 后以新的环境变量或参数执行 `./aqua start`。

## 持久化与访问配置

建议将长期运行的数据放在发布目录之外，并为运行账号创建私有数据目录。以下环境变量可在启动命令或进程管理配置中设置。

```sh
data_dir="$HOME/.local/share/sailfin"
mkdir -p -m 700 "$data_dir"
SAILFIN_DB="$data_dir/sailfin.db" \
SAILFIN_ADDR=127.0.0.1:8080 \
SAILFIN_PUBLIC_URL=http://localhost:8080 \
./aqua start
```

`SAILFIN_DB` 是宿主机上的数据库文件路径，默认 `var/sailfin.db`。其所在目录以绑定挂载提供给容器，同时决定默认站点配置 `sites.json`、探针状态 `local-probe/`、初始凭据和 Nginx 中继配置的位置。迁移已有安装时，将 `SAILFIN_DB` 指向实际使用的数据库文件，包括旧命名的 `tracelite.db`。原来单独配置的 `SAILFIN_SITES_FILE` 和 `SAILFIN_PROBE_STATE` 也要继续使用。已接入的 Nginx 配置可能引用数据库目录下的绝对路径，升级时保留该路径。容器重建时继续使用宿主机上的数据。 [Docker 绑定挂载与端口发布](https://docs.docker.com/reference/cli/docker/container/run/)

通过 `SAILFIN_ADDR` 配置宿主机的回环发布地址和端口。管理界面经 HTTPS 反向代理访问时，将 `SAILFIN_PUBLIC_URL` 设为浏览器实际访问的 Origin，例如 `https://sailfin.example.com`，并将代理上游指向宿主机的发布地址。探针通信路径 `/_sailfin_host/` 只用于本机部署，可在管理端反向代理上拦截。

读取站点日志时，通过 `SAILFIN_LOG_ROOTS` 授权实际日志目录。自动扫描要求 Nginx master 与宿主机探针使用同一 UID，并且该用户有权限读取和写入站点配置、通知对应 master 重载。登录后在 **探针管理 → 站点接入** 查看预览并确认应用。手动接入时，发布目录中的 `examples/sites.example.json` 可作为起点。 [本机探针与 Nginx 接入](host-nginx.md)

## 原位更新

打包时可通过 `SAILFIN_RELEASE_NOTES_FILE` 指向一份最多 8 KiB 的 UTF-8 文本。说明会随 `.bin` 发布，并在管理端「系统信息 → 软件更新」的密码确认前显示。

先在 **系统设置 → 运行设置 → 下载备份** 保存数据库快照，并核对更新包的 SHA-256。更新包要求 Sailfin 已安装到 `/data/sailfin`。在旧版没有启动配置记录时，用原启动配置执行更新，并显式设置 `SAILFIN_DB` 指向实际使用的数据库文件。

```sh
chmod +x ./Sailfin-update-实际MD5.bin
./Sailfin-update-实际MD5.bin --check
./Sailfin-update-实际MD5.bin --apply
```

更新包会导入新镜像、备份旧程序、停止 Sailfin 并替换程序和镜像，最后执行 `/data/sailfin/aqua restart`。数据库、站点配置和探针状态沿用原路径。旧版程序和镜像保存在 `.aqua/previous-release-*`。若新版本启动失败，检查 `.aqua/sailfin.log`，并根据数据库兼容情况决定是否恢复升级前的快照。恢复旧快照会丢失之后写入的数据。 [数据库恢复](security.md#在线备份和恢复)

管理端可预览更新包，命令行负责执行更新。
