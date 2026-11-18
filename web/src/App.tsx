import { useEffect, useState } from 'react'
import { api } from './api'
import type { User, View } from './types'
import Layout from './components/Layout'
import Audit from './pages/Audit'
import Dashboard from './pages/Dashboard'
import Jobs from './pages/Jobs'
import Login from './pages/Login'
import Policies from './pages/Policies'
import Targets from './pages/Targets'

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [needsSetup, setNeedsSetup] = useState(false)
  const [loading, setLoading] = useState(true)
  const [view, setView] = useState<View>('dashboard')
  const [jobID, setJobID] = useState<number | null>(null)

  function navigate(next: View) {
    setJobID(null)
    setView(next)
  }

  function openJob(id: number) {
    setJobID(id)
    setView('jobs')
  }

  useEffect(() => {
    let alive = true
    ;(async () => {
      try {
        const b = await api.bootstrap()
        if (!alive) return
        setNeedsSetup(b.needs_setup)
        if (!b.needs_setup) {
          try {
            const me = await api.me()
            if (alive) setUser(me)
          } catch {
            /* 未登录，停留在登录页 */
          }
        }
      } catch {
        /* 后端不可用时也展示登录页 */
      } finally {
        if (alive) setLoading(false)
      }
    })()
    return () => {
      alive = false
    }
  }, [])

  if (loading) return <div className="center">正在加载…</div>

  if (!user) {
    return (
      <Login
        needsSetup={needsSetup}
        onDone={(u) => {
          setUser(u)
          setNeedsSetup(false)
        }}
      />
    )
  }

  return (
    <Layout
      user={user}
      view={view}
      onView={navigate}
      onLogout={async () => {
        try {
          await api.logout()
        } finally {
          setUser(null)
        }
      }}
    >
      {view === 'dashboard' && <Dashboard onView={navigate} onJob={openJob} />}
      {view === 'targets' && <Targets user={user} onJob={openJob} />}
      {view === 'policies' && <Policies user={user} />}
      {view === 'jobs' && <Jobs user={user} initialJobID={jobID} />}
      {view === 'audit' && <Audit />}
    </Layout>
  )
}
