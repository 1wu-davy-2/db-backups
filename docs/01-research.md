# 关系型数据库备份平台 — 调研与技术方案

> 版本 v0.1 ｜ 2026-11-16 ｜ 范围：PostgreSQL 优先，MySQL 预留



---

## 1. 结论先行

| 问题        | 结论                                                                                                                             |
| --------- | ------------------------------------------------------------------------------------------------------------------------------ |
| 该不该自研     | **如果要通用产品能力，别自研**：Databasus 已是同栈同功能（Go + React + PG/MySQL + 保留策略 + PITR），2.6k star 级别，Apache 2.0                               |
| 什么情况下值得自研 | ① 需要私有化部署且要中文通知（企微/钉钉/飞书）、国产对象存储（OSS/COS/OBS/MinIO）② 需要**备份可验证**（自动拉起实例真恢复）③ 需要和现有 CMDB/工单/审计打通 ④ 学习或团队能力建设                    |
| 自研的技术门槛在哪 | 不在 Web 层，在 **pg_dump 版本矩阵 + 一致性 + 大库可恢复性 + 保留策略不误删** 这四件事上                                                                     |
| PG 备份路线   | v1 只做**逻辑备份**（`pg_dump -Fc`,可跨版本/跨环境恢复，运维成本低）；v2 再上**物理备份 + WAL + PITR**（`pg_basebackup` / `pg_receivewal`），两者不是替代关系，是覆盖不同 RPO |
| 最容易被低估的坑  | 逻辑备份**永远不等于可恢复**。没有自动恢复演练的备份系统，等于没有备份                                                                                          |

---

## 2. 生态调研（2026-11 现状）

### 2.1 主流工具横向对比

| 工具                          | 语言/许可                 | 形态          | 多库                           | 物理/WAL           | PITR | 保留策略             | Web UI |
| --------------------------- | --------------------- | ----------- | ---------------------------- | ---------------- | ---- | ---------------- | ------ |
| **pgBackRest 2.60**         | C / MIT               | CLI         | ❌ 仅 PG                       | ✅ 全量/差异/增量 + WAL | ✅    | ✅ 时间窗 + WAL 归档过期 | ❌      |
| **WAL-G**                   | Go / Apache-2.0       | CLI         | ✅ PG/MySQL/MSSQL/Mongo/Redis | ✅                | ✅    | ✅                | ❌      |
| **Barman**                  | Python / GPL-3        | CLI（EDB 背书） | ❌ 仅 PG                       | ✅ + 备份目录/异地      | ✅    | ✅                | ❌      |
| **pgmoneta**                | C / BSD-3             | Daemon      | ❌ 仅 PG                       | ✅ 增量(PG14+)      | ✅    | ✅                | 基础     |
| **pgBackWeb**               | Go+JS / AGPL-3.0      | Web         | ❌ 仅 PG                       | ❌                | ❌    | ✅                | ✅ 轻量   |
| **Databasus**（原 Postgresus） | Go+React / Apache-2.0 | Web + Agent | ✅ PG/MySQL/MariaDB/Mongo     | ✅ Agent 模式       | ✅    | ✅ 时间/数量/GFS/容量   | ✅ 完整   |
| 自研                          | Go + React            | Web + Agent | 目标 ✅                         | v2               | v2   | 目标 ✅             | ✅      |

### 2.2 必须知道的两个事实

**事实一：pgBackRest 的"停维护"是假消息（信息已过时）。**  
时间线：2026-04-27 维护者 David Steele 宣布停止维护 → 05-04 募资好转 → 05-18 官宣"Will Continue"，AWS/Supabase/pgEdge/Tiger Data/Percona/Eon 组成赞助联盟，引入第二维护者 → 07~~10 连续发布 2.59.0~~2.60.0，已支持 PG 19。网上大量 2026 年中的对比文章仍在引用"已归档"，不要被误导。**但**：这次事件暴露了单一维护者的治理风险，长期看 `pgBunker`（社区 fork）是备选。

