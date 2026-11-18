import type { ReactNode } from 'react'
import type { User, View } from '../types'

const NAV: { key: View; label: string }[] = [
  { key: 'dashboard', label: '总览' },
  { key: 'targets', label: '目标库' },
  { key: 'policies', label: '备份策略' },
  { key: 'jobs', label: '备份任务' },
  { key: 'explorer', label: '数据浏览' },
  { key: 'audit', label: '审计日志' },
]

interface Props {
  user: User
  view: View
  onView: (v: View) => void
  onLogout: () => void
  children: ReactNode
}

export default function Layout({ user, view, onView, onLogout, children }: Props) {
  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">DB</div>
          <div>
            <div className="brand-name">DBBackups</div>
            <div className="brand-sub">多引擎备份平台</div>
          </div>
        </div>

        {NAV.map((item) => (
          <button
            key={item.key}
            className={`nav-item${view === item.key ? ' active' : ''}`}
            onClick={() => onView(item.key)}
          >
            <span className="nav-dot" />
            {item.label}
          </button>
        ))}

        <div className="sidebar-foot">
          <div className="sidebar-user">
            {user.email}
            <div className="faint small">{roleLabel(user.role)}</div>
          </div>
          <button className="btn btn-sm" onClick={onLogout}>
            退出登录
          </button>
        </div>
      </aside>
      <main className="main">{children}</main>
    </div>
  )
}

function roleLabel(role: string): string {
  if (role === 'admin') return '管理员'
  if (role === 'operator') return '操作员'
  return '只读'
}
