# feixi 运行时镜像：二进制由 CI 预编译到 bin/ 后拼装，前端产物已经 go:embed 进二进制。
#
# 编译期依赖（node_modules、pnpm、Go 工具链）全部留在 CI，不进镜像。
# 契约见 Felix2yu/.github/docs/docker.md。

FROM alpine:3.24

# ca-certificates 是硬需求：通知要走 https（apprise 的 slack/tgram/discord 等），
# Go 的 TLS 读系统根证书，缺了就是所有渠道静默失败。
# tzdata：cron 检查项的时区虽然由 Go 内嵌的 time/tzdata 兜底，但容器里有本机时区
# 才能让日志时间戳与宿主一致。
RUN apk add --no-cache tzdata ca-certificates su-exec \
    && addgroup -S feixi \
    && adduser -S -G feixi -h /data feixi

ENV TZ=Asia/Shanghai \
    FEIXI_DB=/data/feixi.db \
    FEIXI_LISTEN=:8080

COPY --chmod=755 bin/feixi /usr/local/bin/feixi
# 入口脚本按 PUID/PGID 调整身份并修正 /data 属主（SQLite WAL 需要对目录本身可写）
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN mkdir -p /data && chown -R feixi:feixi /data
VOLUME ["/data"]

EXPOSE 8080

# 探活走 /api/v3/status/：它真的执行一次 SELECT 1，且本就没有鉴权，不会被口令挡住
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:8080/api/v3/status/ >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["feixi", "serve"]
