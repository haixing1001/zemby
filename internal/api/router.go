// Package api HTTP 路由与 Emby 兼容入口。
package api

import (
        "encoding/json"
        "fmt"
        "net/http"
        "strings"
        "time"

        "gorm.io/gorm"

        "go-emby/internal/auth"
        "go-emby/internal/config"
        "go-emby/internal/logx"
        "go-emby/internal/models"
        "go-emby/internal/scanner"
)

// App API 上下文。
type App struct {
        cfg      *config.Config
        db       *gorm.DB
        scanner  *scanner.Scanner
        serverID string
        version  string
}

// New 创建 App。
func New(cfg *config.Config, database *gorm.DB, sc *scanner.Scanner) *App {
        // 恢复后台保存的设置
        var sv models.Setting
        if database.First(&sv, "key = ?", "server_name").Error == nil && sv.Value != "" {
                cfg.ServerName = sv.Value
        }
        return &App{cfg: cfg, db: database, scanner: sc, serverID: serverIdentifier(cfg), version: "4.8.0.81"}
}

// Handler 返回总处理器。
func (a *App) Handler() http.Handler {
        return auth.Middleware(http.HandlerFunc(a.serve), a.isPublic)
}

// IsDocumentNav 判断请求是否为浏览器文档导航（地址栏直达 / F5 刷新 / 链接整页跳转）。
// 现代浏览器文档导航必带 Sec-Fetch-Mode: navigate 与 Sec-Fetch-Dest: document；
// fetch/XHR/图片等资源请求不会命中这两个头，老浏览器回退 Accept: text/html 判定。
// 反向代理通常原样转发浏览器请求头，两个信号互为冗余，任一命中即视为文档导航。
func IsDocumentNav(r *http.Request) bool {
        if r.Method != http.MethodGet {
                return false
        }
        if r.Header.Get("Sec-Fetch-Mode") == "navigate" || r.Header.Get("Sec-Fetch-Dest") == "document" {
                return true
        }
        return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// isPublic 免认证端点。
func (a *App) isPublic(r *http.Request) bool {
        p := normPath(r.URL.Path)
        switch {
        case p == "/system/ping":
                return true
        case p == "/system/info/public":
                return true
        case p == "/users/authenticatebyname":
                return true
        case p == "/users/public":
                return true
        case p == "/health":
                return true
        case p == "/branding/configuration":
                return true
        case p == "/emby.svg" || p == "/favicon.ico":
                return true
        }
        // 图片：无 tag 也放行到 handler（handler 内部再决定），此处仅放行图片路径
        if strings.Contains(p, "/images/") && (strings.HasPrefix(p, "/items/") || strings.HasPrefix(p, "/emby/items/")) {
                return true
        }
        // 后台 SPA 页面导航（document 请求）放行：仅返回静态 HTML，不含数据；
        // API 请求（fetch/XHR）不会命中文档导航信号，仍需鉴权
        if (p == "/admin" || strings.HasPrefix(p, "/admin/")) && IsDocumentNav(r) {
                return true
        }
        return false
}

// normPath 规范化路径：去 /emby 前缀、合并 //、小写。
func normPath(p string) string {
        for strings.HasPrefix(p, "/emby/") || p == "/emby" {
                p = strings.TrimPrefix(p, "/emby")
                if p == "" {
                        p = "/"
                }
        }
        for strings.Contains(p, "//") {
                p = strings.ReplaceAll(p, "//", "/")
        }
        p = strings.TrimSuffix(p, "/")
        if p == "" {
                p = "/"
        }
        return strings.ToLower(p)
}

// seg 路径分段。
func seg(p string) []string {
        p = strings.TrimPrefix(p, "/")
        if p == "" {
                return nil
        }
        return strings.Split(p, "/")
}

func (a *App) serve(w http.ResponseWriter, r *http.Request) {
        // CORS
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Emby-Token, X-Emby-Authorization, X-Emby-Device-Id, X-MediaBrowser-Token")
        if r.Method == http.MethodOptions {
                w.WriteHeader(http.StatusNoContent)
                return
        }

        raw := r.URL.Path
        p := normPath(raw)

        // 路径原始大小写（ID 等不需要大小写，全部小写匹配即可）
        switch {
        case p == "/health":
                a.json(w, 200, M{"status": "ok", "version": a.version})
        case p == "/system/ping":
                w.Header().Set("Content-Type", "text/plain; charset=utf-8")
                w.Write([]byte("Emby Server"))
        case p == "/system/info/public":
                a.serverInfoPublic(w, r)
        case p == "/users/authenticatebyname" || p == "/users/authenticate":
                a.login(w, r)
        case p == "/users/public":
                a.json(w, 200, []any{})
        case p == "/branding/configuration":
                a.json(w, 200, M{"SplashscreenEnabled": false, "CustomCss": "", "LoginDisclaimer": ""})
        case p == "/emby.svg" || p == "/favicon.ico":
                http.Redirect(w, r, "/assets/logo.svg", http.StatusFound)
        case p == "/admin/logs/stream":
                a.adminGuard(w, r, func() { logx.ServeSSE(w, r) })
        case p == "/admin/logs/stream/all":
                a.adminGuard(w, r, func() { logx.ServeSSE(w, r) })

        default:
                a.serveAuthed(w, r, p)
        }
}

// serveAuthed 需要登录的路由。
func (a *App) serveAuthed(w http.ResponseWriter, r *http.Request, p string) {
        parts := seg(p)

        switch {
        // System
        case p == "/system/info":
                a.serverInfo(w, r)
        case p == "/system/endpoint":
                a.json(w, 200, M{"IsLocal": false, "IsInNetwork": true})
        case p == "/items/counts":
                a.itemCounts(w, r)
        case p == "/sessions":
                a.sessions(w, r)
        case p == "/sessions/logout":
                a.logout(w, r)
        case p == "/sessions/capabilities" || p == "/sessions/capabilities/full":
                w.WriteHeader(http.StatusNoContent)
        case p == "/displaypreferences" || len(parts) >= 2 && parts[0] == "displaypreferences":
                a.json(w, 200, M{"Id": "displaypreferences", "SortBy": "SortName", "SortOrder": "Ascending",
                        "ViewType": "Poster", "CustomPrefs": M{}, "RememberIndexing": false, "RememberSorting": true,
                        "IndexBy": "None"})
        case p == "/users/me":
                a.userMe(w, r)

        // 收藏 / 已看 / 评分（须在 users/{uid} 通用路由之前分发）
        case len(parts) == 4 && parts[0] == "users" && (parts[2] == "favoriteitems" || parts[2] == "playeditems" || parts[2] == "rating" || parts[2] == "userdata"):
                a.userDataRoute(w, r, parts)

        // Users
        case parts[0] == "users" && len(parts) == 1:
                a.usersList(w, r)
        case parts[0] == "users" && len(parts) >= 2:
                a.usersRoute(w, r, parts)

        // Items
        case p == "/items":
                a.itemsQuery(w, r, "")
        case p == "/items/resume":
                a.itemsQuery(w, r, "resume")
        case p == "/items/latest":
                a.itemsLatest(w, r)
        case len(parts) == 2 && parts[0] == "items":
                a.itemDetail(w, r, parts[1])
        case len(parts) == 3 && parts[0] == "items" && parts[2] == "similar":
                a.itemSimilar(w, r, parts[1])
        case len(parts) == 3 && parts[0] == "items" && parts[2] == "refresh":
                a.itemRefresh(w, r, parts[1])
        case len(parts) == 3 && parts[0] == "items" && parts[2] == "playbackinfo":
                a.playbackInfo(w, r, parts[1])
        case len(parts) == 4 && parts[0] == "items" && parts[2] == "images":
                a.images(w, r, parts[1], parts[3])
        case len(parts) >= 4 && parts[0] == "items" && parts[2] == "images":
                a.images(w, r, parts[1], parts[3])
        case len(parts) == 3 && parts[0] == "items" && parts[2] == "download":
                a.itemDownload(w, r, parts[1])
        case len(parts) == 3 && parts[0] == "items" && (parts[2] == "intros" || parts[2] == "theme songs" || parts[2] == "themesongs" || parts[2] == "specialfeatures"):
                a.json(w, 200, M{"Items": []any{}, "TotalRecordCount": 0, "StartIndex": 0})
        case len(parts) == 3 && parts[0] == "items" && parts[2] == "annotations":
                a.json(w, 200, []any{})

        // Shows
        case len(parts) == 3 && parts[0] == "shows" && parts[2] == "seasons":
                a.showSeasons(w, r, parts[1])
        case len(parts) == 3 && parts[0] == "shows" && parts[2] == "episodes":
                a.showEpisodes(w, r, parts[1])
        case p == "/shows/nextup":
                a.json(w, 200, M{"Items": []any{}, "TotalRecordCount": 0, "StartIndex": 0})
        case parts[0] == "shows" && len(parts) >= 2:
                // 其余 shows 子路径退化为 ParentId 查询
                a.itemsQuery(w, r, "")

        // Library
        case p == "/library/mediafolders" || p == "/library/virtualfolders":
                a.mediaFolders(w, r)
        case p == "/library/refresh":
                a.libraryRefresh(w, r)
        case len(parts) == 3 && parts[0] == "libraries" && parts[2] == "refresh":
                a.libraryRefresh(w, r)

        // 播放上报
        case strings.HasPrefix(p, "/sessions/playing"):
                a.sessionPlaying(w, r, p)

        // Videos / stream
        case len(parts) >= 3 && parts[0] == "videos" && (strings.HasPrefix(parts[2], "stream") || parts[2] == "original" || strings.HasPrefix(parts[2], "original.")):
                a.videoStream(w, r, parts[1])
        case len(parts) >= 4 && parts[0] == "videos" && parts[2] == "subtitles":
                a.subtitleStream(w, r, parts)
        case parts[0] == "videos" && (len(parts) >= 3 && (strings.Contains(parts[2], "m3u8") || strings.Contains(parts[2], "transcode") || strings.Contains(parts[2], "hls"))):
                a.transcodeRejected(w, r)
        case parts[0] == "videos":
                a.transcodeRejected(w, r)

        // Audio（不支持）
        case parts[0] == "audio":
                a.transcodeRejected(w, r)

        // 搜索建议
        case p == "/search/hints":
                a.searchHints(w, r)

        // Persons / Genres
        case p == "/persons":
                a.persons(w, r)
        case parts[0] == "persons" && len(parts) >= 2:
                a.personDetail(w, r, parts[1])
        case p == "/genres":
                a.genres(w, r)

        // Movies 推荐
        case p == "/movies/recommendations":
                a.moviesRecommendations(w, r)

        // 管理后台
        case p == "/admin" || (strings.HasPrefix(p, "/admin/") && r.Method == http.MethodGet &&
                strings.Contains(r.Header.Get("Accept"), "text/html")):
                // 后台 SPA 页面导航（含 /admin/* 子路由），交给前端路由处理
                a.serveWeb(w, r, r.URL.Path)
        case p == "/admin/ping" || p == "/admin/status":
                a.adminGuard(w, r, func() { a.adminStatus(w, r) })
        case strings.HasPrefix(p, "/admin/") || strings.HasPrefix(p, "/api/"):
                a.adminRoute(w, r, p, parts)

        default:
                // 静态 Web
                a.serveWeb(w, r, r.URL.Path)
        }
}

// json 输出 JSON。
func (a *App) json(w http.ResponseWriter, status int, v any) {
        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        w.WriteHeader(status)
        enc := json.NewEncoder(w)
        enc.SetEscapeHTML(false)
        _ = enc.Encode(v)
}

// fail 错误输出。
func (a *App) fail(w http.ResponseWriter, status int, msg string) {
        a.json(w, status, M{"error": msg, "Message": msg})
}

// noContent 204。
func (a *App) noContent(w http.ResponseWriter) {
        w.WriteHeader(http.StatusNoContent)
}

// adminGuard 管理员守卫。后台「API 管理」签发的 API Key 与管理员同级（Emby 兼容行为）。
func (a *App) adminGuard(w http.ResponseWriter, r *http.Request, fn func()) {
        id := auth.From(r)
        if id == nil || (!id.IsAPIKey && (id.User == nil || !id.User.IsAdmin)) {
                a.fail(w, http.StatusForbidden, "需要管理员权限")
                return
        }
        fn()
}

// q 查询参数（大小写不敏感）。
func q(r *http.Request, name string) string {
        return r.URL.Query().Get(name)
}

// qBool 查询布尔。
func qBool(r *http.Request, name string) bool {
        v := strings.ToLower(q(r, name))
        return v == "true" || v == "1" || v == "yes"
}

// qInt 查询整数。
func qInt(r *http.Request, name string, def int) int {
        v := strings.TrimSpace(q(r, name))
        if v == "" {
                return def
        }
        n := 0
        for i := 0; i < len(v); i++ {
                if v[i] < '0' || v[i] > '9' {
                        return def
                }
                n = n*10 + int(v[i]-'0')
        }
        return n
}

// bodyJSON 解析请求体。
func bodyJSON(r *http.Request, out any) error {
        dec := json.NewDecoder(r.Body)
        return dec.Decode(out)
}

// serveWeb Web 静态资源（SPA）。
func (a *App) serveWeb(w http.ResponseWriter, r *http.Request, raw string) {
        StaticHandler().ServeHTTP(w, r)
}

// serveWeb 占位说明见上。
func uidOf(parts []string) string { return "" }

// serverIdentifier 稳定服务器 ID（32 位）。
func serverIdentifier(cfg *config.Config) string {
        return longHash(cfg.ServerName + "|" + cfg.DataDir)
}

// sessionDTO 会话。
func (a *App) sessionDTO(t *models.Token, u *models.User) M {
        return M{
                "Id": t.Hash[:16], "ServerId": a.serverID,
                "UserId": u.ID, "UserName": u.Name,
                "Client": t.Client, "ApplicationVersion": t.AppVersion,
                "DeviceId": t.DeviceID, "DeviceName": t.DeviceName,
                "LastActivityDate": embyTime(t.LastSeen),
                "IsActive":         time.Since(t.LastSeen) < 15*time.Minute,
                "AdditionalUsers": []any{},
                "PlayableMediaTypes": []string{"Video"},
                "SupportedCommands":  []string{},
                "SupportsRemoteControl": false, "SupportsMediaControl": false,
                "PlayState": M{"CanSeek": false, "IsPaused": false, "IsMuted": false,
                        "RepeatMode": "RepeatNone", "PlaybackOrder": "Default"},
                "NowPlayingQueue": []any{}, "NowPlayingQueueFullItems": []any{},
        }
}

var _ = fmt.Sprintf