**事实二：Databasus 与你的需求重合度约 90%。**  
Go 单二进制 + React/TS/Vite/Tailwind + 元数据存 PG/SQLite + 逻辑备份 + Agent 物理备份 + WAL + PITR + 保留（时间/数量/GFS/容量上限）+ 多存储（本地/S3/GDrive/R2/Azure/NAS/SFTP）+ AES-256-GCM + 工作区/RBAC/审计 + 多渠道通知 + **备份后拉起容器真恢复校验**。你列的每一条需求它都有。

### 2.3 因此，定位建议（三选一）

| 方案                | 做法                                           | 适用           |
| ----------------- | -------------------------------------------- | ------------ |
| A. 直接用            | 部署 Databasus，贡献中文通知/国产存储适配                   | 目标只是"把备份跑起来" |
| B. 薄封装            | 自研调度层 + 复用 pg_dump/pgBackRest 内核，只做编排/审计/通知  | 已有运维体系，需要集成  |
| C. 自研（推荐给"想做成产品"） | 走差异化：**备份可验证 + 国产化适配 + 恢复编排**，功能面砍到 60%，深度做透 | 有产品化诉求       |

下文按 **C** 展开，但架构上刻意做成"内核可替换"，将来想退回 B 只换 driver。

---

## 3. 技术选型

| 层    | 选型                                                           | 理由                                                                    |
| ---- | ------------------------------------------------------------ | --------------------------------------------------------------------- |
| 后端语言 | **Go 1.24**                                                  | 单二进制分发、`os/exec` 管子进程干净、并发模型适配任务调度                                    |
| HTTP | chi v5 或 echo v4                                             | 轻，中间件够用；不引 Gin（绑定多、生态噪音大）                                             |
| 元数据库 | **PostgreSQL 16+**                                           | 关键：可用 `pg_try_advisory_lock` 做**跨实例任务互斥**，多副本部署不会双跑备份；同时用 JSONB 存策略快照 |
| 迁移   | golang-migrate 或 goose                                       | 纯 SQL 迁移，便于审计                                                         |
| 访问层  | sqlc（推荐）或 ent                                                | sqlc 生成类型安全代码，SQL 可控；ent 上手快但生成物重                                     |
| 调度   | robfig/cron v3                                               | 支持 `CRON_TZ`、秒级、`WithChain(SkipIfStillRunning)`                       |
| 对象存储 | minio-go（S3 兼容）+ 本地 FS                                       | 一个接口覆盖 AWS S3/MinIO/OSS/COS/OBS（后三者都有 S3 兼容端点）                        |
| 加密   | AES-256-GCM **分块 AEAD**                                      | 大文件不能整块 GCM（nonce 复用 + 内存爆）；每 8MiB 一块，chunk index 入 AAD               |
| 日志   | slog（标准库）+ lumberjack                                        | 不引 zap，减依赖                                                            |
| 前端   | React 19 + Vite + TS + TanStack Query + shadcn/ui + Recharts | 与 Databasus 同栈，生态成熟                                                   |
| 部署   | Docker Compose（单机）/ Helm（K8s）                                | Agent 模式需挂载 docker.sock 做恢复演练                                         |
| 观测   | Prometheus `/metrics` + 结构化日志                                | 备份系统的价值一半在"能证明它跑了"                                                    |

---

## 4. 架构

三个角色，一个方向：**控制面永不被数据库反连**。

- **Control Plane（Web + API + Scheduler + GC）**：无状态，可多副本，靠 PG advisory lock 选主执行定时任务。
- **Runner / Agent**：真正执行 `pg_dump` 的进程。两种形态：
  - *Remote 模式*：Control Plane 直接 exec 本地 `pg_dump` 连远端库（要求网络可达，适合云数据库/公网可达）。
  - *Agent 模式*（v2）：部署在 DB 主机，主动**出站**连 Control Plane 拉任务，解决内网库不可达 + 免开入站端口。
- **Storage**：本地磁盘或 S3 兼容对象存储，接口抽象为 `Put/Get/Delete/Stat/List`。

