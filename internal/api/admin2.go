// Package api 管理后台扩展端点：控制台、媒体库操作、刮削管理、媒体信息、增强功能。
package api

import (
        "fmt"
        "net/http"
        "os"
        "os/exec"
        "path/filepath"
        "runtime"
        "strconv"
        "strings"
        "sync"
        "time"

        "go-emby/internal/auth"
        "go-emby/internal/logx"
        "go-emby/internal/models"
        "go-emby/internal/scanner"
)

// ============================================================
// 控制台
// ============================================================

var procStart = time.Now()
var lastCPU struct {
        mu    sync.Mutex
        valid bool
        total float64 // jiffies
        wall  time.Time
        pct   float64
}

// readProcTotalJiffies 读取进程累计 CPU jiffies（utime+stime）。
func readProcTotalJiffies() float64 {
        b, err := os.ReadFile("/proc/self/stat")
        if err != nil {
                return 0
        }
        // pid (comm) state ...；comm 可能含空格，取最后一个 ')' 之后
        s := string(b)
        if i := strings.LastIndex(s, ")"); i >= 0 && i+2 < len(s) {
                fields := strings.Fields(s[i+2:])
                // fields[0]=state, utime=fields[11], stime=fields[12]
                if len(fields) > 12 {
                        u, _ := strconv.ParseFloat(fields[11], 64)
                        sy, _ := strconv.ParseFloat(fields[12], 64)
                        return u + sy
                }
        }
        return 0
}

// sampleCPU 采样 CPU 占用率（与上次采样比较）。
func sampleCPU() float64 {
        lastCPU.mu.Lock()
        defer lastCPU.mu.Unlock()
        now := time.Now()
        total := readProcTotalJiffies()
        if lastCPU.valid {
                dt := now.Sub(lastCPU.wall).Seconds()
                if dt > 0.2 {
                        lastCPU.pct = (total - lastCPU.total) / dt / float64(runtime.NumCPU()) * 100
                        lastCPU.total, lastCPU.wall = total, now
                }
        } else {
                lastCPU.valid, lastCPU.total, lastCPU.wall = true, total, now
                lastCPU.pct = 0
        }
        if lastCPU.pct < 0 {
                lastCPU.pct = 0
        }
        return lastCPU.pct
}

// memMB 当前进程内存占用 MB。
func memMB() float64 {
        var m runtime.MemStats
        runtime.ReadMemStats(&m)
        return float64(m.Sys) / 1024 / 1024
}

