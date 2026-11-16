import { useEffect, useState } from 'react'
import { api } from '../api'
import type { AuditEntry } from '../types'
import { formatTime } from '../utils'

const ACTION_LABEL: Record<string, string> = {
  'auth.login': '登录',
  'auth.setup': '初始化管理员',
  'target.create': '新增目标库',
  'target.update': '修改目标库',
  'target.delete': '删除目标库',
  'policy.create': '新建策略',
  'policy.update': '修改策略',
  'policy.delete': '删除策略',
  'job.trigger': '手动触发备份',
  'job.cancel': '取消任务',
}

export default function Audit() {
  const [list, setList] = useState<AuditEntry[]>([])
  const [err, setErr] = useState('')

  useEffect(() => {
    api.audit(200)
      .then(setList)
      .catch((e) => setErr(e instanceof Error ? e.message : '加载失败'))
  }, [])

  return (
    <>
      <div className="topbar">
        <div>
          <h1 className="page-title">审计日志</h1>
          <div className="page-sub">所有配置变更与恢复操作留痕，最近 200 条</div>
        </div>
      </div>

      {err && <div className="error-bar">{err}</div>}

      <div className="card">
        <div className="table-wrap">
          {list.length === 0 ? (
            <div className="empty">暂无记录</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th style={{ width: 160 }}>时间</th>
                  <th>操作人</th>
                  <th>动作</th>
                  <th>对象</th>
                  <th>来源 IP</th>
                </tr>
              </thead>
              <tbody>
                {list.map((a) => (
                  <tr key={a.id}>
                    <td className="small muted">{formatTime(a.created_at)}</td>
                    <td className="small">{a.actor}</td>
                    <td><span className="badge badge-mute">{ACTION_LABEL[a.action] ?? a.action}</span></td>
                    <td className="small mono">{a.object_ref}</td>
                    <td className="small faint">{a.ip}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </>
  )
}