数据流：`调度触发 → 建 job 记录 → 抢 advisory lock → 预检 → 执行 dump（流式 sha256 + 压缩 + 加密）→ 上传 → 落元数据 → 触发校验 → 触发 GC → 通知`。

---

## 5. 数据模型（元数据库 DDL 要点）

```sql
-- 备份目标（一个库 = 一行）
CREATE TABLE targets (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    engine          TEXT NOT NULL CHECK (engine IN ('postgres','mysql')),
    host            TEXT NOT NULL,
    port            INT  NOT NULL,
    dbname          TEXT NOT NULL,
    username        TEXT NOT NULL,
    password_enc    BYTEA NOT NULL,             -- AES-256-GCM，主密钥来自 env/KMS
    ssl_mode        TEXT NOT NULL DEFAULT 'prefer',
    server_version  INT,                         -- 探测缓存，如 160004
    tags            TEXT[] DEFAULT '{}',
    is_enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 备份策略（1 目标可有多条，如"每日全量 + 每周长留"）
CREATE TABLE policies (
    id                  BIGSERIAL PRIMARY KEY,
    target_id           BIGINT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    cron_expr           TEXT NOT NULL,
    timezone            TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    kind                TEXT NOT NULL DEFAULT 'logical',   -- logical | physical
    dump_format         TEXT NOT NULL DEFAULT 'custom',    -- custom(-Fc) | directory(-Fd)
    compression         TEXT NOT NULL DEFAULT 'zstd:5',
    jobs_parallel       INT  NOT NULL DEFAULT 1,           -- 仅 -Fd 生效
    encrypt             BOOLEAN NOT NULL DEFAULT TRUE,
    storage_id          BIGINT NOT NULL REFERENCES storages(id),
    -- 保留三条件，任一命中即过期（但受 keep_min_count 硬保护）
    retain_count        INT,        -- 保留份数
    retain_days         INT,        -- 保留天数
    retain_max_bytes    BIGINT,     -- 存储上限
    keep_min_count      INT NOT NULL DEFAULT 3,   -- 安全底线：无论规则如何，至少留 N 份
    verify_mode         TEXT NOT NULL DEFAULT 'toc',  -- none | checksum | toc | restore
    timeout_seconds     INT  NOT NULL DEFAULT 7200,
    misfire_policy       TEXT NOT NULL DEFAULT 'skip', -- skip | run_once
    is_enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 一次执行
CREATE TABLE jobs (
    id            BIGSERIAL PRIMARY KEY,
    policy_id     BIGINT REFERENCES policies(id) ON DELETE SET NULL,
    target_id     BIGINT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    trigger       TEXT NOT NULL,          -- schedule | manual | retry
    status        TEXT NOT NULL,          -- pending|running|success|failed|canceled
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    size_bytes    BIGINT,
    dump_sha256   TEXT,
    tool_version  TEXT,                   -- pg_dump 16.4
    server_version TEXT,                  -- PostgreSQL 16.4
    error_code    TEXT,
    error_message TEXT,
    log_object_key TEXT,                  -- 全量日志落存储，不塞 DB
    CONSTRAINT jobs_status_ck CHECK (status IN ('pending','running','success','failed','canceled'))
);
CREATE INDEX ON jobs (target_id, started_at DESC);
CREATE INDEX ON jobs (status) WHERE status IN ('pending','running');

-- 一个 job 产出的对象（-Fd 会有多行）
CREATE TABLE artifacts (
    id          BIGSERIAL PRIMARY KEY,
    job_id      BIGINT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    storage_id  BIGINT NOT NULL,
    object_key  TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL,
    sha256      TEXT,
    is_deleted  BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at  TIMESTAMPTZ
);

-- 校验记录
CREATE TABLE verifications (
    id          BIGSERIAL PRIMARY KEY,
    job_id      BIGINT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    mode        TEXT NOT NULL,       -- checksum | toc | restore
    status      TEXT NOT NULL,       -- success | failed
    duration_ms INT,
    report      JSONB,               -- 表数、行数抽样、扩展版本、警告
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 审计（谁在什么时候恢复了哪个库）
CREATE TABLE audit_logs (
    id         BIGSERIAL PRIMARY KEY,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,   -- target.create | policy.update | job.restore | artifact.delete
    object_ref TEXT,
    detail     JSONB,
    ip         INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

设计取舍：

- **不建 `backups` 表去合并 job+artifact**，因为 `-Fd` 一个 job 多文件、将来物理备份还有 WAL 段，合并会立刻变形。
- **`jobs` 是热表**，日志走对象存储，DB 只留 `log_object_key`，避免元数据库被日志撑爆。
- **`keep_min_count` 独立于三个保留条件**，这是防"配置写错把生产备份清空"的最后一道闸。

---

## 6. PostgreSQL 备份内核（核心难点）

### 6.1 版本矩阵：`pg_dump` 主版本必须 ≥ 服务端主版本

官方规则：`pg_dump` 可备份同版本或更老的服务器，**不能用旧版 pg_dump 备份新版服务器**。所以：

- 镜像内并列安装多套客户端：`/usr/lib/postgresql/{15,16,17,18,19}/bin/pg_dump`（Debian PGDG 源支持多版本共存，不冲突）。
- 启动时扫描这些路径，构建 `map[major]path`。
- 连库后 `SHOW server_version_num`（如 `160004`）→ 取 `160004/10000 = 16` → 选择 `>= 16` 的最小可用版本。
- 目标库版本高于本地最大客户端版本时，**拒绝执行并告警**，不要降级硬跑（会产生无效 dump）。

```sql
-- 探测（不需要 superuser）
SELECT current_setting('server_version_num')::int AS ver_num,
       current_setting('server_version')          AS ver_str,
       pg_database_size(current_database())       AS db_bytes;
