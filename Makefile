APP        := cc-connect
MODULE     := github.com/chenhg5/cc-connect
CMD        := ./cmd/cc-connect
DIST       := dist
LOCAL_NPM_ROOT ?= $(shell npm root -g 2>/dev/null)
LOCAL_CC_CONNECT_DIR ?= $(LOCAL_NPM_ROOT)/cc-connect
LOCAL_CC_CONNECT_BIN ?= $(LOCAL_CC_CONNECT_DIR)/bin/$(APP)

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS := -s -w \
  -X main.version=$(VERSION) \
  -X main.commit=$(COMMIT) \
  -X main.buildTime=$(BUILD_TIME)

PLATFORMS := \
  linux/amd64 \
  linux/arm64 \
  darwin/amd64 \
  darwin/arm64 \
  windows/amd64 \
  windows/arm64

# ---------------------------------------------------------------------------
# Selective compilation via build tags.
#
# By default all agents and platforms are included. To build with only
# specific ones, set AGENTS and/or PLATFORMS_INCLUDE:
#
#   make build AGENTS=claudecode PLATFORMS_INCLUDE=feishu,telegram
#
# You can also exclude specific ones:
#
#   make build EXCLUDE=discord,dingtalk,qq,qqbot,line
# ---------------------------------------------------------------------------

ALL_AGENTS    := acp claudecode codex cursor devin gemini iflow kimi opencode pi qoder
ALL_PLATFORMS := feishu telegram discord slack dingtalk wecom weixin qq qqbot line weibo max
ALL_EXTRAS    := web

COMMA := ,

# Compute exclusion tags from AGENTS / PLATFORMS_INCLUDE / EXCLUDE variables
_EXCLUDE_TAGS :=

ifdef AGENTS
  _WANTED_AGENTS := $(subst $(COMMA), ,$(AGENTS))
  _EXCLUDE_AGENTS := $(filter-out $(_WANTED_AGENTS),$(ALL_AGENTS))
  _EXCLUDE_TAGS += $(addprefix no_,$(_EXCLUDE_AGENTS))
endif

ifdef PLATFORMS_INCLUDE
  _WANTED_PLATFORMS := $(subst $(COMMA), ,$(PLATFORMS_INCLUDE))
  _EXCLUDE_PLATFORMS := $(filter-out $(_WANTED_PLATFORMS),$(ALL_PLATFORMS))
  _EXCLUDE_TAGS += $(addprefix no_,$(_EXCLUDE_PLATFORMS))
endif

ifdef EXCLUDE
  _EXCLUDE_TAGS += $(addprefix no_,$(subst $(COMMA), ,$(EXCLUDE)))
endif

ifdef NO_WEB
  _EXCLUDE_TAGS += no_web
endif

_BUILD_TAGS := $(strip $(_EXCLUDE_TAGS))
_TAGS_FLAG  := $(if $(_BUILD_TAGS),-tags '$(_BUILD_TAGS)',)

.PHONY: build build-local build-noweb run clean test test-fast test-full test-smoke test-e2e test-release test-release-local test-performance pre-test lint release release-all web

web:
	@if [ ! -d web/node_modules ]; then cd web && npm install; fi
	cd web && npm run build

build: web
	go build $(_TAGS_FLAG) -ldflags "$(LDFLAGS)" -o $(APP) $(CMD)

# Build a local-development binary and replace the global npm wrapper files
# plus the real executable. Each invocation bumps the installed local version
# suffix (for example: 1.3.3-beta.2.1 -> 1.3.3-beta.2.2).
build-local: web
	@if [ -z "$(LOCAL_NPM_ROOT)" ]; then \
		echo "npm root -g failed; set LOCAL_CC_CONNECT_BIN manually."; \
		exit 1; \
	fi
	@if [ ! -d "$(LOCAL_CC_CONNECT_DIR)" ]; then \
		echo "Global npm package not found: $(LOCAL_CC_CONNECT_DIR)"; \
		echo "Install with: npm install -g cc-connect"; \
		echo "Or run: make build-local LOCAL_CC_CONNECT_BIN=/your/path/cc-connect"; \
		exit 1; \
	fi
	@set -e; \
		LOCAL_VERSION=$$(node npm/local-version.js next ./npm/package.json "$(LOCAL_CC_CONNECT_DIR)/package.json"); \
		echo "Building local npm version $$LOCAL_VERSION"; \
		go build $(_TAGS_FLAG) -ldflags "-s -w -X main.version=$$LOCAL_VERSION -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)" -o $(APP) $(CMD); \
		node npm/local-version.js write-package ./npm/package.json "$(LOCAL_CC_CONNECT_DIR)/package.json" "$$LOCAL_VERSION"; \
		install -m 755 npm/run.js "$(LOCAL_CC_CONNECT_DIR)/run.js"; \
		install -m 755 npm/install.js "$(LOCAL_CC_CONNECT_DIR)/install.js"; \
		install -m 644 npm/README.md "$(LOCAL_CC_CONNECT_DIR)/README.md"; \
		mkdir -p "$(dir $(LOCAL_CC_CONNECT_BIN))"; \
		install -m 755 $(APP) "$(LOCAL_CC_CONNECT_BIN)"; \
		echo "Updated local npm wrapper files in: $(LOCAL_CC_CONNECT_DIR)"; \
		echo "Updated local npm binary: $(LOCAL_CC_CONNECT_BIN)"; \
		echo "Local npm version is now $$LOCAL_VERSION"

