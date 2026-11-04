# openheron development commands.
#
# The frontend and the backend have a build-order dependency: web/dist is
# embedded into the binary with go:embed, so `make build` must produce the
# frontend before go build. The other way round silently produces a binary with
# no interface on the first build — the interface part is fixed at compile time.

SHELL := /bin/bash
GO    ?= go
BUN   ?= bun
BIN   ?= bin/openheron

# Version: taken from git describe by default (a short hash with -dirty when
# there is no tag). Override with: make VERSION=1.2.3.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# ldflags for release builds: inject the version and strip the symbol table
# (about half the size; the dev build for debugging does not strip).
RELEASE_LDFLAGS ?= -s -w -X main.version=$(VERSION)

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# ── Frontend ───────────────────────────────────────────────────────────────

.PHONY: web
web: ## Build the React frontend into web/dist (embedded with go:embed)
	cd web && $(BUN) install --frozen-lockfile
	cd web && $(BUN) run build

.PHONY: dev
dev: ## Automatic rebuild + restart for Go (air); use alongside make web-dev
	@command -v air >/dev/null || { \
		echo "air not found; install with: go install github.com/air-verse/air@latest"; \
		exit 1; }
	air

.PHONY: web-dev
web-dev: ## Start the Vite dev server (:5173, proxies /v2 to :8080)
	cd web && $(BUN) run dev

# ── Backend ────────────────────────────────────────────────────────────────

.PHONY: build
build: web ## Full build: the frontend + a single binary with the UI embedded
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) .

.PHONY: build-go
build-go: ## Build the backend only (no frontend rebuild) — for fast iteration on Go code
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) .

.PHONY: run
run: build-go ## Run the binary (the UI comes from the existing web/dist)
	$(BIN)

# ── Install ────────────────────────────────────────────────────────────────
#
# The artifact is a **single binary** (the UI is already embedded with
# go:embed), so "installing" means putting it somewhere on PATH — there is no
# runtime dependency to install alongside it.
#
# ★ It installs into ~/.local/bin rather than /usr/local/bin by default: the
#   latter needs root, while ~/.local/bin is already on most people's PATH
#   (the XDG convention). To install into a system directory, use
#   `make install PREFIX=/usr/local/bin`.

PREFIX ?= $(HOME)/.local/bin

.PHONY: install
install: build ## Install the binary into PREFIX (default ~/.local/bin); openheron is then runnable from anywhere
	@mkdir -p $(PREFIX)
	install -m 0755 $(BIN) $(PREFIX)/openheron
	@echo "installed: $(PREFIX)/openheron"
	@command -v openheron >/dev/null 2>&1 || \
		echo "hint: $(PREFIX) is not on PATH — export PATH=\"$(PREFIX):\$$PATH\""

.PHONY: uninstall
uninstall: ## Remove the installed binary from PREFIX
	rm -f $(PREFIX)/openheron
	@echo "removed: $(PREFIX)/openheron"

# ── Multi-platform release ─────────────────────────────────────────────────
#
# The artifact is a single binary (the UI is already embedded with go:embed), so
# "releasing" = cross-compiling + packaging. The UI is embedded at **compile
# time** ⇒ cross-compiling is unaffected, and every platform gets the same UI.
#
# ★ CGO_ENABLED=0: fully static, no dependency on the target's glibc / libc++.
#   openheron has no cgo dependencies (no sqlite in go.mod and the like), so
#   turning it off costs nothing — and it buys "one file that runs on any
#   machine".

RELEASE_DIR ?= dist
PLATFORMS   ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: release
release: web ## Cross-compile every platform: archives and checksums land in dist/
	@rm -rf $(RELEASE_DIR)
	@mkdir -p $(RELEASE_DIR)
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		name="openheron_$(VERSION)_$${os}_$${arch}"; \
		ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "  → $$p"; \
		mkdir -p "$(RELEASE_DIR)/$$name"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath \
			-ldflags "$(RELEASE_LDFLAGS)" -o "$(RELEASE_DIR)/$$name/openheron$$ext" .; \
		if [ "$$os" = "windows" ]; then \
			if command -v zip >/dev/null 2>&1; then \
				(cd $(RELEASE_DIR) && zip -qr "$$name.zip" "$$name"); \
			else \
				(cd $(RELEASE_DIR) && python3 -m zipfile -c "$$name.zip" "$$name"); \
			fi; \
		else \
			tar -C $(RELEASE_DIR) -czf "$(RELEASE_DIR)/$$name.tar.gz" "$$name"; \
		fi; \
		rm -rf "$(RELEASE_DIR)/$$name"; \
	done
	@cd $(RELEASE_DIR) && (sha256sum *.tar.gz *.zip 2>/dev/null || shasum -a 256 *.tar.gz *.zip) > checksums.txt
	@# The installers travel with the release: a user who wants the published
	@# binary should not have to know the asset naming rule, and the script
	@# that knows it must be the one from this version.
	@cp scripts/install.sh scripts/install.ps1 $(RELEASE_DIR)/
	@echo && ls -lh $(RELEASE_DIR)

.PHONY: release-one
release-one: web ## Cross-compile a single platform: make release-one PLATFORM=darwin/arm64
	@$(MAKE) --no-print-directory release PLATFORMS="$(PLATFORM)"

