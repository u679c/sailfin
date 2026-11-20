#!/bin/bash
set -euo pipefail

package='@PACKAGE@'
platform='@PLATFORM@'
payload_line='@PAYLOAD_LINE@'

fail() {
    printf 'Sailfin 安装失败：%s\n' "$*" >&2
    exit 1
}

red='' green='' orange='' reset=''
if [[ -t 1 && -z ${NO_COLOR:-} ]]; then
    red=$'\033[31m'
    green=$'\033[32m'
    orange=$'\033[38;5;208m'
    reset=$'\033[0m'
fi

check_failed=0
passed() { printf '%s[v] %s%s\n' "$green" "$*" "$reset"; }
warning() { printf '%s[·] %s%s\n' "$orange" "$*" "$reset"; }
rejected() {
    printf '%s[x] %s%s\n' "$red" "$*" "$reset"
    check_failed=1
}

case "${1:-}" in
    --check)
        [[ $# -eq 1 ]] || fail '用法：安装文件 [--check]'
        printf '[1/1] 系统校验\n'
        ;;
    '')
        [[ $# -eq 0 ]] || fail '用法：安装文件 [--check]'
        printf '[1/3] 系统校验\n'
        ;;
    *) fail '用法：安装文件 [--check]';;
esac

missing_tools=''
for tool in mkdir ps tar gzip tail rm; do
    command -v "$tool" >/dev/null 2>&1 || missing_tools+=" $tool"
done
if [[ -z $missing_tools ]]; then
    passed '系统工具可用'
else
    rejected "缺少系统工具：${missing_tools# }，请先安装。"
fi

if host_os=$(uname -s) && host_arch=$(uname -m); then
    case "$host_os/$host_arch" in
        Darwin/x86_64) host=darwin/amd64 ;;
        Darwin/arm64) host=darwin/arm64 ;;
        Linux/x86_64) host=linux/amd64 ;;
        Linux/aarch64|Linux/arm64) host=linux/arm64 ;;
        *) host="$host_os/$host_arch" ;;
    esac
    if [[ $host == "$platform" ]]; then
        passed "系统和架构匹配（${host}）"
    else
        rejected "安装包适用于 ${platform}，当前平台为 ${host}。"
    fi
else
    rejected '无法读取系统和架构，请检查 uname。'
fi

if ! command -v docker >/dev/null 2>&1; then
    rejected '找不到 Docker，请自行下载安装。'
elif docker_version=$(docker info --format '{{.OSType}} {{.ServerVersion}}' 2>/dev/null); then
    docker_endpoint=${DOCKER_HOST:-}
    if [[ -n ${DOCKER_CONTEXT:-} || -z $docker_endpoint ]]; then
        docker_endpoint=$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null) || docker_endpoint=''
    fi
    case "$docker_endpoint" in
        unix:///*) passed 'Docker 使用本机 Unix socket' ;;
        *) rejected '请切换到使用本机 Unix socket 的 Docker context。' ;;
    esac
    if [[ $docker_version == 'linux '* ]]; then
        passed "Docker 服务可连接（${docker_version#linux }）"
    else
        rejected '需要支持 Linux 容器的 Docker 服务。'
    fi
else
    rejected '无法连接 Docker，请启动服务并检查当前用户权限。'
fi

if ! command -v nginx >/dev/null 2>&1; then
    rejected '找不到 Nginx，请自行安装并将其加入 PATH。'
elif nginx_build=$(nginx -V 2>&1); then
    nginx_build=${nginx_build//\'/}
    case " $nginx_build " in
        *' --with-http_sub_module '*) passed 'Nginx 可用，已包含 http_sub_module' ;;
        *) rejected 'Nginx 缺少 http_sub_module，请安装包含该模块的版本。' ;;
    esac

    if processes=$(ps -Aww -o uid=,pid=,command= 2>/dev/null); then
        master="(^|"$'\n'")[[:blank:]]*$EUID[[:blank:]]+[0-9]+[[:blank:]]+nginx: master process "
        if [[ $processes =~ $master ]]; then
            passed '已发现由当前用户运行的 Nginx 主进程'
        else
            warning '当前用户下未发现 Nginx 主进程，自动接入前请检查运行用户。'
        fi
    else
        warning '暂时无法查看 Nginx 进程，自动接入前请检查进程读取权限。'
    fi
else
    rejected '无法执行 nginx -V，请检查 Nginx 安装。'
fi

if parent_path=$(dirname -- "$0") && parent=$(cd -- "$parent_path" && pwd -P); then
    destination="$parent/$package"
    if [[ ! -w $parent || ! -x $parent ]]; then
        rejected "安装文件所在目录不可写：$parent"
    elif [[ -e $destination || -L $destination ]]; then
        rejected "安装目录已存在：$destination"
    else
        passed "安装目录可用：$destination"
    fi
else
    rejected '无法读取安装文件所在目录，请检查 dirname 和目录访问权限。'
fi

if [[ $check_failed -ne 0 ]]; then
    printf '\n%s系统校验未通过%s\n' "$red" "$reset"
    exit 1
fi
printf '\n%s系统校验通过%s\n' "$green" "$reset"
if [[ ${1:-} == --check ]]; then
    exit 0
fi

umask 022
printf '\n[2/3] 解压安装文件到 %s\n' "$destination"
mkdir -- "$destination" || fail "无法创建安装目录：$destination"
trap 'rm -rf -- "$destination"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if ! tail -n "+$payload_line" "$0" | tar -xzvf - -C "$destination" --strip-components 1; then
    fail '无法解压安装文件，请重新下载并核对 SHA-256。'
fi
printf '\n[3/3] 验证安装文件\n'
if ! "$destination/bin/sailfin-core" serve --help >/dev/null 2>&1; then
    fail 'Core 无法在当前系统运行，请检查系统运行库和安装文件。'
fi
[[ -x $destination/aqua ]] || fail '安装包缺少 aqua 管理命令。'
[[ -s $destination/image.tar && -s $destination/image-id ]] || fail '安装包缺少 Sailfin Docker 镜像。'
trap - EXIT
printf '\n安装完成。请在安装目录执行：\n  cd %q\n  ./aqua start\n' "$destination"
exit 0
