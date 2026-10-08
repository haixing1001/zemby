// Package api 媒体库端点。
package api

import (
        "net/http"
        "os"
        "strings"
        "time"

        "go-emby/internal/auth"
        "go-emby/internal/logx"
        "go-emby/internal/models"
)

// mediaFolders /library/mediafolders
func (a *App) mediaFolders(w http.ResponseWriter, r *http.Request) {
        var libs []models.Library
        a.db.Order("sort_order, created_at").Find(&libs)
        items := []M{}
        for i := range libs {
                items = append(items, a.libDTO(&libs[i]))
        }
        a.json(w, 200, M{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
}

// libraryRefresh 触发全库刷新。
func (a *App) libraryRefresh(w http.ResponseWriter, r *http.Request) {
        a.adminGuard(w, r, func() {
                var libs []models.Library
                a.db.Find(&libs)
                go func() {
                        for i := range libs {
                                _ = a.scanner.ScanLibrary(libs[i].ID, "update")
                        }
                }()
                logx.InfoC(logx.CatScan, "已请求全库刷新")
                a.noContent(w)
        })
}

// adminRoute 管理后台路由分发。
func (a *App) adminRoute(w http.ResponseWriter, r *http.Request, p string, parts []string) {
        a.adminGuard(w, r, func() {
                switch {
                // ---- 媒体库管理 ----
                case p == "/admin/libraries":
                        switch r.Method {
                        case http.MethodGet:
                                a.adminLibrariesList(w, r)
                        case http.MethodPost:
                                a.adminLibraryCreate(w, r)
                        default:
                                a.fail(w, 405, "方法不支持")
                        }
                case len(parts) == 3 && parts[0] == "admin" && parts[1] == "libraries":
                        switch r.Method {
                        case http.MethodDelete:
                                a.adminLibraryDelete(w, r, parts[2])
                        case http.MethodPatch, http.MethodPut, http.MethodPost:
                                a.adminLibraryPatch(w, r, parts[2])
                        default:
                                a.fail(w, 405, "方法不支持")
                        }
                case len(parts) == 5 && parts[0] == "admin" && parts[1] == "libraries" && parts[3] == "cover" && parts[4] == "generate":
                        a.adminLibraryCoverGenerate(w, r, parts[2])
                case len(parts) == 4 && parts[0] == "admin" && parts[1] == "libraries":
                        lid := parts[2]
                        switch parts[3] {
                        case "poster":
                                a.adminLibraryPoster(w, r, lid)
                        case "cover":
                                if r.Method == http.MethodDelete {
                                        a.adminLibraryCoverRemove(w, r, lid)
                                } else {
                                        a.adminLibraryCover(w, r, lid)
                                }
                        case "generate":
                                a.adminLibraryCoverGenerate(w, r, lid)
                        default:
                                a.fail(w, 404, "未知管理端点")
                        }
                case p == "/admin/dashboard":
                        a.adminDashboard(w, r)
                case p == "/admin/activity" && r.Method == http.MethodDelete:
                        a.clearActivity(w, r)
                case p == "/admin/scrape/config":
                        a.adminScrapeConfig(w, r)
                case p == "/admin/scrape/state":
                        a.adminScrapeState(w, r)
                case p == "/admin/scrape/control":
                        a.adminScrapeControl(w, r)
                case p == "/admin/scrape/failed":
                        a.adminScrapeFailed(w, r)
                case p == "/admin/scrape/retry":
                        a.adminScrapeRetry(w, r)
                case p == "/admin/probe/config":
                        a.adminProbeConfig(w, r)
                case p == "/admin/probe/status":
                        a.adminProbeStatus(w, r)
                case p == "/admin/probe/batch":
                        a.adminProbeBatch(w, r)
                case p == "/admin/settings":
                        a.adminSettings(w, r)
                case p == "/admin/enhancements":
                        a.adminEnhance(w, r)
                case p == "/admin/favorites/cover":
                        a.adminFavCover(w, r)
                case p == "/admin/password":
                        a.adminPassword(w, r)
                case p == "/admin/subtitles":
                        a.adminSubtitles(w, r)
                case p == "/admin/api":
                        a.adminAPIList(w, r)

                // ---- API Key 管理 ----
                case p == "/admin/apikeys":
                        switch r.Method {
                        case http.MethodGet:
                                a.adminApiKeyList(w, r)
                        case http.MethodPost:
                                a.adminApiKeyCreate(w, r)
                        default:
                                a.fail(w, 405, "方法不支持")
                        }
                case len(parts) == 3 && parts[0] == "admin" && parts[1] == "apikeys":
                        switch r.Method {
                        case http.MethodDelete:
                                a.adminApiKeyDelete(w, r, parts[2])
                        default:
                                a.fail(w, 405, "方法不支持")
                        }

                case p == "/admin/scan":
                        a.adminScan(w, r)
                case p == "/admin/scan-all":
                        a.adminScan(w, r)
                case p == "/admin/scan-status":
                        a.adminScanStatus(w, r)
                case p == "/admin/scan-stop":
                        a.adminScanStop(w, r)

                // ---- TMDB 设置 ----
                case p == "/admin/tmdb":
                        a.adminTMDB(w, r)
                case p == "/admin/ai":
                        a.adminAI(w, r)
                case p == "/admin/ai/test":
                        a.adminAITest(w, r)
                case p == "/admin/scrape":
                        a.adminScrape(w, r)

                // ---- 文件管理 ----
                case p == "/admin/files" || p == "/api/files":
                        a.fileList(w, r)
                case p == "/admin/files/op" || p == "/api/files/op":
                        a.fileOp(w, r)
                case p == "/admin/files/upload" || p == "/api/files/upload":
                        a.fileUpload(w, r)

                // ---- 日志 ----
                case p == "/admin/logs":
                        a.adminLogs(w, r)

                // ---- 状态 ----
                case p == "/admin/status":
                        a.adminStatus(w, r)

                default:
                        a.fail(w, 404, "未知管理端点")
                }
        })
}

// adminLibrariesList 媒体库列表（含扫描状态）。
func (a *App) adminLibrariesList(w http.ResponseWriter, r *http.Request) {
        var libs []models.Library
        a.db.Order("sort_order, created_at").Find(&libs)
        type itemCount struct {
                LibraryID string `gorm:"column:library_id"`
                Count     int64  `gorm:"column:item_count"`
        }
        var counts []itemCount
        a.db.Model(&models.Item{}).Select("library_id, COUNT(1) AS item_count").
                Where("type IN ?", []string{"Movie", "Episode"}).Group("library_id").Scan(&counts)
        countByLibrary := make(map[string]int64, len(counts))
        for _, row := range counts {
                countByLibrary[row.LibraryID] = row.Count
        }
        out := []M{}
        for i := range libs {
                lib := &libs[i]
                scanning := a.scanner.IsScanning(lib.ID)
                out = append(out, M{
                        "ID": lib.ID, "Name": lib.Name, "Path": lib.Path, "Type": lib.Type,
                        "EnableTMDB": lib.EnableTMDB, "Language": lib.Language, "SortOrder": lib.SortOrder,
                        "ItemCount": countByLibrary[lib.ID], "Scanning": scanning,
                        "Hidden": lib.Hidden, "HasPoster": lib.Poster != "", "DefaultSort": lib.DefaultSort,
                        "LastScan": lib.LastScan, "DateCreated": lib.CreatedAt,
                })
        }
        a.json(w, 200, out)
}

// adminLibraryCreate 创建媒体库。
func (a *App) adminLibraryCreate(w http.ResponseWriter, r *http.Request) {
        var body struct {
                Name       string `json:"Name"`
                Path       string `json:"Path"`
                Type       string `json:"Type"`
                EnableTMDB *bool  `json:"EnableTMDB"`
                Language   string `json:"Language"`
        }
        if err := bodyJSON(r, &body); err != nil {
                a.fail(w, 400, "请求体格式错误")
                return
        }
        if body.Name == "" || body.Path == "" {
                a.fail(w, 400, "名称与路径不能为空")
                return
        }
        libType := body.Type
        if libType != "tvshows" {
                libType = "movies"
        }
        // 校验路径
        for _, part := range strings.Split(body.Path, ";") {
                part = strings.TrimSpace(part)
                if part == "" {
                        continue
                }
                if !a.pathAllowed(part) {
                        a.fail(w, 400, "路径不在允许的媒体根目录内: "+part)
                        return
                }
                if _, err := os.Stat(part); err != nil {
                        a.fail(w, 400, "目录不存在: "+part)
                        return
                }
        }
        var cnt int64
        a.db.Model(&models.Library{}).Where("path = ?", body.Path).Count(&cnt)
        if cnt > 0 {
                a.fail(w, 400, "该路径已添加为媒体库")
                return
        }
        enable := true
        if body.EnableTMDB != nil {
                enable = *body.EnableTMDB
        }
        lang := body.Language
        if lang == "" {
                lang = "zh-CN"
        }
        lib := models.Library{
                ID: models.NewID(), Name: body.Name, Path: body.Path, Type: libType,
                EnableTMDB: enable, Language: lang, CreatedAt: time.Now(),
        }
        if err := a.db.Create(&lib).Error; err != nil {
                a.fail(w, 500, "创建失败")
                return
        }
        logx.InfoC(logx.CatScan, "创建媒体库「%s」(%s, %s)", lib.Name, lib.Type, lib.Path)
        // 自动触发首次扫描
        _ = a.scanner.ScanLibrary(lib.ID, "full")
        a.json(w, 200, M{"ID": lib.ID, "Name": lib.Name})
}

// adminLibraryDelete 删除媒体库。
func (a *App) adminLibraryDelete(w http.ResponseWriter, r *http.Request, id string) {
        var lib models.Library
        if err := a.db.First(&lib, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "媒体库不存在")
                return
        }
        // 删除条目
        var items []models.Item
        a.db.Where("library_id = ?", lib.ID).Find(&items)
        for _, it := range items {
                a.db.Where("item_id = ?", it.ID).Delete(&models.MediaStream{})
                a.db.Where("item_id = ?", it.ID).Delete(&models.MediaSource{})
                a.db.Where("item_id = ?", it.ID).Delete(&models.UserDatum{})
        }
        a.db.Where("library_id = ?", lib.ID).Delete(&models.Item{})
        a.db.Delete(&lib)
        logx.InfoC(logx.CatScan, "删除媒体库「%s」", lib.Name)
        a.noContent(w)
}

// adminScan 扫描请求。
func (a *App) adminScan(w http.ResponseWriter, r *http.Request) {
        var body struct {
                ID   string `json:"ID"`
                Mode string `json:"Mode"` // full | update
        }
        _ = bodyJSON(r, &body)
        mode := body.Mode
        if mode != "full" {
                mode = "update"
        }
        if body.ID == "" {
                // 扫全部
                var libs []models.Library
                a.db.Find(&libs)
                if len(libs) == 0 {
                        a.fail(w, 400, "尚无媒体库，请先添加")
                        return
                }
                go func() {
                        for i := range libs {
                                _ = a.scanner.ScanLibrary(libs[i].ID, mode)
                        }
                }()
                a.json(w, 200, M{"OK": true, "Message": "已开始全库扫描"})
                return
        }
        if err := a.scanner.ScanLibrary(body.ID, mode); err != nil {
                a.fail(w, 400, err.Error())
                return
        }
        a.json(w, 200, M{"OK": true})
}

// adminScanStatus 扫描状态。
func (a *App) adminScanStatus(w http.ResponseWriter, r *http.Request) {
        var libs []models.Library
        a.db.Find(&libs)
        scanning := []string{}
        for i := range libs {
                if a.scanner.IsScanning(libs[i].ID) {
                        scanning = append(scanning, libs[i].Name)
                }
        }
        var tasks []models.ScanTask
        a.db.Order("started_at DESC").Limit(10).Find(&tasks)
        a.json(w, 200, M{"Scanning": scanning, "Tasks": tasks})
}

// adminTMDB TMDB 设置读写。
func (a *App) adminTMDB(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet {
                set := tmdbSettings()
                // 脱敏
                masked := set.APIKey
                if len(masked) > 8 {
                        masked = masked[:4] + "****" + masked[len(masked)-4:]
                }
		a.json(w, 200, M{"APIKey": masked, "HasKey": set.APIKey != "", "Language": set.Language, "DownloadImages": set.DownloadImgs, "APIBaseURL": set.APIBaseURL, "ImageBaseURL": set.ImageBaseURL})
                return
        }
        var body struct {
                APIKey         string `json:"APIKey"`
                Language       string `json:"Language"`
                DownloadImages *bool  `json:"DownloadImages"`
			APIBaseURL     *string `json:"APIBaseURL"`
			ImageBaseURL   *string `json:"ImageBaseURL"`
        }
        if err := bodyJSON(r, &body); err != nil {
                a.fail(w, 400, "请求体格式错误")
                return
        }
        set := tmdbSettings()
        if body.APIKey != "" && !strings.Contains(body.APIKey, "****") {
                set.APIKey = strings.TrimSpace(body.APIKey)
        }
        if body.Language != "" {
                set.Language = body.Language
        }
        if body.DownloadImages != nil {
                set.DownloadImgs = *body.DownloadImages
        }
		if body.APIBaseURL != nil {
			set.APIBaseURL = *body.APIBaseURL
		}
		if body.ImageBaseURL != nil {
			set.ImageBaseURL = *body.ImageBaseURL
		}
        if err := saveTMDBSettings(set); err != nil {
			a.fail(w, 400, err.Error())
                return
        }
        logx.InfoC(logx.CatTMDB, "TMDB 刮削设置已更新（语言=%s）", set.Language)
        a.json(w, 200, M{"OK": true})
}

// adminScrape 手动刮削。
func (a *App) adminScrape(w http.ResponseWriter, r *http.Request) {
        var body struct {
                ItemID string `json:"ItemID"`
        }
        _ = bodyJSON(r, &body)
        if body.ItemID == "" {
                a.fail(w, 400, "缺少 ItemID")
                return
        }
        go func() {
                if err := a.scanner.ScrapeNow(strings.ToLower(body.ItemID)); err != nil {
                        logx.WarnC(logx.CatScrape, "手动刮削失败: %v", err)
                }
        }()
        a.json(w, 200, M{"OK": true, "Message": "刮削任务已开始"})
}

// adminStatus 后台状态。
func (a *App) adminStatus(w http.ResponseWriter, r *http.Request) {
        var movies, series, episodes, users, libs int64
        a.db.Model(&models.Item{}).Where("type = ?", "Movie").Count(&movies)
        a.db.Model(&models.Item{}).Where("type = ?", "Series").Count(&series)
        a.db.Model(&models.Item{}).Where("type = ?", "Episode").Count(&episodes)
        a.db.Model(&models.User{}).Count(&users)
        a.db.Model(&models.Library{}).Count(&libs)
        var online int64
        a.db.Model(&models.PlaySession{}).Where("updated_at > ?", time.Now().Add(-time.Duration(a.cfg.DeviceLeaseSeconds)*time.Second)).Count(&online)
        a.json(w, 200, M{
                "ServerName": a.cfg.ServerName, "Version": a.version,
                "Movies": movies, "Series": series, "Episodes": episodes,
                "Users": users, "Libraries": libs,
                "OnlineDevices":      online,
                "Transcoding":        false,
                "Playback":           "302 redirect / direct stream",
                "DeviceLeaseSeconds": a.cfg.DeviceLeaseSeconds,
                "MediaRoots":         a.cfg.MediaRoots,
        })
}

// adminLogs 日志查询（支持分类/级别过滤，返回分类计数）。
func (a *App) adminLogs(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodDelete {
                logx.Clear()
                logx.Info("日志已清空")
                a.noContent(w)
                return
        }
        n := qInt(r, "limit", 500)
        if n <= 0 || n > 2000 {
                n = 2000
        }
        cat := q(r, "category")
        level := q(r, "level")
        if cat == "error" { // 兼容：错误日志作为特殊分类
                level = "error"
                cat = ""
        }
        entries := logx.SnapshotFiltered(cat, level, n)
        counts := logx.Counts()
        a.json(w, 200, M{"Entries": entries, "Counts": counts, "Total": len(entries)})
}

var _ = auth.ClientIP

// adminScanStop 停止全部正在进行的扫描。
func (a *App) adminScanStop(w http.ResponseWriter, r *http.Request) {
        var libs []models.Library
        a.db.Find(&libs)
        n := 0
        for i := range libs {
                if a.scanner.IsScanning(libs[i].ID) {
                        a.scanner.RequestStop(libs[i].ID)
                        n++
                }
        }
        logx.InfoC(logx.CatScan, "已请求停止 %d 个扫描任务", n)
        a.json(w, 200, M{"OK": true, "Stopped": n})
}
