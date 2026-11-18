import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { DBStructure, DBTable, QueryResult, Target, User } from '../types'

const EMPTY_FILTER = { column: '', operator: 'eq', value: '' }

export default function Explorer({ user, initialTargetID }: { user: User; initialTargetID: number | null }) {
  const [targets, setTargets] = useState<Target[]>([])
  const [targetID, setTargetID] = useState(initialTargetID ?? 0)
  const [tables, setTables] = useState<DBTable[]>([])
  const [loaded, setLoaded] = useState(false)
  const [search, setSearch] = useState('')
  const [table, setTable] = useState<DBTable | null>(null)
  const [structure, setStructure] = useState<DBStructure | null>(null)
  const [tab, setTab] = useState('data')
  const [sql, setSQL] = useState('SELECT 1 AS example;')
  const [filter, setFilter] = useState(EMPTY_FILTER)
  const [applied, setApplied] = useState(EMPTY_FILTER)
  const [offset, setOffset] = useState(0)
  const [pageHistory, setPageHistory] = useState<number[]>([])
  const [data, setData] = useState<QueryResult | null>(null)
  const [result, setResult] = useState<QueryResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [canceling, setCanceling] = useState(false)
  const [err, setErr] = useState('')
  const [notice, setNotice] = useState('')
  const active = useRef<{ id: string; target: number; controller: AbortController } | null>(null)
  const mounted = useRef(false)

  useEffect(() => {
    mounted.current = true
    let alive = true
    if (user.role !== 'viewer') api.targets().then((items) => {
      if (!alive) return
      setTargets(items)
      setTargetID((current) => current || items.find((t) => t.is_enabled)?.id || 0)
    }).catch((e) => { if (alive) setErr(e.message) })
    return () => { alive = false; mounted.current = false; active.current?.controller.abort() }
  }, [user.role])

  const target = targets.find((t) => t.id === targetID)
  const visible = tables.filter((t) => `${t.schema}.${t.name}`.toLowerCase().includes(search.toLowerCase()))

  async function request<T>(body: Record<string, unknown>): Promise<T> {
    const current = { id: crypto.randomUUID(), target: targetID, controller: new AbortController() }
    active.current = current
    try {
      const response = await api.explore<T>(targetID, { ...body, request_id: current.id }, current.controller.signal)
      if (!mounted.current || current.controller.signal.aborted) throw new DOMException('已取消', 'AbortError')
      return response
    } finally { if (active.current === current) active.current = null }
  }

  async function operate(action: () => Promise<void>) {
    if (busy) return
    setBusy(true); setErr(''); setNotice('')
    try { await action() } catch (e) {
      if (mounted.current) {
        if (e instanceof Error && e.name === 'AbortError') setNotice('查询已取消')
        else setErr(e instanceof Error ? e.message : '查询失败')
      }
    } finally { if (mounted.current) { setBusy(false); setCanceling(false) } }
  }

  async function cancel() {
    const current = active.current
    if (!current) return
    setCanceling(true)
    try {
      await api.cancelExplore(current.target, current.id)
      if (active.current === current) current.controller.abort()
    } catch (e) {
      if (active.current === current) setErr(e instanceof Error ? e.message : '取消失败')
    } finally { if (mounted.current) setCanceling(false) }
  }

  function loadTables() {
    void operate(async () => {
      const items = await request<DBTable[]>({ action: 'tables' })
      setTables(items); setLoaded(true); setTable(null); setStructure(null); setData(null)
    })
  }

  function selectTable(selected: DBTable) {
    void operate(async () => {
      setTable(selected); setTab('data'); setStructure(null); setData(null); setFilter(EMPTY_FILTER); setApplied(EMPTY_FILTER); setOffset(0); setPageHistory([])
      const description = await request<DBStructure>({ action: 'structure', schema: selected.schema, table: selected.name })
      setStructure(description)
      const q = (name: string) => target?.engine === 'postgres' ? `"${name.replaceAll('"', '""')}"` : '`' + name.replaceAll('`', '``') + '`'
      setSQL(`SELECT * FROM ${q(selected.schema)}.${q(selected.name)} LIMIT 100;`)
      setData(await request<QueryResult>({ action: 'data', schema: selected.schema, table: selected.name, limit: 50, offset: 0 }))
    })
  }

  function loadData(nextOffset: number, nextFilter = applied, history: number[] = []) {
    if (!table) return
    void operate(async () => {
      const response = await request<QueryResult>({ action: 'data', schema: table.schema, table: table.name, ...nextFilter, limit: 50, offset: nextOffset })
      setData(response); setOffset(nextOffset); setApplied(nextFilter); setPageHistory(history)
    })
  }

  function executeSQL() {
    void operate(async () => { setResult(null); setResult(await request<QueryResult>({ action: 'query', sql })) })
  }

  if (user.role === 'viewer') return <div className="empty">数据浏览仅对管理员和操作员开放</div>

  return <>
    <div className="topbar"><div><h1 className="page-title">数据浏览</h1><div className="page-sub">检查表结构、筛选数据、执行只读查询；不会改变整库备份范围</div></div><span className="badge badge-info">只读工作区</span></div>
    <div className="card explorer-connection">
      <label className="label" htmlFor="explorer-target">目标库</label>
      <select id="explorer-target" className="select" disabled={busy} value={targetID} onChange={(e) => {
        setTargetID(Number(e.target.value)); setTables([]); setLoaded(false); setTable(null); setStructure(null); setData(null); setResult(null); setSQL('SELECT 1 AS example;'); setSearch(''); setErr(''); setNotice('')
      }}><option value={0}>请选择目标库</option>{targets.map((t) => <option key={t.id} value={t.id} disabled={!t.is_enabled}>{t.name} · {t.engine}{!t.is_enabled ? '（停用）' : ''}</option>)}</select>
      <button className="btn btn-primary" disabled={busy || !target?.is_enabled} onClick={loadTables}>{loaded ? '刷新表列表' : '连接并加载表'}</button>
      {busy && <button className="btn btn-danger" disabled={canceling} onClick={() => void cancel()}>{canceling ? '取消中…' : '取消查询'}</button>}
      <span className="small muted">{busy ? '正在读取源库…' : '单次 10 秒 · 最多 500 行 / 2 MiB'}</span>
    </div>
    {err && <div className="error-bar" role="alert">{err}</div>}
    {notice && <div className="ok-bar" role="status">{notice}</div>}
    <div className="explorer-grid">
      <aside className="card explorer-tables" aria-label="源库表列表">
        <div className="card-head"><span className="card-title">表与视图</span><span className="small faint">{visible.length} / {tables.length}</span></div>
        <div className="explorer-search"><input className="input" aria-label="筛选表名" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="筛选表名或 schema" /></div>
        <div className="explorer-table-list">
          {visible.map((t) => <button key={`${t.schema}.${t.name}`} disabled={busy} className={`explorer-table${table?.schema === t.schema && table.name === t.name ? ' selected' : ''}`} onClick={() => selectTable(t)}><strong>{t.name}</strong><span>{t.schema} · {t.kind === 'VIEW' ? '视图' : '表'}</span></button>)}
          {!visible.length && <div className="empty">{loaded ? '没有匹配的表' : '连接后显示可访问的表'}</div>}
        </div>
        <div className="hint explorer-search">最多列出 2000 张表</div>
      </aside>
      <section className="card explorer-workspace" aria-busy={busy}>
        <div className="card-head"><span className="card-title mono">{table ? `${table.schema}.${table.name}` : '查询工作区'}</span></div>
        <div className="explorer-tabs">{[['data', '表数据'], ['structure', '结构与索引'], ['sql', 'SQL 查询']].map(([key, label]) => <button className={`btn btn-sm${tab === key ? ' btn-primary' : ''}`} aria-pressed={tab === key} key={key} onClick={() => setTab(key)}>{label}</button>)}</div>
        {tab === 'data' && (table && structure ? <>
          <form className="explorer-filter" onSubmit={(e) => { e.preventDefault(); loadData(0, filter) }}>
            <select className="select" aria-label="筛选列" disabled={busy} value={filter.column} onChange={(e) => setFilter({ ...filter, column: e.target.value })}><option value="">全部数据</option>{structure.columns.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}</select>
            <select className="select" aria-label="筛选条件" disabled={busy || !filter.column} value={filter.operator} onChange={(e) => setFilter({ ...filter, operator: e.target.value })}>{[['eq', '='], ['ne', '≠'], ['gt', '>'], ['gte', '≥'], ['lt', '<'], ['lte', '≤'], ['like', 'LIKE'], ['null', 'IS NULL'], ['notnull', 'IS NOT NULL']].map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
            <input className="input" aria-label="筛选值" disabled={busy || !filter.column || ['null', 'notnull'].includes(filter.operator)} value={filter.value} onChange={(e) => setFilter({ ...filter, value: e.target.value })} placeholder="LIKE 可使用 % 通配符" />
            <button className="btn" disabled={busy} type="submit">应用筛选</button>
          </form>
          {!structure.columns.some((c) => c.primary) && <div className="hint explorer-search">该表无可见主键，分页顺序可能随数据变化。</div>}
          <Results result={data} />
          {data && <div className="pagination"><span className="small muted">偏移 {offset} · 本页 {data.rows.length} 行</span><div className="row"><button className="btn btn-sm" disabled={busy || offset === 0} onClick={() => loadData(pageHistory.at(-1) ?? 0, applied, pageHistory.slice(0, -1))}>上一页</button><button className="btn btn-sm" disabled={busy || !data.truncated || offset + data.rows.length > 1000000} onClick={() => loadData(offset + data.rows.length, applied, [...pageHistory, offset])}>下一页</button></div></div>}
        </> : <div className="empty">从左侧选择一张表，或切换到 SQL 查询。</div>)}
        {tab === 'structure' && (structure ? <div className="card-body"><div className="table-wrap"><table><thead><tr><th>列名</th><th>类型</th><th>可为空</th><th>默认值</th></tr></thead><tbody>{structure.columns.map((c) => <tr key={c.name}><td className="mono">{c.name} {c.primary && <span className="badge badge-info">PK</span>}</td><td>{c.type}</td><td>{c.nullable ? '是' : '否'}</td><td className="mono">{c.default || '—'}</td></tr>)}</tbody></table></div><h3 className="card-title">索引</h3>{structure.indexes.length ? structure.indexes.map((i) => <div className="explorer-index" key={i.name}><strong>{i.name}</strong><pre>{i.definition}</pre></div>) : <div className="muted small">无可见索引</div>}</div> : <div className="empty">请先选择一张表。</div>)}
        {tab === 'sql' && <><div className="card-body"><label className="label" htmlFor="explorer-sql">只读 SQL</label><textarea id="explorer-sql" className="input mono explorer-editor" spellCheck={false} maxLength={32768} value={sql} disabled={busy} onChange={(e) => setSQL(e.target.value)} onKeyDown={(e) => { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); if (!busy && target?.is_enabled) executeSQL() } }} /><div className="explorer-sql-actions"><button className="btn btn-primary" disabled={busy || !target?.is_enabled || !sql.trim()} onClick={executeSQL}>执行查询</button><span className="small faint">Ctrl / ⌘ + Enter</span></div><div className="hint">仅支持单条 SELECT 及常用内置聚合、字符串函数；不支持注释、CTE、写入、锁或自定义函数。请使用最小权限源库账号，数据浏览不是不可信数据库代码的沙箱。审计仅保存 SQL 摘要，不保存正文或结果。</div></div><Results result={result} /></>}
      </section>
    </div>
  </>
}

function Results({ result }: { result: QueryResult | null }) {
  if (!result) return null
  return <><div className="explorer-result-meta small muted" role="status">{result.rows.length} 行 · {result.duration_ms} ms {result.truncated && <span className="badge badge-warn">结果已截断</span>}</div><div className="table-wrap explorer-results">{result.rows.length ? <table><thead><tr>{result.columns.map((c, i) => <th key={i}>{c}</th>)}</tr></thead><tbody>{result.rows.map((row, i) => <tr key={i}>{row.map((value, j) => <td key={j} className="mono">{value === null ? <span className="faint">NULL</span> : <span title={typeof value === 'object' ? JSON.stringify(value) : String(value)}>{typeof value === 'object' ? JSON.stringify(value) : String(value)}</span>}</td>)}</tr>)}</tbody></table> : <div className="empty">查询成功，没有匹配的数据。</div>}</div></>
}
