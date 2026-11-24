# 单机打包与部署

单机发布包包含 Sailfin Docker 镜像和宿主机探针。Core、管理 API 和管理页面在 Docker 容器中运行，探针在宿主机读取授权日志、接收浏览器事件并执行经管理端确认的 Nginx 接入操作。两者由安装目录中的 `aqua` 管理。

## 生成发布包

在 macOS 或 Linux 的 amd64、arm64 构建机上准备 Go ≥1.22、Node ≥20.19、npm 和能够构建目标架构 Linux 镜像的 Docker。默认按构建机平台打包。Linux 包可用 `--platform` 指定目标系统和架构，宿主机程序不依赖 glibc，SQLite 在镜像内运行。macOS 包仍在同架构的 macOS 上构建，需要 C 工具链和 `codesign`。

以下命令中的版本号和 Git 引用是示例。

```sh
npm ci
./aqua build install 0.1.1
./aqua build update 0.1.2 --from 99806fc
```

从 Apple Silicon Mac 为 Linux amd64 服务器打包时，使用支持 amd64 构建的 Docker，并指定目标平台：

```sh
./aqua build install 0.1.1 --platform linux/amd64
./aqua build update 0.1.2 --platform linux/amd64 --from 99806fc
```

已有安装需要网页更新时，构建更新包沿用其公钥对应的私钥。默认密钥位置与覆盖方式见下文。

新装包和更新包都会在临时构建目录压缩 SDK、缩短内部变量名，再将同一份 SDK 嵌入宿主机探针和 Docker 镜像。发布包中的 SDK 不附带源映射文件。

打包命令传入的版本号会写入 Core 和宿主机探针。打包成功后，脚本会将该值保存到 `internal/branding/VERSION`。省略版本参数时沿用此文件的值。

以 Linux amd64 为例，新装包是 `dist/sailfin-0.1.1-linux-amd64/Sailfin-installer-<md5>.bin`，更新包是 `dist/sailfin-update-0.1.2-linux-amd64/Sailfin-update-<md5>.bin`。`--from` 可指定起始 Git 提交或标签，打包时把从该引用到当前 HEAD 的提交摘要加入说明。两种包均包含当前工作区构建的程序和镜像。命令会打印 SHA-256，可随发布说明提供给下载者。原有 `./scripts/package.sh 0.1.1` 和 `make package VERSION=0.1.1` 仍可生成新装包。

再次打包时，脚本会在构建前清除 `dist/` 下同版本、同平台的旧产物目录。

## 安装与启动

下载与操作系统和架构匹配的 Sailfin 安装文件。安装前自行准备以下依赖。

