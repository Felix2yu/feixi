# feixi —— 目标约定见 Felix2yu/.github/docs/makefile.md
#
# 七个必备目标：build / dev / test / lint / fmt / docker / clean
.PHONY: build dev test lint lint-strict fmt docker clean web run vet

BIN  := bin/feixi
MAIN := ./cmd/feixi

# 产出 $(BIN)：前端先构建并摆进 go:embed 位置，再编译后端。
# 顺序不能反——装配式镜像里没有编译器，二进制必须在这里就绪。
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN) $(MAIN)

# 前端热更新用 vite dev server（:5173，已配到 :8088 的代理），后端另起一个终端。
dev:
	go run $(MAIN) serve

run: build
	./$(BIN) serve

web:
	cd web && pnpm install --frozen-lockfile && pnpm run build
	rm -rf internal/web/dist && mkdir -p internal/web/dist && cp -R web/dist/. internal/web/dist/
	@touch internal/web/dist/.gitkeep  # vite 的 emptyOutDir 会连占位文件一起清掉，补回来才编得动

test:
	go test ./...

# gofmt -l 只列出未格式化文件、不写盘，属于检查而非格式化
lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "未格式化："; echo "$$out"; exit 1; fi
	go vet ./...
	cd web && pnpm exec tsc --noEmit

# 深度检查：在 lint 之上加 golangci-lint（存量告警未清零前会红，故不并入 lint）
lint-strict: lint
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "  （未安装 golangci-lint，跳过）"; fi

# 兼容旧调用：vet 曾是 lint 的旧名字
vet: lint

fmt:
	gofmt -w $$(git ls-files '*.go')

# 装配式镜像：先出 bin/feixi（前端产物已 go:embed 进二进制），镜像只拼装。
docker: build
	docker build -t feixi:dev .

clean:
	rm -rf bin web/dist internal/web/dist
	@mkdir -p internal/web/dist && touch internal/web/dist/.gitkeep
