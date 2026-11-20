GO_LDFLAGS ?= $(if $(filter Darwin,$(shell uname -s)),-linkmode=external,)

.PHONY: setup dev test build package docker-test
setup:
	@command -v go >/dev/null && command -v node >/dev/null && command -v npm >/dev/null
	go mod download
	npm install
	@mkdir -p var
	@echo 'Dependencies ready. Run make dev; first Core startup creates var/bootstrap-admin.txt.'
dev:
	npm run dev
test:
	go test -ldflags="$(GO_LDFLAGS)" ./...
	npm test
build:
	mkdir -p bin
	go build -ldflags="$(GO_LDFLAGS)" -trimpath -o bin/sailfin-core ./cmd/core
	npm run build -w web
package: export SAILFIN_PACKAGE_VERSION = $(VERSION)
package: export SAILFIN_PACKAGE_LDFLAGS = $(GO_LDFLAGS)
package:
	bash scripts/package.sh
docker-test:
	go test -ldflags="$(GO_LDFLAGS)" ./internal/agent -run TestLocalDocker -v -count=1

.PHONY: site check
site:
	node scripts/site.mjs
check:
	npm run check