# ── Docker Sandbox Kit ──────────────────────────────────────────────────────
#
# ★ A separate artifact, not part of the release pipeline: `make release` still
#   produces the six static binaries and nothing else. The Kit packages
#   openheron as a Docker Sandboxes workload — its design is
#   docs/sandbox-kit-design.md.
#
# The build context is sandbox-kit/, because the Kit specification finds the
# content recipe by the descriptor's filename stem, beside it.

KIT_DIR    ?= sandbox-kit
KIT_VERSION ?= 0.0.6
KIT_IMAGE  ?= openheron-kit:$(KIT_VERSION)
KIT_BUILDER ?= kit-builder
KIT_REPO   ?= docker.io/ouranoshong/openheron-kit

.PHONY: kit
kit: ## Build the Docker Sandbox Kit image into the local docker store (needs docker buildx)
	@docker buildx inspect $(KIT_BUILDER) >/dev/null 2>&1 || \
		docker buildx create --name $(KIT_BUILDER) --driver docker-container --bootstrap
	cd $(KIT_DIR) && docker buildx build --builder $(KIT_BUILDER) . -f openheron.yaml -t $(KIT_IMAGE) --load
# ★ --builder is not optional: the docker driver with the classic image store
#   drops the manifest annotations, and an image without the descriptor
#   annotation is not a Kit any more.
# ★ --load is not optional either: the container driver keeps the result in
#   its own build cache, and without it there is no local image to validate.

.PHONY: kit-push
kit-push: ## Publish the Kit to the registry (needs docker login)
	cd $(KIT_DIR) && docker buildx build --builder $(KIT_BUILDER) . -f openheron.yaml \
		-t $(KIT_REPO):$(KIT_VERSION) --push

.PHONY: kit-check
kit-check: ## Validate the Kit against the specification (needs kit-tck)
	kit-tck validate $(KIT_IMAGE)

# ── Verification ───────────────────────────────────────────────────────────

.PHONY: test
test: ## The full Go test suite
	$(GO) test ./...

.PHONY: test-install
test-install: ## Test the release installer against a local fake release
	sh scripts/install_test.sh

.PHONY: install-hooks
install-hooks: ## Install the pre-commit hooks (gitleaks + gofmt)
	@command -v pre-commit >/dev/null || { \
		echo "pre-commit is not installed — pipx install pre-commit (or brew install pre-commit)"; \
		exit 1; }
	pre-commit install

.PHONY: lint
lint: lint-go lint-web ## Go vet + gofmt + frontend oxlint + TypeScript type check

# ★ The two halves are separate targets so that a job that only needs one of
#   them does not have to install the other's toolchain: the CI runs
#   lint-go without bun and lint-web without Go. The recipe stays in one
#   place — `make lint` is still both of them.
.PHONY: lint-go
lint-go: ## Go vet + gofmt (no frontend toolchain needed)
	$(GO) vet ./...
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then \
		echo "gofmt: these files are not gofmt-clean (gofmt -w <file>):"; \
		echo "$$files"; \
		exit 1; \
	fi

.PHONY: lint-web
lint-web: ## Frontend oxlint + TypeScript type check (no Go toolchain needed)
	cd web && $(BUN) run lint
	cd web && $(BUN) x tsc -b

# ── Test environment ───────────────────────────────────────────────────────
#
# ★ The tests use **another** set of configuration and ports, completely
#   separate from development (defined in .env.test):
#
#     Development   Test
#     :8080         :18099   openheron itself (its own config + session directory)
#     :5173         :15173   Vite
#     —             :18180   E2E fake backend
#
#   Different ports are only the first layer. **State** must be isolated too:
#   openheron writes its sessions under the config root's state/ directory,
#   and a test writing there pollutes the real session history. So test-backend
#   uses .openheron-test/ as the project root, pre-seeds its own .openheron/,
#   and names the config root with OPENHERON_CONFIG_DIR — the config root
#   **never searches upwards**, so it structurally cannot reach the
#   repository's copy.

.PHONY: test-env-backend
test-env-backend: build-go ## Start the test instance (:18099, its own config and sessions; foreground)
	./scripts/test-backend.sh --reset

.PHONY: test-env-reset
test-env-reset: ## Only rebuild the test environment directories, do not start
	./scripts/test-backend.sh --reset --prepare-only

.PHONY: test-env-sync
test-env-sync: ## Sync the development-side agent definitions into the test environment
	./scripts/test-backend.sh --sync --prepare-only

.PHONY: kb-index
kb-index: build-go ## Rebuild the knowledge base index (a full run takes on the order of ten minutes, see docs/kb-design.md §9.2)
	$(BIN) kb index

.PHONY: kb-status
kb-status: build-go ## Inspect the knowledge base index (no embedding service needed)
	$(BIN) kb status

.PHONY: smoke
smoke: ## Integration smoke tests (against the test instance :18099; override the target with OPENHERON_BASE)
	cd web && $(BUN) run smoke:interop
	cd web && $(BUN) run smoke:no-undefined-url
	cd web && $(BUN) run smoke:hitl

.PHONY: test-e2e
test-e2e: ## Browser E2E (brings its own fake backend, uses :15173/:18180, never takes the development ports)
	cd web && $(BUN) x playwright test

.PHONY: check
check: lint test ## Full verification before committing

.PHONY: clean
clean: ## Remove the build artifacts
	rm -rf $(BIN) tmp web/dist $(RELEASE_DIR)
