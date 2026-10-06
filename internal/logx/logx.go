// Package logx 内存环形日志 + SSE 广播，用于后台实时日志。
package logx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"go-emby/internal/models"
)

const (
	maxEntries = 2000
	maxSubs    = 32
)

type hub struct {
	mu       sync.Mutex
	entries  []models.LogEntry
	seq      int64
	subs     map[chan models.LogEntry]struct{}
}

var h = &hub{subs: make(map[chan models.LogEntry]struct{})}

func init() { go worker() }

// push 写入一条日志并广播。
func push(level, msg, detail string) {
	h.mu.Lock()
	h.seq++
	e := models.LogEntry{Seq: h.seq, Level: level, Message: msg, Detail: detail, Time: time.Now()}
	h.entries = append(h.entries, e)
	if len(h.entries) > maxEntries {
		h.entries = h.entries[len(h.entries)-maxEntries:]
	}
	h.mu.Unlock()
	// 非阻塞广播
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
	stdPrint(e)
}

func stdPrint(e models.LogEntry) {
	ts := e.Time.Format("2006-01-02 15:04:05")
	switch e.Level {
	case "error":
		fmt.Fprintf(os.Stderr, "%s [ERROR] %s\n", ts, e.Message)
	case "warn":
		fmt.Fprintf(os.Stderr, "%s [WARN] %s\n", ts, e.Message)
	case "scan":
		fmt.Fprintf(os.Stdout, "%s [SCAN] %s\n", ts, e.Message)
	case "playback":
		fmt.Fprintf(os.Stdout, "%s [PLAY] %s\n", ts, e.Message)
	default:
		fmt.Fprintf(os.Stdout, "%s [INFO] %s\n", ts, e.Message)
	}
	if e.Detail != "" {
		fmt.Fprintf(os.Stdout, "%s        %s\n", ts, e.Detail)
	}
}

// Info / Warn / Error / Scan / Playback 便捷函数。
func Info(format string, a ...any)          { push("info", fmt.Sprintf(format, a...), "") }
func Warn(format string, a ...any)          { push("warn", fmt.Sprintf(format, a...), "") }
func Error(format string, a ...any)         { push("error", fmt.Sprintf(format, a...), "") }
func Scan(format string, a ...any)          { push("scan", fmt.Sprintf(format, a...), "") }
func Playback(format string, a ...any)      { push("playback", fmt.Sprintf(format, a...), "") }
func Detail(level, detail, format string, a ...any) { push(level, fmt.Sprintf(format, a...), detail) }

// worker 延迟初始化通道缓冲。
func worker() {}

// Subscribe 订阅实时日志。
func Subscribe() chan models.LogEntry {
	ch := make(chan models.LogEntry, 64)
	h.mu.Lock()
	if len(h.subs) < maxSubs {
		h.subs[ch] = struct{}{}
	}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅。
func Unsubscribe(ch chan models.LogEntry) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// Snapshot 返回最近 n 条日志（按时间倒序）。
func Snapshot(n int) []models.LogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]models.LogEntry, 0, len(h.entries))
	if n <= 0 || n > len(h.entries) {
		n = len(h.entries)
	}
	if n > 0 {
		out = append(out, h.entries[len(h.entries)-n:]...)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // 倒序（最新在前）
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Clear 清空日志。
func Clear() {
	h.mu.Lock()
	h.entries = nil
	h.mu.Unlock()
}

// ServeSSE 实时日志 SSE 端点。
func ServeSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 先发历史
	for _, e := range Snapshot(200) {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	fl.Flush()

	ch := Subscribe()
	defer Unsubscribe(ch)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// SortBySeq 排序辅助。
func SortBySeq(entries []models.LogEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Seq > entries[j].Seq })
}
