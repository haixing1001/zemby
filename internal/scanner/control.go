// Package scanner 运行控制：刮削状态机、探测并发池、实时监控、批量提取、配置读写。
package scanner

import (
        "context"
        "encoding/json"
        "fmt"
        "os"
        "path/filepath"
        "strings"
        "sync"
        "sync/atomic"
        "time"

        "go-emby/internal/db"
        "go-emby/internal/logx"
        "go-emby/internal/models"
)

// ============================================================
// KV 配置读写（复用 Setting 表）
// ============================================================

func kvGet(key string, out any) bool {
        var s models.Setting
        if err := db.DB.First(&s, "key = ?", key).Error; err != nil {
                return false
        }
        return json.Unmarshal([]byte(s.Value), out) == nil
}

func kvPut(key string, v any) {
        b, _ := json.Marshal(v)
        db.DB.Save(&models.Setting{Key: key, Value: string(b)})
}

// ScrapeConfig 刮削管理配置。
type ScrapeConfig struct {
        Enabled   bool   `json:"enabled"`
        Realtime  bool   `json:"realtime"`
        AutoRefre bool   `json:"autoRefresh"`
        Manual    bool   `json:"manual"`
        Overwrite string `json:"overwrite"` // skip | overwrite
}

// LoadScrapeConfig 读取刮削配置（默认值：开启刮削、自动刷新、跳过已有）。
func LoadScrapeConfig() ScrapeConfig {
        c := ScrapeConfig{Enabled: true, Realtime: false, AutoRefre: true, Manual: true, Overwrite: "skip"}
        kvGet("scrape_config", &c)
        if c.Overwrite != "overwrite" {
                c.Overwrite = "skip"
        }
        return c
}

// SaveScrapeConfig 保存刮削配置。
func SaveScrapeConfig(c ScrapeConfig) { kvPut("scrape_config", c) }

// ProbeConfig 媒体信息提取配置。
type ProbeConfig struct {
        OnBrowse    bool   `json:"onBrowse"`
        PreloadNext bool   `json:"preloadNext"`
        Persist     bool   `json:"persist"`
        SaveDir     string `json:"saveDir"`
        Concurrency int    `json:"concurrency"`
}

// appDataDir 数据目录（由 New 注入）。
var dataDirOnce sync.Once
var dataDirValue string

func appDataDir() string { return dataDirValue }

// LoadProbeConfig 读取探测配置（默认：浏览提取+预加载开启，并发 2）。
func LoadProbeConfig() ProbeConfig {
        c := ProbeConfig{OnBrowse: true, PreloadNext: true, Persist: false, SaveDir: "", Concurrency: 2}
        kvGet("probe_config", &c)
        if c.Concurrency < 1 {
                c.Concurrency = 1
        }
        if c.Concurrency > 8 {
                c.Concurrency = 8
        }
        if c.SaveDir == "" {
                c.SaveDir = filepath.Join(appDataDir(), "media-info")
        }
        return c
}

// SaveProbeConfig 保存探测配置。
func SaveProbeConfig(c ProbeConfig) { kvPut("probe_config", c) }

// ============================================================
// 刮削状态机：idle -> running <-> paused，stopped 清空队列
// ============================================================

var (
        scrapeStateMu sync.Mutex
        scrapeState   = "idle" // idle | running | paused
        scrapeResume  chan struct{}
)

func waitWhilePaused() {
        scrapeStateMu.Lock()
        if scrapeState != "paused" {
                scrapeStateMu.Unlock()
                return
        }
        ch := scrapeResume
        scrapeStateMu.Unlock()
        if ch != nil {
                select {
                case <-ch:
                case <-time.After(10 * time.Minute): // 防泄漏：暂停超时自动继续
                        SetScrapeState("running")
                }
        }
}

