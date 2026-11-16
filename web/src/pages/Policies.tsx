import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import { Modal } from '../components/Overlay'
import type { Policy, Target, User } from '../types'
import { formatBytes, formatTime } from '../utils'

const EMPTY = {
  target_id: 0,
  cron_expr: '0 2 * * *',
  timezone: 'Asia/Shanghai',
  retain_count: 7,
  retain_days: 30,
  retain_max_gb: 0,
  keep_min_count: 3,
  compression: 'zstd:5',
  verify_mode: 'toc',
  timeout_seconds: 7200,
  encrypt: true,
  is_enabled: true,
}

type FormState = typeof EMPTY

const CRON_PRESETS = [
  { label: '每天 02:00', value: '0 2 * * *' },
  { label: '每 6 小时', value: '0 */6 * * *' },
  { label: '每周日 03:00', value: '0 3 * * 0' },
  { label: '每月 1 日 03:00', value: '0 3 1 * *' },
]

export default function Policies({ user }: { user: User }) {
  const canWrite = user.role !== 'viewer'
  const [list, setList] = useState<Policy[]>([])
  const [targets, setTargets] = useState<Target[]>([])
  const [err, setErr] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState<Policy | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY)

  async function load() {
    try {
      const [p, t] = await Promise.all([api.policies(), api.targets()])
      setList(p)
      setTargets(t)
      setErr('')
    } catch (e) {
      setErr(e instanceof Error ? e.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  const usedTargets = useMemo(() => new Set(list.map((p) => p.target_id)), [list])

  function openCreate() {
    const free = targets.find((t) => !usedTargets.has(t.id))
    setEditing(null)
    setForm({ ...EMPTY, target_id: free?.id ?? 0 })
    setShowForm(true)
  }

  function openEdit(p: Policy) {
    setEditing(p)
    setForm({
      target_id: p.target_id,
      cron_expr: p.cron_expr,
      timezone: p.timezone,
      retain_count: p.retain_count,
      retain_days: p.retain_days,
      retain_max_gb: p.retain_max_bytes > 0 ? p.retain_max_bytes / 1024 / 1024 / 1024 : 0,
      keep_min_count: p.keep_min_count,
      compression: p.compression,
      verify_mode: p.verify_mode,
      timeout_seconds: p.timeout_seconds,
      encrypt: p.encrypt,
      is_enabled: p.is_enabled,
    })
    setShowForm(true)
  }

  async function save() {
    if (!form.target_id) {
      setErr('请选择目标库')
      return
    }
    setBusy(true)
    setErr('')
    try {
      const body = {
        target_id: Number(form.target_id),
        cron_expr: form.cron_expr.trim(),
        timezone: form.timezone,
        dump_format: 'custom',
        compression: form.compression,
        encrypt: form.encrypt,
        retain_count: Number(form.retain_count) || 0,
        retain_days: Number(form.retain_days) || 0,
        retain_max_bytes: Math.round(Number(form.retain_max_gb) * 1024 * 1024 * 1024),
        keep_min_count: Number(form.keep_min_count) || 1,
        verify_mode: form.verify_mode,
        timeout_seconds: Number(form.timeout_seconds) || 7200,
        misfire_policy: 'skip',
        is_enabled: form.is_enabled,
      }
      if (editing) {
        await api.updatePolicy(editing.id, body)
        setNotice('策略已更新，调度已重新加载')
      } else {
        await api.createPolicy(body)
        setNotice('策略已创建')
      }
      setShowForm(false)
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function remove(p: Policy) {
    if (!window.confirm(`删除 ${p.target_name} 的备份策略？已产生的备份不会被删除。`)) return
    try {
      await api.deletePolicy(p.id)
      setNotice('策略已删除')
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '删除失败')
    }
  }

  const availableTargets = targets.filter(
    (t) => !usedTargets.has(t.id) || t.id === editing?.target_id,
  )

  return (
    <>
      <div className="topbar">
        <div>
          <h1 className="page-title">备份策略</h1>
          <div className="page-sub">一个库一条策略；保留规则三条件任一命中即清理，最新 N 份永不动</div>
        </div>
        {canWrite && (
          <div className="topbar-actions">
            <button className="btn btn-primary" onClick={openCreate} disabled={targets.length === 0}>
              新建策略
            </button>
          </div>
        )}
      </div>

      {err && <div className="error-bar">{err}</div>}
      {notice && <div className="ok-bar">{notice}</div>}

      <div className="card">
        <div className="table-wrap">
          {list.length === 0 ? (
            <div className="empty">
              {targets.length === 0 ? '请先添加目标库。' : '还没有策略，备份不会自动执行。'}
            </div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>目标库</th>
                  <th>调度</th>
                  <th>保留</th>
                  <th>下次执行</th>
                  <th>校验</th>
                  <th>状态</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {list.map((p) => (
                  <tr key={p.id}>
                    <td style={{ fontWeight: 500 }}>{p.target_name || `#${p.target_id}`}</td>
                    <td className="mono small">
                      {p.cron_expr}
                      <div className="faint">{p.timezone}</div>
                    </td>
                    <td className="small">
                      {p.retain_count > 0 && <div>{p.retain_count} 份</div>}
                      {p.retain_days > 0 && <div>{p.retain_days} 天</div>}
                      {p.retain_max_bytes > 0 && <div>上限 {formatBytes(p.retain_max_bytes)}</div>}
                      <div className="faint">保底 {p.keep_min_count} 份</div>
                    </td>
                    <td className="small muted">{formatTime(p.next_run_at)}</td>
                    <td>
                      <span className="badge badge-mute">
                        {p.verify_mode === 'toc' ? 'TOC' : p.verify_mode === 'checksum' ? '校验和' : '关闭'}
                      </span>
                      {p.encrypt && <div className="faint small" style={{ marginTop: 4 }}>已加密</div>}
                    </td>
                    <td>
                      {p.is_enabled ? (
                        <span className="badge badge-ok"><span className="badge-dot" />启用</span>
                      ) : (
                        <span className="badge badge-mute">暂停</span>
                      )}
                    </td>
                    <td>
                      <div className="cell-actions">
                        {canWrite && (
                          <>
                            <button className="btn btn-sm" onClick={() => openEdit(p)}>编辑</button>
                            <button className="btn btn-sm btn-danger" onClick={() => remove(p)}>删除</button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {showForm && (
        <Modal
          title={editing ? `编辑策略 · ${editing.target_name}` : '新建备份策略'}
          onClose={() => setShowForm(false)}
          footer={
            <>
              <button className="btn" onClick={() => setShowForm(false)}>取消</button>
              <button className="btn btn-primary" onClick={save} disabled={busy}>
                {busy ? '保存中…' : '保存策略'}
              </button>
            </>
          }
        >
          <div className="field">
            <label className="label">目标库</label>
            <select
              className="select"
              value={form.target_id}
              disabled={!!editing}
              onChange={(e) => setForm({ ...form, target_id: Number(e.target.value) })}
            >
              <option value={0}>请选择</option>
              {availableTargets.map((t) => (
                <option key={t.id} value={t.id}>{t.name}（{t.host}/{t.dbname}）</option>
              ))}
            </select>
            {availableTargets.length === 0 && (
              <div className="hint">所有目标库都已有策略，一个库只能有一条。</div>
            )}
          </div>

          <div className="field">
            <label className="label">执行时间（cron，5 段）</label>
            <input className="input mono" value={form.cron_expr}
              onChange={(e) => setForm({ ...form, cron_expr: e.target.value })} />
            <div className="row" style={{ flexWrap: 'wrap', marginTop: 4 }}>
              {CRON_PRESETS.map((p) => (
                <button key={p.value} className="btn btn-sm"
                  onClick={() => setForm({ ...form, cron_expr: p.value })}>
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          <div className="field-row">
            <div className="field">
              <label className="label">时区</label>
              <input className="input" value={form.timezone}
                onChange={(e) => setForm({ ...form, timezone: e.target.value })} />
            </div>
            <div className="field">
              <label className="label">单次超时（秒）</label>
              <input className="input" type="number" value={form.timeout_seconds}
                onChange={(e) => setForm({ ...form, timeout_seconds: Number(e.target.value) })} />
            </div>
          </div>

          <div style={{ borderTop: '1px solid var(--border)', margin: '16px 0', paddingTop: 14 }}>
            <div className="label" style={{ marginBottom: 10 }}>保留策略（任一条件命中即清理，三项至少填一项）</div>
            <div className="field-row">
              <div className="field">
                <label className="label">保留份数</label>
                <input className="input" type="number" min={0} value={form.retain_count}
                  onChange={(e) => setForm({ ...form, retain_count: Number(e.target.value) })} />
              </div>
              <div className="field">
                <label className="label">保留天数</label>
                <input className="input" type="number" min={0} value={form.retain_days}
                  onChange={(e) => setForm({ ...form, retain_days: Number(e.target.value) })} />
              </div>
            </div>
            <div className="field-row">
              <div className="field">
                <label className="label">容量上限（GB，0 表示不限）</label>
                <input className="input" type="number" min={0} step="0.5" value={form.retain_max_gb}
                  onChange={(e) => setForm({ ...form, retain_max_gb: Number(e.target.value) })} />
              </div>
              <div className="field">
                <label className="label">保底份数（永不删除）</label>
                <input className="input" type="number" min={1} value={form.keep_min_count}
                  onChange={(e) => setForm({ ...form, keep_min_count: Number(e.target.value) })} />
              </div>
            </div>
          </div>

          <div className="field-row">
            <div className="field">
              <label className="label">压缩</label>
              <select className="select" value={form.compression}
                onChange={(e) => setForm({ ...form, compression: e.target.value })}>
                <option value="zstd:5">zstd:5（推荐）</option>
                <option value="zstd:9">zstd:9（更小更慢）</option>
                <option value="gzip:6">gzip:6</option>
                <option value="none">不压缩</option>
              </select>
              <div className="hint">zstd 需要 PG 16+ 客户端，低版本会自动回退 gzip</div>
            </div>
            <div className="field">
              <label className="label">备份后校验</label>
              <select className="select" value={form.verify_mode}
                onChange={(e) => setForm({ ...form, verify_mode: e.target.value })}>
                <option value="toc">解析归档目录（TOC）</option>
                <option value="checksum">仅校验和</option>
                <option value="none">不校验</option>
              </select>
            </div>
          </div>

          <div className="row" style={{ gap: 24 }}>
            <label className="checkbox">
              <input type="checkbox" checked={form.encrypt}
                onChange={(e) => setForm({ ...form, encrypt: e.target.checked })} />
              备份文件加密存储
            </label>
            <label className="checkbox">
              <input type="checkbox" checked={form.is_enabled}
                onChange={(e) => setForm({ ...form, is_enabled: e.target.checked })} />
              启用该策略
            </label>
          </div>
        </Modal>
      )}
    </>
  )
}
