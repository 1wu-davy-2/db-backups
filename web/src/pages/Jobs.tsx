import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { Drawer } from '../components/Overlay'
import type { Artifact, Job, Target, User } from '../types'
import { formatBytes, formatDuration, formatTime, statusMeta, triggerLabel } from '../utils'

export default function Jobs({ user }: { user: User }) {
  const canWrite = user.role !== 'viewer'
  const [items, setItems] = useState<Job[]>([])
  const [total, setTotal] = useState(0)
  const [targets, setTargets] = useState<Target[]>([])
  const [targetID, setTargetID] = useState(0)
  const [status, setStatus] = useState('')
  const [err, setErr] = useState('')
  const [openID, setOpenID] = useState<number | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await api.jobs({ target_id: targetID || undefined, status, limit: 50 })
      setItems(r.items ?? [])
      setTotal(r.total)
      setErr('')
    } catch (e) {
      setErr(e instanceof Error ? e.message : '加载失败')
    }
  }, [targetID, status])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    api.targets().then(setTargets).catch(() => undefined)
  }, [])

  return (
    <>
      <div className="topbar">
        <div>
          <h1 className="page-title">备份任务</h1>
          <div className="page-sub">共 {total} 条记录，点击任意行查看实时日志</div>
        </div>
        <div className="topbar-actions">
          <select className="select" style={{ width: 180 }} value={targetID}
            onChange={(e) => setTargetID(Number(e.target.value))}>
            <option value={0}>全部目标库</option>
            {targets.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <select className="select" style={{ width: 130 }} value={status}
            onChange={(e) => setStatus(e.target.value)}>
            <option value="">全部状态</option>
            <option value="success">成功</option>
            <option value="failed">失败</option>
            <option value="running">执行中</option>
            <option value="canceled">已取消</option>
          </select>
          <button className="btn" onClick={() => void load()}>刷新</button>
        </div>
      </div>

      {err && <div className="error-bar">{err}</div>}

      <div className="card">
        <div className="table-wrap">
          {items.length === 0 ? (
            <div className="empty">没有符合条件任务</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th style={{ width: 56 }}>ID</th>
                  <th>目标库</th>
                  <th>状态</th>
                  <th>触发</th>
                  <th className="num">大小</th>
                  <th className="num">耗时</th>
                  <th>开始时间</th>
                </tr>
              </thead>
              <tbody>
                {items.map((j) => {
                  const st = statusMeta(j.status)
                  return (
                    <tr key={j.id} style={{ cursor: 'pointer' }} onClick={() => setOpenID(j.id)}>
                      <td className="mono faint">#{j.id}</td>
                      <td style={{ fontWeight: 500 }}>{j.target_name}</td>
                      <td>
                        <span className={`badge ${st.cls}`}>
                          {(j.status === 'running' || j.status === 'pending') && <span className="badge-dot" />}
                          {st.label}
                        </span>
                        {j.status === 'failed' && j.error_code && (
                          <div className="faint small">{j.error_code}</div>
                        )}
                      </td>
                      <td className="small muted">{triggerLabel(j.trigger)}</td>
                      <td className="num mono">{formatBytes(j.size_bytes)}</td>
                      <td className="num small muted">{formatDuration(j.duration_ms)}</td>
                      <td className="small muted">{formatTime(j.started_at ?? j.created_at)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {openID !== null && (
        <JobDetail id={openID} canWrite={canWrite} onClose={() => { setOpenID(null); void load() }} />
      )}
    </>
  )
}

function JobDetail({ id, canWrite, onClose }: { id: number; canWrite: boolean; onClose: () => void }) {
  const [job, setJob] = useState<Job | null>(null)
  const [lines, setLines] = useState<string[]>([])
  const [arts, setArts] = useState<Artifact[]>([])
  const [err, setErr] = useState('')
  const logRef = useRef<HTMLPreElement>(null)

  const reload = useCallback(async () => {
    try {
      const [j, a] = await Promise.all([api.job(id), api.jobArtifacts(id)])
      setJob(j)
      setArts(a ?? [])
    } catch (e) {
      setErr(e instanceof Error ? e.message : '加载失败')
    }
  }, [id])

  useEffect(() => {
    void reload()
  }, [reload])

  const active = job?.status === 'running' || job?.status === 'pending'

  useEffect(() => {
    const es = new EventSource(`/api/jobs/${id}/logs/stream`)
    es.addEventListener('reset', () => setLines([]))
    es.onmessage = (ev) => {
      setLines((prev) => {
        const next = [...prev, ev.data]
        return next.length > 800 ? next.slice(-800) : next
      })
    }
    es.onerror = () => {
      /* 浏览器会自动重连 */
    }
    return () => es.close()
  }, [id])

  useEffect(() => {
    const el = logRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines])

  useEffect(() => {
    if (!job) return
    if (job.status === 'success' || job.status === 'failed' || job.status === 'canceled') return
    const t = setInterval(() => void reload(), 4000)
    return () => clearInterval(t)
  }, [job, reload])

  async function cancel() {
    try {
      await api.cancelJob(id)
      await reload()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '取消失败')
    }
  }

  return (
    <Drawer
      title={`任务 #${id}${job ? ` · ${job.target_name}` : ''}`}
      subtitle={job ? `${triggerLabel(job.trigger)}触发 · ${formatTime(job.started_at ?? job.created_at)}` : ''}
      onClose={onClose}
      footer={
        <>
          {canWrite && active && (
            <button className="btn btn-danger" onClick={cancel}>取消任务</button>
          )}
          <button className="btn" onClick={onClose}>关闭</button>
        </>
      }
    >
      {err && <div className="error-bar" style={{ margin: 16 }}>{err}</div>}

      <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)' }}>
        <div className="row" style={{ marginBottom: 10, flexWrap: 'wrap' }}>
          {job && <span className={`badge ${statusMeta(job.status).cls}`}>{statusMeta(job.status).label}</span>}
          {job?.error_code && <span className="badge badge-err">{job.error_code}</span>}
          <span className="spacer" />
          {job?.size_bytes ? <span className="small muted">{formatBytes(job.size_bytes)}</span> : null}
        </div>
        <dl className="kv">
          <dt>目标库</dt><dd>{job?.target_name ?? '—'}</dd>
          <dt>耗时</dt><dd>{formatDuration(job?.duration_ms ?? 0)}</dd>
          <dt>客户端</dt><dd>{job?.tool_version || '—'}（源库 {job?.server_version || '—'}）</dd>
          <dt>sha256</dt><dd className="mono">{job?.dump_sha256 || '—'}</dd>
          {job?.error_message && (<><dt>错误</dt><dd style={{ color: 'var(--err)' }}>{job.error_message}</dd></>)}
        </dl>
        {arts.length > 0 && (
          <div style={{ marginTop: 10 }}>
            {arts.map((a) => (
              <div key={a.id} className="row small" style={{ marginTop: 4 }}>
                <span className="mono faint" style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {a.object_key}
                </span>
                <span className="muted">{formatBytes(a.size_bytes)}</span>
                <a className="btn btn-sm" href={api.artifactURL(a.id)}>下载</a>
              </div>
            ))}
          </div>
        )}
      </div>

      <pre className="log" ref={logRef}>
        {lines.length === 0 ? (
          <span className="faint">等待日志…</span>
        ) : (
          lines.map((l, i) => (
            <span
              key={i}
              className={`log-line${l.includes('[warn]') ? ' warn' : ''}${l.includes('[error]') || l.includes('[err]') ? ' err' : ''}`}
            >
              {l}
            </span>
          ))
        )}
      </pre>
    </Drawer>
  )
}