// adminDashboard 控制台数据。
func (a *App) adminDashboard(w http.ResponseWriter, r *http.Request) {
        var movies, series, episodes, users int64
        a.db.Model(&models.Item{}).Where("type = ?", "Movie").Count(&movies)
        a.db.Model(&models.Item{}).Where("type = ?", "Series").Count(&series)
        a.db.Model(&models.Item{}).Where("type = ?", "Episode").Count(&episodes)
        a.db.Model(&models.User{}).Count(&users)

        lease := time.Duration(a.cfg.DeviceLeaseSeconds) * time.Second
        var playing []map[string]any
        var sessions []models.PlaySession
        a.db.Where("updated_at > ?", time.Now().Add(-lease)).Order("updated_at DESC").Find(&sessions)
        for _, ps := range sessions {
                name, typ, runtime := "", "", int64(0)
                var it models.Item
                if a.db.First(&it, "id = ?", ps.ItemID).Error == nil {
                        name, typ, runtime = it.Name, it.Type, it.RunTimeTicks
                        if it.Type == "Episode" && it.SeriesName != "" {
                                name = it.SeriesName + " · " + it.Name
                        }
                }
                var u models.User
                userName := ""
                if a.db.Select("name").First(&u, "id = ?", ps.UserID).Error == nil {
                        userName = u.Name
                }
                playing = append(playing, map[string]any{
                        "UserId": ps.UserID, "UserName": userName, "ItemId": ps.ItemID,
                        "ItemName": name, "ItemType": typ, "DeviceId": ps.DeviceID,
                        "RuntimeTicks": runtime, "UpdatedAt": embyTime(ps.UpdatedAt),
                })
        }

        // 任务：运行中的扫描 + 最近扫描任务
        var tasks []models.ScanTask
        a.db.Order("started_at DESC").Limit(10).Find(&tasks)
        taskList := []map[string]any{}
        for _, t := range tasks {
                libName := ""
                var lib models.Library
                if a.db.Select("name").First(&lib, "id = ?", t.LibraryID).Error == nil {
                        libName = lib.Name
                }
                taskList = append(taskList, map[string]any{
                        "ID": t.ID, "Library": libName, "Mode": t.Mode, "State": t.State,
                        "Message": t.Message, "Total": t.Total, "Done": t.Done, "StartedAt": embyTime(t.StartedAt),
                        "Logs": logx.TaskLogSnapshot(fmt.Sprintf("scan:%d", t.ID), 400),
                })
        }

        // 活跃状态：最近播放活动
        var acts []models.PlayActivity
        a.db.Order("created_at DESC").Limit(50).Find(&acts)
        activity := []map[string]any{}
        for _, at := range acts {
                activity = append(activity, map[string]any{
                        "UserName": at.UserName, "ItemName": at.ItemName, "Action": at.Action,
                        "Device": at.Device, "CreatedAt": embyTime(at.CreatedAt),
                })
        }

        // 刮削/提取任务计数
        var scrapePending, scrapeFailed int64
        a.db.Model(&models.Item{}).Where("type IN ? AND scraped = ?", []string{"Movie", "Series"}, false).Count(&scrapePending)
        a.db.Model(&models.Item{}).Where("type IN ? AND scraped = ? AND scrape_error != ''", []string{"Movie", "Series"}, false).Count(&scrapeFailed)

        uptime := time.Since(procStart)
        dur := func(d time.Duration) string {
                days := int(d.Hours()) / 24
                hours := int(d.Hours()) % 24
                mins := int(d.Minutes()) % 60
                if days > 0 {
                        return fmt.Sprintf("%d天 %d小时", days, hours)
                }
                return fmt.Sprintf("%d小时 %d分钟", hours, mins)
        }
        a.json(w, 200, M{
                "ServerName": a.cfg.ServerName, "Version": a.version,
                "Healthy": true, "Uptime": dur(uptime), "UptimeSeconds": int(uptime.Seconds()),
                "Movies": movies, "Series": series, "Episodes": episodes, "Users": users,
                "CPUPercent": sampleCPU(), "MemMB": memMB(),
                "PlayingCount": len(playing), "NowPlaying": playing,
                "Tasks": taskList, "Activity": activity,
                "ScrapeState": scanner.GetScrapeState(),
                "ScrapePending": scrapePending, "ScrapeFailed": scrapeFailed,
                "ScrapeLogs": logx.TaskLogSnapshot("scrape", 250),
                "ProbeLogs":  logx.TaskLogSnapshot("probe", 250),
        })
}

// clearActivity 清空播放活动记录。
func (a *App) clearActivity(w http.ResponseWriter, r *http.Request) {
        a.db.Where("1 = 1").Delete(&models.PlayActivity{})
        a.noContent(w)
}

// recordActivity 记录播放活动（供 play.go 调用）。
func (a *App) recordActivity(userName, itemID, itemName, device, action string, position int64) {
        a.db.Create(&models.PlayActivity{
                UserName: userName, ItemID: itemID, ItemName: itemName,
                Device: device, Action: action, Position: position, CreatedAt: time.Now(),
        })
        // 只保留最近 500 条
        var cnt int64
        a.db.Model(&models.PlayActivity{}).Count(&cnt)
        if cnt > 500 {
                var oldest models.PlayActivity
                a.db.Order("created_at DESC").Offset(500).Limit(1).First(&oldest)
                if oldest.ID > 0 {
                        a.db.Where("id < ?", oldest.ID).Delete(&models.PlayActivity{})
                }
        }
}

// ============================================================
// 媒体库扩展操作
// ============================================================

