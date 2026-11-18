package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/weigong/dbbackups/internal/domain"
)

// ---------- jobs ----------

const jobCols = `j.id, j.policy_id, j.target_id, coalesce(t.name,''), j.trigger, j.status,
	j.started_at, j.finished_at, j.size_bytes, j.dump_sha256, j.tool_version, j.server_version,
	j.error_code, j.error_message, j.duration_ms, j.created_at`

func scanJob(row pgx.Row) (*domain.Job, error) {
	var j domain.Job
	err := row.Scan(&j.ID, &j.PolicyID, &j.TargetID, &j.TargetName, &j.Trigger, &j.Status,
		&j.StartedAt, &j.FinishedAt, &j.SizeBytes, &j.DumpSHA256, &j.ToolVersion, &j.ServerVersion,
		&j.ErrorCode, &j.ErrorMessage, &j.DurationMS, &j.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	return &j, nil
}

func (s *Store) CreateJob(ctx context.Context, policyID *int64, targetID int64, trigger string) (*domain.Job, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO jobs (policy_id, target_id, trigger, status)
		VALUES ($1, $2, $3, 'pending') RETURNING id`, policyID, targetID, trigger).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

func (s *Store) GetJob(ctx context.Context, id int64) (*domain.Job, error) {
	return scanJob(s.pool.QueryRow(ctx, `
		SELECT `+jobCols+` FROM jobs j LEFT JOIN targets t ON t.id=j.target_id WHERE j.id=$1`, id))
}

// MarkJobRunning 只有 pending 状态才能进入 running。
// 返回 false 说明任务已被取消或已结束，调用方必须立即退出，不能再执行备份。
func (s *Store) MarkJobRunning(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status='running', started_at=now() WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) CancelPendingJob(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE jobs SET status='canceled', finished_at=now(), error_code='CANCELED', error_message='排队期间被取消' WHERE id=$1 AND status='pending'`, id)
	return err
}

type JobResult struct {
	Status        string
	SizeBytes     int64
	SHA256        string
	ToolVersion   string
	ServerVersion string
	ErrorCode     string
	ErrorMessage  string
	DurationMS    int64
	StartedAt     *time.Time
}

func (s *Store) FinishJob(ctx context.Context, id int64, r JobResult) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status=$2, finished_at=now(), size_bytes=$3, dump_sha256=$4,
			tool_version=$5, server_version=$6, error_code=$7, error_message=$8, duration_ms=$9,
			started_at=coalesce(started_at, now())
		WHERE id=$1`,
		id, r.Status, r.SizeBytes, r.SHA256, r.ToolVersion, r.ServerVersion,
		r.ErrorCode, r.ErrorMessage, r.DurationMS)
	return err
}

// FailStaleJobs 在进程重启后把卡在 running/pending 的任务标记为失败，避免僵尸任务。
func (s *Store) FailStaleJobs(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status='failed', finished_at=now(),
			error_code='INTERRUPTED', error_message='进程重启，任务被中断'
		WHERE status IN ('pending','running') AND created_at < now() - interval '1 hour'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type JobFilter struct {
	TargetID int64
	Status   string
	Limit    int
	Offset   int
}

func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]domain.Job, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	where := `WHERE ($1 = 0 OR j.target_id = $1) AND ($2 = '' OR j.status = $2)`
	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs j `+where, f.TargetID, f.Status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+jobCols+` FROM jobs j LEFT JOIN targets t ON t.id=j.target_id `+where+`
		ORDER BY j.id DESC LIMIT $3 OFFSET $4`, f.TargetID, f.Status, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *j)
	}
	return out, total, rows.Err()
}

// ---------- artifacts ----------