```

### 6.2 权限：只读账号即可，但别漏了序列和大对象

```sql
-- PG 14+ 最省事：预定义角色
CREATE ROLE backup_ro LOGIN PASSWORD 'xxxxx';
GRANT pg_read_all_data TO backup_ro;   -- 覆盖现有及未来所有表的 SELECT

-- PG 13 及以下需手工授权
GRANT USAGE ON SCHEMA public TO backup_ro;
GRANT SELECT ON ALL TABLES    IN SCHEMA public TO backup_ro;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO backup_ro;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO backup_ro;
```

要点：

- `pg_dump` 需要读**序列**（取当前值），漏授 `SELECT ON SEQUENCES` 会导致序列值没带上，恢复后自增主键冲突。
- 有 large object 的库还需要 `pg_read_all_data` 覆盖（或 `SELECT ON pg_largeobject`）。
- 应用侧建议在连接后校验权限：`has_table_privilege` 抽样检查，并**检测是否误给了写权限**（只读是安全声明，不是功能需要）。

### 6.3 一致性边界（必须写进产品文档）

| 场景                     | 一致性                                                                                               |
| ---------------------- | ------------------------------------------------------------------------------------------------- |
| 单个 `pg_dump -d one_db` | **库内一致**：整个 dump 在同一个 REPEATABLE READ 快照内                                                         |
| 对同一实例的 3 个库分别 dump     | **跨库不一致**：3 个独立快照                                                                                 |
| `pg_dumpall`           | 全集群一致，但只能纯文本、不能并行、不能选择性恢复                                                                         |
| 需要跨库一致                 | 用 `pg_export_snapshot()` + `pg_dump --snapshot=<id>`（PG 9.2+），但要求同一连接导出、dump 期间持有该连接；v1 不做，明确标注限制 |

这点极易被误解：用户以为"备份了整个实例就是一致的"，实际不是。UI 上要写明。

### 6.4 锁与干扰：`pg_dump` 会卡住 DDL，不会卡 DML

`pg_dump` 对每张表持 `ACCESS SHARE` 锁（很短），不阻塞 `INSERT/UPDATE/DELETE`，但**会阻塞 `ALTER TABLE`/`DROP`**（它们要 `ACCESS EXCLUSIVE`）。反过来，正在跑的 DDL 会让 `pg_dump` 排队等锁。

必须加的参数：

```bash
--lock-wait-timeout=30s     # 等锁超 30s 直接失败退出，不挂死
```

以及在某些企业策略下服务端会设 `statement_timeout`，需要在会话层显式清零：

```bash
PGOPTIONS='-c statement_timeout=0 -c idle_in_transaction_session_timeout=0' \
PGPASSWORD="$PGPASS" \
/usr/lib/postgresql/16/bin/pg_dump \
  -h db.internal -p 5432 -U backup_ro -d appdb \
  -Fc -Z zstd:5 \
  --lock-wait-timeout=30s \
  --no-owner --no-privileges \
  -f /scratch/appdb-20261116T020000.dump
