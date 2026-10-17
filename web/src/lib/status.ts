import type { Display } from './types';

export interface StatusMeta {
  label: string;
  // Badge colours: up green, grace orange, down red.
  color: string;
  text: string;
  tip: string;
}

const meta: Record<Display, StatusMeta> = {
  new: {
    label: '新建',
    color: '#9aa3af',
    text: 'text-idle',
    tip: '新检查项，从未收到过 ping。'
  },
  up: {
    label: '正常',
    color: '#4caf50',
    text: 'text-up',
    tip: '一切正常，成功信号按时到达。'
  },
  grace: {
    label: '延迟',
    color: '#fe7d37',
    text: 'text-late',
    tip: '成功信号迟到了，宽限期还没过。'
  },
  down: {
    label: '宕机',
    color: '#e05d44',
    text: 'text-down',
    tip: '成功信号没来，宽限期已过。'
  },
  paused: {
    label: '暂停',
    color: '#9aa3af',
    text: 'text-idle',
    tip: '监控已暂停。发送 ping 即可恢复。'
  },
  started: {
    label: '运行中',
    color: '#9aa3af',
    text: 'text-ink-soft',
    tip: '收到了 start 信号，还没结束。'
  }
};

export function statusMeta(status: Display): StatusMeta {
  return meta[status] ?? meta.new;
}

// A badge's "late" state is the same thing the dashboard calls grace.
export function badgeStatus(status: Display): 'up' | 'late' | 'down' {
  if (status === 'down') return 'down';
  if (status === 'grace') return 'late';
  return 'up';
}

export const STATUS_FILTERS: Array<{ value: string; label: string }> = [
  { value: '', label: '全部状态' },
  { value: 'new', label: '新建' },
  { value: 'paused', label: '已暂停' },
  { value: 'started', label: '运行中' },
  { value: 'up', label: '正常' },
  { value: 'grace', label: '延迟' },
  { value: 'down', label: '宕机' }
];
