# DBBackups

PostgreSQL 备份调度平台。Go + React，单二进制 + 静态前端，面向"多库、定时、可验证"的日常备份场景。

> 当前为 **M1 里程碑**：逻辑备份全链路已打通。物理备份 / PITR / MySQL 在路线图中。

---

## 1. 它解决什么

裸 `pg_dump` + crontab 的典型失效方式：

- 备份脚本静默失败，半年后才发现没有可用备份
- `pg_dump` 版本低于服务端主版本，dump 出来是废文件
- 保留策略写错一条 SQL，把生产备份全删了
- 备份文件没有校验，恢复时才知道损坏
- 多台机器都跑 cron，同一个库被重复备份

本项目的对应处理：版本矩阵自动选客户端、保留策略带硬保护、L1/TOC 校验、advisory lock 防重复执行、全链路日志与审计。

## 2. 当前能力

| 能力 | 状态 |
| --- | --- |
| 目标库管理（保存前强制连通性校验、版本/容量探测） | 已完成 |
| 定时备份（cron + 时区，每库一条策略） | 已完成 |
| 手动备份、取消、实时日志（SSE） | 已完成 |
| `pg_dump -Fc` + zstd/gzip（低版本客户端自动回退 gzip） | 已完成 |
| 保留策略：份数 / 天数 / 容量上限 + 保底份数 | 已完成 |
| 备份文件分块 AES-256-GCM 加密存储 | 已完成 |
| 校验：SHA-256 + `pg_restore --list` 解析 TOC | 已完成 |
| 本地对象存储、下载（服务端解密流式输出） | 已完成 |
| 多副本互斥（PG advisory lock） | 已完成 |
| 审计日志、RBAC（admin / operator / viewer） | 已完成 |
| 对象存储 S3 / OSS / COS | M2 |
| 恢复向导、自动恢复演练（L3） | M3 |
| 物理备份 + WAL + PITR | M4 |
| MySQL / MariaDB | M4 |

## 3. 快速开始

### 3.1 Docker Compose（推荐）

```bash
# 生成主密钥，务必保存好；丢失后已加密的备份不可解密
export BACKUP_MASTER_KEY=$(openssl rand -base64 32)
docker compose up -d --build
```

浏览器打开 `http://localhost:8080`，首次访问会引导创建管理员。

### 3.2 本地运行

要求：Go 1.24+、Node 22+、一个可用的 PostgreSQL（作元数据库）、`pg_dump` 客户端。

```bash
# 1) 元数据库
createdb dbbackups

# 2) 环境变量
cp .env.example .env
# 编辑 .env，至少填 BACKUP_DATABASE_URL

# 3) 前端
cd web && npm install && npm run build && cd ..

# 4) 启动（会自动执行迁移）
set -a && source .env && set +a
go run ./cmd/backupd
```

开发前端时用 `npm run dev`（Vite 已配置 `/api` 代理到 `:8080`）。

### 3.3 pg_dump 版本矩阵

程序启动时扫描以下位置，构建 `主版本 → 路径` 映射：

```
$BACKUP_PG_BIN_DIRS（逗号分隔）
/usr/lib/postgresql/{19,18,17,16,15,14,13}/bin
/usr/pgsql-{19,18,17,16,15}/bin
C:\Program Files\PostgreSQL\*\bin（Windows）
/opt/homebrew/opt/postgresql@*/bin、/Applications/Postgres.app/...（macOS）
$BACKUP_DATA_DIR/pgclient/*/bin（便携版）
PATH 中的 pg_dump
```

规则：**所选客户端主版本必须 ≥ 目标库主版本**，取满足条件的最小版本。目标库版本更高时任务直接失败并给出明确提示，不会产出无效 dump。

Docker 镜像已并列安装 15~18 四个版本。本机裸跑时可用 `backupd --check-tools` 查看探测到了哪些版本、还探测了哪些路径。

**为什么不内置 pg_dump？**

- `pg_dump` 是 PostgreSQL 官方的独立 C 客户端程序（依赖 libpq 与压缩库），不是可以 `go get` 的库。
- 官方硬约束：客户端主版本必须 ≥ 服务端主版本，否则 dump 无效。内置一个固定版本就只能备份更老的库，内置“最新版”也会随目标库升级立刻失效。
- pgBackRest / Barman / WAL-G 等主流工具同样要求安装匹配的客户端，本项目的 **Docker 形态已经内置四个版本**，属于开箱即用。
- 本机裸跑的解决办法：设 `BACKUP_PG_BIN_DIRS`（可多个目录，逗号分隔），或把便携版放到 `$BACKUP_DATA_DIR/pgclient/<版本>/bin`。找不到时报错会列出所有探测过的路径和三种修复方式。

## 4. 使用流程

1. **添加目标库**：填连接信息 → 保存时真连一次，失败不允许保存。建议用只读账号：

```sql
CREATE ROLE backup_ro LOGIN PASSWORD 'xxx';
GRANT pg_read_all_data TO backup_ro;   -- PG 14+
```

2. **建策略**：选目标库、cron、保留规则。保留三条件**任一命中即清理**，`保底份数` 是最新 N 份的硬保护，任何规则都不会删它们。