```

参数说明：

- `-Fc`：自定义格式，支持选择性恢复（`pg_restore -t`）、并行恢复（`-j`）、内置压缩。**首选**。
- `-Z zstd:5`：zstd 需 PG 16+ 的客户端；15 及以下用 `-Z 5`（gzip）或外部 zstd 流式压缩。实测 zstd:5 相比 gzip-6 通常省 20~~40% 体积、快 2~~4 倍。
- `--no-owner --no-privileges`：跨环境恢复（生产→测试）必备，否则恢复到没有同名角色的实例会一堆 ERROR。**但要作为可选开关**，同环境恢复保留原权限更合适。
- `-j N` **只对 `-Fd`（目录格式）生效**，`-Fc` 单文件不能并行。大库想并行必须用 `-Fd` 再逐文件上传，或流式 tar。
- 不要用 `--no-sync`：省的那点时间不值得赌 fsync。


### 6.5 密码传递：绝不放 argv

`ps aux` 能看到命令行参数，`-W`/`PGPASSWORD` 明文入参等于泄露。做法：

```go
cmd := exec.Command(pgDumpBin, args...)
cmd.Env = append(os.Environ(),
    "PGPASSWORD="+plainPassword,   // 只经 env
    "PGSSLMODE="+sslMode,
    "PGOPTIONS=-c statement_timeout=0",
    "PGCONNECT_TIMEOUT=10",
)
cmd.Stderr = logWriter  // 必须捕获，pg_dump 的错误全在 stderr
```

更稳的做法是临时 `.pgpass`（0600）指向，用完立即删除。注意 error 信息里可能带连接串，入库前要脱敏。

### 6.6 执行流水线（Go 侧）

```
1. 抢锁        pg_try_advisory_lock(hashtext('job:target:'||target_id))
2. 预检        server_version_num / 磁盘剩余 / 目录可达性 / 权限抽样
3. 空间估算    db_bytes * 0.35（zstd 经验值）* 2 安全系数，不足则拒绝启动
4. 执行 dump   exec + context.WithTimeout，Setpgid 以便超时杀进程组
5. 流式摘要    边写边算 sha256（上传前可校验，上传后可复算）
6. 上传        multipart，失败重试 3 次（指数退避），成功后删 scratch
7. 落元数据    artifacts + jobs.status=success
8. 校验        按 verify_mode 分级
9. GC          见第 7 节
10. 通知       webhook / 邮件 / 企微
```

关键工程点：
- **子进程必须进程组隔离**：`SysProcAttr{Setpgid:true}`（Linux）/ `CreationFlags: CREATE_NEW_PROCESS_GROUP`（Windows），超时时杀整个进程组，否则 `pg_dump` 变孤儿继续占 IO/连接。
- **scratch 目录先落盘再上传**，不要纯流式直传。理由：dump 失败要能立刻判断（退出码），上传失败可重试而不必重跑 dump。大库（>100GB）用 `-Fd` 逐文件流式上传。
- **退出码语义**：`pg_dump` 返回 1 表示发生错误（通常是权限/依赖），不是"完成但有警告"。任何非 0 都必须标记 failed 并清理半成品文件。

### 6.7 校验分级（这是差异化的核心）

| 级别 | 做法 | 成本 | 能证明什么 |
| --- | --- | --- | --- |
| L0 `none` | 不校验 | 0 | 无 |
| L1 `checksum` | 复算 sha256 / 解压读尾部 | 低（读一遍） | 文件没坏 |
| L2 `toc` | `pg_restore --list` 解析 TOC | 低 | 归档结构可解析、对象数符合预期 |
| L3 `restore` | 临时 PG 实例真恢复 + 探针 SQL | 高（要 CPU/IO/容器） | **真的能恢复**，且数据对得上 |

L3 探针建议（每库自动生成）：

```sql
-- 对象数对比（源库 vs 恢复库，允许 0 差异）
SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema');