// SetScrapeState 设置刮削任务状态。
func SetScrapeState(st string) {
        scrapeStateMu.Lock()
        defer scrapeStateMu.Unlock()
        switch st {
        case "running":
                if scrapeState == "paused" && scrapeResume != nil {
                        close(scrapeResume)
                        scrapeResume = nil
                }
                scrapeState = "running"
        case "paused":
                if scrapeState != "paused" {
                        scrapeResume = make(chan struct{})
                        scrapeState = "paused"
                }
        default: // idle / stopped
                if scrapeState == "paused" && scrapeResume != nil {
                        close(scrapeResume)
                        scrapeResume = nil
                }
                scrapeState = "idle"
        }
}

// GetScrapeState 当前状态。
func GetScrapeState() string {
        scrapeStateMu.Lock()
        defer scrapeStateMu.Unlock()
        return scrapeState
}

// EnqueueAllUnscraped 将所有未刮削条目入队，返回入队数量。
func (s *Scanner) EnqueueAllUnscraped() int {
        var ids []string
        db.DB.Model(&models.Item{}).Where("type IN ? AND scraped = ?", []string{"Movie", "Series"}, false).Pluck("id", &ids)
        n := 0
        for _, id := range ids {
                select {
                case s.scrapeQueue <- scrapeTask{itemID: id}:
                        n++
                default:
                }
        }
        if n > 0 {
                SetScrapeState("running")
        }
        return n
}

// DrainScrape 清空刮削队列，返回丢弃数量。
func (s *Scanner) DrainScrape() int {
        n := 0
        for {
                select {
                case <-s.scrapeQueue:
                        n++
                default:
                        probeWaiting.Add(int64(-n))
                        return n
                }
        }
}

// RetryFailedScrape 重试全部失败条目：清除错误标记并入队。
func (s *Scanner) RetryFailedScrape() int {
        var ids []string
        db.DB.Model(&models.Item{}).Where("type IN ? AND scraped = ? AND scrape_error != ''", []string{"Movie", "Series"}, false).Pluck("id", &ids)
        if len(ids) == 0 {
                return 0
        }
        db.DB.Model(&models.Item{}).Where("id IN ?", ids).Update("scrape_error", "")
        n := 0
        for _, id := range ids {
                select {
                case s.scrapeQueue <- scrapeTask{itemID: id}:
                        n++
                default:
                }
        }
        if n > 0 {
                SetScrapeState("running")
        }
        return n
}

// FailedScrapes 失败清单。
func FailedScrapes(limit int) []map[string]any {
        var items []models.Item
        db.DB.Where("type IN ? AND scraped = ? AND scrape_error != ''", []string{"Movie", "Series"}, false).
                Order("updated_at DESC").Limit(limit).Find(&items)
        out := []map[string]any{}
        for i := range items {
                out = append(out, map[string]any{
                        "ID": items[i].ID, "Name": items[i].Name, "Type": items[i].Type,
                        "Path": items[i].Path, "Year": items[i].Year, "Error": items[i].ScrapeError,
                })
        }
        return out
}

// ============================================================
// 探测并发池与计数器
// ============================================================

var (
        probeWaiting    atomic.Int64 // 队列等待
        probeRunning    atomic.Int64 // 正在提取
        probeDone       atomic.Int64 // 完成
        probeFailed     atomic.Int64 // 失败
        probeSkipped    atomic.Int64 // 跳过（远程流等）
        probePoolMu     sync.Mutex
        probePoolCtx    context.Context
        probePoolCancel context.CancelFunc
        probeConcurrency atomic.Int64
        batchRunning     atomic.Bool
        batchStop        atomic.Bool
)

// StartWorkers 启动探测/刮削工作线程（按配置并发）。
func (s *Scanner) StartWorkers() {
        cfg := LoadProbeConfig()
        s.syncProbePool(cfg.Concurrency)
        for i := 0; i < 2; i++ {
                go s.scrapeWorker()
        }
}

// syncProbePool 调整探测并发数。
func (s *Scanner) syncProbePool(n int) {
        probePoolMu.Lock()
        defer probePoolMu.Unlock()
        if probePoolCancel != nil {
                probePoolCancel()
        }
        ctx, cancel := context.WithCancel(context.Background())
        probePoolCtx, probePoolCancel = ctx, cancel
        probeConcurrency.Store(int64(n))
        for i := 0; i < n; i++ {
                go s.probeWorkerPooled(ctx)
        }
        logx.Info("媒体信息提取并发调整为 %d", n)
}

