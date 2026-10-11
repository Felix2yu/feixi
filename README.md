# 飞檄（feixi）

定时任务与守护进程的**死信开关**：给每个需要按时冒头的活儿一个 URL，它到点没来就报警。

单 Go 二进制 + 内嵌 React 界面，数据落在一个 SQLite 文件里，通知走 apprise 的 URL 语法。

## 功能

- 两类计划：**固定间隔**（超时时间 + 宽限期）与 **cron 表达式**（带每个检查项自己的时区）
- 四种信号：成功、开始、日志、失败（含退出码），`start` 与终态用同一个 `rid` 配对可算出运行时长
- 状态徽章（SVG / JSON / shields.io），地址带签名，公开也没法猜
- 对外状态页：`/status/<项目 badge_key>` 是只读的公开视图（无管理入口，每 5 秒自动刷新），数据接口 `GET /front/status-page/<badge_key>`；地址在「徽章」页复制
- 通知渠道用 apprise URL 描述，覆盖 Telegram / Slack / Discord / 企业微信 / ntfy / 邮件 等 200+ 服务
- 告警抑制：每个检查项可设「失败容忍次数」（连续 N 个失败信号才判宕），实例级抖动检测会在状态频繁翻转时自动合并逐条告警
- 宕机可按渠道开启重复催促（`FEIXI_NAG_PERIOD`），失败的通知可在界面上一键重发
- 管理 API 与常见监控 agent 的调用形态兼容，可直接用 curl 或现成脚本接入
- 月度可用率与宕机统计、通知投递历史、ping 请求体留档
- 检查项事件流：信号、状态翻转（含宕机原因）与通知投递合并成一条时间线；另有纯文本状态接口 `/front/checks/<code>/status` 供脚本读取
- 仪表板可按状态、名称、下次到期、最后 ping 排序（点表头，选择记在浏览器里），可勾选多项批量暂停 / 恢复 / 删除
- 检查项可一键清空历史：回到「新建」重新计时，宕机统计与状态翻转记录保留
- 整个项目的 ping 地址可在「设置」切成 `<项目前缀>/<slug>` 形态（没有 slug 的检查项仍用 UUID），深浅色也能手动切换，默认跟随系统
- 可安装为 PWA：桌面与手机主屏都有独立窗口与图标（`/manifest.webmanifest` + 全套图标），但**有意不含离线能力**，见「边界」

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

## 告警抑制：失败容忍与抖动

