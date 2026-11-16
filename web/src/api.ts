import type {
  Artifact, AuditEntry, DailyCount, Job, Page, Policy,
  Stats, SystemInfo, Target, User,
} from './types'

const BASE = '/api'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    credentials: 'same-origin',
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
    ...init,
  })
  if (!res.ok) {
    let msg = `请求失败 (${res.status})`
    try {
      const body = await res.json()
      if (body?.error) msg = body.error
    } catch {
      /* 非 JSON 响应，保留默认文案 */
    }
    throw new Error(msg)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

function qs(params: Record<string, string | number | undefined>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '' && v !== 0) p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

export const api = {
  bootstrap: () => req<{ needs_setup: boolean; version: string }>('/system/bootstrap'),
  setup: (email: string, password: string) =>
    req<User>('/auth/setup', { method: 'POST', body: JSON.stringify({ email, password }) }),
  login: (email: string, password: string) =>
    req<User>('/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  logout: () => req<{ ok: boolean }>('/auth/logout', { method: 'POST' }),
  me: () => req<User>('/auth/me'),

  stats: () => req<Stats>('/stats'),
  daily: (days = 7) => req<DailyCount[]>(`/stats/daily${qs({ days })}`),
  systemInfo: () => req<SystemInfo>('/system/info'),
  audit: (limit = 100) => req<AuditEntry[]>(`/audit${qs({ limit })}`),

  targets: () => req<Target[]>('/targets'),
  target: (id: number) => req<Target>(`/targets/${id}`),
  createTarget: (body: Record<string, unknown>) =>
    req<Target>('/targets', { method: 'POST', body: JSON.stringify(body) }),
  updateTarget: (id: number, body: Record<string, unknown>) =>
    req<Target>(`/targets/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteTarget: (id: number) => req<{ ok: boolean }>(`/targets/${id}`, { method: 'DELETE' }),
  testTarget: (id: number) =>
    req<{ version: string; table_count: number; unreadable_tables: number; db_size_bytes: number }>(
      `/targets/${id}/test`, { method: 'POST' },
    ),
  backupNow: (id: number) => req<Job>(`/targets/${id}/backup`, { method: 'POST' }),

  policies: () => req<Policy[]>('/policies'),
  createPolicy: (body: Record<string, unknown>) =>
    req<Policy>('/policies', { method: 'POST', body: JSON.stringify(body) }),
  updatePolicy: (id: number, body: Record<string, unknown>) =>
    req<Policy>(`/policies/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  deletePolicy: (id: number) => req<{ ok: boolean }>(`/policies/${id}`, { method: 'DELETE' }),

  jobs: (params: { target_id?: number; status?: string; limit?: number; offset?: number }) =>
    req<Page<Job>>(`/jobs${qs(params)}`),
  job: (id: number) => req<Job>(`/jobs/${id}`),
  jobLogs: (id: number) => req<{ lines: string[] }>(`/jobs/${id}/logs`),
  jobArtifacts: (id: number) => req<Artifact[]>(`/jobs/${id}/artifacts`),
  cancelJob: (id: number) => req<{ ok: boolean }>(`/jobs/${id}/cancel`, { method: 'POST' }),
  artifactURL: (id: number) => `${BASE}/artifacts/${id}/download`,
}