// SetProbeConcurrency 更新并发配置并重建线程池。
func (s *Scanner) SetProbeConcurrency(n int) {
        if n < 1 {
                n = 1
        }
        if n > 8 {
                n = 8
        }
        cfg := LoadProbeConfig()
        cfg.Concurrency = n
        SaveProbeConfig(cfg)
        s.syncProbePool(n)
}

// probeWorkerPooled 带取消的探测线程。
func (s *Scanner) probeWorkerPooled(ctx context.Context) {
        for {
                select {
                case <-ctx.Done():
                        return
                case t, ok := <-s.probeQueue:
                        if !ok {
                                return
                        }
                        if ctx.Err() != nil {
                                return
                        }
                        s.doProbe(t)
                }
        }
}

// BatchProbeStart 批量提取：所有缺流信息的本地条目入队。
func (s *Scanner) BatchProbeStart() (int, error) {
        if !hasFFprobe {
                return 0, fmt.Errorf("服务器未安装 ffprobe")
        }
        var ids []string
        db.DB.Model(&models.Item{}).Where("type IN ?", []string{"Movie", "Episode"}).Pluck("id", &ids)
        n := 0
        for _, id := range ids {
                var it models.Item
                if db.DB.First(&it, "id = ?", id).Error != nil {
                        continue
                }
                var src models.MediaSource
                if db.DB.Where("item_id = ?", it.ID).First(&src).Error != nil {
                        continue
                }
                if strings.HasPrefix(src.Path, "http://") || strings.HasPrefix(src.Path, "https://") {
                        probeSkipped.Add(1)
                        continue
                }
                var cnt int64
                db.DB.Model(&models.MediaStream{}).Where("item_id = ? AND is_external = ?", it.ID, false).Count(&cnt)
                if cnt > 0 {
                        continue // 已有完整信息
                }
                select {
                case s.probeQueue <- probeTask{sourceID: src.ID, itemID: it.ID, path: src.Path, mtime: it.Mtime}:
                        n++
                default:
                }
        }
        batchRunning.Store(true)
        batchStop.Store(false)
        if n > 0 {
                logx.Info("批量提取媒体信息：%d 个条目入队", n)
        }
        return n, nil
}

// BatchProbeStop 停止批量提取（清空队列）。
func (s *Scanner) BatchProbeStop() {
        batchRunning.Store(false)
        batchStop.Store(true)
        for {
                select {
                case <-s.probeQueue:
                        probeSkipped.Add(1)
                default:
                        return
                }
        }
}

// ProbeStatus 探测状态汇总。
func ProbeStatus() map[string]any {
        cfg := LoadProbeConfig()
        state := "idle"
        if probeRunning.Load() > 0 {
                state = "extracting"
        }
        if probeWaiting.Load() > 0 {
                state = "running"
        }
        if batchStop.Load() {
                state = "stopped"
        }
        return map[string]any{
                "State":       state,
                "Waiting":     probeWaiting.Load(),
                "Running":     probeRunning.Load(),
                "Completed":   probeDone.Load(),
                "Failed":      probeFailed.Load(),
                "Skipped":     probeSkipped.Load(),
                "Concurrency": cfg.Concurrency,
                "BatchRunning": batchRunning.Load(),
        }
}

// ============================================================
// 实时监控（定时增量扫描代替文件系统监听，容器内挂载不可靠）
// ============================================================

var realtimeMu sync.Mutex
var realtimeStop chan struct{}

// SetRealtime 开/关实时监控：每 2 分钟增量扫描全部媒体库。
func (s *Scanner) SetRealtime(on bool) {
        realtimeMu.Lock()
        defer realtimeMu.Unlock()
        if realtimeStop != nil {
                close(realtimeStop)
                realtimeStop = nil
        }
        if !on {
                return
        }
        stop := make(chan struct{})
        realtimeStop = stop
        go func() {
                t := time.NewTicker(2 * time.Minute)
                defer t.Stop()
                for {
                        select {
                        case <-stop:
                                return
                        case <-t.C:
                                var libs []models.Library
                                db.DB.Find(&libs)
                                for i := range libs {
                                        if s.IsScanning(libs[i].ID) {
                                                continue
                                        }
                                        _ = s.ScanLibrary(libs[i].ID, "update")
                                }
                        }
                }
        }()
        logx.Info("实时监控已开启：每 2 分钟增量扫描")
}