-- 关键表行数（在 dump 时刻的估算 vs 恢复后的精确值，给容差）
SELECT reltuples::bigint FROM pg_class WHERE oid='public.orders'::regclass;

-- 序列当前值（恢复后必须 >= dump 时的值）
SELECT last_value FROM public.orders_id_seq;

-- 扩展
SELECT extname, extversion FROM pg_extension ORDER BY 1;
```

L3 落地方案：`docker run --rm postgres:16-alpine` 起临时实例 → `pg_restore -j 4` → 跑探针 → 比对 → 销毁。需要挂载 `docker.sock`（安全上要接受，或改用预置的"验收集群"通过连接串指定）。

---

## 7. 保留策略引擎（最容易出事故的模块）

三条规则**任一命中即过期**，但受 `keep_min_count` 硬保护：

```go
type Artifact struct {
    JobID     int64
    CreatedAt time.Time
    SizeBytes int64
    Status    string // 关联 job 的状态，running/verifying 不可删
}

// Plan 返回待删列表，不执行删除
func Plan(arts []Artifact, p Policy) (kill []Artifact) {
    sort.Slice(arts, func(i, j int) bool { return arts[i].CreatedAt.After(arts[j].CreatedAt) })
    now := time.Now()
    var acc int64
    for i, a := range arts {
        if i < p.KeepMinCount {          // 硬保护：最新 N 份永不删
            acc += a.SizeBytes
            continue
        }
        if a.Status == "running" || a.Status == "verifying" {
            acc += a.SizeBytes
            continue
        }
        tooOld := p.RetainDays > 0 && now.Sub(a.CreatedAt) > time.Duration(p.RetainDays)*24*time.Hour
        tooMany := p.RetainCount > 0 && i >= p.RetainCount
        tooBig := p.RetainMaxBytes > 0 && acc+a.SizeBytes > p.RetainMaxBytes
        if tooOld || tooMany || tooBig {
            kill = append(kill, a)
            continue                      // 注意：不计入 acc，避免双重扣减
        }
        acc += a.SizeBytes
    }
    return
}
```

执行顺序必须是 **先标记后删除**：

```
BEGIN;
UPDATE artifacts SET is_deleted=true, deleted_at=now() WHERE id = ANY($1);
COMMIT;
-- 再调 storage.Delete()，成功一个更新一个（失败留待下一轮重试，不会重复计数）
```

反模式（务必避免）：
- ❌ 先删对象再改 DB：删成功但 DB 事务回滚 → 元数据指向不存在的文件，恢复时才发现。
- ❌ 用 `created_at < now() - interval` 一条 SQL 批量删：会无视 `keep_min_count`，配置写错一次清空全库备份。
- ❌ 把容量计算算成"当前目录实际大小"：对象存储里 `List` 有延迟且成本高，必须用 `artifacts.size_bytes` 求和。
- ❌ 删除不写审计：`audit_logs` 必须记录，否则事后无法追责。

补充规则：
- **保留策略变更不回溯**：改小 `retain_days` 后，下一轮 GC 会立刻删掉超出部分，UI 必须二次确认并显示"将删除 N 份，释放 X GB"。
- **至少留一份全量且其校验记录为 success**：如果最新备份连续失败，GC 不应把最后一个成功备份删掉。加一条：`keep` 集合里若没有任何 `verify=success` 的备份，暂停 GC 并告警。
- 未来接物理备份时，**WAL 不能按时间删**，要按 base backup 的 `start_lsn/stop_lsn` 区间保留（恢复某个全量需要它之后的所有 WAL 直到下一个全量）。

---

## 8. 调度器

| 设计点 | 做法 |
| --- | --- |
| 表达式 | robfig/cron v3，标准 5 段 + `CRON_TZ=Asia/Shanghai`，UI 提供可视化构造器 + 下次执行时间预览 |
| 多副本选主 | 每条 policy 执行前 `pg_try_advisory_lock(key)`，拿不到就跳过（另一副本在跑）。比 leader election 简单且精确到 policy 粒度 |
| 防重入 | `cron.WithChain(cron.SkipIfStillRunning(...))` 进程内 + advisory lock 跨进程，双保险 |
| 错过执行 | 记录 `last_fired_at`，启动时比对；`misfire_policy=skip` 只记一条"错过"事件，`run_once` 立即补一次（不补全部） |
| 并发上限 | 全局 semaphore（默认 4），per-target 互斥（同一库不并发 dump，会互相拖慢 IO） |
| 超时取消 | `context.WithTimeout` → 杀进程组 → 标记 `failed`，error_code=`TIMEOUT` |
| 时间源 | 全部 UTC 存库，展示按用户时区；cron 求值用 policy 自带时区 |
| 可观测 | `backup_job_total{status}`、`backup_duration_seconds`、`backup_last_success_timestamp`（告警基石：`time() - backup_last_success_timestamp > 26h`） |

`backup_last_success_timestamp` 这一条指标比任何 UI 都重要——它让"备份静默失败"变成可告警事件。

---

## 9. 恢复（比备份更该被设计）

备份系统 90% 的代码在备份，90% 的事故在恢复。最小可用恢复能力：

1. **下载**：单文件 dump 直接下载；`-Fd` 目录格式打包 tar 流式下载。
2. **恢复到指定实例**：填目标 host/port/新库名 → 后端拼 `pg_restore`（或 `psql` for plain）。
   - 必须**白名单 + 二次确认**：目标实例来自"恢复目标"配置，不允许自由填任意 host（防误恢复到生产）。
   - `--create --clean` 是危险组合（会 DROP 同名库），默认禁用，需显式勾选。
   - 恢复前检查目标库是否已存在，存在则中止。
3. **恢复演练**（L3 校验复用同一套代码）：一键"演练" → 临时实例 → 恢复 → 探针 → 报告 → 销毁。产出"可恢复性 SLA"报表。
4. **恢复进度**：`pg_restore --verbose` 的 stderr 逐行转 SSE 推前端。

---

## 10. 安全清单

- [ ] 凭据 AES-256-GCM 加密存库，主密钥 32B 来自 `BACKUP_MASTER_KEY`（env/KMS/文件 0600），启动时校验密钥指纹，不匹配拒绝启动（避免用错密钥把密文解成垃圾）
- [ ] 密码只经 `cmd.Env` 传子进程，绝不入 argv / 日志 / 错误信息
- [ ] 日志脱敏中间件：匹配 `password=`、`postgres://user:pass@` 等模式后替换
- [ ] 备份文件默认加密（分块 AEAD），密钥不与备份同存储
- [ ] API：JWT + RBAC（viewer/operator/admin），恢复与删除仅 admin
- [ ] 对象 key 由服务端生成（`{engine}/{target_id}/{yyyy}/{mm}/{job_id}.dump.enc`），不接受用户输入拼接
- [ ] 本地存储路径 `filepath.Clean` + 前缀校验，防 `../` 穿越
- [ ] 审计所有写操作与恢复操作
- [ ] 元数据库连接自身用 TLS + 最小权限账号

