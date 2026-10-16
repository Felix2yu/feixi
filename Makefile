.PHONY: run dev build test vet web docker clean

BIN := bin/feixi

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/feixi

run: build
	./$(BIN) serve

# 前端热更新用 vite dev server（:5173，已配到 :8088 的代理），后端另起一个终端。
dev:
	go run ./cmd/feixi serve

web:
	cd web && pnpm install --frozen-lockfile && pnpm run build
	rm -rf internal/web/dist && mkdir -p internal/web/dist && cp -R web/dist/. internal/web/dist/
	@touch internal/web/dist/.gitkeep  # vite 的 emptyOutDir 会连占位文件一起清掉，补回来才编得动

# 装配式镜像：先出 bin/feixi（前端产物已 go:embed 进二进制），镜像只拼装。
docker: build
	docker build -t feixi:dev .

test:
	go test ./...

vet:
	gofmt -l . && go vet ./... && cd web && pnpm exec tsc --noEmit

clean:
	rm -rf bin web/dist internal/web/dist
	@mkdir -p internal/web/dist && touch internal/web/dist/.gitkeep
