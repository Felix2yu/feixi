# 飞檄（feixi）

定时任务与守护进程的**死信开关**：给每个需要按时冒头的活儿一个 URL，它到点没来就报警。

单 Go 二进制 + 内嵌 React 界面，数据落在一个 SQLite 文件里，通知走 apprise 的 URL 语法。

## 功能

- 两类计划：**固定间隔**（超时时间 + 宽限期）与 **cron 表达式**（带每个检查项自己的时区）
- 四种信号：成功、开始、日志、失败（含退出码），`start` 与终态用同一个 `rid` 配对可算出运行时长
- 状态徽章（SVG / JSON / shields.io），地址带签名，公开也没法猜
- 通知渠道用 apprise URL 描述，覆盖 Telegram / Slack / Discord / 企业微信 / ntfy / 邮件 等 200+ 服务
- 管理 API 与常见监控 agent 的调用形态兼容，可直接用 curl 或现成脚本接入
- 月度可用率与宕机统计、通知投递历史、ping 请求体留档

## 状态语义

库里只存 **4 个持久态**，`延迟` 与 `运行中` 是读取时算出来的派生态：

| 显示态 | 中文 | 来源 | 徽章里 | 颜色 |
| --- | --- | --- | --- | --- |
| `new` | 新建 | 持久态 | up | 灰 |
| `up` | 正常 | 持久态 | up | 绿 `#4c1` |
| `grace` | **延迟** | 派生态 | late | 橙 `#fe7d37` |
| `down` | 宕机 | 持久态 | down | 红 `#e05d44` |
| `paused` | 暂停 | 持久态 | up | 灰 |
| `started` | 运行中 | 派生态（三个跳动圆点） | — | — |

判定方式：固定间隔类型看「上次成功 ping + 超时时间」，cron 类型看「下一次计划触发时刻」（在检查项自己的时区里算，算完立刻转回 UTC，避免夏令时把到期时间挪一小时），再往后加宽限期才是宕机。

- **暂停**保留历史 ping，只把状态置为 paused，不发通知。
- **恢复**回到「新建」，必须收到一次新 ping 才算正常；打开「粘性暂停」后，暂停期间的 ping 会被记为忽略而不是恢复。
- 暂停/恢复也写进状态翻转事件表，因此不会被算成一段宕机时长。

## 快速开始

```bash
docker compose up -d               # 数据落在 ./data/feixi.db
# 或本地跑
make web && make run               # 前端产物 go:embed 进二进制，只有一个文件要部署
```

打开 `http://localhost:8080` 新建检查项，然后按页面上给的地址发信号：

```bash
curl http://localhost:8080/ping/<uuid>              # 成功（等价于 /success）
curl http://localhost:8080/ping/<uuid>/start        # 开始运行
curl http://localhost:8080/ping/<uuid>/log          # 中间日志，不改状态
curl http://localhost:8080/ping/<uuid>/success      # 成功（显式写法）
curl http://localhost:8080/ping/<uuid>/fail         # 失败
curl http://localhost:8080/ping/<uuid>/3            # 退出码 3 ⇒ 失败
curl "http://localhost:8080/ping/<uuid>?rid=<uuid>" # 用同一个 rid 配对，算出运行时长
```

除了 UUID，也可以用 `<项目前缀>/<slug>` 形态的地址；带 `?create=1` 时首次 ping 会自动建档，适合任务名会动态变化的场景。

## 通知渠道

一个渠道 = 一组 apprise URL（每行一个，都会收到）。支持的 scheme 由 apprise-go 决定，常用的：

```
tgram://bottoken/chatid
slack://workspace/channel/token
discord://webhook_id/webhook_token
mailto://user:password@gmail.com
ntfy://topic
json://hooks.internal/feixi
```

**变量替换**：URL 交给 apprise 之前会先替换这些占位符（值会做 URL 编码）：

```
$CODE $STATUS $NOW $NAME $NAME_JSON $SLUG $TAGS $TAG1..$TAG9
$JSON $BODY $BODY_JSON $EXITSTATUS
```

配合 apprise 自己的参数前缀就能拼出任意请求体：`?:字段=值` 进 payload，`?-字段=值` 进查询串，`?+字段=值` 进 header。例如

```
json://hooks.internal/feixi?:check=$NAME&:status=$STATUS&:body=$BODY
```

超时与重定向由服务端统一控制（连接/读响应各 10s，单次投递 45s 硬上限），URL 上写 `?cto=&rto=&redirect=` 会被忽略——apprise-go 把这几个参数存在进程级全局变量里，并发发送时不能按渠道区分。

默认**允许**通知打到内网地址（自托管监控的对象通常就在局域网里）。要收紧就设 `FEIXI_ALLOW_PRIVATE_URLS=false`，届时会拒绝 private/loopback/link-local/multicast，并且重定向的每一跳都重新校验。