---

## 11. 风险清单

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| `pg_dump` 版本矩阵维护成本 | 新 PG 版本发布即不可备份 | 镜像跟随 PGDG 多版本安装，CI 矩阵测 13~19；启动时自检并暴露 `/healthz` |
| 大库逻辑备份不可行（>500GB 或 >4h） | 备份窗口撑不住 | v1 明确容量上限（建议 ≤200GB），v2 上 `pg_basebackup` + WAL |
| 备份成功但不可恢复 | 最致命 | L3 恢复演练设为默认周任务，失败即告警 |
| 容器内做恢复演练需要 docker.sock | 安全争议 | 提供"外部验收集群"模式（只给连接串），或接受 socket 挂载并限制为独立 runner 节点 |
| 与 Databasus 功能重合 | 做完发现没必要 | 先定差异化（国产化 + 可验证 + 集成），否则建议直接用 |
| 多副本双跑备份 | 源库 IO 双倍、存储重复 | PG advisory lock 精确到 policy |
| GC 误删 | 数据灾难 | `keep_min_count` 硬保护 + 先标记后删 + 无成功校验则暂停 GC |

---

## 12. 里程碑

| 阶段 | 内容 | 验收标准 |
| --- | --- | --- |
| **M0 骨架** | Go 服务 + PG 迁移 + React 脚手架 + docker-compose | `docker compose up` 能登录进空后台 |
| **M1 MVP** | target CRUD、手动/定时 `pg_dump -Fc`、本地存储、实时日志、保留（份数/天数）、下载 | 建一个库→定时备份→文件生成→超期自动清理，全链路可跑 |
| **M2 存储与安全** | S3/MinIO、分块加密、容量上限、通知（webhook/邮件/企微）、L1+L2 校验 | 备份加密上传 S3，篡改一个字节能被 L1 检出 |
| **M3 可验证** | 恢复向导、L3 恢复演练、审计日志、RBAC、Dashboard 与 Prometheus 指标 | 每周自动演练，报表显示"最近 7 天可恢复性 100%" |
| **M4 物理与多引擎** | Agent 模式、`pg_basebackup`、WAL 归档、PITR；MySQL driver（`mysqldump --single-transaction --source-data=2` + 可选 mydumper） | 能把库恢复到任意时间点；MySQL 复用同一套 job/retention |

