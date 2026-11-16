package backup

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/store"
)

type scheduled struct {
	c  *cron.Cron
	id cron.EntryID
}

// Scheduler 每条策略按自己的时区求值，因此按时区分组持有多个 cron 实例。
type Scheduler struct {
	store  *store.Store
	runner *Runner
	log    func(string)

	mu      sync.Mutex
	crons   map[string]*cron.Cron
	entries map[int64]scheduled
}

func NewScheduler(st *store.Store, runner *Runner, logf func(string)) *Scheduler {
	if logf == nil {
		logf = func(string) {}
	}
	return &Scheduler{
		store: st, runner: runner, log: logf,
		crons:   make(map[string]*cron.Cron),
		entries: make(map[int64]scheduled),
	}
}

// ValidateCron 供 API 复用，保证写入库的表达式一定能被调度器解析。
func ValidateCron(expr, tz string) error {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}
	p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := p.Parse(expr); err != nil {
		return err
	}
	_ = loc
	return nil
}

func (s *Scheduler) Start(ctx context.Context) error {
	return s.Reload(ctx)
}

func (s *Scheduler) Reload(ctx context.Context) error {
	policies, err := s.store.ListEnabledPolicies(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	for _, sc := range s.entries {
		sc.c.Remove(sc.id)
	}
	for tz, c := range s.crons {
		stopCtx := c.Stop()
		select {
		case <-stopCtx.Done():
		case <-time.After(3 * time.Second):
		}
		delete(s.crons, tz)
	}
	s.entries = make(map[int64]scheduled)
	s.mu.Unlock()

	for _, p := range policies {
		if err := s.add(p); err != nil {
			s.log("策略 " + policyLabel(p) + " 调度失败: " + err.Error())
		}
	}
	return nil
}

func (s *Scheduler) add(p domain.Policy) error {
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		loc = time.Local
	}

	s.mu.Lock()
	c, ok := s.crons[p.Timezone]
	if !ok {
		c = cron.New(cron.WithLocation(loc), cron.WithChain(cron.SkipIfStillRunning(cron.DiscardLogger)))
		c.Start()
		s.crons[p.Timezone] = c
	}
	s.mu.Unlock()

	policyID := p.ID
	id, err := c.AddFunc(p.CronExpr, func() { s.fire(policyID) })
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.entries[p.ID] = scheduled{c: c, id: id}
	s.mu.Unlock()

	if next := c.Entry(id).Next; !next.IsZero() {
		_ = s.store.SetPolicyNextRun(context.Background(), p.ID, next)
	}
	return nil
}

func (s *Scheduler) fire(policyID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := s.store.GetPolicy(ctx, policyID)
	if err != nil || !p.IsEnabled {
		return
	}
	job, err := s.store.CreateJob(ctx, &p.ID, p.TargetID, domain.TriggerSchedule)
	if err != nil {
		s.log("创建定时任务失败: " + err.Error())
		return
	}
	s.log("触发定时备份 #" + itoa(job.ID) + " → " + p.TargetName)

	s.mu.Lock()
	sc, ok := s.entries[p.ID]
	s.mu.Unlock()
	if ok {
		if next := sc.c.Entry(sc.id).Next; !next.IsZero() {
			_ = s.store.TouchPolicyFired(context.Background(), p.ID, next)
		}
	}

	s.runner.Submit(job.ID)
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tz, c := range s.crons {
		c.Stop()
		delete(s.crons, tz)
	}
	s.entries = make(map[int64]scheduled)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func policyLabel(p domain.Policy) string {
	if p.TargetName != "" {
		return p.TargetName
	}
	return itoa(p.ID)
}
