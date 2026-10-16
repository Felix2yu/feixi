#!/bin/sh
# feixi 容器入口：按 PUID/PGID 调整运行身份，并修正 /data 属主。
#
# 为什么要这一层：容器内 uid 与宿主 uid 是同一套数字。绑定挂载的 ./data 属于宿主
# uid 1000，容器里以别的 uid 跑就对它没有写权限；SQLite 以 WAL 打开时需要对目录
# 本身可写，症状是 error 14: unable to open database file。chmod 777 只是把问题
# 推到下次备份恢复之后。

set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

# 与镜像里已有用户冲突时 usermod/groupmod 会失败，那是常态，不该让容器起不来。
if [ "$(id -g feixi 2>/dev/null)" != "$PGID" ]; then
  groupmod -g "$PGID" feixi 2>/dev/null || true
fi
if [ "$(id -u feixi 2>/dev/null)" != "$PUID" ]; then
  usermod -u "$PUID" -g "$PGID" feixi 2>/dev/null || true
fi

# 历史遗留的 root 属主文件（比如某次用 root 跑过）会残留下来继续挡住写入。
chown -R "${PUID}:${PGID}" /data 2>/dev/null || true

# exec 让降权后的进程替换 PID 1，否则 docker stop 的信号收不到，会等满 10 秒被
# SIGKILL —— WAL 就来不及 checkpoint。
exec su-exec "${PUID}:${PGID}" "$@"
