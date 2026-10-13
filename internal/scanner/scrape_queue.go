package scanner

import "sync"
import "sync/atomic"

// scrapeTaskQueue is an in-memory, unbounded FIFO. A bounded channel silently
// lost scrape jobs when a large library filled the 4096-entry buffer.
type scrapeTaskQueue struct {
	mu      sync.Mutex
	ready   *sync.Cond
	tasks   []scrapeTask
	head    int
	pending map[string]struct{}
	waiting atomic.Int64 // 队列中排队任务数（原子读，供任务状态展示，避免锁序冲突）
}

func newScrapeTaskQueue() *scrapeTaskQueue {
	q := &scrapeTaskQueue{pending: make(map[string]struct{})}
	q.ready = sync.NewCond(&q.mu)
	return q
}

// Len 队列中排队任务数（不含正在处理的）。
func (q *scrapeTaskQueue) Len() int { return int(q.waiting.Load()) }

func (q *scrapeTaskQueue) Push(task scrapeTask) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if task.itemID == "" {
		return false
	}
	if _, exists := q.pending[task.itemID]; exists {
		return false
	}
	q.pending[task.itemID] = struct{}{}
	q.tasks = append(q.tasks, task)
	q.waiting.Add(1)
	q.ready.Signal()
	return true
}

func (q *scrapeTaskQueue) Pop() scrapeTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.head == len(q.tasks) {
		q.ready.Wait()
	}
	task := q.tasks[q.head]
	q.tasks[q.head] = scrapeTask{}
	q.head++
	q.waiting.Add(-1)
	if q.head == len(q.tasks) {
		q.tasks = nil
		q.head = 0
	} else if q.head >= 1024 && q.head*2 >= len(q.tasks) {
		q.tasks = append([]scrapeTask(nil), q.tasks[q.head:]...)
		q.head = 0
	}
	return task
}

func (q *scrapeTaskQueue) Done(itemID string) {
	q.mu.Lock()
	delete(q.pending, itemID)
	q.mu.Unlock()
}

// Drain removes queued work but leaves tasks already claimed by workers alone.
func (q *scrapeTaskQueue) Drain() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.tasks) - q.head
	for i := q.head; i < len(q.tasks); i++ {
		delete(q.pending, q.tasks[i].itemID)
	}
	q.tasks = nil
	q.head = 0
	q.waiting.Add(int64(-n))
	return n
}
