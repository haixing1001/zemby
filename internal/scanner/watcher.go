// Package scanner 文件变动监听：fsnotify 监听媒体库目录树，防抖合并后触发增量扫描。
// 与刮削「实时监控」共用扫描通道（ScanLibrary update），IsScanning 防重入。
package scanner

import (
        "os"
        "path/filepath"
        "strings"
        "sync"
        "time"

        "github.com/fsnotify/fsnotify"

        "go-emby/internal/db"
        "go-emby/internal/logx"
        "go-emby/internal/models"
)

type mediaWatcher struct {
        mu      sync.Mutex
        stop    chan struct{}
        watch   *fsnotify.Watcher
        delay   int
        scanner *Scanner

        pending map[string]time.Time // libID -> 最近变动时间（防抖起点）
        changed map[string]map[string]bool // libID -> 变更目录集合
        running map[string]bool
        watched map[string]bool
        roots   map[string]string // 路径 -> libID
}

var (
        watchMu   sync.Mutex
        watchInst *mediaWatcher
)

// applyWatchConfig 按配置启动/停止/更新监听（幂等）。
func (s *Scanner) applyWatchConfig(cfg EnhanceConfig) {
        watchMu.Lock()
        defer watchMu.Unlock()
        if watchInst != nil {
                watchInst.setDelay(cfg.WatchDelaySeconds)
        }
        if !cfg.WatchEnabled {
                if watchInst != nil {
                        watchInst.close()
                        watchInst = nil
                        logx.InfoC(logx.CatScan, "文件变动监听已关闭")
                }
                return
        }
        if watchInst != nil {
                return // 已在运行
        }
        w := &mediaWatcher{
                stop:    make(chan struct{}),
                delay:   cfg.WatchDelaySeconds,
                scanner: s,
                pending: map[string]time.Time{},
                changed: map[string]map[string]bool{},
                running: map[string]bool{},
                watched: map[string]bool{},
                roots:   map[string]string{},
        }
        fw, err := fsnotify.NewWatcher()
        if err != nil {
                logx.WarnC(logx.CatScan, "文件监听初始化失败: %v", err)
                return
        }
        w.watch = fw
        watchInst = w
        go w.loop()
        logx.InfoC(logx.CatScan, "文件变动监听已开启（延时 %d 秒）", cfg.WatchDelaySeconds)
}

func (w *mediaWatcher) setDelay(d int) {
        if d < 10 || d > 86400 {
                d = 30
        }
        w.mu.Lock()
        w.delay = d
        w.mu.Unlock()
}

func (w *mediaWatcher) close() {
        select {
        case <-w.stop:
                return // 已关闭
        default:
        }
        close(w.stop)
        if w.watch != nil {
                _ = w.watch.Close()
        }
}

// syncRoots 同步监听目录与媒体库的对应关系（15 秒周期 + 启动时）。
func (w *mediaWatcher) syncRoots() {
        w.mu.Lock()
        defer w.mu.Unlock()
        var libs []models.Library
        db.DB.Find(&libs)
        next := map[string]string{}
        for i := range libs {
                for _, p := range strings.Split(libs[i].Path, ";") {
                        p = strings.TrimSpace(p)
                        if p == "" {
                                continue
                        }
                        next[p] = libs[i].ID
                        if !w.watched[p] {
                                w.addTreeLocked(p)
                        }
                }
        }
        for p := range w.watched {
                keep := false
                for root := range next {
                        if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
                                keep = true
                                break
                        }
                }
                if !keep {
                        _ = w.watch.Remove(p)
                        delete(w.watched, p)
                }
        }
        w.roots = next
}

// addTreeLocked 递归监听目录树（跳过隐藏目录）。
func (w *mediaWatcher) addTreeLocked(root string) {
        _ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
                if err != nil {
                        return err
                }
                if !d.IsDir() {
                        return nil
                }
                if p != root && strings.HasPrefix(d.Name(), ".") {
                        return filepath.SkipDir
                }
                if !w.watched[p] {
                        if err := w.watch.Add(p); err != nil {
                                return nil // 单目录失败不中断（权限等）
                        }
                        w.watched[p] = true
                }
                return nil
        })
}

