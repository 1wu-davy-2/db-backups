import { useEffect, useState } from 'react'
import { api } from '../api'
import type { DailyCount, Job, Stats, SystemInfo, View } from '../types'
import { formatBytes, formatDuration, formatTime, relTime, statusMeta, triggerLabel } from '../utils'

interface Props {
  onView: (v: View) => void
  onJob: (id: number) => void
}

export default function Dashboard({ onView, onJob }: Props) {
  const [stats, setStats] = useState<Stats | null>(null)
  const [daily, setDaily] = useState<DailyCount[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const [err, setErr] = useState('')
  const [system, setSystem] = useState<SystemInfo | null>(null)
  const [systemErr, setSystemErr] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let alive = true
    setLoading(true)
    ;(async () => {
      try {
        const [s, d, j] = await Promise.all([api.stats(), api.daily(7), api.jobs({ limit: 8 })])
        if (!alive) return
        setStats(s)
        setDaily(d)
        setJobs(j.items ?? [])
        setErr('')
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : '加载失败')
      } finally {
        if (alive) setLoading(false)
      }
    })()
    api.systemInfo().then((info) => {
      if (alive) { setSystem(info); setSystemErr('') }
    }).catch((e) => {
      if (alive) setSystemErr(e instanceof Error ? e.message : '客户端状态加载失败')
    })
    return () => { alive = false }
  }, [refresh])

  const maxTotal = Math.max(1, ...daily.map((d) => d.total))

  return (
    <>
      <div className="topbar">
        <div>
          <h1 className="page-title">总览</h1>
          <div className="page-sub">备份健康度与最近执行情况</div>
        </div>
        <div className="topbar-actions">
          {stats && stats.running_jobs > 0 && (
            <span className="badge badge-info">
              <span className="badge-dot" />
              {stats.running_jobs} 个任务执行中
            </span>
          )}
          <button className="btn" disabled={loading} onClick={() => setRefresh((n) => n + 1)}>{loading ? '加载中…' : '刷新'}</button>
          <button className="btn" onClick={() => onView('jobs')}>查看全部任务</button>
        </div>
      </div>

      {err && <div className="error-bar">{err}</div>}

      <div className="grid-4">
        <Metric label="目标库" value={stats ? String(stats.targets) : '—'} foot={`${stats?.policies ?? 0} 条启用策略`} />
        <Metric
          label="近 24 小时任务"
          value={stats ? String(stats.jobs_24h) : '—'}
          foot={stats && stats.failed_24h > 0 ? `${stats.failed_24h} 个失败` : '无失败'}
          tone={stats && stats.failed_24h > 0 ? 'err' : undefined}
        />
        <Metric label="备份占用" value={stats ? formatBytes(stats.live_bytes) : '—'} foot="未清理的备份总量" />
        <Metric
          label="最近成功备份"
          value={stats ? relTime(stats.last_success_at) : '—'}
          foot={stats?.last_success_at ? formatTime(new Date(stats.last_success_at * 1000).toISOString()) : '尚无成功记录'}
        />
      </div>

      <div className="grid-2" style={{ marginTop: 16 }}>
        <div className="card">
          <div className="card-head">
            <span className="card-title">近 7 天执行</span>
            <span className="faint small">深色为成功</span>
          </div>
          <div className="card-body">
            {daily.length === 0 ? (
              <div className="empty">暂无数据</div>
            ) : (
              <div className="chart">
                {daily.map((d) => (
                  <div className="chart-col" key={d.day}>
                    <div
                      className="chart-bar"
                      style={{ height: `${Math.max(5, (d.total / maxTotal) * 100)}%` }}
                      title={`${d.day}：${d.total} 次，成功 ${d.success}`}
                    >
                      <div
                        className="chart-bar-fill"
                        style={{ height: d.total ? `${(d.success / d.total) * 100}%` : '0%' }}
                      />
                    </div>
                    <div className="chart-day">{d.day.slice(5)}</div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <span className="card-title">最近任务</span>
            <button className="btn-link" onClick={() => onView('jobs')}>全部</button>
          </div>
          <div className="table-wrap">
            {jobs.length === 0 ? (
              <div className="empty">还没有执行过备份</div>
            ) : (
              <table>
                <tbody>
                  {jobs.map((j) => {
                    const st = statusMeta(j.status)
                    return (
                      <tr key={j.id}>
                        <td style={{ width: 44 }} className="mono">
                          <button className="btn-link mono" aria-label={`查看任务 #${j.id}`} onClick={() => onJob(j.id)}>#{j.id}</button>
                        </td>
                        <td>
                          <div>{j.target_name}</div>
                          <div className="faint small">{triggerLabel(j.trigger)}</div>
                        </td>
                        <td>
                          <span className={`badge ${st.cls}`}>{st.label}</span>
                        </td>
                        <td className="num mono muted">{formatBytes(j.size_bytes)}</td>
                        <td className="num small muted">{formatDuration(j.duration_ms)}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>

      <section className="card toolchain-card" aria-label="备份客户端状态">
        <div className="card-head">
          <span className="card-title">备份客户端</span>
          {system && !systemErr && <span className={`badge ${system.toolchain.length ? 'badge-ok' : 'badge-warn'}`}>
            {system.toolchain.length ? `${system.toolchain.length} 个可用版本` : '需要配置'}
          </span>}
        </div>
        <div className="card-body">
          {systemErr ? <div className="error-bar" role="alert">{systemErr} <button className="btn-link" onClick={() => setRefresh((n) => n + 1)}>重试</button></div>
            : !system ? <div className="muted small">正在读取客户端状态…</div>
              : system.toolchain.length > 0 ? <div className="toolchain-list">
                {system.toolchain.map((tool) => <div className="toolchain-row" key={tool.major}>
                  <span className="badge badge-info">pg_dump {tool.version}</span>
                  <span className="mono small muted toolchain-path">{tool.path}</span>
                </div>)}
              </div> : <>
                <div style={{ color: 'var(--warn)', fontWeight: 500 }}>未发现可用的 pg_dump</div>
                <div className="small muted" style={{ marginTop: 6 }}>在服务端设置 <code>BACKUP_PG_BIN_DIRS</code>，或将客户端放入数据目录下的 <code>pgclient/&lt;版本&gt;/bin</code>，然后重启服务。</div>
              </>}
          <div className="toolchain-help small muted">
            客户端主版本必须 ≥ 目标库主版本；系统自动选择满足条件的最小版本，并不代表兼容所有目标库。
            在服务端运行 <code>backupd --check-tools</code> 可查看探测路径；安装客户端后需重启服务。
          </div>
        </div>
      </section>
    </>
  )
}

function Metric({ label, value, foot, tone }: { label: string; value: string; foot: string; tone?: 'err' }) {
  return (
    <div className="card metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value" style={tone === 'err' ? { color: 'var(--err)' } : undefined}>
        {value}
      </div>
      <div className="metric-foot">{foot}</div>
    </div>
  )
}
