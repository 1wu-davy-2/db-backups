import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api } from '../api'
import { Drawer } from '../components/Overlay'
import type { Artifact, Job, Target, User } from '../types'
import { formatBytes, formatDuration, formatTime, statusMeta, triggerLabel } from '../utils'

const PAGE_SIZE = 50

export default function Jobs({ user, initialJobID }: { user: User; initialJobID: number | null }) {
  const canWrite = user.role !== 'viewer'
  const [items, setItems] = useState<Job[]>([])
  const [total, setTotal] = useState(0)
  const [targets, setTargets] = useState<Target[]>([])
  const [targetID, setTargetID] = useState(0)
  const [status, setStatus] = useState('')
  const [offset, setOffset] = useState(0)
  const [refresh, setRefresh] = useState(0)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [targetErr, setTargetErr] = useState('')
  const [openID, setOpenID] = useState<number | null>(initialJobID)

  useEffect(() => {
    let alive = true
    let pending = false
    let timer: ReturnType<typeof setTimeout>
    setLoading(true)
    async function load() {
      if (pending) return
      pending = true
      try {
        const r = await api.jobs({ target_id: targetID || undefined, status, limit: PAGE_SIZE, offset })
        if (!alive) return
        if (offset > 0 && offset >= r.total) {
          setOffset(Math.max(0, Math.ceil(r.total / PAGE_SIZE) - 1) * PAGE_SIZE)
          return
        }
        setItems(r.items ?? [])
        setTotal(r.total)
        setErr('')
        if (r.items?.some((j) => j.status === 'pending' || j.status === 'running')) {
          timer = setTimeout(() => void load(), 4000)
        }
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : '加载失败')
      } finally {
        pending = false
        if (alive) setLoading(false)
      }
    }
    void load()
    return () => { alive = false; clearTimeout(timer) }
  }, [targetID, status, offset, refresh])

  useEffect(() => {
    let alive = true
    api.targets().then((t) => {
      if (alive) { setTargets(t); setTargetErr('') }
    }).catch(() => {
      if (alive) setTargetErr('目标库筛选项加载失败，请刷新重试')
    })
    return () => { alive = false }
  }, [refresh])

  return (
    <>
      <div className="topbar">
        <div>
          <h1 className="page-title">备份任务</h1>
          <div className="page-sub">共 {total} 条记录，点击任务查看详情与日志</div>
        </div>
        <div className="topbar-actions">
          <select className="select" aria-label="目标库筛选" style={{ width: 180 }} value={targetID}
            onChange={(e) => { setTargetID(Number(e.target.value)); setOffset(0) }}>
            <option value={0}>全部目标库</option>
            {targets.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <select className="select" aria-label="任务状态" style={{ width: 130 }} value={status}
            onChange={(e) => { setStatus(e.target.value); setOffset(0) }}>
            <option value="">全部状态</option>
            <option value="pending">排队中</option>
            <option value="running">执行中</option>
            <option value="success">成功</option>
            <option value="failed">失败</option>
            <option value="canceled">已取消</option>
          </select>
          <button className="btn" disabled={loading} onClick={() => setRefresh((n) => n + 1)}>{loading ? '加载中…' : '刷新'}</button>
        </div>
      </div>

      {err && <div className="error-bar" role="alert">{err}</div>}
      {targetErr && <div className="error-bar" role="alert">{targetErr}</div>}

      <div className="card" aria-busy={loading}>
        <div className="table-wrap">
          {loading ? <div className="empty">正在加载任务…</div> : items.length === 0 ? (
            <div className="empty">{err ? '任务加载失败，请刷新重试' : '没有符合条件的任务'}</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th style={{ width: 56 }}>ID</th><th>目标库</th><th>状态</th><th>触发</th>
                  <th className="num">大小</th><th className="num">耗时</th><th>开始时间</th>
                </tr>
              </thead>
              <tbody>
                {items.map((j) => {
                  const st = statusMeta(j.status)
                  return (
                    <tr key={j.id} className="clickable-row" onClick={() => setOpenID(j.id)}>
                      <td className="mono">
                        <button className="btn-link mono" aria-label={`查看任务 #${j.id}`} onClick={(e) => { e.stopPropagation(); setOpenID(j.id) }}>#{j.id}</button>
                      </td>
                      <td style={{ fontWeight: 500 }}>{j.target_name}</td>
                      <td>
                        <span className={`badge ${st.cls}`}>
                          {(j.status === 'running' || j.status === 'pending') && <span className="badge-dot" />}{st.label}
                        </span>
                        {j.status === 'failed' && j.error_code && <div className="faint small">{j.error_code}</div>}
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
        <div className="pagination">
          <span className="small muted">{total === 0 ? '0 条记录' : `第 ${offset + 1}–${Math.min(offset + PAGE_SIZE, total)} 条，共 ${total} 条`}</span>
          <div className="row">
            <button className="btn btn-sm" disabled={loading || offset === 0} onClick={() => setOffset((n) => Math.max(0, n - PAGE_SIZE))}>上一页</button>
            <span className="small muted">{Math.floor(offset / PAGE_SIZE) + 1} / {Math.max(1, Math.ceil(total / PAGE_SIZE))}</span>
            <button className="btn btn-sm" disabled={loading || offset + PAGE_SIZE >= total} onClick={() => setOffset((n) => n + PAGE_SIZE)}>下一页</button>
          </div>
        </div>
      </div>

      {openID !== null && <JobDetail key={openID} id={openID} canWrite={canWrite}
        onClose={() => { setOpenID(null); setRefresh((n) => n + 1) }} />}
    </>
  )
}

function JobDetail({ id, canWrite, onClose }: { id: number; canWrite: boolean; onClose: () => void }) {
  const [job, setJob] = useState<Job | null>(null)
  const [lines, setLines] = useState<string[]>([])
  const [arts, setArts] = useState<Artifact[]>([])
  const [err, setErr] = useState('')
  const [actionErr, setActionErr] = useState('')
  const [logErr, setLogErr] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [logRefresh, setLogRefresh] = useState(0)
  const [logState, setLogState] = useState('正在加载日志…')
  const [follow, setFollow] = useState(true)
  const [canceling, setCanceling] = useState(false)
  const [cancelRequested, setCancelRequested] = useState(false)
  const logRef = useRef<HTMLPreElement>(null)
  const active = job?.status === 'running' || job?.status === 'pending'

  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const [j, a] = await Promise.all([api.job(id), api.jobArtifacts(id)])
        if (!alive) return
        setJob(j)
        setArts(a ?? [])
        setErr('')
        if (j.status === 'running' || j.status === 'pending') timer = setTimeout(() => void load(), 4000)
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : '加载失败')
      }
    }
    void load()
    return () => { alive = false; clearTimeout(timer) }
  }, [id, refresh])

  const jobStatus = job?.status
  useEffect(() => {
    if (!jobStatus) return
    let alive = true
    setLogErr('')
    if (jobStatus !== 'running' && jobStatus !== 'pending') {
      setLogState('正在加载历史日志…')
      api.jobLogs(id).then((r) => {
        if (!alive) return
        setLines(r.lines)
        setLogState('历史日志')
      }).catch((e) => {
        if (!alive) return
        setLogErr(e instanceof Error ? e.message : '日志加载失败')
        setLogState('日志加载失败')
      })
      return () => { alive = false }
    }
    setLogState('正在连接实时日志…')
    const es = new EventSource(`/api/jobs/${id}/logs/stream`)
    es.onopen = () => { if (alive) setLogState('实时日志已连接') }
    es.addEventListener('reset', () => { if (alive) setLines([]) })
    es.onmessage = (ev) => {
      if (alive) setLines((prev) => [...prev, ev.data].slice(-800))
    }
    es.onerror = () => { if (alive) setLogState('实时日志连接中断，正在重连…') }
    return () => { alive = false; es.close() }
  }, [id, jobStatus, logRefresh])

  useLayoutEffect(() => {
    const el = logRef.current
    if (el && follow) el.scrollTop = el.scrollHeight
  }, [lines, follow])

  async function cancel() {
    if (canceling || cancelRequested) return
    if (!window.confirm(`确认取消任务 #${id}？未完成的备份将无法下载。`)) return
    setCanceling(true)
    setActionErr('')
    try {
      await api.cancelJob(id)
      setCancelRequested(true)
      setRefresh((n) => n + 1)
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : '取消失败')
    } finally {
      setCanceling(false)
    }
  }

  return (
    <Drawer title={`任务 #${id}${job ? ` · ${job.target_name}` : ''}`}
      subtitle={job ? `${triggerLabel(job.trigger)}触发 · ${formatTime(job.started_at ?? job.created_at)}` : '正在加载任务…'}
      onClose={onClose} busy={canceling}
      footer={<>
        {canWrite && active && <button className="btn btn-danger" disabled={canceling || cancelRequested} onClick={() => void cancel()}>
          {canceling ? '取消中…' : cancelRequested ? '已请求取消' : '取消任务'}
        </button>}
        <button className="btn" disabled={canceling} onClick={onClose}>关闭</button>
      </>}
    >
      {err && <div className="error-bar detail-notice" role="alert">{err} <button className="btn-link" onClick={() => setRefresh((n) => n + 1)}>重试详情</button></div>}
      {actionErr && <div className="error-bar detail-notice" role="alert">{actionErr}</div>}
      {cancelRequested && active && <div className="ok-bar detail-notice" role="status">取消请求已发送，正在等待任务结束。</div>}
      {job && <div className="job-summary">
        <div className="row" style={{ marginBottom: 10, flexWrap: 'wrap' }}>
          <span className={`badge ${statusMeta(job.status).cls}`}>{statusMeta(job.status).label}</span>
          {job.error_code && <span className="badge badge-err">{job.error_code}</span>}
          <span className="spacer" />
          {job.size_bytes > 0 && <span className="small muted">{formatBytes(job.size_bytes)}</span>}
        </div>
        <dl className="kv">
          <dt>目标库</dt><dd>{job.target_name}</dd>
          <dt>耗时</dt><dd>{formatDuration(job.duration_ms)}</dd>
          <dt>客户端</dt><dd>{job.tool_version || '—'}（源库 {job.server_version || '—'}）</dd>
          <dt>sha256</dt><dd className="mono">{job.dump_sha256 || '—'}</dd>
          {job.error_message && <><dt>错误</dt><dd className="job-error">{job.error_message}</dd></>}
        </dl>
        {arts.map((a) => <div key={a.id} className="artifact-row">
          <span className="mono faint artifact-name" title={a.object_key}>{a.object_key}</span>
          <span className="muted">{formatBytes(a.size_bytes)}</span>
          <a className="btn btn-sm" href={api.artifactURL(a.id)}>下载</a>
        </div>)}
        {job.status === 'success' && arts.length === 0 && <div className="hint">无可下载备份，文件可能已清理。</div>}
      </div>}
      <div className="log-toolbar">
        <span className="small muted" role="status">{logState}</span>
        <label className="checkbox small"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />自动滚动</label>
      </div>
      {logErr && <div className="error-bar detail-notice" role="alert">{logErr} <button className="btn-link" onClick={() => setLogRefresh((n) => n + 1)}>重试日志</button></div>}
      <pre className="log" ref={logRef} aria-label="任务日志" tabIndex={0} onScroll={() => {
        const el = logRef.current
        if (el && el.scrollHeight - el.scrollTop - el.clientHeight > 40) setFollow(false)
      }}>
        {lines.length === 0 ? <span className="faint">{logErr ? '日志暂不可用，请重试。' : active ? '等待日志…' : job ? '暂无日志' : '正在加载…'}</span>
          : lines.map((l, i) => <span key={i} className={`log-line${l.includes('[warn]') ? ' warn' : ''}${l.includes('[error]') || l.includes('[err]') ? ' err' : ''}`}>{l}</span>)}
      </pre>
    </Drawer>
  )
}