// adminLibraryPatch 重命名 / 隐藏 / 默认排序 / 追加媒体文件夹。
func (a *App) adminLibraryPatch(w http.ResponseWriter, r *http.Request, id string) {
        var lib models.Library
        if err := a.db.First(&lib, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "媒体库不存在")
                return
        }
        var body struct {
                Name        *string `json:"Name"`
                Hidden      *bool   `json:"Hidden"`
                DefaultSort *string `json:"DefaultSort"`
                AddFolder   *string `json:"AddFolder"`
        }
        if err := bodyJSON(r, &body); err != nil {
                a.fail(w, 400, "请求体格式错误")
                return
        }
        if body.Name != nil && *body.Name != "" {
                lib.Name = *body.Name
        }
        if body.Hidden != nil {
                lib.Hidden = *body.Hidden
        }
        if body.DefaultSort != nil {
                ds := *body.DefaultSort
                if ds != "" && !strings.Contains(ds, "|") {
                        ds = ds + "|Ascending"
                }
                lib.DefaultSort = ds
        }
        if body.AddFolder != nil && *body.AddFolder != "" {
                p := *body.AddFolder
                if !a.pathAllowed(p) {
                        a.fail(w, 400, "路径不在允许的媒体根目录内: "+p)
                        return
                }
                if _, err := os.Stat(p); err != nil {
                        a.fail(w, 400, "目录不存在: "+p)
                        return
                }
                for _, part := range strings.Split(lib.Path, ";") {
                        if strings.TrimSpace(part) == p {
                                a.fail(w, 400, "该目录已存在")
                                return
                        }
                }
                lib.Path = lib.Path + ";" + p
                logx.InfoC(logx.CatScan, "媒体库「%s」新增目录 %s", lib.Name, p)
        }
        if err := a.db.Save(&lib).Error; err != nil {
                a.fail(w, 500, "保存失败")
                return
        }
        logx.InfoC(logx.CatScan, "媒体库「%s」设置已更新", lib.Name)
        a.json(w, 200, M{"OK": true})
}

// adminLibraryCover 插入封面（指定图片路径）。
func (a *App) adminLibraryCover(w http.ResponseWriter, r *http.Request, id string) {
        var lib models.Library
        if err := a.db.First(&lib, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "媒体库不存在")
                return
        }
        var body struct {
                Path string `json:"Path"`
        }
        if err := bodyJSON(r, &body); err != nil || body.Path == "" {
                a.fail(w, 400, "缺少图片路径")
                return
        }
        if !a.pathAllowed(body.Path) && !a.pathAllowed(filepath.Dir(body.Path)) {
                a.fail(w, 400, "路径不在允许的媒体目录内")
                return
        }
        if _, err := os.Stat(body.Path); err != nil {
                a.fail(w, 400, "图片文件不存在")
                return
        }
        if err := a.saveLibPoster(&lib, body.Path); err != nil {
                a.fail(w, 500, "封面保存失败: "+err.Error())
                return
        }
        a.json(w, 200, M{"OK": true})
}

// adminLibraryCoverGenerate 用 ffmpeg 从库内第一个视频抽帧生成封面。
func (a *App) adminLibraryCoverGenerate(w http.ResponseWriter, r *http.Request, id string) {
        var lib models.Library
        if err := a.db.First(&lib, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "媒体库不存在")
                return
        }
        // 找库内第一个本地视频
        var src models.MediaSource
        var itemIDs []string
        a.db.Model(&models.Item{}).Where("library_id = ? AND type IN ?", lib.ID, []string{"Movie", "Episode"}).Pluck("id", &itemIDs)
        found := false
        for _, iid := range itemIDs {
                var s models.MediaSource
                if a.db.Where("item_id = ?", iid).First(&s).Error == nil && !strings.HasPrefix(s.Path, "http") {
                        src = s
                        found = true
                        break
                }
        }
        if !found {
                a.fail(w, 400, "库内暂无本地视频文件")
                return
        }
        if _, err := exec.LookPath("ffmpeg"); err != nil {
                a.fail(w, 500, "服务器未安装 ffmpeg")
                return
        }
        tmp := filepath.Join(a.cfg.MetaDir(), fmt.Sprintf("lib-%s.gen.jpg", lib.ID))
        _ = os.MkdirAll(a.cfg.MetaDir(), 0o755)
        base := []string{"-y", "-i", src.Path, "-frames:v", "1", "-vf", "scale=600:-2", "-q:v", "3"}
        // 依次尝试：60s → 5s → 不跳转（兼容短视频）
        for _, ss := range []string{"60", "5", ""} {
                args := base
                if ss != "" {
                        args = []string{"-y", "-ss", ss, "-i", src.Path, "-frames:v", "1", "-vf", "scale=600:-2", "-q:v", "3"}
                }
                cmd := exec.Command("ffmpeg", append(args, tmp)...)
                if err := cmd.Run(); err == nil {
                        if err := a.saveLibPoster(&lib, tmp); err != nil {
                                a.fail(w, 500, "封面保存失败: "+err.Error())
                                return
                        }
                        a.json(w, 200, M{"OK": true})
                        return
                }
        }
        a.fail(w, 500, "抽帧失败，请改用插入封面")
}

