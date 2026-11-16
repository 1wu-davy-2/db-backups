-- 0001_init.sql  M1 基线
-- 约定：时间统一 TIMESTAMPTZ；金额/大小统一 BIGINT 字节；状态用 TEXT + CHECK

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'viewer'
                  CHECK (role IN ('admin', 'operator', 'viewer')),
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_uq ON users (lower(email));

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent TEXT NOT NULL DEFAULT '',
    ip         TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE targets (
    id                 BIGSERIAL PRIMARY KEY,
    name               TEXT NOT NULL,
    engine             TEXT NOT NULL DEFAULT 'postgres'
                       CHECK (engine IN ('postgres', 'mysql')),
    host               TEXT NOT NULL,
    port               INT NOT NULL DEFAULT 5432,
    dbname             TEXT NOT NULL,
    username           TEXT NOT NULL,
    password_enc       BYTEA NOT NULL,
    ssl_mode           TEXT NOT NULL DEFAULT 'prefer',
    server_version     TEXT NOT NULL DEFAULT '',
    server_version_num INT NOT NULL DEFAULT 0,
    db_size_bytes      BIGINT NOT NULL DEFAULT 0,
    tags               TEXT[] NOT NULL DEFAULT '{}',
    is_enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    last_probe_at      TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX targets_name_uq ON targets (name);

CREATE TABLE policies (
    id               BIGSERIAL PRIMARY KEY,
    target_id        BIGINT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    cron_expr        TEXT NOT NULL,
    timezone         TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    kind             TEXT NOT NULL DEFAULT 'logical'
                     CHECK (kind IN ('logical')),
    dump_format      TEXT NOT NULL DEFAULT 'custom'
                     CHECK (dump_format IN ('custom', 'directory')),
    compression      TEXT NOT NULL DEFAULT 'zstd:5',
    encrypt          BOOLEAN NOT NULL DEFAULT TRUE,
    retain_count     INT NOT NULL DEFAULT 0,
    retain_days      INT NOT NULL DEFAULT 0,
    retain_max_bytes BIGINT NOT NULL DEFAULT 0,
    keep_min_count   INT NOT NULL DEFAULT 3,
    verify_mode      TEXT NOT NULL DEFAULT 'toc'
                     CHECK (verify_mode IN ('none', 'checksum', 'toc')),
    timeout_seconds  INT NOT NULL DEFAULT 7200,
    misfire_policy   TEXT NOT NULL DEFAULT 'skip'
                     CHECK (misfire_policy IN ('skip', 'run_once')),
    is_enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    next_run_at      TIMESTAMPTZ,
    last_fired_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT policies_retention_ck CHECK (
        retain_count > 0 OR retain_days > 0 OR retain_max_bytes > 0
    ),
    CONSTRAINT policies_keepmin_ck CHECK (keep_min_count >= 1)
);
CREATE INDEX policies_target_idx ON policies (target_id);
CREATE INDEX policies_enabled_idx ON policies (is_enabled) WHERE is_enabled;

CREATE TABLE jobs (
    id             BIGSERIAL PRIMARY KEY,
    policy_id      BIGINT REFERENCES policies(id) ON DELETE SET NULL,
    target_id      BIGINT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    trigger        TEXT NOT NULL CHECK (trigger IN ('schedule', 'manual', 'retry')),
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'running', 'success', 'failed', 'canceled')),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    size_bytes     BIGINT NOT NULL DEFAULT 0,
    dump_sha256    TEXT NOT NULL DEFAULT '',
    tool_version   TEXT NOT NULL DEFAULT '',
    server_version TEXT NOT NULL DEFAULT '',
    error_code     TEXT NOT NULL DEFAULT '',
    error_message  TEXT NOT NULL DEFAULT '',
    duration_ms    BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX jobs_target_started_idx ON jobs (target_id, started_at DESC);
CREATE INDEX jobs_created_idx ON jobs (created_at DESC);
CREATE INDEX jobs_active_idx ON jobs (status) WHERE status IN ('pending', 'running');

CREATE TABLE artifacts (
    id          BIGSERIAL PRIMARY KEY,
    job_id      BIGINT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    object_key  TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    sha256      TEXT NOT NULL DEFAULT '',
    encrypted   BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted  BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX artifacts_job_idx ON artifacts (job_id);
CREATE INDEX artifacts_live_idx ON artifacts (created_at DESC) WHERE NOT is_deleted;

CREATE TABLE verifications (
    id          BIGSERIAL PRIMARY KEY,
    job_id      BIGINT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    mode        TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('success', 'failed')),
    duration_ms BIGINT NOT NULL DEFAULT 0,
    report      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX verifications_job_idx ON verifications (job_id);

CREATE TABLE audit_logs (
    id         BIGSERIAL PRIMARY KEY,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    object_ref TEXT NOT NULL DEFAULT '',
    detail     JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip         TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_created_idx ON audit_logs (created_at DESC);
