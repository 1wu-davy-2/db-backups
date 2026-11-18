package backup

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weigong/dbbackups/internal/domain"
	"github.com/weigong/dbbackups/internal/store"
)

func TestCancelQueuedBackup(t *testing.T) {
	if os.Getenv("BACKUP_TEST_METADATA") == "" {
		t.Skip("set BACKUP_TEST_METADATA for isolated fixture metadata")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	pool, err := store.Open(ctx, os.Getenv("BACKUP_TEST_METADATA"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool)
	var targetID int64
	if err := pool.QueryRow(ctx, "SELECT id FROM targets WHERE name='integration-postgres'").Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateJob(ctx, nil, targetID, domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(st, nil, nil, nil, nil, NewLogHub(), t.TempDir(), 1)
	r.sem <- struct{}{}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, job.ID) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		registered := r.cancel[job.ID] != nil
		r.mu.Unlock()
		if registered {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Cancel(job.ID)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "CANCELED") {
			t.Fatalf("unexpected result: %v", err)
		}
		stored, err := st.GetJob(context.Background(), job.ID)
		if err != nil || stored.Status != domain.JobCanceled {
			t.Fatalf("job=%+v err=%v", stored, err)
		}
	case <-time.After(time.Second):
		stop()
		<-done
		t.Fatal("queued backup did not respond to cancellation")
	}
}