3. **等执行或手动触发**：任务详情页有实时日志，包含 pg_dump 的 `--verbose` 输出。

4. **取回备份**：任务详情 → 下载。加密备份由服务端解密后流式输出。

## 5. 关键设计

### 5.1 保留策略（最容易出事故的地方）

```
排序（新→旧）→ 前 keep_min 份强制保留
            → 其余逐条判断：超天数 / 超份数 / 超容量，任一命中即删
            → 若保留集合中没有任何成功备份，本轮 GC 直接跳过
```

最后一条是防呆：当最近备份连续失败时，删掉旧备份等于自断后路。

执行顺序是**先在元数据库标记 deleted，再删对象**。宁可泄漏空间，也不制造"元数据指向不存在文件"的坏引用。

### 5.2 并发与多副本

- 进程内：`map[targetID]busy` 互斥
- 跨实例：`pg_try_advisory_lock(hash)`，键由 targetID 派生
- 全局并发：信号量，默认 4

多副本部署时，同一个库同一时刻只会有一个实例在备份，其余记一条 `LOCKED` 跳过记录。

### 5.3 凭据与文件加密

- 数据库密码：AES-256-GCM 加密存 `targets.password_enc`，主密钥来自 `BACKUP_MASTER_KEY` 或 `$DATA_DIR/master.key`（0600）
- 子进程传参：密码只经 `cmd.Env` 的 `PGPASSWORD`，不出现在 argv（`ps` 可见）与日志中
- 备份文件：分块 AEAD（1 MiB/块，每文件独立派生密钥，块序号作 nonce 与 AAD），末尾写结束标记，截断会被检测
- 加密只是"落盘保护"，传输安全依赖 HTTPS

### 5.4 一致性边界（必须知道）

| 场景 | 一致性 |
| --- | --- |
| 单个库 `pg_dump -Fc` | 库内一致（同一 REPEATABLE READ 快照） |
| 同一实例多个库分别备份 | **跨库不一致**，各自独立快照 |
| 需要跨库一致 | 需 `pg_export_snapshot()` + `--snapshot`，当前版本不支持 |

`pg_dump` 持有 `ACCESS SHARE` 锁，不阻塞 DML，但会阻塞 DDL；反之正在执行的 `ALTER TABLE` 会让备份排队。程序已加 `--lock-wait-timeout=30s`，等锁超时直接失败而不是挂死。

## 6. 配置项

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `BACKUP_DATABASE_URL` | 必填 | 元数据库连接串 |
| `BACKUP_MASTER_KEY` | 空 | base64 的 32 字节主密钥，空则自动生成文件 |
| `BACKUP_HTTP_ADDR` | `:8080` | 监听地址 |
| `BACKUP_DATA_DIR` | `./data` | 数据目录（对象、日志、主密钥） |
| `BACKUP_WEB_DIR` | `./web/dist` | 前端静态资源 |
| `BACKUP_MAX_CONCURRENT` | `4` | 最大并发备份数 |
| `BACKUP_SESSION_TTL_DAYS` | `7` | 会话有效期 |
| `BACKUP_COOKIE_SECURE` | `0` | HTTPS 部署时置 1 |
| `BACKUP_PG_BIN_DIRS` | 空 | 额外 pg_dump 搜索目录 |

## 7. API 摘要

```
GET  /api/system/bootstrap          初始化状态
POST /api/auth/setup                创建首个管理员
POST /api/auth/login                登录（HttpOnly Cookie）

GET  /api/targets                   目标库列表
POST /api/targets                   新增（保存前探测）
POST /api/targets/{id}/test         连接测试
POST /api/targets/{id}/backup       立即备份

GET  /api/policies                  策略列表
POST /api/policies                  新建（cron 校验）

GET  /api/jobs                      任务列表（筛选/分页）
GET  /api/jobs/{id}/logs            历史日志
GET  /api/jobs/{id}/logs/stream     SSE 实时日志
GET  /api/artifacts/{id}/download   下载（自动解密）

GET  /api/stats                     概览指标
GET  /api/audit                     审计日志
```

## 8. 目录结构

```
cmd/backupd/            入口：装配 + 优雅退出
internal/config/        环境变量
internal/store/         pgx 仓储 + 内嵌 SQL 迁移
internal/pg/            探测、版本矩阵、pg_dump 执行、进程组终止
internal/backup/        Runner、保留策略、调度器、日志中心
internal/secrets/       主密钥、凭据加密、分块文件加密
internal/storage/       对象存储接口 + 本地实现
internal/api/           HTTP 路由与处理器
web/                    React 19 + Vite + TS
```

## 9. 测试

```bash
go test ./...                    # 保留策略等纯逻辑单测
go vet ./...
```

## 10. 已知限制

- 一个目标库只能有一条策略（避免多策略保留规则互相覆盖）
- 仅支持 `-Fc` 单文件格式；`-Fd` 并行目录格式未实现
- 逻辑备份适合单库 ≤ 200 GB；更大的库需要物理备份（M4）
- 不支持跨库一致性快照
- 恢复目前只提供下载，尚未提供"一键恢复到指定实例"（M3）
- 未实现操作通知（邮件/企微/Webhook，M2）
