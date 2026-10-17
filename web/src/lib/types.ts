// Shapes returned by the feixi backend. These field names are the compatibility
// surface: agents and scripts send and parse them.

export type Display = 'new' | 'up' | 'grace' | 'down' | 'paused' | 'started';

export interface Check {
  name: string;
  slug: string;
  tags: string;
  desc: string;
  grace: number;
  n_pings: number;
  status: Display;
  started: boolean;
  last_ping: string | null;
  next_ping: string | null;
  manual_resume: boolean;
  methods: string;
  subject: string;
  subject_fail: string;
  start_kw: string;
  success_kw: string;
  failure_kw: string;
  filter_subject: boolean;
  filter_body: boolean;
  filter_http_body: boolean;
  filter_default_fail: boolean;
  failure_tolerance: number;
  failure_count?: number;
  flapping?: boolean;
  badge_url: string;
  uuid: string;
  ping_url: string;
  update_url: string;
  pause_url: string;
  resume_url: string;
  channels: string;
  timeout?: number;
  schedule?: string;
  tz?: string;
  last_duration?: number;
}

export interface Ping {
  type: 'success' | 'start' | 'log' | 'fail' | 'ign';
  date: string;
  n: number;
  scheme: string;
  remote_addr: string;
  method: string;
  ua: string;
  rid: string | null;
  body_url: string | null;
  duration?: number;
  exit_status?: number | null;
}

export interface FlipEvent {
  timestamp: string;
  up: 0 | 1;
  old_status?: string;
  new_status?: string;
  reason?: '' | 'timeout' | 'fail';
}

// 检查项事件流的一行：ping、状态翻转、通知投递三者合并。
export type StreamEvent =
  | {
      type: 'ping';
      date: string;
      n: number;
      kind: Ping['type'];
      rid: string | null;
      duration?: number;
      exit_status?: number;
    }
  | {
      type: 'flip';
      date: string;
      up: boolean;
      old_status: string;
      new_status: string;
      reason?: 'timeout' | 'fail';
    }
  | {
      type: 'notification';
      date: string;
      code: string;
      channel: string;
      status: 'up' | 'down' | 'flap' | 'stable';
      error: string;
    };

export interface Channel {
  code: string;
  name: string;
  kind: string;
  urls: string[];
  up: boolean;
  down: boolean;
  nag: boolean;
  disabled: boolean;
  last_notify: string | null;
  last_notify_duration: number | null;
  last_error: string;
}

export interface Meta {
  apprise_schemas: string[];
  site_root: string;
  nag_period_seconds: number;
  flap_window_seconds: number;
  flap_threshold: number;
}

export interface NotificationRow {
  code: string;
  created: string | null;
  check_name: string;
  channel_name: string;
  status: string;
  error: string;
}

export interface DowntimeRow {
  boundary: string;
  seconds: number;
  count: number;
  uptime: number;
  no_data: boolean;
}

export interface StatusPageCheck {
  name: string;
  tags: string;
  status: Display;
  started: boolean;
  flapping: boolean;
  last_ping: string | null;
}

export interface StatusPageData {
  project: string;
  summary: 'ok' | 'grace' | 'down';
  total: number;
  down: number;
  grace: number;
  updated_at: string;
  checks: StatusPageCheck[];
}

export interface StatusFeed {
  title: string;
  details: Array<{
    code: string;
    status: Display;
    started: boolean;
    last_ping: string | null;
    next_ping: string | null;
  }>;
  tags: Record<string, Display>;
}

export interface ProjectInfo {
  name: string;
  badge_key: string;
  ping_key: string;
  show_slugs: boolean;
  check_count: number;
  // 0 means no cap (FEIXI_MAX_CHECKS unset).
  max_checks: number;
  ping_body_limit: number;
  ping_log_limit: number;
  site_root: string;
  master_badge_label: string;
}

export interface CheckSpec {
  name?: string;
  slug?: string;
  tags?: string;
  desc?: string;
  timeout?: number;
  grace?: number;
  schedule?: string;
  tz?: string;
  channels?: string;
  methods?: string;
  manual_resume?: boolean;
  filter_http_body?: boolean;
  filter_default_fail?: boolean;
  start_kw?: string;
  success_kw?: string;
  failure_kw?: string;
  failure_tolerance?: number;
  unique?: string[];
}
