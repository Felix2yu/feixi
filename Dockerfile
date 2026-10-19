# feixi 运行时镜像：二进制由 CI 预编译到 bin/ 后拼装，前端产物已经 go:embed 进二进制。
#
# 编译期依赖（node_modules、pnpm、Go 工具链）全部留在 CI，不进镜像。
# 基镜 ghcr.io/felix2yu/base:alpine-3.24 提供 ca-certificates（通知渠道走 https，
# 缺了所有渠道静默失败）、tzdata、su-exec 与 PUID/PGID 降权入口。
# 契约见 Felix2yu/.github/docs/docker.md。
FROM ghcr.io/felix2yu/base:alpine-3.24

# 容器内布局（全 fleet 统一）：二进制放 /app/feixi，数据放 /app/data 挂载卷。
# /app 不在 PATH 上，所以外部备份工具的 BACKUP_SQLITE_CMD 必须写绝对路径
# `/app/feixi backup`，裸名在 docker exec 里会 command not found。
# /app/data 由基镜入口按 PUID/PGID chown（同时是 SQLite WAL 需要可写的目录）。
WORKDIR /app

ENV FEIXI_DB=/app/data/feixi.db \
    FEIXI_LISTEN=:8080 \
    APP_DATA_DIR=/app/data

COPY --chmod=755 bin/feixi /app/feixi

RUN mkdir -p /app/data
VOLUME ["/app/data"]

EXPOSE 8080

# 探活走 /api/v3/status/：它真的执行一次 SELECT 1，且本就没有鉴权，不会被口令挡住
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:8080/api/v3/status/ >/dev/null || exit 1

CMD ["/app/feixi", "serve"]
