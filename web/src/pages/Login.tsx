import { useState } from 'react'
import type { FormEvent } from 'react'
import { api } from '../api'
import type { User } from '../types'

interface Props {
  needsSetup: boolean
  onDone: (u: User) => void
}

export default function Login({ needsSetup, onDone }: Props) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (needsSetup && password !== confirm) {
      setErr('两次输入的密码不一致')
      return
    }
    setBusy(true)
    setErr('')
    try {
      const u = needsSetup ? await api.setup(email, password) : await api.login(email, password)
      onDone(u)
    } catch (e) {
      setErr(e instanceof Error ? e.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="auth-wrap">
      <div className="auth-card">
        <div className="auth-mark">DB</div>
        <h1 className="auth-title">{needsSetup ? '初始化管理员' : '登录 DBBackups'}</h1>
        <p className="auth-sub">
          {needsSetup
            ? '首次使用，请创建管理员账号。该账号拥有全部权限。'
            : '数据库备份调度与恢复管理'}
        </p>

        {err && <div className="error-bar">{err}</div>}

        <form onSubmit={submit}>
          <div className="field">
            <label className="label" htmlFor="email">邮箱</label>
            <input
              id="email"
              className="input"
              type="email"
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="admin@example.com"
              required
            />
          </div>
          <div className="field">
            <label className="label" htmlFor="password">密码</label>
            <input
              id="password"
              className="input"
              type="password"
              autoComplete={needsSetup ? 'new-password' : 'current-password'}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={needsSetup ? '至少 8 位' : ''}
              required
            />
          </div>
          {needsSetup && (
            <div className="field">
              <label className="label" htmlFor="confirm">确认密码</label>
              <input
                id="confirm"
                className="input"
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
              />
            </div>
          )}
          <button className="btn btn-primary" style={{ width: '100%' }} disabled={busy}>
            {busy ? '处理中…' : needsSetup ? '创建并登录' : '登录'}
          </button>
        </form>
      </div>
    </div>
  )
}