---

## 13. MySQL 预留（不在 v1 做，但接口先留好）

设计上把"引擎差异"收进 `EngineDriver` 接口：

```go
type EngineDriver interface {
    Probe(ctx, conn) (ServerInfo, error)          // 版本、大小、只读校验
    BackupCmd(ctx, spec) (*exec.Cmd, error)       // pg_dump / mysqldump
    RestoreCmd(ctx, spec) (*exec.Cmd, error)      // pg_restore / mysql
    VerifyProbes(ctx, conn) ([]Probe, error)      // 探针 SQL
    SupportsPITR() bool
}
```

MySQL 侧的差异点（提前记下，避免二次返工）：
- `mysqldump --single-transaction` 只对 InnoDB 一致；有 MyISAM 表要 `--lock-tables`（会锁写，需窗口）。
- `--source-data=2`（8.0.26+，旧名 `--master-data`）用于 binlog 位点，是 MySQL PITR 的前提。
- `--set-gtid-purged=OFF` 否则恢复到非 GTID 环境报错；`--hex-blob` 防二进制损坏；`--routines --triggers --events` 别漏。
- 大库用 mydumper/myloader 并行；物理备份走 xtrabackup（Percona）。
- 保留策略、调度、存储、审计完全复用，只有 `BackupCmd/RestoreCmd/Probe` 三个方法要分别实现。

---

## 14. 需要你确认的三个参数（影响方案取舍）

1. **目标规模**：要管几个实例、单库最大多大？>200GB 就必须 M4 提前，逻辑备份扛不住。
2. **RPO 要求**：能接受"丢一天"（逻辑备份够）还是必须"丢几秒"（必须 WAL + PITR）？
3. **定位**：内部自用、还是对外产品？自用建议直接上 Databasus；对外产品则按第 2.3 节 C 路线，把"可验证"和"国产化"做透。
