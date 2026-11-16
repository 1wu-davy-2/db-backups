package backup

import (
	"sync"
	"time"
)

const (
	maxRingLines = 300
	maxJobsKept  = 200
)

// LogHub 负责实时日志分发：SSE 订阅 + 断线重连补发历史。
// 采用"满了就丢"策略，绝不让日志反压备份进程。
type LogHub struct {
	mu   sync.Mutex
	subs map[int64]map[chan string]struct{}
	ring map[int64][]string
}

func NewLogHub() *LogHub {
	return &LogHub{
		subs: make(map[int64]map[chan string]struct{}),
		ring: make(map[int64][]string),
	}
}

func (h *LogHub) Publish(jobID int64, line string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	buf := h.ring[jobID]
	buf = append(buf, line)
	if len(buf) > maxRingLines {
		buf = append(buf[:0], buf[len(buf)-maxRingLines:]...)
	}
	h.ring[jobID] = buf

	for ch := range h.subs[jobID] {
		select {
		case ch <- line:
		default: // 订阅端读不过来就丢，不阻塞
		}
	}
}

func (h *LogHub) Subscribe(jobID int64) (<-chan string, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan string, 128)
	if h.subs[jobID] == nil {
		h.subs[jobID] = make(map[chan string]struct{})
	}
	h.subs[jobID][ch] = struct{}{}

	history := make([]string, len(h.ring[jobID]))
	copy(history, h.ring[jobID])
	for _, line := range history {
		select {
		case ch <- line:
		default:
		}
	}

	cancel := func() {
		h.mu.Lock()
		if m := h.subs[jobID]; m != nil {
			if _, ok := m[ch]; ok {
				delete(m, ch)
				close(ch)
			}
			if len(m) == 0 {
				delete(h.subs, jobID)
			}
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// History 供 HTTP 一次性拉取已完成任务的日志。
func (h *LogHub) History(jobID int64) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.ring[jobID]))
	copy(out, h.ring[jobID])
	return out
}

// Release 在任务结束后延迟回收内存，避免长期运行累积。
func (h *LogHub) Release(jobID int64) {
	time.AfterFunc(10*time.Minute, func() {
		h.mu.Lock()
		delete(h.ring, jobID)
		if m := h.subs[jobID]; len(m) == 0 {
			delete(h.subs, jobID)
		}
		h.mu.Unlock()
	})
}
