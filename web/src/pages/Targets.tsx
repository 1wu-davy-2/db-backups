import { useEffect, useState } from 'react'
import { api } from '../api'
import { Modal } from '../components/Overlay'
import type { Target, User } from '../types'
import { formatBytes, formatTime } from '../utils'

const EMPTY = {
  name: '', host: '', port: 5432, dbname: '', username: '',
  password: '', ssl_mode: 'prefer', tags: '', is_enabled: true,
}

type FormState = typeof EMPTY

export default function Targets({ user }: { user: User }) {
  const canWrite = user.role !== 'viewer'
  const [list, setList] = useState<Target[]>([])
  const [err, setErr] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState<Target | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<FormState>(EMPTY)

  async function load() {
    try {
      setList(await api.targets())
      setErr('')
    } catch (e) {
      setErr(e instanceof Error ? e.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  function openCreate() {
    setEditing(null)
    setForm(EMPTY)
    setShowForm(true)
  }

  function openEdit(t: Target) {
    setEditing(t)
    setForm({
      name: t.name, host: t.host, port: t.port, dbname: t.dbname,
      username: t.username, password: '', ssl_mode: t.ssl_mode,
      tags: t.tags?.join(', ') ?? '', is_enabled: t.is_enabled,
    })
    setShowForm(true)
  }

  async function save() {
    setBusy(true)
    setErr('')
    try {
      const body = {
        name: form.name,
        host: form.host,
        port: Number(form.port),
        dbname: form.dbname,
        username: form.username,
        password: form.password,
        ssl_mode: form.ssl_mode,
        tags: form.tags.split(',').map((s) => s.trim()).filter(Boolean),
        is_enabled: form.is_enabled,
      }
      if (editing) {
        await api.updateTarget(editing.id, body)
        setNotice('已保存，连接测试通过')
      } else {
        const created = await api.createTarget(body)
        setNotice(`已添加 ${created.name}（PostgreSQL ${created.server_version}）`)
      }
      setShowForm(false)
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  async function test(t: Target) {
    setErr('')
    setNotice('')
    try {
      const r = await api.testTarget(t.id)
      setNotice(`${t.name}：PostgreSQL ${r.version}，${r.table_count} 张表，${formatBytes(r.db_size_bytes)}`)
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '连接失败')
    }
  }

  async function backup(t: Target) {
    setErr('')
    try {
      const job = await api.backupNow(t.id)
      setNotice(`已创建任务 #${job.id}，可在"备份任务"查看进度`)
    } catch (e) {
      setErr(e instanceof Error ? e.message : '触发失败')
    }
  }

  async function remove(t: Target) {
    if (!window.confirm(`删除 ${t.name}？该库的策略与历史任务记录会一并删除。`)) return
    try {
      await api.deleteTarget(t.id)
      setNotice('已删除')
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '删除失败')
    }
  }

  return (
    <>
      <div className="topbar">
        <div>
          <h1 className="page-title">目标库</h1>
          <div className="page-sub">添加即做连通性校验，避免凌晨才发现连不上</div>
        </div>
        {canWrite && (
          <div className="topbar-actions">
            <button className="btn btn-primary" onClick={openCreate}>添加目标库</button>
          </div>
        )}
      </div>

      {err && <div className="error-bar">{err}</div>}
      {notice && <div className="ok-bar">{notice}</div>}

      <div className="card">
        <div className="table-wrap">
          {list.length === 0 ? (
            <div className="empty">
              还没有目标库。{canWrite ? '点击右上角"添加目标库"开始。' : ''}
            </div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>连接</th>
                  <th>版本</th>
                  <th className="num">数据量</th>
                  <th>状态</th>
                  <th>最近探测</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {list.map((t) => (
                  <tr key={t.id}>
                    <td>
                      <div style={{ fontWeight: 500 }}>{t.name}</div>
                      <div className="faint small">{t.tags?.join(' · ')}</div>
                    </td>
                    <td className="mono small">
                      {t.host}:{t.port}/{t.dbname}
                      <div className="faint">{t.username}</div>
                    </td>
                    <td className="small">{t.server_version || '—'}</td>
                    <td className="num mono">{formatBytes(t.db_size_bytes)}</td>
                    <td>
                      {t.is_enabled ? (
                        <span className="badge badge-ok"><span className="badge-dot" />启用</span>
                      ) : (
                        <span className="badge badge-mute">已停用</span>
                      )}
                    </td>
                    <td className="small muted">{formatTime(t.last_probe_at)}</td>
                    <td>
                      <div className="cell-actions">
                        {canWrite && (
                          <>
                            <button className="btn btn-sm" onClick={() => test(t)}>测试</button>
                            <button className="btn btn-sm" onClick={() => backup(t)}>立即备份</button>
                            <button className="btn btn-sm" onClick={() => openEdit(t)}>编辑</button>
                            <button className="btn btn-sm btn-danger" onClick={() => remove(t)}>删除</button>
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
          title={editing ? `编辑 ${editing.name}` : '添加目标库'}
          onClose={() => setShowForm(false)}
          footer={
            <>
              <button className="btn" onClick={() => setShowForm(false)}>取消</button>
              <button className="btn btn-primary" onClick={save} disabled={busy}>
                {busy ? '连接测试中…' : '连接测试并保存'}
              </button>
            </>
          }
        >
          <div className="field-row">
            <div className="field">
              <label className="label">名称</label>
              <input className="input" value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="订单库-生产" />
            </div>
            <div className="field">
              <label className="label">环境标签（逗号分隔）</label>
              <input className="input" value={form.tags}
                onChange={(e) => setForm({ ...form, tags: e.target.value })} placeholder="prod, core" />
            </div>
          </div>
          <div className="field-row">
            <div className="field">
              <label className="label">主机</label>
              <input className="input" value={form.host}
                onChange={(e) => setForm({ ...form, host: e.target.value })} placeholder="127.0.0.1" />
            </div>
            <div className="field">
              <label className="label">端口</label>
              <input className="input" type="number" value={form.port}
                onChange={(e) => setForm({ ...form, port: Number(e.target.value) })} />
            </div>
          </div>
          <div className="field">
            <label className="label">数据库名</label>
            <input className="input" value={form.dbname}
              onChange={(e) => setForm({ ...form, dbname: e.target.value })} placeholder="appdb" />
          </div>
          <div className="field-row">
            <div className="field">
              <label className="label">用户名</label>
              <input className="input" value={form.username} autoComplete="off"
                onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder="backup_ro" />
            </div>
            <div className="field">
              <label className="label">
                密码 {editing && <span className="faint">（留空不修改）</span>}
              </label>
              <input className="input" type="password" value={form.password} autoComplete="new-password"
                onChange={(e) => setForm({ ...form, password: e.target.value })} />
            </div>
          </div>
          <div className="field-row">
            <div className="field">
              <label className="label">SSL 模式</label>
              <select className="select" value={form.ssl_mode}
                onChange={(e) => setForm({ ...form, ssl_mode: e.target.value })}>
                {['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full'].map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
              </select>
            </div>
            <div className="field" style={{ alignContent: 'end' }}>
              <label className="checkbox">
                <input type="checkbox" checked={form.is_enabled}
                  onChange={(e) => setForm({ ...form, is_enabled: e.target.checked })} />
                启用定时备份
              </label>
            </div>
          </div>
          <div className="hint">
            保存前会真实连接一次。建议使用只读账号（PG 14+ 可授予 pg_read_all_data），
            密码以 AES-256-GCM 加密存储，不会出现在日志里。
          </div>
        </Modal>
      )}
    </>
  )
}
