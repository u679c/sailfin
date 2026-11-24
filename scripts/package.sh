#!/usr/bin/env bash
set -euo pipefail

CDPATH= cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
kind=install
case "${1:-}" in
    install|update) kind=$1; shift ;;
esac
version_file="$PWD/internal/branding/VERSION"
version=${SAILFIN_PACKAGE_VERSION:-$(<"$version_file")}
if [[ $# -gt 0 && $1 != --* ]]; then
    version=$1
    shift
fi
if [[ ! $version =~ ^[0-9][A-Za-z0-9._-]*$ ]]; then
    printf 'Usage: ./aqua build install|update [version] [--platform <os/arch>] [--from <git-ref> (update only)]\n' >&2
    exit 2
fi
platform=''
from=''
while [[ $# -gt 0 ]]; do
    if [[ $1 == --platform && -z $platform && -n ${2:-} ]]; then
        platform=$2
    elif [[ $1 == --from && $kind == update && -z $from && -n ${2:-} && $2 != --* ]]; then
        from=$2
    else
        printf 'Invalid build argument: %s. See ./aqua for usage.\n' "$1" >&2
        exit 2
    fi
    shift 2
done
base=''
git_history=''
if [[ -n $from ]]; then
    base=$(git rev-parse --verify --end-of-options "$from^{commit}")
    if ! git merge-base --is-ancestor "$base" HEAD; then
        printf 'The base commit must be an ancestor of HEAD.\n' >&2
        exit 2
    fi
    git_history=$(git log --format='%h %s' "$base..HEAD")
elif [[ $kind == update ]]; then
    repo=$(git rev-parse --show-toplevel 2>/dev/null || :)
    if [[ $repo == "$PWD" ]]; then
        base=$(git describe --tags --abbrev=0 --match 'v[0-9]*' --match '[0-9]*' HEAD^ 2>/dev/null || :)
        if [[ -n $base ]]; then
            git_history=$(git log --format='%h %s' "$base..HEAD")
        else
            git_history=$(git log -n 10 --format='%h %s')
        fi
    else
        printf 'Git history unavailable in this source directory.\n' >&2
    fi
fi

host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
platform=${platform:-$host_os/$host_arch}
case "$platform" in
    darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
    *) printf 'Unsupported package platform: %s\n' "$platform" >&2; exit 2 ;;
esac
os=${platform%/*}
arch=${platform#*/}
if [[ $os == darwin && $platform != "$host_os/$host_arch" ]]; then
    printf 'Build macOS packages on a matching macOS host: %s\n' "$platform" >&2
    exit 2
fi
ldflags=${SAILFIN_PACKAGE_LDFLAGS-}
if [[ $os == darwin && -z ${SAILFIN_PACKAGE_LDFLAGS+set} ]]; then
    ldflags=-linkmode=external
fi
ldflags="${ldflags:+$ldflags }-X sailfin/internal/branding.packageVersion=$version"

name="sailfin-$version-$os-$arch"
release_notes=''
if [[ -n ${SAILFIN_RELEASE_NOTES_FILE:-} ]]; then
    notes_file=$SAILFIN_RELEASE_NOTES_FILE
    if [[ ! -f $notes_file || $(wc -c < "$notes_file") -gt 8192 ]]; then
        printf 'Release notes must be a file of at most 8192 bytes.\n' >&2
        exit 2
    fi
    release_notes=$(cat -- "$notes_file")
fi
# Keep the preview within the release-note size limit without dropping full history.
if [[ -n $git_history ]]; then
    notes_bytes=$(printf '%s' "$release_notes" | wc -c)
    git_summary=$'Git 提交\n'
    summary_bytes=$(printf '%s' "$git_summary" | wc -c)
    separator=${release_notes:+$'\n\n'}
    while IFS= read -r line; do
        line_bytes=$(printf '%s\n' "$line" | wc -c)
        if (( notes_bytes + ${#separator} + summary_bytes + line_bytes > 8192 )); then
            printf 'Git history preview truncated. The complete selected history is in git-history.txt in the package.\n' >&2
            break
        fi
        git_summary+="$line"$'\n'
        summary_bytes=$(( summary_bytes + line_bytes ))
    done <<< "$git_history"
    if [[ $git_summary != $'Git 提交\n' ]]; then
        release_notes+="$separator$git_summary"
    fi
fi
release_notes=$(printf '%s' "$release_notes" | base64 | tr -d '\r\n')
sign_script="$PWD/scripts/sign-package.mjs"
sign_key=${SAILFIN_UPDATE_SIGNING_KEY:-}
key_mode=--public-key
if [[ -z $sign_key ]]; then
    config_dir=${XDG_CONFIG_HOME:-${HOME:+$HOME/.config}}
    if [[ -z $config_dir ]]; then
        printf 'Set SAILFIN_UPDATE_SIGNING_KEY to an Ed25519 private key file.\n' >&2
        exit 2
    fi
    sign_key="$config_dir/sailfin/update-signing-key.pem"
    key_mode=--ensure-public-key
fi
if [[ $sign_key != /* ]]; then sign_key="$PWD/$sign_key"; fi
public_key=$(node "$sign_script" "$key_mode" "$sign_key")
output="$PWD/dist"
release="$output/$name"
package_prefix=Sailfin-installer
if [[ $kind == update ]]; then
    release="$output/sailfin-update-$version-$os-$arch"
    package_prefix=Sailfin-update
fi
if [[ -L $output ]]; then
    printf 'Release output must be a local directory: %s\n' "$output" >&2
    exit 1
fi
mkdir -p "$output"
if [[ -e $release || -L $release ]]; then
    printf 'Removing previous release: %s\n' "$release"
    rm -rf -- "$release"
fi
stage=$(mktemp -d "$output/.package.XXXXXXXX")
trap 'rm -rf -- "$stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
bundle="$stage/$name"

# Build into an empty directory so binaries and hashed Web assets belong to this build.
mkdir -p "$bundle/bin" "$bundle/examples"
if [[ -n $git_history ]]; then
    printf '%s\n' "$git_history" > "$bundle/git-history.txt"
fi
if [[ $kind == install ]]; then
    printf '%s\n' "$public_key" > "$bundle/update-public.pem"
fi
# The Linux host process runs Aqua and the probe. SQLite runs inside the image.
host_cgo=0
if [[ $os == darwin ]]; then
    host_cgo=1
fi
CGO_ENABLED="$host_cgo" GOOS="$os" GOARCH="$arch" \
    go build -ldflags="$ldflags" -trimpath -o "$bundle/bin/sailfin-core" ./cmd/core
if [[ $os == darwin ]]; then
    codesign --force --sign - "$bundle/bin/sailfin-core"
    codesign --verify --strict "$bundle/bin/sailfin-core"
fi
image_context="$stage/image"
mkdir -p "$image_context/cmd" "$image_context/browser-agent"
cp go.mod go.sum "$image_context/"
cp -R cmd/core "$image_context/cmd/"
cp -R internal "$image_context/"
cp browser-agent/embed.go "$image_context/browser-agent/"
cp -R browser-agent/src "$image_context/browser-agent/"
npm run build -w web -- --outDir "$image_context/web"
test -s "$image_context/web/index.html"
image_tag="sailfin-package:$os-$arch-${stage##*.}"
docker build --platform "linux/$arch" --tag "$image_tag" --build-arg "SAILFIN_VERSION=$version" \
    --file deploy/Dockerfile "$image_context"
docker image save --output "$bundle/image.tar" "$image_tag"
printf '%s\n' "$image_tag" > "$bundle/image-ref"
install -m 755 deploy/aqua "$bundle/aqua"
install -m 644 examples/nginx/host-sites.example.json "$bundle/examples/sites.example.json"

installer_template="$PWD/deploy/installer.sh"
mkdir "$stage/release"
cd "$stage/release"

payload_line=$(( $(wc -l < "$installer_template") + 1 ))
sed -e "s|@PACKAGE@|$name|g" -e "s|@PLATFORM@|$os/$arch|g" \
    -e "s|@PACKAGE_KIND@|$kind|g" \
    -e "s|@PACKAGE_SIGNATURE@|$(printf '%0128d' 0)|g" \
    -e "s|@RELEASE_NOTES@|$release_notes|g" \
    -e "s|@PAYLOAD_LINE@|$payload_line|g" "$installer_template" > installer.bin
COPYFILE_DISABLE=1 tar -czf - -C "$stage" "$name" >> installer.bin
if [[ $kind == update ]]; then
    node "$sign_script" installer.bin "$sign_key"
fi
if command -v md5sum >/dev/null 2>&1; then
    digest=$(md5sum installer.bin)
    digest=${digest%% *}
else
    digest=$(md5 -q installer.bin)
fi
installer="$package_prefix-$digest.bin"
mv installer.bin "$installer"
chmod 755 "$installer"

if command -v sha256sum >/dev/null 2>&1; then
    checksum=$(sha256sum "$installer")
else
    checksum=$(shasum -a 256 "$installer")
fi
cd "$output"
# Publish the finished installer as one directory.
node -e 'require("node:fs").renameSync(process.argv[1], process.argv[2])' "$stage/release" "$release"
if [[ $(<"$version_file") != "$version" ]]; then
    printf '%s\n' "$version" > "$stage/VERSION"
    mv -f -- "$stage/VERSION" "$version_file"
fi
printf 'Package: %s/%s\nSHA-256: %s\n' "$release" "$installer" "${checksum%% *}"
