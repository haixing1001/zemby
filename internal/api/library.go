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
		logx.Info("已请求全库刷新")
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
			a.adminLibraryDelete(w, r, parts[2])
		case p == "/admin/scan":
			a.adminScan(w, r)
		case p == "/admin/scan-all":
			a.adminScan(w, r)
		case p == "/admin/scan-status":
			a.adminScanStatus(w, r)

		// ---- TMDB 设置 ----
		case p == "/admin/tmdb":
			a.adminTMDB(w, r)
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
	out := []M{}
	for i := range libs {
		lib := &libs[i]
		var items int64
		a.db.Model(&models.Item{}).Where("library_id = ?", lib.ID).Where("type IN ?", []string{"Movie", "Episode"}).Count(&items)
		scanning := a.scanner.IsScanning(lib.ID)
		out = append(out, M{
			"ID": lib.ID, "Name": lib.Name, "Path": lib.Path, "Type": lib.Type,
			"EnableTMDB": lib.EnableTMDB, "Language": lib.Language, "SortOrder": lib.SortOrder,
			"ItemCount": items, "Scanning": scanning,
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
	logx.Info("创建媒体库「%s」(%s, %s)", lib.Name, lib.Type, lib.Path)
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
	logx.Info("删除媒体库「%s」", lib.Name)
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
		a.json(w, 200, M{"APIKey": masked, "HasKey": set.APIKey != "", "Language": set.Language, "DownloadImages": set.DownloadImgs})
		return
	}
	var body struct {
		APIKey         string `json:"APIKey"`
		Language       string `json:"Language"`
		DownloadImages *bool  `json:"DownloadImages"`
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
	if err := saveTMDBSettings(set); err != nil {
		a.fail(w, 500, "保存失败")
		return
	}
	logx.Info("TMDB 刮削设置已更新（语言=%s）", set.Language)
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
			logx.Warn("手动刮削失败: %v", err)
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
		"OnlineDevices":   online,
		"Transcoding":     false,
		"Playback":        "302 redirect / direct stream",
		"DeviceLeaseSeconds": a.cfg.DeviceLeaseSeconds,
		"MediaRoots":      a.cfg.MediaRoots,
	})
}

// adminLogs 日志查询。
func (a *App) adminLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		logx.Clear()
		a.noContent(w)
		return
	}
	n := qInt(r, "limit", 200)
	a.json(w, 200, M{"Entries": logxSnapshot(n)})
}

var _ = auth.ClientIP
