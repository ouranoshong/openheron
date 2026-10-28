# openheron 开发命令。
#
# 前端与后端的构建顺序是有依赖的：web/dist 会被 go:embed 进二进制，
# 所以 `make build` 必须先出前端再 go build。反过来会在第一次构建时
# 静默产出一个没有界面的二进制 —— 界面那部分是编译期就定下来的。

SHELL := /bin/bash
GO    ?= go
BUN   ?= bun
BIN   ?= bin/openheron

# 版本号：默认取 git describe（无 tag 时是短哈希，带 -dirty）。
# 覆盖方式：make VERSION=1.2.3。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# 发布用的 ldflags：注入版本号 + 去符号表（体积小一半，调试用 dev 构建不加）。
RELEASE_LDFLAGS ?= -s -w -X main.version=$(VERSION)

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# ── 前端 ──────────────────────────────────────────────────────────────────

.PHONY: web
web: ## 构建 React 前端到 web/dist（会被 go:embed）
	cd web && $(BUN) install --frozen-lockfile
	cd web && $(BUN) run build

.PHONY: dev
dev: ## Go 自动重建 + 重启（air）；配合 make web-dev 使用
	@command -v air >/dev/null || { \
		echo "air not found; install with: go install github.com/air-verse/air@latest"; \
		exit 1; }
	air

.PHONY: web-dev
web-dev: ## 启动 Vite dev server（:5173，代理 /v2 到 :8080）
	cd web && $(BUN) run dev

# ── 后端 ──────────────────────────────────────────────────────────────────

.PHONY: build
build: web ## 完整构建：前端 + 含界面的单文件二进制
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) .

.PHONY: build-go
build-go: ## 只构建后端（不重建前端，用于快速迭代 Go 代码）
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) .

.PHONY: run
run: build-go ## 运行二进制（界面来自已存在的 web/dist）
	$(BIN)

# ── 安装 ──────────────────────────────────────────────────────────────────
#
# 产物是**单文件二进制**（界面已 go:embed 进去），所以「安装」就是把它放到
# PATH 里某个目录 —— 没有运行时依赖要一起装。
#
# ★ 默认装到 ~/.local/bin 而不是 /usr/local/bin：后者要 root，而绝大多数
#   人的 PATH 里本来就有 ~/.local/bin（XDG 惯例）。要装到系统目录就
#   `make install PREFIX=/usr/local/bin`。

PREFIX ?= $(HOME)/.local/bin

.PHONY: install
install: build ## 把二进制装到 PREFIX（默认 ~/.local/bin），装完任意目录可用 openheron
	@mkdir -p $(PREFIX)
	install -m 0755 $(BIN) $(PREFIX)/openheron
	@echo "installed: $(PREFIX)/openheron"
	@command -v openheron >/dev/null 2>&1 || \
		echo "hint: $(PREFIX) 不在 PATH 里 —— export PATH=\"$(PREFIX):\$$PATH\""

.PHONY: uninstall
uninstall: ## 从 PREFIX 移除已安装的二进制
	rm -f $(PREFIX)/openheron
	@echo "removed: $(PREFIX)/openheron"

# ── 多平台发布 ────────────────────────────────────────────────────────────
#
# 产物是单文件二进制（界面已 go:embed 进去），所以「发布」= 交叉编译 + 打包。
# 界面是**编译期**嵌进去的 ⇒ 交叉编译不受影响，各平台拿到的是同一份界面。
#
# ★ CGO_ENABLED=0：全静态，不依赖目标机的 glibc / libc++。
#   openheron 没有 cgo 依赖（go.mod 里没有 sqlite 之类），关掉它没有代价 ——
#   而它换来的是「一个文件扔到任何机器上都能跑」。

RELEASE_DIR ?= dist
PLATFORMS   ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: release
release: web ## 交叉编译全部平台：压缩包与校验和进 dist/
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
	@echo && ls -lh $(RELEASE_DIR)

.PHONY: release-one
release-one: web ## 只编译一个平台：make release-one PLATFORM=darwin/arm64
	@$(MAKE) --no-print-directory release PLATFORMS="$(PLATFORM)"

# ── 校验 ──────────────────────────────────────────────────────────────────

.PHONY: test
test: ## Go 全量测试
	$(GO) test ./...

.PHONY: lint
lint: ## Go vet + gofmt + 前端 oxlint + TypeScript 类型检查
	$(GO) vet ./...
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then \
		echo "gofmt: 以下文件不是 gofmt-clean（gofmt -w <文件>）:"; \
		echo "$$files"; \
		exit 1; \
	fi
	cd web && $(BUN) run lint
	cd web && $(BUN) x tsc -b

# ── 测试环境 ──────────────────────────────────────────────────────────────
#
# ★ 测试用**另一套**配置与端口，与开发完全错开（定义在 .env.test）：
#
#     开发          测试
#     :8080        :18099   openheron 本体（独立配置 + 独立会话目录）
#     :5173        :15173   Vite
#     —            :18180   E2E 假后端
#
#   端口错开只是第一层。**状态**必须一起隔离：openheron 的会话写在
#   .openheron/state/，测试若写进去就污染了真实会话历史。所以
#   test-backend 用 .openheron-test/ 作为项目根，且预置自己的 .openheron/，
#   让配置上溯**停在那里**而不是找到仓库根那份。

.PHONY: test-env-backend
test-env-backend: build-go ## 起测试实例（:18099，独立配置与会话；前台）
	./scripts/test-backend.sh --reset

.PHONY: test-env-reset
test-env-reset: ## 只重建测试环境目录，不启动
	./scripts/test-backend.sh --reset --prepare-only

.PHONY: test-env-sync
test-env-sync: ## 把开发侧的 agent 定义同步进测试环境
	./scripts/test-backend.sh --sync --prepare-only

.PHONY: kb-index
kb-index: build-go ## 重建知识库索引（全量十分钟量级，见 docs/kb-design.md §9.2）
	$(BIN) kb index

.PHONY: kb-status
kb-status: build-go ## 看知识库索引的现状（不需要向量化服务在跑）
	$(BIN) kb status

.PHONY: smoke
smoke: ## 联调冒烟（连测试实例 :18099；用 OPENHERON_BASE 可改指向）
	cd web && $(BUN) run smoke:interop
	cd web && $(BUN) run smoke:no-undefined-url
	cd web && $(BUN) run smoke:hitl

.PHONY: test-e2e
test-e2e: ## 浏览器 E2E（自带假后端，用 :15173/:18180，不占开发端口）
	cd web && $(BUN) x playwright test

.PHONY: check
check: lint test ## 提交前全量校验

.PHONY: clean
clean: ## 清理构建产物
	rm -rf $(BIN) tmp web/dist $(RELEASE_DIR)