// adminLibraryCoverRemove 移除封面。
func (a *App) adminLibraryCoverRemove(w http.ResponseWriter, r *http.Request, id string) {
        var lib models.Library
        if err := a.db.First(&lib, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "媒体库不存在")
                return
        }
        if lib.Poster != "" {
                _ = os.Remove(lib.Poster)
        }
        lib.Poster = ""
        a.db.Model(&lib).Update("poster", "")
        a.noContent(w)
}

// saveLibPoster 复制图片到元数据目录并更新库记录。
func (a *App) saveLibPoster(lib *models.Library, srcPath string) error {
        dstDir := filepath.Join(a.cfg.MetaDir(), "libraries")
        if err := os.MkdirAll(dstDir, 0o755); err != nil {
                return err
        }
        dst := filepath.Join(dstDir, lib.ID+filepath.Ext(srcPath))
        in, err := os.ReadFile(srcPath)
        if err != nil {
                return err
        }
        if err := os.WriteFile(dst, in, 0o644); err != nil {
                return err
        }
        lib.Poster = dst
        return a.db.Model(lib).Update("poster", dst).Error
}

// adminLibraryPoster 输出库封面图。
func (a *App) adminLibraryPoster(w http.ResponseWriter, r *http.Request, id string) {
        var lib models.Library
        if err := a.db.Select("poster").First(&lib, "id = ?", id).Error; err != nil || lib.Poster == "" {
                http.NotFound(w, r)
                return
        }
        http.ServeFile(w, r, lib.Poster)
}

// ============================================================
// 刮削管理
// ============================================================

// adminScrapeConfig 刮削配置读写。
func (a *App) adminScrapeConfig(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet {
                a.json(w, 200, scanner.LoadScrapeConfig())
                return
        }
        var body scanner.ScrapeConfig
        if err := bodyJSON(r, &body); err != nil {
                a.fail(w, 400, "请求体格式错误")
                return
        }
        if body.Overwrite != "overwrite" {
                body.Overwrite = "skip"
        }
        scanner.SaveScrapeConfig(body)
        a.scanner.SetRealtime(body.Realtime)
        logx.InfoC(logx.CatScrape, "刮削配置已更新（开启=%v 实时监控=%v 自动刷新=%v 策略=%s）",
                body.Enabled, body.Realtime, body.AutoRefre, body.Overwrite)
        a.json(w, 200, M{"OK": true})
}

// adminScrapeState 刮削任务状态。
func (a *App) adminScrapeState(w http.ResponseWriter, r *http.Request) {
        var pending int64
        a.db.Model(&models.Item{}).Where("type IN ? AND scraped = ?", []string{"Movie", "Series"}, false).Count(&pending)
        var failed int64
        a.db.Model(&models.Item{}).Where("type IN ? AND scraped = ? AND scrape_error != ''", []string{"Movie", "Series"}, false).Count(&failed)
        a.json(w, 200, M{
                "State": scanner.GetScrapeState(), "Pending": pending, "Failed": failed,
                "Logs": logx.TaskLogSnapshot("scrape", 300),
        })
}

