package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weigong/dbbackups/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func wrap(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---------- users & sessions ----------

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, hash, role string) (*domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id, email, password_hash, role, created_at`,
		email, hash, role).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, created_at
		FROM users WHERE lower(email) = lower($1) AND is_active`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	return &u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, created_at
		FROM users WHERE id = $1 AND is_active`, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	return &u, nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, ua, ip string, exp time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, tokenHash, userID, ua, ip, exp)
	return err
}

func (s *Store) GetSessionUser(ctx context.Context, tokenHash string) (*domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.password_hash, u.role, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.is_active`, tokenHash).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	return &u, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------- targets ----------

const targetCols = `id, name, engine, host, port, dbname, username, ssl_mode,
	server_version, server_version_num, db_size_bytes, tags, is_enabled,
	coalesce(last_probe_at, 'epoch'::timestamptz), created_at`

func scanTarget(row pgx.Row) (*domain.Target, error) {
	var t domain.Target
	err := row.Scan(&t.ID, &t.Name, &t.Engine, &t.Host, &t.Port, &t.DBName, &t.Username,
		&t.SSLMode, &t.ServerVersion, &t.ServerVerNum, &t.DBSizeBytes, &t.Tags,
		&t.IsEnabled, &t.LastProbeAt, &t.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	return &t, nil
}

func (s *Store) ListTargets(ctx context.Context) ([]domain.Target, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+targetCols+` FROM targets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Target{}
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) GetTarget(ctx context.Context, id int64) (*domain.Target, error) {
	return scanTarget(s.pool.QueryRow(ctx, `SELECT `+targetCols+` FROM targets WHERE id = $1`, id))
}

// GetTargetSecret 返回加密后的凭据，仅内部使用，绝不进 JSON 响应。
func (s *Store) GetTargetSecret(ctx context.Context, id int64) (enc []byte, err error) {
	err = s.pool.QueryRow(ctx, `SELECT password_enc FROM targets WHERE id = $1`, id).Scan(&enc)
	return enc, wrap(err)
}

func (s *Store) GetTargetByName(ctx context.Context, name string) (*domain.Target, error) {
	return scanTarget(s.pool.QueryRow(ctx, `SELECT `+targetCols+` FROM targets WHERE name = $1`, name))
}

func (s *Store) CreateTarget(ctx context.Context, t *domain.Target, enc []byte) (*domain.Target, error) {
	return scanTarget(s.pool.QueryRow(ctx, `
		INSERT INTO targets (name, engine, host, port, dbname, username, password_enc,
		                     ssl_mode, server_version, server_version_num, db_size_bytes, tags,
		                     is_enabled, last_probe_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, now())
		RETURNING `+targetCols,
		t.Name, t.Engine, t.Host, t.Port, t.DBName, t.Username, enc,
		t.SSLMode, t.ServerVersion, t.ServerVerNum, t.DBSizeBytes, t.Tags, t.IsEnabled))
}

func (s *Store) UpdateTarget(ctx context.Context, t *domain.Target, enc []byte) (*domain.Target, error) {
	if enc == nil {
		return scanTarget(s.pool.QueryRow(ctx, `
			UPDATE targets SET name=$2, host=$3, port=$4, dbname=$5, username=$6,
			       ssl_mode=$7, is_enabled=$8, tags=$9, updated_at=now()
			WHERE id=$1 RETURNING `+targetCols,
			t.ID, t.Name, t.Host, t.Port, t.DBName, t.Username, t.SSLMode, t.IsEnabled, t.Tags))
	}
	return scanTarget(s.pool.QueryRow(ctx, `
		UPDATE targets SET name=$2, host=$3, port=$4, dbname=$5, username=$6,
		       ssl_mode=$7, is_enabled=$8, tags=$9, password_enc=$10, updated_at=now()
		WHERE id=$1 RETURNING `+targetCols,
		t.ID, t.Name, t.Host, t.Port, t.DBName, t.Username, t.SSLMode, t.IsEnabled, t.Tags, enc))
}

func (s *Store) UpdateTargetProbe(ctx context.Context, id int64, ver string, verNum int, size int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE targets SET server_version=$2, server_version_num=$3, db_size_bytes=$4, last_probe_at=now()
		WHERE id=$1`, id, ver, verNum, size)
	return err
}