build-noweb:
	go build $(_TAGS_FLAG) -tags 'no_web' -ldflags "$(LDFLAGS)" -o $(APP) $(CMD)

run: build
	./$(APP)

clean:
	rm -f $(APP)
	rm -rf $(DIST)

# ---------------------------------------------------------------------------
# Testing targets.
#
# test-fast:  Unit tests + smoke tests (< 2 min). Runs on every push.
# test-full:   Full test suite including regression (< 10 min). PR requirement.
# test-smoke:  Smoke tests only (< 1 min). Quick sanity check.
# test-e2e:    E2E and regression tests only.
# test-release: Full + performance benchmarks. Before release.
# pre-test:    Prerequisites (build + vet) before running tests.
# ---------------------------------------------------------------------------

pre-test:
	go build ./...
	go vet ./...

# Fast test: unit tests + smoke tests
test-fast: pre-test
	go test -parallel=4 -race ./...
	go test -parallel=4 -tags=smoke ./tests/e2e/...

# Full test: unit + smoke + regression (PR requirement)
test-full: pre-test
	go test -parallel=4 -race ./...
	go test -parallel=4 -tags=smoke ./tests/e2e/...
	go test -parallel=2 -tags=regression ./tests/e2e/...

# Smoke tests only
test-smoke: pre-test
	go test -v -tags=smoke ./tests/e2e/...

# E2E/regression tests only
test-e2e: pre-test
	go test -v -tags=regression ./tests/e2e/...

# Performance benchmarks only
test-performance: pre-test
	go test -bench=. -benchmem -tags=performance ./tests/performance/...

# Release test: full + performance benchmarks
test-release: pre-test
	go test -parallel=4 -race ./...
	go test -parallel=4 -tags=smoke ./tests/e2e/...
	go test -parallel=2 -tags=regression ./tests/e2e/...
	go test -bench=. -benchmem -tags=performance ./tests/performance/...

# Release-local gate: deterministic release checks that do not require real IM
# credentials, real provider accounts, or supervisor-managed services.
test-release-local:
	go test ./tests/release_local/...
	go test ./config
	go test ./core -run 'TestEngineSendToSessionWithAttachments|TestProcessInteractiveEvents_SuppressesDuplicateSideChannelText|TestCmdList_AllSessionsVisibleAfterRepeatedNew|TestCmdList_SessionVisibleDuringAgentProcessing|TestEngine_Alias|TestEngine_BannedWords|TestEngine_DisabledCommands'
	go test ./platform/feishu -run 'TestUserIDFromEventFallsBackToUserID|TestResolveUserNameSkipsInvalidLookupID|TestNew_CanDisableInteractiveCards'

# Legacy: runs unit tests only
test:
	go test -v ./...

lint:
	golangci-lint run ./...

release-all: clean
	@mkdir -p $(DIST)
	@$(foreach platform,$(PLATFORMS), \
		$(eval GOOS   := $(word 1,$(subst /, ,$(platform)))) \
		$(eval GOARCH := $(word 2,$(subst /, ,$(platform)))) \
		$(eval EXT    := $(if $(filter windows,$(GOOS)),.exe,)) \
		$(eval OUT    := $(DIST)/$(APP)-$(VERSION)-$(GOOS)-$(GOARCH)$(EXT)) \
		echo "Building $(OUT)" && \
		GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
			go build $(_TAGS_FLAG) -ldflags "$(LDFLAGS)" -o $(OUT) $(CMD) && \
	) true
	@echo "Packaging archives..."
	@cd $(DIST) && for f in $(APP)-*; do \
		case "$$f" in \
			*.tar.gz|*.zip) continue ;; \
			*.exe) zip "$${f%.exe}.zip" "$$f" ;; \
			*)     tar czf "$$f.tar.gz" "$$f" ;; \
		esac; \
	done
	@cd $(DIST) && sha256sum * > checksums.txt
	@echo "Done. Binaries and archives in $(DIST)/"

release:
	@if [ -z "$(TARGET)" ]; then \
		echo "Usage: make release TARGET=linux/amd64"; \
		echo "Available: $(PLATFORMS)"; \
		exit 1; \
	fi
	@mkdir -p $(DIST)
	$(eval GOOS   := $(word 1,$(subst /, ,$(TARGET))))
	$(eval GOARCH := $(word 2,$(subst /, ,$(TARGET))))
	$(eval EXT    := $(if $(filter windows,$(GOOS)),.exe,))
	$(eval OUT    := $(DIST)/$(APP)-$(VERSION)-$(GOOS)-$(GOARCH)$(EXT))
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build $(_TAGS_FLAG) -ldflags "$(LDFLAGS)" -o $(OUT) $(CMD)
	@echo "Built: $(OUT)"
