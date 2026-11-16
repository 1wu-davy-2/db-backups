package domain

import "time"

const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
)

const (
	JobPending  = "pending"
	JobRunning  = "running"
	JobSuccess  = "success"
	JobFailed   = "failed"
	JobCanceled = "canceled"
)

const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	TriggerRetry    = "retry"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// DumpFormat 目前只支持 custom（-Fc）。
const (
	FormatCustom    = "custom"
	FormatDirectory = "directory"
)

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type Target struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Engine        string    `json:"engine"`
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	DBName        string    `json:"dbname"`
	Username      string    `json:"username"`
	SSLMode       string    `json:"ssl_mode"`
	ServerVersion string    `json:"server_version"`
	ServerVerNum  int       `json:"server_version_num"`
	DBSizeBytes   int64     `json:"db_size_bytes"`
	Tags          []string  `json:"tags"`
	IsEnabled     bool      `json:"is_enabled"`
	LastProbeAt   time.Time `json:"last_probe_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type Policy struct {
	ID             int64     `json:"id"`
	TargetID       int64     `json:"target_id"`
	TargetName     string    `json:"target_name,omitempty"`
	CronExpr       string    `json:"cron_expr"`
	Timezone       string    `json:"timezone"`
	Kind           string    `json:"kind"`
	DumpFormat     string    `json:"dump_format"`
	Compression    string    `json:"compression"`
	Encrypt        bool      `json:"encrypt"`
	RetainCount    int       `json:"retain_count"`
	RetainDays     int       `json:"retain_days"`
	RetainMaxBytes int64     `json:"retain_max_bytes"`
	KeepMinCount   int       `json:"keep_min_count"`
	VerifyMode     string    `json:"verify_mode"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	MisfirePolicy  string    `json:"misfire_policy"`
	IsEnabled      bool      `json:"is_enabled"`
	NextRunAt      time.Time `json:"next_run_at"`
	LastFiredAt    time.Time `json:"last_fired_at"`
	CreatedAt      time.Time `json:"created_at"`
}

type Job struct {
	ID            int64      `json:"id"`
	PolicyID      *int64     `json:"policy_id"`
	TargetID      int64      `json:"target_id"`
	TargetName    string     `json:"target_name,omitempty"`
	Trigger       string     `json:"trigger"`
	Status        string     `json:"status"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	SizeBytes     int64      `json:"size_bytes"`
	DumpSHA256    string     `json:"dump_sha256"`
	ToolVersion   string     `json:"tool_version"`
	ServerVersion string     `json:"server_version"`
	ErrorCode     string     `json:"error_code"`
	ErrorMessage  string     `json:"error_message"`
	DurationMS    int64      `json:"duration_ms"`
	CreatedAt     time.Time  `json:"created_at"`
}

type Artifact struct {
	ID          int64      `json:"id"`
	JobID       int64      `json:"job_id"`
	ObjectKey   string     `json:"object_key"`
	SizeBytes   int64      `json:"size_bytes"`
	SHA256      string     `json:"sha256"`
	Encrypted   bool       `json:"encrypted"`
	IsDeleted   bool       `json:"is_deleted"`
	DeletedAt   *time.Time `json:"deleted_at"`
	CreatedAt   time.Time  `json:"created_at"`
	TargetID    int64      `json:"-"`
	TargetName  string     `json:"-"`
	PolicyID    *int64     `json:"-"`
	JobStatus   string     `json:"-"`
	JobFinished *time.Time `json:"-"`
}

type Verification struct {
	ID         int64     `json:"id"`
	JobID      int64     `json:"job_id"`
	Mode       string    `json:"mode"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	Report     string    `json:"report"`
	CreatedAt  time.Time `json:"created_at"`
}

type AuditLog struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	ObjectRef string    `json:"object_ref"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
}