## 管理 API

`/api/v1|v2|v3` 三套路径共用实现（`status` 只有 v1 会返回 `started`）：

```bash
curl $SITE_ROOT/api/v3/checks/                       # 列表，支持 ?tag= &slug=
curl -X POST $SITE_ROOT/api/v3/checks/ -d '{"name":"备份","timeout":3600,"grace":60}'
curl -X POST $SITE_ROOT/api/v3/checks/<uuid>/pause
curl -X POST $SITE_ROOT/api/v3/checks/<uuid>/resume  # 未暂停时返回 409
curl $SITE_ROOT/api/v3/checks/<uuid>/pings/
curl $SITE_ROOT/api/v3/checks/<uuid>/flips/?seconds=86400
curl $SITE_ROOT/api/v3/badges/
```

**没有鉴权**：定位是内网自用，所有 `/api`、`/front`、`/ping` 接口都是开放的。要放到公网请自己套反向代理 + Basic Auth，或者只监听 `127.0.0.1`。

## 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `FEIXI_LISTEN` | `:8080` | 监听地址 |
| `FEIXI_DB` | `data/feixi.db` | SQLite 文件路径 |
| `FEIXI_SITE_ROOT` | `http://localhost<listen>` | 生成 ping_url / 徽章地址用的对外地址 |
| `FEIXI_MAX_CHECKS` | `100` | 检查项数量上限 |
| `FEIXI_PING_BODY_LIMIT` | `10000` | 单个 ping 请求体保留字节数 |
| `FEIXI_PING_LOG_LIMIT` | `100` | 每个检查项保留的 ping 条数 |
| `FEIXI_SECRET_KEY` | 自动生成并存库 | 徽章 URL 签名密钥 |
| `FEIXI_ALLOW_PRIVATE_URLS` | `true` | 是否允许通知打到内网 |
| `FEIXI_MASTER_BADGE_LABEL` | `飞檄` | 全量徽章左半边的文字 |
| `FEIXI_LOG` | `info` | `debug`/`info`/`warn`/`error` |

## 部署

镜像 `ghcr.io/felix2yu/feixi`，装配式构建：CI 先编出 `bin/feixi`（前端产物已内嵌），Dockerfile 只做拼装，镜像里没有 node_modules 和 Go 工具链。

| 项 | 值 |
| --- | --- |
| 监听 | `:8080`（`FEIXI_LISTEN`） |
| 数据 | `/data/feixi.db`，绑定挂载 `./data` 时用 `PUID`/`PGID` 对齐宿主属主 |
| 探活 | `GET /api/v3/status/`（真跑一次 `SELECT 1`，免鉴权），Dockerfile 里已带 `HEALTHCHECK` |
| 运行身份 | 非 root（alpine `feixi` 用户，入口脚本按 PUID/PGID 降权并 chown `/data`） |

pnpm 命令要在 `internal/web` 目录里跑，那里才有 `packageManager` 声明供其切换版本。

SQLite 是 WAL 模式，**需要对数据目录本身可写**；宿主目录属主与容器内 uid 不一致时症状是 `error 14: unable to open database file`，`chmod 777` 只是把问题推到下次恢复备份之后。

## 开发

```bash
make web     # pnpm install --frozen-lockfile + build 到 internal/web/dist（go:embed 的位置）
make run     # 编译到 bin/feixi 并启动
make dev     # 只起后端；前端另在 internal/web 跑 pnpm dev 走 :5173 代理
make test    # go test ./...
make vet     # gofmt + go vet + tsc --noEmit
```

目录：`internal/engine` 是纯函数状态机（不碰数据库，所有时间边界都在这层测），`internal/store` 是唯一的数据访问层（SQLite 写连接池 `MaxOpenConns=1` + `BEGIN IMMEDIATE`），`internal/notify` 是 apprise 封装 + 投递队列，`internal/web` 是 React SPA。

CI 接的是组织统一的 reusable workflow（`Felix2yu/.github`，pin 在 `@v1`）：`test` → `image` → `release` 三个 job。版本号由 git tag 通过 `-X main.version` 注入，本地 `go build` 出来是 `dev`。工具链版本只有一个来源：仓库根 `.nvmrc` 与 `internal/web/package.json` 的 `packageManager`。

## 边界

以下这些**有意不做**，需要时再评估，别顺手加上：

- 多用户、团队、权限与登录：单实例内网自用，访问控制交给反向代理
- 邮件形态的日报 / 周报 / 重复催促：要额外维护 SMTP 与一套发送状态
- 用邮件地址发 ping、把大请求体外置到对象存储
- systemd `OnCalendar` 表达式：cron 覆盖绝大多数场景
- 进程内执行命令型的通知渠道（等于把 RCE 交给配置项）
- Prometheus 指标与外部监控接入：单进程用日志 + `/api/v3/status/` 足够