- **失败容忍次数**（检查项设置里的 `failure_tolerance`，0–100，默认 0）：连续收到 N 个失败信号（`/fail` 或非零退出码）仍保持正常、不发告警，第 N+1 个才判宕；任意成功信号清零计数。注意它只管显式失败信号——到点没收到 ping 的**超时**仍按宽限期直接判宕。
- **抖动检测**（实例级，`FEIXI_FLAP_WINDOW` / `FEIXI_FLAP_THRESHOLD`）：滚动窗口内会告警的状态翻转（down、以及离开 down 的恢复）达到阈值时，检查项进入「抖动中」：当条翻转不发告警，改为向订阅宕机的渠道发一条抖动通知，窗口内后续翻转全部抑制；状态在同一态稳定满一个窗口后自动结算——当前正常则发「已恢复稳定」，当前仍宕则补发一条正常宕机告警。暂停/恢复会立即退出抖动状态。

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
curl -I http://localhost:8080/ping/<uuid>           # HEAD 同样算一次信号
```

三种方法都能用：`GET`、`HEAD`、`POST`。检查项打开「仅 POST」后，`GET`/`HEAD` 仍会留档，但记为忽略、不改状态。

除了 UUID，也可以用 `<项目前缀>/<slug>` 形态的地址；带 `?create=1` 时首次 ping 会自动建档，适合任务名会动态变化的场景。自动建档的额度可以临时超到 `FEIXI_MAX_CHECKS` 的两倍——脚本突然多出一批任务名时先收下 ping，再由人去清；超过两倍仍回 `404`。

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
curl $SITE_ROOT/api/v3/checks/                       # 列表，支持 ?tag= &slug= &orderby=
curl $SITE_ROOT/api/v3/checks/?orderby=-last_ping    # 排序：name -name last_ping -last_ping created
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
| `FEIXI_MAX_CHECKS` | `100` | 检查项数量上限；`?create=1` 的自动建档可临时超到两倍 |
| `FEIXI_PING_BODY_LIMIT` | `10000` | 单个 ping 请求体保留字节数 |
| `FEIXI_PING_LOG_LIMIT` | `100` | 每个检查项保留的 ping 条数 |
| `FEIXI_PRUNE_INTERVAL` | `24h` | 全量清理周期；启动时先扫一次，`0` 表示只靠 ping 触发的修剪 |
| `FEIXI_SECRET_KEY` | 自动生成并存库 | 徽章 URL 签名密钥 |
| `FEIXI_ALLOW_PRIVATE_URLS` | `true` | 是否允许通知打到内网 |
| `FEIXI_MASTER_BADGE_LABEL` | `飞檄` | 全量徽章左半边的文字 |
| `FEIXI_NAG_PERIOD` | 空（关闭） | 宕机重复催促间隔，如 `30m`、`1h`；设了之后还需在渠道里勾选「催促」 |
| `FEIXI_NAG_SCAN_INTERVAL` | `1m` | 催促扫描周期 |
| `FEIXI_FLAP_WINDOW` | `1h` | 抖动检测的滚动窗口，如 `30m`、`2h` |
| `FEIXI_FLAP_THRESHOLD` | `3` | 窗口内翻转达到多少次后合并告警；设为 `0` 关闭抖动检测 |
| `FEIXI_FLAP_SCAN_INTERVAL` | `1m` | 抖动结算扫描周期 |
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

安装入口只在安全上下文里出现：HTTPS 或 `localhost`。LAN 裸 HTTP（如 `http://192.168.1.10:8080`）打不出「安装」按钮属正常，徽章、ping、API 都不受影响；临时装一次可以 `ssh -L 8080:localhost:8080 <host>` 走本地回环，长期用就给反向代理上 TLS。

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

PWA 图标的母版是 `internal/web/brand/favicon.svg`（+ 栅格化出的 `icon-512-source.png`）；`internal/web/public/` 下的七件套与 `favicon.svg` 是 `python3 /path/to/.github/pwa-preset/gen-icons.py --src internal/web/brand/icon-512-source.png --svg internal/web/brand/favicon.svg --bg '#1f2933' --out internal/web/public` 的产物，别手改。Vite 会把 `public/` 原样拷进 `dist/` 根，再由 `go:embed all:dist` 打进二进制。

CI 接的是组织统一的 reusable workflow（`Felix2yu/.github`，pin 在 `@v1`）：`test` → `image` → `release` 三个 job。版本号由 git tag 通过 `-X main.version` 注入，本地 `go build` 出来是 `dev`。工具链版本只有一个来源：仓库根 `.nvmrc` 与 `internal/web/package.json` 的 `packageManager`。

## 边界

以下这些**有意不做**，需要时再评估，别顺手加上：

- 离线可用与 Service Worker：断网时面板里没有可展示的数据，兜底页只会误导，故 PWA 只做到「能装、有图标、独立窗口」，不注册 SW
- 多用户、团队、权限与登录：单实例内网自用，访问控制交给反向代理
- 邮件形态的日报 / 周报：要额外维护 SMTP 与一套发送状态（宕机催促已按渠道内置，见 `FEIXI_NAG_PERIOD`）
- 用邮件地址发 ping、把大请求体外置到对象存储
- systemd `OnCalendar` 表达式：cron 覆盖绝大多数场景
- 进程内执行命令型的通知渠道（等于把 RCE 交给配置项）
- Prometheus 指标与外部监控接入：单进程用日志 + `/api/v3/status/` 足够