// mark 归属事件到媒体库。
func (w *mediaWatcher) mark(name string, isDir bool) {
        w.mu.Lock()
        defer w.mu.Unlock()
        for root, lib := range w.roots {
                if name != root && !strings.HasPrefix(name, root+string(filepath.Separator)) {
                        continue
                }
                w.pending[lib] = time.Now()
                if w.changed[lib] == nil {
                        w.changed[lib] = map[string]bool{}
                }
                target := filepath.Dir(name)
                if isDir || strings.EqualFold(filepath.Ext(name), ".strm") {
                        target = name
                }
                if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
                        w.changed[lib][target] = true
                }
                return
        }
}

func (w *mediaWatcher) unwatchTree(name string) {
        w.mu.Lock()
        defer w.mu.Unlock()
        for p := range w.watched {
                if p == name || strings.HasPrefix(p, name+string(filepath.Separator)) {
                        _ = w.watch.Remove(p)
                        delete(w.watched, p)
                }
        }
}

func (w *mediaWatcher) loop() {
        defer func() { w.mu.Lock(); w.pending = map[string]time.Time{}; w.mu.Unlock() }()
        w.syncRoots()
        tick := time.NewTicker(time.Second)
        defer tick.Stop()
        refresh := time.NewTicker(15 * time.Second)
        defer refresh.Stop()
        for {
                select {
                case <-w.stop:
                        return
                case ev, ok := <-w.watch.Events:
                        if !ok {
                                return
                        }
                        if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
                                continue
                        }
                        isDir := false
                        if fi, err := os.Stat(ev.Name); err == nil {
                                isDir = fi.IsDir()
                        }
                        if isDir && ev.Has(fsnotify.Create) {
                                w.mu.Lock()
                                w.addTreeLocked(ev.Name)
                                w.mu.Unlock()
                        }
                        w.mark(ev.Name, isDir)
                        if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
                                w.unwatchTree(ev.Name)
                        }
                case _, ok := <-w.watch.Errors:
                        if !ok {
                                return
                        }
                case <-refresh.C:
                        w.syncRoots()
                case now := <-tick.C:
                        w.fire(now)
                }
        }
}

// fire 防抖到期后触发对应库的增量扫描（共用扫描通道）。
func (w *mediaWatcher) fire(now time.Time) {
        w.mu.Lock()
        delay := time.Duration(w.delay) * time.Second
        type job struct {
                id     string
                paths  []string
        }
        var jobs []job
        for lib, at := range w.pending {
                if now.Sub(at) < delay {
                        continue
                }
                delete(w.pending, lib)
                if w.running[lib] {
                        continue
                }
                // 去除被其它目录覆盖的子目录
                var scopes []string
                for p := range w.changed[lib] {
                        covered := false
                        for other := range w.changed[lib] {
                                if other != p && strings.HasPrefix(p, other+string(filepath.Separator)) {
                                        covered = true
                                        break
                                }
                        }
                        if !covered {
                                scopes = append(scopes, p)
                        }
                }
                delete(w.changed, lib)
                w.running[lib] = true
                jobs = append(jobs, job{id: lib, paths: scopes})
        }
        w.mu.Unlock()
        for _, j := range jobs {
                libID := j.id
                nPaths := len(j.paths)
                go func() {
                        var lib models.Library
                        if db.DB.First(&lib, "id = ?", libID).Error == nil {
                                logx.InfoC(logx.CatScan, "文件变动触发增量扫描「%s」（%d 个变更路径）", lib.Name, nPaths)
                                _ = w.scanner.ScanLibrary(libID, "update")
                        }
                        w.mu.Lock()
                        delete(w.running, libID)
                        w.mu.Unlock()
                }()
        }
}
