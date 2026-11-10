# Common tasks. CI runs the same targets.
GO_PACKAGES := ./cmd/... ./internal/...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web-install web-build web-test web-e2e web-lint go-build go-test go-vet test lint build release clean

all: build

web-install:
	cd web && npm ci --no-audit --no-fund

web-build:
	cd web && npm run build

web-test:
	cd web && npm test

web-e2e: build
	cd web && npx playwright test

web-lint:
	cd web && npm run typecheck && npm run lint && npm run format:check

go-vet:
	gofmt -l cmd internal | tee /dev/stderr | test -z "$$(cat)"
	go vet $(GO_PACKAGES)

go-test:
	go test -race -count=1 $(GO_PACKAGES)

go-build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cc-manager ./cmd/cc-manager

generate:
	go run ./internal/errs/gen

check-generated:
	go test ./internal/errs/ -run TestGeneratedArtifactsAreCurrent

test: go-vet go-test web-test check-generated

lint: go-vet web-lint

build: web-build go-build

# Produces dist/cc-manager-<version>-<os>-<arch>.tar.gz with a SHA256SUMS file.
release: build
	@set -e; os=$$(go env GOOS); arch=$$(go env GOARCH); name=cc-manager-$(VERSION)-$$os-$$arch; \
	rm -rf dist/$$name && mkdir -p dist/$$name/web dist/$$name/deploy; \
	cp bin/cc-manager dist/$$name/ && cp -R web/dist dist/$$name/web/dist && cp -R deploy/production dist/$$name/deploy/production; \
	(cd dist/$$name && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 shasum -a 256 > SHA256SUMS); \
	tar -C dist -czf dist/$$name.tar.gz $$name; \
	(cd dist && shasum -a 256 $$name.tar.gz > $$name.tar.gz.sha256); \
	echo "built dist/$$name.tar.gz"

clean:
	rm -rf bin dist web/dist