- Docker CLI 和已启动的 Docker 服务。安装检查验证当前 context 使用本机 Unix socket，并且支持 Linux 容器。 [Docker 官方下载](https://docs.docker.com/get-docker/)
- Bash、`tar`、`gzip`、`ps` 等系统工具，以及创建 `/data/sailfin` 的权限。

宿主机自动注入需要 `PATH` 中可用的 `nginx`、`http_sub_module` 以及配置写入和重载权限。符合条件的 1Panel OpenResty 可在启动时指定目标容器后自动接入。容器化 Web 服务也可手动指定挂载到宿主机的日志文件路径。 [1Panel 自动接入与手动日志接入](host-nginx.md) · [Nginx 模块文档](https://nginx.org/en/docs/http/ngx_http_sub_module.html)

将下例中的 MD5 替换为下载文件名中的实际值。安装前可用 `sha256sum "$installer"`（macOS 使用 `shasum -a 256 "$installer"`）计算摘要，并与发布说明中的 SHA-256 核对。

```sh
installer=Sailfin-installer-实际MD5.bin
chmod +x "$installer" &&
  "./$installer"
```

安装文件逐项显示预检结果。终端用绿色 `[v]` 表示通过、红色 `[x]` 表示失败、橙色 `[·]` 表示警告。任一必检项失败时，检查完成后显示「系统校验未通过」并以非零状态退出。未找到宿主机 Nginx、缺少注入模块或未发现同一用户运行的 Nginx 主进程时，预检显示警告并继续检查。

安装过程显示三个阶段：系统校验、解压安装文件、验证安装文件。新装固定写入 `/data/sailfin`，完成后打印 `aqua` 启动命令。可运行 `"./$installer" --check` 单独执行系统校验。已有目标目录时校验失败。

以下运行管理命令在安装目录下执行。`./aqua start` 会把内嵌镜像导入本机 Docker，启动当前安装专用的 Core 容器和宿主机探针。停止和重启仅操作当前安装创建的容器。日志写入安装目录中的 `.aqua/sailfin.log`。

```sh
./aqua --help
./aqua start
./aqua status
./aqua restart
./aqua stop
```

容器端口默认发布到宿主机的 `127.0.0.1:8080`，管理地址为 `http://localhost:8080`。启动后可以检查数据库连通状态。

```sh
curl --fail --silent --show-error http://127.0.0.1:8080/api/v1/health
```

首次启动未配置管理员密码时，生成的凭据位于数据库同级的 `bootstrap-admin.txt`，默认路径为 `var/bootstrap-admin.txt`。使用该凭据登录后可修改密码。`./aqua restart` 沿用最近成功启动的参数和环境配置。要更改启动配置，可用 `./aqua stop` 后以新的环境变量或参数执行 `./aqua start`。

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

读取站点日志时，通过 `SAILFIN_LOG_ROOTS` 授权实际日志目录。扫描宿主机 Nginx 要求 master 与宿主机探针使用同一 UID，并且该用户有权限读取和写入站点配置、通知对应 master 重载。登录后在 **探针管理 → 站点接入 → 添加站点 → 一键扫描** 点击「扫描站点」，再勾选并确认应用。手动接入时，发布目录中的 `examples/sites.example.json` 可作为起点。 [本机探针与 Nginx 接入](host-nginx.md)

## 原位更新

更新包会把 Git 提交摘要写入「更新说明」。用 `--from <提交或标签>` 指定起点时，收录该起点之后到当前 HEAD 的提交。省略 `--from` 时，从最近的上一个版本标签取记录。如果仓库没有版本标签，则收录最近 10 条提交。仅有源码文件的目录无法读取 Git 历史，可从完整 Git 仓库打包。

也可通过 `SAILFIN_RELEASE_NOTES_FILE` 指定一份最多 8 KiB 的 UTF-8 更新说明，自定义内容显示在提交摘要前面。管理端预览最多显示 8 KiB，完整的所选提交摘要随包放在 `git-history.txt`。

先在 **系统设置 → 运行设置 → 下载备份** 保存数据库快照，并核对更新包的 SHA-256。更新包要求 Sailfin 已安装到 `/data/sailfin`。在旧版没有启动配置记录时，用原启动配置执行更新，并显式设置 `SAILFIN_DB` 指向实际使用的数据库文件。

```sh
chmod +x ./Sailfin-update-实际MD5.bin
./Sailfin-update-实际MD5.bin --check
./Sailfin-update-实际MD5.bin --apply
```

更新包会导入新镜像、备份旧程序、停止 Sailfin 并替换程序和镜像，最后执行 `/data/sailfin/aqua restart`。数据库、站点配置和探针状态沿用原路径。旧版程序和镜像保存在 `.aqua/previous-release-*`。若新版本启动失败，检查 `.aqua/sailfin.log`，并根据数据库兼容情况决定是否恢复升级前的快照。恢复旧快照会丢失之后写入的数据。 [数据库恢复](security.md#在线备份和恢复)

### 从管理端更新

打包使用 Ed25519 密钥。未设置 `SAILFIN_UPDATE_SIGNING_KEY` 时，脚本使用 `$XDG_CONFIG_HOME/sailfin/update-signing-key.pem`，未设置 `XDG_CONFIG_HOME` 时使用 `$HOME/.config/sailfin/update-signing-key.pem`。默认路径没有密钥时会自动生成，后续打包复用。指定 `SAILFIN_UPDATE_SIGNING_KEY` 时，脚本读取该路径下已有的私钥。

新装包附带从该私钥导出的 `update-public.pem`，`aqua start` 会自动读取。更新包用该私钥签名。换打包机或构建账号时，沿用原私钥签名的新包才能通过已有安装的公钥验证。发布包只包含公钥或签名。

```sh
release_keys="$HOME/.config/sailfin-release"
mkdir -p "$release_keys"
if [ ! -e "$release_keys/sailfin-update-private.pem" ]; then
    (umask 077; openssl genpkey -algorithm ED25519 -out "$release_keys/sailfin-update-private.pem")
fi

SAILFIN_UPDATE_SIGNING_KEY="$release_keys/sailfin-update-private.pem" ./aqua build install 0.2.1
SAILFIN_UPDATE_SIGNING_KEY="$release_keys/sailfin-update-private.pem" ./aqua build update 0.2.2
```

已有安装继续使用原有的公钥。还没有公钥的旧安装，可在通过命令行升级到支持网页更新的版本后，在宿主机放置对应的公钥，并用 `SAILFIN_UPDATE_PUBLIC_KEY=/path/to/sailfin-update-public.pem` 带原有配置启动一次。显式指定的公钥优先于安装目录中的公钥。两种方式都要求公钥位于容器的数据挂载之外。网页更新包沿用原公钥进行验证，升级时不会替换它。更换打包机时可迁移原私钥，无需逐台调整公钥。

反向代理要允许此上传接口接收更新包，请求体上限至少能容纳实际包大小和表单开销，Sailfin 支持最多 256 MiB 的包。OpenResty 可以为 `/api/v1/system/update/install` 设置 `client_max_body_size 257m`。

在「系统信息 → 软件更新」选择签名后的 `.bin`，验证管理员密码并点击「确认安装」。宿主机重新验证签名，执行更新包的 `--check` 和 `--apply`。管理页面会在重启后查询更新结果。尚未配置网页更新公钥的旧安装可使用上面的命令行更新流程。