// adminScrapeControl 刮削任务控制：scan=扫描入库 start=开始 pause=暂停 stop=停止。
func (a *App) adminScrapeControl(w http.ResponseWriter, r *http.Request) {
        var body struct {
                Action string `json:"Action"`
        }
        _ = bodyJSON(r, &body)
        switch strings.ToLower(body.Action) {
        case "scan":
                var libs []models.Library
                a.db.Find(&libs)
                go func() {
                        for i := range libs {
                                _ = a.scanner.ScanLibrary(libs[i].ID, "update")
                        }
                        scanner.SetScrapeState("running")
                }()
                logx.InfoC(logx.CatScrape, "手动刮削：已触发全部媒体库增量扫描")
                a.json(w, 200, M{"OK": true, "State": "running"})
        case "start":
                n := a.scanner.EnqueueAllUnscraped()
                logx.InfoC(logx.CatScrape, "手动刮削任务开始：%d 个条目待刮削", n)
                a.json(w, 200, M{"OK": true, "State": "running", "Queued": n})
        case "pause":
                scanner.SetScrapeState("paused")
                a.json(w, 200, M{"OK": true, "State": "paused"})
        case "stop":
                scanner.SetScrapeState("idle")
                n := a.scanner.DrainScrape()
                logx.InfoC(logx.CatScrape, "刮削任务已停止，丢弃 %d 个排队任务", n)
                a.json(w, 200, M{"OK": true, "State": "idle"})
        default:
                a.fail(w, 400, "未知操作")
        }
}

// adminScrapeFailed 失败清单。
func (a *App) adminScrapeFailed(w http.ResponseWriter, r *http.Request) {
        a.json(w, 200, scanner.FailedScrapes(qInt(r, "limit", 50)))
}

// adminScrapeRetry 重试失败项。
func (a *App) adminScrapeRetry(w http.ResponseWriter, r *http.Request) {
        n := a.scanner.RetryFailedScrape()
        logx.InfoC(logx.CatScrape, "重试失败刮削：%d 个条目入队", n)
        a.json(w, 200, M{"OK": true, "Queued": n})
}

// ============================================================
// 媒体信息提取
// ============================================================

// adminProbeConfig 提取配置读写。
func (a *App) adminProbeConfig(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet {
                a.json(w, 200, scanner.LoadProbeConfig())
                return
        }
        var body scanner.ProbeConfig
        if err := bodyJSON(r, &body); err != nil {
                a.fail(w, 400, "请求体格式错误")
                return
        }
        if body.Concurrency < 1 || body.Concurrency > 8 {
                a.fail(w, 400, "并发需在 1-8 之间")
                return
        }
        scanner.SaveProbeConfig(body)
        a.scanner.SetProbeConcurrency(body.Concurrency)
        logx.InfoC(logx.CatProbe, "媒体信息提取配置已更新（并发=%d）", body.Concurrency)
        a.json(w, 200, M{"OK": true})
}

// adminProbeStatus 提取状态。
func (a *App) adminProbeStatus(w http.ResponseWriter, r *http.Request) {
        a.json(w, 200, scanner.ProbeStatus())
}

// adminProbeBatch 批量提取控制。
func (a *App) adminProbeBatch(w http.ResponseWriter, r *http.Request) {
        var body struct {
                Action string `json:"Action"`
        }
        _ = bodyJSON(r, &body)
        switch strings.ToLower(body.Action) {
        case "start":
                n, err := a.scanner.BatchProbeStart()
                if err != nil {
                        a.fail(w, 400, err.Error())
                        return
                }
                a.json(w, 200, M{"OK": true, "Queued": n})
        case "stop":
                a.scanner.BatchProbeStop()
                a.json(w, 200, M{"OK": true})
        default:
                a.fail(w, 400, "未知操作")
        }
}

// ============================================================
// 增强功能：设置 / 密码 / 字幕 / API 文档
// ============================================================

// adminSettings 服务器设置。
func (a *App) adminSettings(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet {
                a.json(w, 200, M{
                        "ServerName": a.cfg.ServerName, "Version": a.version,
                        "Addr": a.cfg.Addr, "MediaRoots": a.cfg.MediaRoots,
                        "DeviceLeaseSeconds": a.cfg.DeviceLeaseSeconds,
                        "Playback":           "直连 / 302 重定向（不转码）",
                })
                return
        }
        var body struct {
                ServerName string `json:"ServerName"`
        }
        if err := bodyJSON(r, &body); err != nil {
                a.fail(w, 400, "请求体格式错误")
                return
        }
        if body.ServerName == "" {
                a.fail(w, 400, "服务器名称不能为空")
                return
        }
        a.cfg.ServerName = body.ServerName
        a.db.Save(&models.Setting{Key: "server_name", Value: body.ServerName})
        logx.Info("服务器名称已改为「%s」", body.ServerName)
        a.json(w, 200, M{"OK": true})
}

