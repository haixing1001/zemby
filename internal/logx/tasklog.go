// Package logx 任务级内存日志环：为每个后台任务（扫描/刮削/提取）保留最近的详细日志，
// 供控制台任务状态、刮削管理、媒体信息页按任务展示。
package logx

import (
	"fmt"
	"sync"
	"time"
)

// TaskLine 任务日志行。
type TaskLine struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"` // info | warn | error
	Msg   string    `json:"msg"`
}

// TaskRing 单任务的环形日志缓冲（并发安全）。
type TaskRing struct {
	mu    sync.Mutex
	lines []TaskLine
	cap   int
}

// NewTaskRing 创建指定容量的任务日志环。
func NewTaskRing(capacity int) *TaskRing {
	if capacity < 64 {
		capacity = 64
	}
	return &TaskRing{cap: capacity}
}

// Addf 追加一行日志（超容量丢弃最旧行）。
func (t *TaskRing) Addf(level, format string, a ...any) {
	line := TaskLine{Time: time.Now(), Level: level, Msg: fmt.Sprintf(format, a...)}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) >= t.cap {
		t.lines = t.lines[len(t.lines)-t.cap+1:]
	}
	t.lines = append(t.lines, line)
}

// Snapshot 返回最近 n 行（时间正序）；n<=0 返回全部。
func (t *TaskRing) Snapshot(n int) []TaskLine {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n <= 0 || n > len(t.lines) {
		n = len(t.lines)
	}
	out := make([]TaskLine, n)
	copy(out, t.lines[len(t.lines)-n:])
	return out
}

// Len 当前行数。
func (t *TaskRing) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.lines)
}

var (
	ringsMu  sync.Mutex
	rings    = map[string]*TaskRing{}
	ringKeys []string
	maxRings = 48 // 最多同时保留的任务环数（FIFO 淘汰）
)

// TaskRingSet 注册一个已创建的环（同名覆盖，不参与容量统计会重复，因此覆盖时先清理旧键序）。
func TaskRingSet(key string, r *TaskRing) {
	ringsMu.Lock()
	defer ringsMu.Unlock()
	if _, ok := rings[key]; !ok {
		ringKeys = append(ringKeys, key)
	}
	rings[key] = r
	for len(ringKeys) > maxRings {
		old := ringKeys[0]
		ringKeys = ringKeys[1:]
		delete(rings, old)
	}
}

// TaskRingGet 取任务日志环（get-or-create，默认容量 800）。
func TaskRingGet(key string) *TaskRing {
	ringsMu.Lock()
	defer ringsMu.Unlock()
	if r, ok := rings[key]; ok {
		return r
	}
	r := NewTaskRing(800)
	rings[key] = r
	ringKeys = append(ringKeys, key)
	for len(ringKeys) > maxRings {
		old := ringKeys[0]
		ringKeys = ringKeys[1:]
		delete(rings, old)
	}
	return r
}

// TaskRingHas 判断环是否存在。
func TaskRingHas(key string) bool {
	ringsMu.Lock()
	defer ringsMu.Unlock()
	_, ok := rings[key]
	return ok
}

// TaskLog 向任务日志环追加一行（get-or-create）。
func TaskLog(key, level, format string, a ...any) {
	TaskRingGet(key).Addf(level, format, a...)
}

// TaskLogSnapshot 读取任务日志最近 n 行（时间正序）；环不存在返回 nil。
func TaskLogSnapshot(key string, n int) []TaskLine {
	ringsMu.Lock()
	_, ok := rings[key]
	ringsMu.Unlock()
	if !ok {
		return nil
	}
	return TaskRingGet(key).Snapshot(n)
}

// TaskRingDrop 删除任务日志环。
func TaskRingDrop(key string) {
	ringsMu.Lock()
	defer ringsMu.Unlock()
	delete(rings, key)
	for i, k := range ringKeys {
		if k == key {
			ringKeys = append(ringKeys[:i], ringKeys[i+1:]...)
			break
		}
	}
}
