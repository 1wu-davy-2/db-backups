import type { JobStatus } from './types'

export function formatBytes(n: number): string {
  if (!n || n <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const digits = i === 0 ? 0 : v >= 100 ? 0 : 1
  return `${v.toFixed(digits)} ${units[i]}`
}

export function formatTime(s?: string | null): string {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 1971) return '—'
  return d.toLocaleString('zh-CN', { hour12: false })
}

export function formatDuration(ms: number): string {
  if (!ms || ms <= 0) return '—'
  if (ms < 1000) return `${ms} ms`
  const sec = ms / 1000
  if (sec < 60) return `${sec.toFixed(1)} s`
  const m = Math.floor(sec / 60)
  const s = Math.round(sec % 60)
  if (m < 60) return `${m} m ${s} s`
  return `${Math.floor(m / 60)} h ${m % 60} m`
}

export function relTime(epochSec?: number): string {
  if (!epochSec) return '暂无成功备份'
  const diff = Date.now() / 1000 - epochSec
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
}

export function statusMeta(s: JobStatus): { label: string; cls: string } {
  switch (s) {
    case 'success':
      return { label: '成功', cls: 'badge-ok' }
    case 'failed':
      return { label: '失败', cls: 'badge-err' }
    case 'running':
      return { label: '执行中', cls: 'badge-info' }
    case 'pending':
      return { label: '排队中', cls: 'badge-warn' }
    case 'canceled':
      return { label: '已取消', cls: 'badge-mute' }
    default:
      return { label: s, cls: 'badge-mute' }
  }
}

export function triggerLabel(t: string): string {
  switch (t) {
    case 'schedule':
      return '定时'
    case 'manual':
      return '手动'
    case 'retry':
      return '重试'
    default:
      return t
  }
}
