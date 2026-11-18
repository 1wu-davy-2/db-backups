export type Role = 'admin' | 'operator' | 'viewer'

export type View = 'dashboard' | 'targets' | 'policies' | 'jobs' | 'audit' | 'explorer'

export interface User {
  id: number
  email: string
  role: Role
  created_at: string
}

export interface Target {
  id: number
  name: string
  engine: string
  host: string
  port: number
  dbname: string
  username: string
  ssl_mode: string
  server_version: string
  server_version_num: number
  db_size_bytes: number
  tags: string[]
  is_enabled: boolean
  last_probe_at: string
  created_at: string
}

export interface Policy {
  id: number
  target_id: number
  target_name: string
  cron_expr: string
  timezone: string
  kind: string
  dump_format: string
  compression: string
  encrypt: boolean
  retain_count: number
  retain_days: number
  retain_max_bytes: number
  keep_min_count: number
  verify_mode: string
  timeout_seconds: number
  misfire_policy: string
  is_enabled: boolean
  next_run_at: string
  last_fired_at: string
  created_at: string
}

export type JobStatus = 'pending' | 'running' | 'success' | 'failed' | 'canceled'

export interface Job {
  id: number
  policy_id: number | null
  target_id: number
  target_name: string
  trigger: string
  status: JobStatus
  started_at: string | null
  finished_at: string | null
  size_bytes: number
  dump_sha256: string
  tool_version: string
  server_version: string
  error_code: string
  error_message: string
  duration_ms: number
  created_at: string
}

export interface Artifact {
  id: number
  job_id: number
  object_key: string
  size_bytes: number
  sha256: string
  encrypted: boolean
  is_deleted: boolean
  deleted_at: string | null
  created_at: string
}

export interface Stats {
  targets: number
  policies: number
  jobs_24h: number
  failed_24h: number
  live_bytes: number
  last_success_at: number
  running_jobs: number
}

export interface DailyCount {
  day: string
  total: number
  success: number
}

export interface AuditEntry {
  id: number
  actor: string
  action: string
  object_ref: string
  detail: string
  ip: string
  created_at: string
}

export interface ToolInfo {
  major: number
  version: string
  path: string
}

export interface SystemInfo {
  version: string
  toolchain: ToolInfo[]
  mysql_toolchain?: (ToolInfo & { engine: string; minor: number })[]
  storage: string
  data_dir: string
  encryption: string
}

export interface DBTable { schema: string; name: string; kind: string }
export interface DBColumn { name: string; type: string; nullable: boolean; default: string; primary: boolean }
export interface DBStructure { columns: DBColumn[]; indexes: { name: string; definition: string }[] }
export interface QueryResult { columns: string[]; rows: unknown[][]; truncated: boolean; duration_ms: number }

export interface Page<T> {
  items: T[]
  total: number
}
