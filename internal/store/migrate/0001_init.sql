-- feixi 初始结构。时间列一律 INTEGER（unix 微秒，永远 UTC），枚举列用 CHECK 兜住脏写。

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 单租户：只播种一行 project（id=1）。多项目/团队以后再加，不用改形状。
CREATE TABLE projects (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  code       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL DEFAULT '默认项目',
  badge_key  TEXT NOT NULL UNIQUE,
  ping_key   TEXT NOT NULL DEFAULT '',
  show_slugs INTEGER NOT NULL DEFAULT 0,
  created    INTEGER NOT NULL
);

CREATE TABLE checks (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  code                TEXT NOT NULL UNIQUE,
  badge_key           TEXT NOT NULL UNIQUE,
  project_id          INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL DEFAULT '',
  slug                TEXT NOT NULL DEFAULT '',
  tags                TEXT NOT NULL DEFAULT '',
  description         TEXT NOT NULL DEFAULT '',
  created             INTEGER NOT NULL,

  kind                TEXT NOT NULL CHECK (kind IN ('simple','cron')),
  timeout_us          INTEGER NOT NULL DEFAULT 86400000000,
  grace_us            INTEGER NOT NULL DEFAULT 3600000000,
  schedule            TEXT NOT NULL DEFAULT '',
  tz                  TEXT NOT NULL DEFAULT 'UTC',

  n_pings             INTEGER NOT NULL DEFAULT 0,
  last_ping           INTEGER NULL,
  last_start          INTEGER NULL,
  last_start_rid      TEXT NULL,
  last_duration_us    INTEGER NULL,
  -- 只有 daemon 与 ping 写；NULL 表示这个检查项不会自己转 down
  alert_after         INTEGER NULL,
  status              TEXT NOT NULL CHECK (status IN ('up','down','new','paused')),

  methods             TEXT NOT NULL DEFAULT '' CHECK (methods IN ('','POST')),
  manual_resume       INTEGER NOT NULL DEFAULT 0,
  filter_http_body    INTEGER NOT NULL DEFAULT 0,
  filter_default_fail INTEGER NOT NULL DEFAULT 0,
  start_kw            TEXT NOT NULL DEFAULT '',
  success_kw          TEXT NOT NULL DEFAULT '',
  failure_kw          TEXT NOT NULL DEFAULT ''
);

-- 等价原版的部分索引 api_check_aa_not_down
CREATE INDEX checks_going_down ON checks(alert_after) WHERE status != 'down';
CREATE INDEX checks_project ON checks(project_id);
CREATE INDEX checks_tags ON checks(project_id, tags);
CREATE UNIQUE INDEX checks_project_slug ON checks(project_id, slug) WHERE slug != '';

CREATE TABLE pings (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  n           INTEGER NOT NULL,
  owner_id    INTEGER NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
  created     INTEGER NOT NULL,
  kind        TEXT NULL CHECK (kind IN ('start','fail','log','ign')),
  scheme      TEXT NOT NULL DEFAULT 'http',
  remote_addr TEXT NULL,
  method      TEXT NOT NULL DEFAULT '',
  ua          TEXT NOT NULL DEFAULT '',
  body        BLOB NULL,
  body_size   INTEGER NOT NULL DEFAULT 0,
  exitstatus  INTEGER NULL,
  rid         TEXT NULL
);
CREATE UNIQUE INDEX pings_owner_n ON pings(owner_id, n);
CREATE INDEX pings_owner_created ON pings(owner_id, created DESC);
CREATE INDEX pings_rid ON pings(rid) WHERE rid IS NOT NULL;

-- 状态翻转事件：通知队列 + 宕机统计的唯一事实源
CREATE TABLE flips (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_id   INTEGER NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
  created    INTEGER NOT NULL,
  processed  INTEGER NULL,
  old_status TEXT NOT NULL,
  new_status TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '' CHECK (reason IN ('','timeout','fail'))
);
CREATE INDEX flips_pending ON flips(id) WHERE processed IS NULL;
CREATE INDEX flips_owner_created ON flips(owner_id, created DESC);

-- 通知渠道：全部走 apprise，urls 是换行分隔的 apprise URL
CREATE TABLE channels (
  id                        INTEGER PRIMARY KEY AUTOINCREMENT,
  code                      TEXT NOT NULL UNIQUE,
  project_id                INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name                      TEXT NOT NULL DEFAULT '',
  urls                      TEXT NOT NULL DEFAULT '',
  conf                      TEXT NOT NULL DEFAULT '{"up":true,"down":true}',
  disabled                  INTEGER NOT NULL DEFAULT 0,
  last_notify               INTEGER NULL,
  last_notify_duration_us   INTEGER NULL,
  last_error                TEXT NOT NULL DEFAULT '',
  created                   INTEGER NOT NULL
);
CREATE INDEX channels_project ON channels(project_id);

CREATE TABLE channel_checks (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  check_id   INTEGER NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
  PRIMARY KEY (channel_id, check_id)
);
CREATE INDEX channel_checks_check ON channel_checks(check_id);

CREATE TABLE notifications (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  code         TEXT NOT NULL UNIQUE,
  owner_id     INTEGER NULL REFERENCES checks(id) ON DELETE CASCADE,
  channel_id   INTEGER NULL REFERENCES channels(id) ON DELETE CASCADE,
  check_status TEXT NOT NULL,
  created      INTEGER NOT NULL,
  error        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX notifications_created ON notifications(created DESC);