// adminPassword 修改管理员自身密码。
func (a *App) adminPassword(w http.ResponseWriter, r *http.Request) {
        id := auth.From(r)
        if id == nil || id.User == nil {
                a.fail(w, 403, "需要登录")
                return
        }
        var body struct {
                Old string `json:"Old"`
                New string `json:"New"`
        }
        if err := bodyJSON(r, &body); err != nil || body.New == "" {
                a.fail(w, 400, "新密码不能为空")
                return
        }
        if !auth.CheckPassword(id.User.PasswordHash, body.Old) {
                a.fail(w, 400, "原密码错误")
                return
        }
        hash, err := auth.HashPassword(body.New)
        if err != nil {
                a.fail(w, 500, "密码加密失败")
                return
        }
        a.db.Model(id.User).Update("password_hash", hash)
        logx.Info("用户「%s」修改了密码", id.User.Name)
        a.json(w, 200, M{"OK": true})
}

// adminSubtitles 外挂字幕清单。
func (a *App) adminSubtitles(w http.ResponseWriter, r *http.Request) {
        var streams []models.MediaStream
        a.db.Where("type = ? AND is_external = ?", "Subtitle", true).Order("item_id").Limit(500).Find(&streams)
        out := []map[string]any{}
        for _, s := range streams {
                var it models.Item
                name, libID := "", ""
                if a.db.Select("name, library_id").First(&it, "id = ?", s.ItemID).Error == nil {
                        name, libID = it.Name, it.LibraryID
                }
                out = append(out, map[string]any{
                        "ItemID": s.ItemID, "ItemName": name, "LibraryID": libID,
                        "Path": s.Path, "Language": s.Language, "Title": s.Title,
                        "DisplayTitle": s.DisplayTitle, "IsDefault": s.IsDefault, "IsForced": s.IsForced,
                })
        }
        a.json(w, 200, M{"Items": out, "Total": len(out)})
}

// adminAPIList API 文档。
func (a *App) adminAPIList(w http.ResponseWriter, r *http.Request) {
        type apiDoc struct {
                Group   string `json:"group"`
                Method  string `json:"method"`
                Path    string `json:"path"`
                Desc    string `json:"desc"`
        }
        docs := []apiDoc{
                {"系统", "GET", "/emby/System/Info", "服务器信息"},
                {"系统", "GET", "/emby/System/Info/Public", "公开服务器信息（免鉴权）"},
                {"系统", "GET", "/emby/System/Ping", "心跳"},
                {"认证", "POST", "/emby/Users/AuthenticateByName", "用户名密码登录，返回 AccessToken"},
                {"认证", "GET", "/emby/*?api_key=<密钥>", "后台「API 管理」签发的密钥，等同管理员（X-Emby-Token 头亦可）"},
                {"媒体库", "GET", "/emby/Users/{uid}/Views", "媒体库视图列表"},
                {"媒体库", "GET", "/emby/Library/MediaFolders", "媒体文件夹"},
                {"媒体库", "POST", "/emby/Library/Refresh", "全库刷新"},
                {"条目", "GET", "/emby/Items?ParentId=&Recursive=true", "条目查询（分页/排序/过滤）"},
                {"条目", "GET", "/emby/Items/{id}", "条目详情"},
                {"条目", "GET", "/emby/Items/Latest", "最近添加"},
                {"条目", "GET", "/emby/Items/Resume", "继续观看"},
                {"条目", "GET", "/emby/Items/{id}/Similar", "相似推荐"},
                {"条目", "POST", "/emby/Items/{id}/Refresh", "刷新单个条目（重新刮削）"},
                {"剧集", "GET", "/emby/Shows/{id}/Seasons", "季列表"},
                {"剧集", "GET", "/emby/Shows/{id}/Episodes?SeasonId=", "集列表"},
                {"图片", "GET", "/emby/Items/{id}/Images/Primary", "海报（支持 MaxWidth/裁剪）"},
                {"播放", "POST", "/emby/Items/{id}/PlaybackInfo", "播放信息（MediaSources）"},
                {"播放", "GET", "/emby/Videos/{id}/stream.mp4?Static=true", "直连流（HTTP Range）"},
                {"播放", "GET", "/emby/Videos/{id}/subtitles/{i}/stream.vtt", "内嵌字幕转 VTT"},
                {"播放", "POST", "/emby/Sessions/Playing", "播放开始上报"},
                {"播放", "POST", "/emby/Sessions/Playing/Progress", "播放进度上报"},
                {"播放", "POST", "/emby/Sessions/Playing/Stopped", "播放结束上报"},
                {"用户", "GET", "/emby/Users", "用户列表"},
                {"用户", "POST", "/emby/Users/{uid}/PlayedItems/{iid}", "标记已看"},
                {"用户", "POST", "/emby/Users/{uid}/FavoriteItems/{iid}", "标记收藏"},
                {"搜索", "GET", "/emby/Search/Hints?SearchTerm=", "搜索提示"},
                {"管理", "GET", "/admin/dashboard", "控制台数据（本页）"},
                {"管理", "GET", "/admin/scrape/failed", "刮削失败清单"},
                {"管理", "POST", "/admin/ai/test", "AI 识别辅助测试（从文件路径提取关键词）"},
                {"管理", "GET", "/admin/probe/status", "媒体信息提取状态"},
        }
        a.json(w, 200, M{"ApiVersion": a.version, "Endpoints": docs})
}