func (s *Store) DeleteTarget(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM targets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- policies ----------

const policyCols = `p.id, p.target_id, coalesce(t.name,''), p.cron_expr, p.timezone, p.kind,
	p.dump_format, p.compression, p.encrypt, p.retain_count, p.retain_days,
	p.retain_max_bytes, p.keep_min_count, p.verify_mode, p.timeout_seconds,
	p.misfire_policy, p.is_enabled,
	coalesce(p.next_run_at,'epoch'::timestamptz), coalesce(p.last_fired_at,'epoch'::timestamptz),
	p.created_at`

func scanPolicy(row pgx.Row) (*domain.Policy, error) {
	var p domain.Policy
	err := row.Scan(&p.ID, &p.TargetID, &p.TargetName, &p.CronExpr, &p.Timezone, &p.Kind,
		&p.DumpFormat, &p.Compression, &p.Encrypt, &p.RetainCount, &p.RetainDays,
		&p.RetainMaxBytes, &p.KeepMinCount, &p.VerifyMode, &p.TimeoutSeconds,
		&p.MisfirePolicy, &p.IsEnabled, &p.NextRunAt, &p.LastFiredAt, &p.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	return &p, nil
}

func (s *Store) ListPolicies(ctx context.Context) ([]domain.Policy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+policyCols+`
		FROM policies p LEFT JOIN targets t ON t.id = p.target_id
		ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Policy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) GetPolicy(ctx context.Context, id int64) (*domain.Policy, error) {
	return scanPolicy(s.pool.QueryRow(ctx, `
		SELECT `+policyCols+` FROM policies p LEFT JOIN targets t ON t.id=p.target_id WHERE p.id=$1`, id))
}

func (s *Store) PolicyCountForTarget(ctx context.Context, targetID, excludeID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM policies WHERE target_id=$1 AND id <> $2`, targetID, excludeID).Scan(&n)
	return n, err
}

func (s *Store) CreatePolicy(ctx context.Context, p *domain.Policy) (*domain.Policy, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO policies (target_id, cron_expr, timezone, kind, dump_format, compression,
			encrypt, retain_count, retain_days, retain_max_bytes, keep_min_count, verify_mode,
			timeout_seconds, misfire_policy, is_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id`,
		p.TargetID, p.CronExpr, p.Timezone, p.Kind, p.DumpFormat, p.Compression,
		p.Encrypt, p.RetainCount, p.RetainDays, p.RetainMaxBytes, p.KeepMinCount, p.VerifyMode,
		p.TimeoutSeconds, p.MisfirePolicy, p.IsEnabled).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetPolicy(ctx, id)
}

func (s *Store) UpdatePolicy(ctx context.Context, p *domain.Policy) (*domain.Policy, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE policies SET cron_expr=$2, timezone=$3, dump_format=$4, compression=$5,
			encrypt=$6, retain_count=$7, retain_days=$8, retain_max_bytes=$9, keep_min_count=$10,
			verify_mode=$11, timeout_seconds=$12, misfire_policy=$13, is_enabled=$14, updated_at=now()
		WHERE id=$1`,
		p.ID, p.CronExpr, p.Timezone, p.DumpFormat, p.Compression, p.Encrypt, p.RetainCount,
		p.RetainDays, p.RetainMaxBytes, p.KeepMinCount, p.VerifyMode, p.TimeoutSeconds,
		p.MisfirePolicy, p.IsEnabled)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetPolicy(ctx, p.ID)
}

func (s *Store) DeletePolicy(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM policies WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListEnabledPolicies(ctx context.Context) ([]domain.Policy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+policyCols+`
		FROM policies p LEFT JOIN targets t ON t.id = p.target_id
		WHERE p.is_enabled AND t.is_enabled
		ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Policy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) TouchPolicyFired(ctx context.Context, id int64, next time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE policies SET last_fired_at=now(), next_run_at=$2 WHERE id=$1`, id, next)
	return err
}

func (s *Store) SetPolicyNextRun(ctx context.Context, id int64, next time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE policies SET next_run_at=$2 WHERE id=$1`, id, next)
	return err
}

func (s *Store) PolicyByTarget(ctx context.Context, targetID int64) (*domain.Policy, error) {
	return scanPolicy(s.pool.QueryRow(ctx, `
		SELECT `+policyCols+` FROM policies p LEFT JOIN targets t ON t.id=p.target_id
		WHERE p.target_id=$1 ORDER BY p.id LIMIT 1`, targetID))
}
