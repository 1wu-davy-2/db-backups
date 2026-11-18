import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api } from '../api'
import { Modal } from '../components/Overlay'
import type { Target, User } from '../types'
import { formatBytes, formatTime } from '../utils'

const EMPTY = {
  engine: 'postgres', name: '', host: '', port: 5432, dbname: '', username: '',
  password: '', ssl_mode: 'prefer', tags: '', is_enabled: true,
}

type FormState = typeof EMPTY
type Action = { id: number; kind: 'test' | 'backup' | 'delete' }

export default function Targets({ user, onJob, onExplore }: { user: User; onJob: (id: number) => void; onExplore: (id: number) => void }) {
  const canWrite = user.role !== 'viewer'
  const [list, setList] = useState<Target[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [formErr, setFormErr] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [action, setAction] = useState<Action | null>(null)
  const [editing, setEditing] = useState<Target | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<FormState>(EMPTY)

  async function load() {
    setLoading(true)
    try {
      setList(await api.targets())
      setErr('')
    } catch (e) {
      setErr(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  function openCreate() {
    setEditing(null)
    setForm(EMPTY)
    setFormErr('')
    setShowForm(true)
  }

  function openEdit(t: Target) {
    setEditing(t)
    setForm({
      engine: t.engine, name: t.name, host: t.host, port: t.port, dbname: t.dbname,
      username: t.username, password: '', ssl_mode: t.ssl_mode,
      tags: t.tags?.join(', ') ?? '', is_enabled: t.is_enabled,
    })
    setFormErr('')
    setShowForm(true)
  }

  async function save(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setFormErr('')
    setNotice('')
    try {
      const body = {
        engine: form.engine, name: form.name.trim(), host: form.host.trim(), port: Number(form.port),
        dbname: form.dbname.trim(), username: form.username.trim(), password: form.password,
        ssl_mode: form.ssl_mode,
        tags: form.tags.split(',').map((s) => s.trim()).filter(Boolean),
        is_enabled: form.is_enabled,
      }
      if (editing) {
        await api.updateTarget(editing.id, body)
        setNotice('已保存，连接测试通过')
      } else {
        const created = await api.createTarget(body)
        setNotice(`已添加 ${created.name}（${created.engine} ${created.server_version}）`)
      }
      setShowForm(false)
      await load()
    } catch (e) {
      setFormErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function runAction(t: Target, kind: Action['kind']) {
    if (action) return
    if (kind === 'delete' && !window.confirm(`删除 ${t.name}？该库的策略与历史任务记录会一并删除。`)) return
    setAction({ id: t.id, kind })
    setErr('')
    setNotice('')
    try {
      if (kind === 'test') {
        const r = await api.testTarget(t.id)
        setNotice(`${t.name}：${t.engine} ${r.version}，${r.table_count} 张表，${formatBytes(r.db_size_bytes)}${r.unreadable_tables ? `；注意：${r.unreadable_tables} 张表无读取权限` : ''}`)
        await load()
      } else if (kind === 'backup') {
        const job = await api.backupNow(t.id)
        onJob(job.id)
      } else {
        await api.deleteTarget(t.id)
        setNotice(`已删除 ${t.name}`)
        await load()
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : '操作失败')
    } finally {
      setAction(null)
    }
  }

  return (
    <>
      <div className="topbar">
        <div>
          <h1 className="page-title">目标库</h1>
          <div className="page-sub">添加即做连通性校验，避免凌晨才发现连不上</div>
        </div>
        <div className="topbar-actions">
          <button className="btn" disabled={loading || !!action} onClick={() => void load()}>
            {loading ? '加载中…' : '刷新'}
          </button>
          {canWrite && <button className="btn btn-primary" disabled={loading || !!action} onClick={openCreate}>添加目标库</button>}
        </div>
      </div>

      {err && <div className="error-bar" role="alert">{err}</div>}
      {notice && <div className="ok-bar" role="status">{notice}</div>}

      <div className="card" aria-busy={loading}>
        <div className="table-wrap">
          {loading && list.length === 0 ? <div className="empty">正在加载目标库…</div> : list.length === 0 ? (
            <div className="empty">
              {err ? '目标库加载失败，请重试。' : <>还没有目标库。{canWrite ? '点击右上角“添加目标库”开始。' : ''}</>}
            </div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>名称</th><th>连接</th><th>版本</th><th className="num">数据量</th>
                  <th>状态</th><th>最近探测</th><th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {list.map((t) => (
                  <tr key={t.id}>
                    <td>
                      <div style={{ fontWeight: 500 }}>{t.name}</div>
                      <div className="faint small">{t.engine} · {t.tags?.join(' · ')}</div>
                    </td>
                    <td className="mono small">
                      {t.host}:{t.port}/{t.dbname}<div className="faint">{t.username}</div>
                    </td>
                    <td className="small">{t.server_version || '—'}</td>
                    <td className="num mono">{formatBytes(t.db_size_bytes)}</td>
                    <td>
                      {t.is_enabled ? <span className="badge badge-ok"><span className="badge-dot" />启用</span>
                        : <span className="badge badge-mute">已停用</span>}
                    </td>
                    <td className="small muted">{formatTime(t.last_probe_at)}</td>
                    <td>
                      {canWrite && <div className="cell-actions">
                        <button className="btn btn-sm" disabled={!t.is_enabled || !!action} onClick={() => onExplore(t.id)}>浏览</button>
                        <button className="btn btn-sm" disabled={!!action || loading} onClick={() => void runAction(t, 'test')}>
                          {action?.id === t.id && action.kind === 'test' ? '测试中…' : '测试'}
                        </button>
                        <button className="btn btn-sm" disabled={!!action || loading || !t.is_enabled} title={!t.is_enabled ? '请先启用目标库' : undefined} onClick={() => void runAction(t, 'backup')}>
                          {action?.id === t.id && action.kind === 'backup' ? '创建中…' : '立即备份'}
                        </button>
                        <button className="btn btn-sm" disabled={!!action || loading} onClick={() => openEdit(t)}>编辑</button>
                        <button className="btn btn-sm btn-danger" disabled={!!action || loading} onClick={() => void runAction(t, 'delete')}>
                          {action?.id === t.id && action.kind === 'delete' ? '删除中…' : '删除'}
                        </button>
                      </div>}
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
          title={editing ? `编辑 ${editing.name}` : '添加目标库'} busy={busy}
          onClose={() => setShowForm(false)}
          footer={<>
            <button className="btn" disabled={busy} onClick={() => setShowForm(false)}>取消</button>
            <button className="btn btn-primary" type="submit" form="target-form" disabled={busy}>
              {busy ? '连接测试中…' : '连接测试并保存'}
            </button>
          </>}
        >
          {formErr && <div className="error-bar" role="alert">{formErr}</div>}
          <form id="target-form" onSubmit={save}>
            <fieldset className="form-fields" disabled={busy}>
              <div className="field">
                <label className="label" htmlFor="target-engine">数据库类型</label>
                <select id="target-engine" className="select" value={form.engine} disabled={!!editing}
                  onChange={(e) => setForm({ ...form, engine: e.target.value, port: e.target.value === 'postgres' ? 5432 : 3306, ssl_mode: e.target.value === 'postgres' ? 'prefer' : 'verify-full' })}>
                  <option value="postgres">PostgreSQL</option><option value="mysql">MySQL</option><option value="mariadb">MariaDB</option>
                </select>
              </div>
              <div className="field-row">
                <div className="field">
                  <label className="label" htmlFor="target-name">名称</label>
                  <input id="target-name" className="input" value={form.name} required
                    onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="订单库-生产" />
                </div>
                <div className="field">
                  <label className="label" htmlFor="target-tags">环境标签（逗号分隔）</label>
                  <input id="target-tags" className="input" value={form.tags}
                    onChange={(e) => setForm({ ...form, tags: e.target.value })} placeholder="prod, core" />
                </div>
              </div>
              <div className="field-row">
                <div className="field">
                  <label className="label" htmlFor="target-host">主机</label>
                  <input id="target-host" className="input" value={form.host} required
                    onChange={(e) => setForm({ ...form, host: e.target.value })} placeholder="127.0.0.1" />
                </div>
                <div className="field">
                  <label className="label" htmlFor="target-port">端口</label>
                  <input id="target-port" className="input" type="number" min={1} max={65535} required value={form.port}
                    onChange={(e) => setForm({ ...form, port: Number(e.target.value) })} />
                </div>
              </div>
              <div className="field">
                <label className="label" htmlFor="target-dbname">数据库名</label>
                <input id="target-dbname" className="input" value={form.dbname} required
                  onChange={(e) => setForm({ ...form, dbname: e.target.value })} placeholder="appdb" />
              </div>
              <div className="field-row">
                <div className="field">
                  <label className="label" htmlFor="target-username">用户名</label>
                  <input id="target-username" className="input" value={form.username} autoComplete="off" required
                    onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder="backup_ro" />
                </div>
                <div className="field">
                  <label className="label" htmlFor="target-password">密码 {editing && <span className="faint">（留空不修改）</span>}</label>
                  <input id="target-password" className="input" type="password" value={form.password} autoComplete="new-password" required={!editing}
                    onChange={(e) => setForm({ ...form, password: e.target.value })} />
                </div>
              </div>
              <div className="field-row">
                <div className="field">
                  <label className="label" htmlFor="target-ssl">SSL 模式</label>
                  <select id="target-ssl" className="select" value={form.ssl_mode}
                    onChange={(e) => setForm({ ...form, ssl_mode: e.target.value })}>
                    {(form.engine === 'postgres' ? ['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full'] : ['disable', 'verify-full']).map((m) => <option key={m} value={m}>{m}</option>)}
                  </select>
                </div>
                <div className="field" style={{ alignContent: 'end' }}>
                  <label className="checkbox">
                    <input type="checkbox" checked={form.is_enabled}
                      onChange={(e) => setForm({ ...form, is_enabled: e.target.checked })} />启用目标库
                  </label>
                  <div className="hint">停用后不能执行手动或定时备份</div>
                </div>
              </div>
              <div className="hint">保存前会真实连接一次。{form.engine === 'postgres' ? '建议使用只读账号（PG 14+ 可授予 pg_read_all_data）。' : 'SQL 备份要求 InnoDB 表；账号需本库 SELECT、SHOW VIEW、TRIGGER、EVENT 和读取例程定义的权限，不需要全局写入权限。'} 密码以 AES-256-GCM 加密存储。请勿使用超级管理员账号。</div>
            </fieldset>
          </form>
        </Modal>
      )}
    </>
  )
}
