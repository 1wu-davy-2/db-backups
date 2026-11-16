package backup

import (
	"testing"
	"time"
)

func mk(id int64, daysAgo int, size int64, status string) Candidate {
	return Candidate{
		ID:        id,
		CreatedAt: time.Now().Add(-time.Duration(daysAgo) * 24 * time.Hour),
		SizeBytes: size,
		JobStatus: status,
	}
}

func ids(cs []Candidate) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestRetentionByCount(t *testing.T) {
	rule := RetentionRule{Count: 2, KeepMin: 1}
	plan := PlanRetention([]Candidate{
		mk(5, 0, 100, "success"), mk(4, 1, 100, "success"), mk(3, 2, 100, "success"),
		mk(2, 3, 100, "success"), mk(1, 4, 100, "success"),
	}, rule, time.Now())

	if got := len(plan.Keep); got != 2 {
		t.Fatalf("keep=%d want 2 (%v)", got, ids(plan.Keep))
	}
	if got := len(plan.Kill); got != 3 {
		t.Fatalf("kill=%d want 3", got)
	}
}

func TestRetentionByDays(t *testing.T) {
	rule := RetentionRule{Days: 7, KeepMin: 1}
	plan := PlanRetention([]Candidate{
		mk(4, 0, 10, "success"), mk(3, 3, 10, "success"),
		mk(2, 8, 10, "success"), mk(1, 30, 10, "success"),
	}, rule, time.Now())

	if len(plan.Keep) != 2 {
		t.Fatalf("keep=%v want [4 3]", ids(plan.Keep))
	}
	if len(plan.Kill) != 2 {
		t.Fatalf("kill=%v want 2", ids(plan.Kill))
	}
}

func TestRetentionByMaxBytes(t *testing.T) {
	rule := RetentionRule{MaxBytes: 300, KeepMin: 1}
	plan := PlanRetention([]Candidate{
		mk(5, 0, 100, "success"), mk(4, 1, 100, "success"), mk(3, 2, 100, "success"),
		mk(2, 3, 100, "success"), mk(1, 4, 100, "success"),
	}, rule, time.Now())

	if len(plan.Keep) != 3 {
		t.Fatalf("keep=%v want 3 items", ids(plan.Keep))
	}
}

func TestKeepMinProtectsNewest(t *testing.T) {
	rule := RetentionRule{Count: 1, Days: 1, MaxBytes: 1, KeepMin: 3}
	plan := PlanRetention([]Candidate{
		mk(5, 0, 1000, "success"), mk(4, 10, 1000, "success"), mk(3, 20, 1000, "success"),
		mk(2, 30, 1000, "success"),
	}, rule, time.Now())

	if len(plan.Keep) != 3 {
		t.Fatalf("keep=%v want newest 3", ids(plan.Keep))
	}
	if len(plan.Kill) != 1 || plan.Kill[0].ID != 2 {
		t.Fatalf("kill=%v want [2]", ids(plan.Kill))
	}
}

func TestRunningNeverDeleted(t *testing.T) {
	rule := RetentionRule{Count: 1, KeepMin: 1}
	plan := PlanRetention([]Candidate{
		mk(5, 0, 100, "success"), mk(4, 1, 100, "running"), mk(3, 2, 100, "success"),
	}, rule, time.Now())

	for _, k := range plan.Kill {
		if k.ID == 4 {
			t.Fatal("running 状态的备份不应被删除")
		}
	}
}

func TestHasSuccessful(t *testing.T) {
	plan := RetentionPlan{Keep: []Candidate{{ID: 1, JobStatus: "failed"}}}
	if plan.HasSuccessful() {
		t.Fatal("全部失败时不应返回 true")
	}
	plan.Keep = append(plan.Keep, Candidate{ID: 2, JobStatus: "success"})
	if !plan.HasSuccessful() {
		t.Fatal("存在成功备份时应返回 true")
	}
}
