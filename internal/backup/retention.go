package backup

import (
	"sort"
	"time"
)

// Candidate 是参与保留计算的一份备份。
type Candidate struct {
	ID        int64
	CreatedAt time.Time
	SizeBytes int64
	JobStatus string
	ObjectKey string
}

// RetentionRule 三个条件任一命中即过期。
type RetentionRule struct {
	Count    int
	Days     int
	MaxBytes int64
	KeepMin  int
}

type RetentionPlan struct {
	Keep []Candidate
	Kill []Candidate
}

// PlanRetention 是纯函数：不碰 IO，便于单测覆盖边界。
// 排序假设：入参按时间倒序，函数内部仍会再排一次以保证正确性。
func PlanRetention(items []Candidate, rule RetentionRule, now time.Time) RetentionPlan {
	sorted := make([]Candidate, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].ID > sorted[j].ID
		}
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})

	keepMin := rule.KeepMin
	if keepMin < 1 {
		keepMin = 1 // 安全底线：无论配置如何，至少留一份
	}

	var plan RetentionPlan
	var acc int64
	keptCount := 0

	for i, it := range sorted {
		// 硬保护：最新 N 份、以及正在执行的任务产物
		protected := i < keepMin || it.JobStatus == "running" || it.JobStatus == "pending"
		if protected {
			plan.Keep = append(plan.Keep, it)
			acc += it.SizeBytes
			keptCount++
			continue
		}

		tooOld := rule.Days > 0 && now.Sub(it.CreatedAt) > time.Duration(rule.Days)*24*time.Hour
		tooMany := rule.Count > 0 && keptCount >= rule.Count
		tooBig := rule.MaxBytes > 0 && acc+it.SizeBytes > rule.MaxBytes

		if tooOld || tooMany || tooBig {
			plan.Kill = append(plan.Kill, it)
			continue
		}
		plan.Keep = append(plan.Keep, it)
		acc += it.SizeBytes
		keptCount++
	}
	return plan
}

// HasSuccessful 判断保留集合里是否还有一份成功备份。
// 若没有，GC 必须暂停：这说明最近的备份全挂了，此时删旧的等于自断后路。
func (p RetentionPlan) HasSuccessful() bool {
	for _, it := range p.Keep {
		if it.JobStatus == "success" {
			return true
		}
	}
	return false
}
