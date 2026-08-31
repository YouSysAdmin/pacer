.PHONY: all build build-linux-amd64 build-docker frontend run dev test test-go test-frontend lint release clean help

VERSION       := $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS       := -ldflags "-X github.com/yousysadmin/pacer/pkg.Version=$(VERSION)"
BIN           := bin/pacer
BIN_LINUX_X64 := bin/pacer-linux-amd64
DEV_CONFIG    := examples/dev.yaml

# Cross-compiled artifacts produced by `make release`. Filename
# encodes GOOS and GOARCH so the static pattern rule below parses
# them out of the stem -- adding a platform = appending one entry.
RELEASE_TARGETS := \
	bin/pacer-linux-amd64 \
	bin/pacer-linux-arm64 \
	bin/pacer-darwin-arm64

# Override DOCKER_GOARCH=amd64 (or arm64) to build a dev image for
# a non-host architecture. Default tracks the host's GOARCH.
DOCKER_GOARCH ?= $(shell go env GOARCH)
DOCKER_IMAGE  ?= pacer:dev
DOCKER_CTX    := bin/docker

help:
	@echo "Targets:"
	@echo "  all                alias for build (frontend + host binary)"
	@echo "  build              frontend + binary for the host platform"
	@echo "  build-linux-amd64  frontend + cross-compile static linux/amd64 binary (for EC2 deploy)"
	@echo "  release            frontend + cross-compile linux/amd64, linux/arm64, darwin/arm64 binaries"
	@echo "  build-docker       frontend + linux/$(DOCKER_GOARCH) binary + docker image $(DOCKER_IMAGE)"
	@echo "  frontend           build Vue SPA into frontend/dist/ (embedded by go:embed)"
	@echo "  run                build + run serve"
	@echo "  dev                go run with $(DEV_CONFIG) (UI-only, no GitHub App / AWS)"
	@echo "  test               run Go + frontend test suites"
	@echo "  test-go            run Go test suite"
	@echo "  test-frontend      run vitest suite"
	@echo "  lint               run eslint + prettier over frontend/"
	@echo "  clean              bin/ and frontend/dist/ contents"

all: build

frontend:
	cd frontend && bun install && bun run build
	@printf '*\n!.gitignore\n' > frontend/dist/.gitignore

# Frontend lint via eslint (flat config). Run before commits / CI.
# Auto-fix variant available as `cd frontend && bun run lint:fix`.
lint:
	cd frontend && bun install && bun run lint

build: frontend
	mkdir -p bin
	go build $(LDFLAGS) -o $(BIN) ./cmd/pacer

# Cross-compiled platform binaries. Static pattern rule parses
# <os>-<arch> from the stem (the % match) and feeds it to go build
# via GOOS / GOARCH. CGO_ENABLED=0 keeps the modernc/sqlite-driven
# builds fully static so the artifacts run on a bare distro.
$(RELEASE_TARGETS): bin/pacer-%: frontend
	@mkdir -p bin
	CGO_ENABLED=0 \
		GOOS=$(word 1,$(subst -, ,$*)) \
		GOARCH=$(word 2,$(subst -, ,$*)) \
		go build $(LDFLAGS) -o $@ ./cmd/pacer
	@file $@ 2>/dev/null || true

# Compatibility alias: existing call-sites use `make build-linux-amd64`.
build-linux-amd64: $(BIN_LINUX_X64)

release: $(RELEASE_TARGETS)
	@echo "release artifacts ($(VERSION)):"
	@ls -lh $(RELEASE_TARGETS)

# Build a dev container image. The Dockerfile is intentionally
# minimal -- it expects a pre-built linux `pacer` binary at the
# build-context root and just copies it into a distroless image.
# Goreleaser uses the same Dockerfile in its release pipeline.
build-docker: frontend
	mkdir -p $(DOCKER_CTX)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DOCKER_GOARCH) \
		go build $(LDFLAGS) -o $(DOCKER_CTX)/pacer ./cmd/pacer
	docker build -f Dockerfile -t $(DOCKER_IMAGE) $(DOCKER_CTX)
	@echo "built $(DOCKER_IMAGE) (linux/$(DOCKER_GOARCH))"

run: build
	./$(BIN) serve

dev: frontend
	go run $(LDFLAGS) ./cmd/pacer serve --config $(DEV_CONFIG)

test: test-go test-frontend

test-go:
	go test ./...

test-frontend:
	cd frontend && bun run test

clean:
	rm -rf bin
	find frontend/dist -mindepth 1 ! -name '.gitignore' -delete
	@printf '*\n!.gitignore\n' > frontend/dist/.gitignore