func (s *Store) CreateArtifact(ctx context.Context, jobID int64, key string, size int64, sha string, enc bool) (*domain.Artifact, error) {
	var a domain.Artifact
	err := s.pool.QueryRow(ctx, `
		INSERT INTO artifacts (job_id, object_key, size_bytes, sha256, encrypted)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, job_id, object_key, size_bytes, sha256, encrypted, is_deleted, deleted_at, created_at`,
		jobID, key, size, sha, enc).
		Scan(&a.ID, &a.JobID, &a.ObjectKey, &a.SizeBytes, &a.SHA256, &a.Encrypted,
			&a.IsDeleted, &a.DeletedAt, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListArtifactsByJob(ctx context.Context, jobID int64) ([]domain.Artifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, job_id, object_key, size_bytes, sha256, encrypted, is_deleted, deleted_at, created_at
		FROM artifacts WHERE job_id=$1 AND NOT is_deleted ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Artifact{}
	for rows.Next() {
		var a domain.Artifact
		if err := rows.Scan(&a.ID, &a.JobID, &a.ObjectKey, &a.SizeBytes, &a.SHA256,
			&a.Encrypted, &a.IsDeleted, &a.DeletedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetArtifact(ctx context.Context, id int64) (*domain.Artifact, error) {
	var a domain.Artifact
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.job_id, a.object_key, a.size_bytes, a.sha256, a.encrypted,
		       a.is_deleted, a.deleted_at, a.created_at, j.target_id, coalesce(t.name,''), j.status
		FROM artifacts a JOIN jobs j ON j.id=a.job_id JOIN targets t ON t.id=j.target_id
		WHERE a.id=$1`, id).
		Scan(&a.ID, &a.JobID, &a.ObjectKey, &a.SizeBytes, &a.SHA256, &a.Encrypted,
			&a.IsDeleted, &a.DeletedAt, &a.CreatedAt, &a.TargetID, &a.TargetName, &a.JobStatus)
	if err != nil {
		return nil, wrap(err)
	}
	return &a, nil
}

// LiveArtifactsByTarget 供保留策略计算，按时间倒序（最新在前）。
type LiveArtifact struct {
	ID        int64
	JobID     int64
	ObjectKey string
	SizeBytes int64
	CreatedAt time.Time
	JobStatus string
	PolicyID  *int64
}

func (s *Store) LiveArtifactsByTarget(ctx context.Context, targetID int64) ([]LiveArtifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.job_id, a.object_key, a.size_bytes,
		       coalesce(j.finished_at, a.created_at) AS done_at, j.status, j.policy_id
		FROM artifacts a JOIN jobs j ON j.id = a.job_id
		WHERE j.target_id = $1 AND NOT a.is_deleted
		ORDER BY done_at DESC, a.id DESC`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveArtifact{}
	for rows.Next() {
		var a LiveArtifact
		if err := rows.Scan(&a.ID, &a.JobID, &a.ObjectKey, &a.SizeBytes, &a.CreatedAt,
			&a.JobStatus, &a.PolicyID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) MarkArtifactDeleted(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE artifacts SET is_deleted=TRUE, deleted_at=now() WHERE id=$1 AND NOT is_deleted`, id)
	return err
}

func (s *Store) HasVerifiedBackup(ctx context.Context, targetID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM verifications v
			JOIN jobs j ON j.id = v.job_id
			WHERE j.target_id=$1 AND v.status='success'
		)`, targetID).Scan(&ok)
	return ok, err
}

// ---------- verifications & audit ----------

func (s *Store) CreateVerification(ctx context.Context, jobID int64, mode, status string, durMS int64, report []byte) error {
	if len(report) == 0 {
		report = []byte(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO verifications (job_id, mode, status, duration_ms, report)
		VALUES ($1,$2,$3,$4,$5)`, jobID, mode, status, durMS, report)
	return err
}

func (s *Store) AddAudit(ctx context.Context, actor, action, ref, ip string, detail []byte) {
	if len(detail) == 0 {
		detail = []byte(`{}`)
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO audit_logs (actor, action, object_ref, ip, detail)
		VALUES ($1,$2,$3,$4,$5)`, actor, action, ref, ip, detail)
}

type AuditEntry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	ObjectRef string    `json:"object_ref"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor, action, object_ref, detail::text, ip, created_at
		FROM audit_logs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.ObjectRef, &a.Detail, &a.IP, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- stats ----------

type Stats struct {
	Targets       int   `json:"targets"`
	Policies      int   `json:"policies"`
	Jobs24h       int   `json:"jobs_24h"`
	Failed24h     int   `json:"failed_24h"`
	LiveBytes     int64 `json:"live_bytes"`
	LastSuccessAt int64 `json:"last_success_at"`
	RunningJobs   int   `json:"running_jobs"`
}

func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
		 (SELECT count(*) FROM targets),
		 (SELECT count(*) FROM policies WHERE is_enabled),
		 (SELECT count(*) FROM jobs WHERE created_at > now() - interval '24 hours'),
		 (SELECT count(*) FROM jobs WHERE status='failed' AND created_at > now() - interval '24 hours'),
		 (SELECT coalesce(sum(size_bytes),0) FROM artifacts WHERE NOT is_deleted),
		 (SELECT coalesce(max(extract(epoch FROM finished_at))::bigint,0) FROM jobs WHERE status='success'),
		 (SELECT count(*) FROM jobs WHERE status='running')`).
		Scan(&st.Targets, &st.Policies, &st.Jobs24h, &st.Failed24h, &st.LiveBytes,
			&st.LastSuccessAt, &st.RunningJobs)
	return &st, err
}

type DailyCount struct {
	Day     string `json:"day"`
	Total   int    `json:"total"`
	Success int    `json:"success"`
}

func (s *Store) DailyStats(ctx context.Context, days int) ([]DailyCount, error) {
	if days <= 0 || days > 90 {
		days = 7
	}
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(d.day, 'YYYY-MM-DD'),
		       count(j.id),
		       count(j.id) FILTER (WHERE j.status='success')
		FROM generate_series(current_date - ($1::int - 1), current_date, interval '1 day') AS d(day)
		LEFT JOIN jobs j ON j.created_at >= d.day AND j.created_at < d.day + interval '1 day'
		GROUP BY d.day ORDER BY d.day`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyCount{}
	for rows.Next() {
		var c DailyCount
		if err := rows.Scan(&c.Day, &c.Total, &c.Success); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