// ============================================================
// 浏览时提取 / 预加载下一集
// ============================================================

var probeDedupMu sync.Mutex
var probeDedup = map[string]bool{}

// TryProbeOnBrowse 浏览详情时若缺流信息则后台补提取（带去重）。
func (s *Scanner) TryProbeOnBrowse(itemID string) {
        cfg := LoadProbeConfig()
        if !cfg.OnBrowse || !hasFFprobe {
                return
        }
        probeDedupMu.Lock()
        if probeDedup[itemID] {
                probeDedupMu.Unlock()
                return
        }
        probeDedupMu.Unlock()

        var it models.Item
        if db.DB.First(&it, "id = ?", itemID).Error != nil {
                return
        }
        var src models.MediaSource
        if db.DB.Where("item_id = ?", it.ID).First(&src).Error != nil {
                return
        }
        if strings.HasPrefix(src.Path, "http://") || strings.HasPrefix(src.Path, "https://") {
                return
        }
        var cnt int64
        db.DB.Model(&models.MediaStream{}).Where("item_id = ? AND is_external = ?", it.ID, false).Count(&cnt)
        if cnt > 0 {
                return
        }
        probeDedupMu.Lock()
        probeDedup[itemID] = true
        probeDedupMu.Unlock()
        s.enqueueProbe(&it, src, it.Mtime)
}

// PreloadNext 播放开始后预提取同剧下一集的媒体信息。
func (s *Scanner) PreloadNext(seriesID string, season, episode int) {
        cfg := LoadProbeConfig()
        if !cfg.PreloadNext || !hasFFprobe || seriesID == "" {
                return
        }
        var next models.Item
        q := db.DB.Where("series_id = ? AND type = 'Episode'", seriesID)
        if season > 0 && episode > 0 {
                q = q.Where("(parent_index_number > ?) OR (parent_index_number = ? AND index_number > ?)",
                        season, season, episode)
        } else {
                return
        }
        if err := q.Order("parent_index_number, index_number").First(&next).Error; err != nil {
                return
        }
        s.TryProbeOnBrowse(next.ID)
}

// ============================================================
// 媒体信息持久化（条目移除后保留，重新入库复用）
// ============================================================

// persistMediaInfo 将条目流信息保存为 JSON 文件。
func (s *Scanner) persistMediaInfo(itemID string) {
        cfg := LoadProbeConfig()
        if !cfg.Persist {
                return
        }
        var streams []models.MediaStream
        db.DB.Where("item_id = ? AND is_external = ?", itemID, false).Find(&streams)
        if len(streams) == 0 {
                return
        }
        if err := os.MkdirAll(cfg.SaveDir, 0o755); err != nil {
                return
        }
        b, _ := json.MarshalIndent(streams, "", " ")
        _ = os.WriteFile(filepath.Join(cfg.SaveDir, itemID+".json"), b, 0o644)
}

// restoreMediaInfo 条目重新入库时恢复持久化的流信息。
func (s *Scanner) restoreMediaInfo(itemID string) bool {
        cfg := LoadProbeConfig()
        if !cfg.Persist {
                return false
        }
        b, err := os.ReadFile(filepath.Join(cfg.SaveDir, itemID+".json"))
        if err != nil {
                return false
        }
        var streams []models.MediaStream
        if json.Unmarshal(b, &streams) != nil || len(streams) == 0 {
                return false
        }
        var cnt int64
        db.DB.Model(&models.MediaStream{}).Where("item_id = ?", itemID).Count(&cnt)
        if cnt > 0 {
                return false
        }
        for i := range streams {
                streams[i].ID = 0
                db.DB.Create(&streams[i])
        }
        return true
}
