#!/usr/bin/env bash
set -euo pipefail

CDPATH= cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
version=${1:-${SAILFIN_PACKAGE_VERSION:-}}
if [[ $# -gt 1 || ! $version =~ ^[0-9][A-Za-z0-9._-]*$ ]]; then
    printf 'Usage: ./scripts/package.sh 0.1.1\n' >&2
    exit 2
fi

os=$(go env GOHOSTOS)
arch=$(go env GOHOSTARCH)
case "$os/$arch" in
    darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
    *) printf 'Unsupported build platform: %s/%s\n' "$os" "$arch" >&2; exit 1 ;;
esac
ldflags=${SAILFIN_PACKAGE_LDFLAGS-}
if [[ $os == darwin && -z ${SAILFIN_PACKAGE_LDFLAGS+set} ]]; then
    ldflags=-linkmode=external
fi

name="sailfin-$version-$os-$arch"
output="$PWD/dist"
release="$output/$name"
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
docker build --platform "linux/$arch" --tag "$image_tag" \
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
    -e "s|@PAYLOAD_LINE@|$payload_line|g" "$installer_template" > installer.bin
COPYFILE_DISABLE=1 tar -czf - -C "$stage" "$name" >> installer.bin
if command -v md5sum >/dev/null 2>&1; then
    digest=$(md5sum installer.bin)
    digest=${digest%% *}
else
    digest=$(md5 -q installer.bin)
fi
installer="Sailfin-installer-$digest.bin"
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
printf 'Installer: %s/%s\nSHA-256: %s\n' "$release" "$installer" "${checksum%% *}"