// ============================================================
// API Key 管理（Emby 兼容密钥签发）
// ============================================================

func maskKey(k models.ApiKey) string {
        return k.Prefix + "••••••••••" + k.Suffix
}

// adminApiKeyList GET /admin/apikeys 密钥列表。
func (a *App) adminApiKeyList(w http.ResponseWriter, r *http.Request) {
        var keys []models.ApiKey
        a.db.Order("created_at DESC").Find(&keys)
        out := make([]M, 0, len(keys))
        for _, k := range keys {
                last := ""
                if k.LastSeen != nil {
                        last = k.LastSeen.Format("2006/1/2 15:04:05")
                }
                out = append(out, M{
                        "ID":          k.ID,
                        "Name":        k.Name,
                        "Masked":      maskKey(k),
                        "DateCreated": k.CreatedAt.Format("2006/1/2 15:04:05"),
                        "LastSeen":    last,
                })
        }
        a.json(w, 200, M{"Items": out, "TotalRecordCount": len(out)})
}

// adminApiKeyCreate POST /admin/apikeys {Name} 生成密钥（明文仅此一次返回）。
func (a *App) adminApiKeyCreate(w http.ResponseWriter, r *http.Request) {
        var body struct {
                Name string `json:"Name"`
        }
        _ = bodyJSON(r, &body)
        name := strings.TrimSpace(body.Name)
        if name == "" {
                name = "未命名密钥"
        }
        if len(name) > 100 {
                name = name[:100]
        }
        raw, hash := models.NewToken()
        key := models.ApiKey{Name: name, Hash: hash, Prefix: raw[:6], Suffix: raw[len(raw)-4:]}
        if err := a.db.Create(&key).Error; err != nil {
                a.fail(w, 500, "创建失败: "+err.Error())
                return
        }
        logx.Info("已签发 API Key「%s」（后缀 %s）", name, key.Suffix)
        a.json(w, 200, M{
                "ID":          key.ID,
                "Name":        key.Name,
                "Key":         raw,
                "Masked":      maskKey(key),
                "DateCreated": key.CreatedAt.Format("2006/1/2 15:04:05"),
        })
}

// adminApiKeyDelete DELETE /admin/apikeys/{id} 撤销密钥。
func (a *App) adminApiKeyDelete(w http.ResponseWriter, r *http.Request, id string) {
        var key models.ApiKey
        if err := a.db.First(&key, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "密钥不存在")
                return
        }
        a.db.Delete(&key)
        logx.Info("已撤销 API Key「%s」", key.Name)
        a.noContent(w)
}
