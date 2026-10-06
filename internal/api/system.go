// Package api 系统 / 认证 / 用户端点。
package api

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"go-emby/internal/auth"
	"go-emby/internal/db"
	"go-emby/internal/logx"
	"go-emby/internal/models"
)

// userDTO 用户 DTO（客户端强依赖，数组不可为 null）。
func (a *App) userDTO(u *models.User) M {
	policy := M{
		"IsAdministrator": u.IsAdmin,
		"IsDisabled":      false,
		"IsHidden":        false,
		"IsHiddenRemotely": false,
		"EnableMediaPlayback":              u.Allowed,
		"EnableAudioPlaybackTranscoding":   false,
		"EnableVideoPlaybackTranscoding":   false,
		"EnablePlaybackRemuxing":           false,
		"EnableContentDeletion":            u.IsAdmin,
		"EnableContentDownloading":         u.IsAdmin,
		"EnableSubtitleDownloading":        false,
		"EnableSubtitleManagement":         u.IsAdmin,
		"EnableSyncTranscoding":            false,
		"EnableAllFolders":                 true,
		"EnableAllDevices":                 true,
		"EnableAllChannels":                false,
		"EnableRemoteAccess":               true,
		"EnableLiveTvAccess":               false,
		"EnableLiveTvManagement":           false,
		"EnableSharedDeviceControl":        false,
		"EnableRemoteControlOfOtherUsers":  false,
		"EnableCollectionManagement":       u.IsAdmin,
		"EnablePublicSharing":              false,
		"EnableUserPreferenceAccess":       true,
		"RemoteClientBitrateLimit":         0,
		"InvalidLoginAttemptCount":         0,
		"SimultaneousStreamLimit":          u.MaxDevices,
		"BlockedTags":                      []any{},
		"IncludeTags":                      []any{},
		"AccessSchedules":                  []any{},
		"BlockUnratedItems":                []any{},
		"EnabledFolders":                   []any{},
		"EnabledDevices":                   []any{},
		"EnabledChannels":                  []any{},
		"ExcludedSubFolders":               []any{},
		"EnableContentDeletionFromFolders": []any{},
	}
	config := M{
		"OrderedViews":                []any{},
		"DisplayMissingEpisodes":      false,
		"PlayDefaultAudioTrack":       true,
		"SubtitleMode":                "Smart",
		"LatestItemsExcludes":         []any{},
		"MyMediaExcludes":             []any{},
		"HidePlayedInLatest":          false,
		"RememberAudioSelections":     false,
		"RememberSubtitleSelections":  false,
		"EnableNextEpisodeAutoPlay":   true,
		"ResumeRewindSeconds":         0,
		"EnableLocalPassword":         false,
	}
	return M{
		"Id": u.ID, "Name": u.Name, "ServerId": a.serverID,
		"HasPassword":             u.PasswordHash != "",
		"HasConfiguredPassword":   u.PasswordHash != "",
		"HasConfiguredEasyPassword": false,
		"Policy":                  policy,
		"Configuration":           config,
		"DateCreated":             embyTime(u.CreatedAt),
	}
}

// serverInfoPublic 公开服务器信息（客户端探测用）。
func (a *App) serverInfoPublic(w http.ResponseWriter, r *http.Request) {
	osName := "Linux"
	if runtime.GOOS == "darwin" {
		osName = "macOS"
	} else if runtime.GOOS == "windows" {
		osName = "Windows"
	}
	a.json(w, 200, M{
		"Id": a.serverID, "ServerName": a.cfg.ServerName,
		"Version": a.version, "ProductName": "Emby Server",
		"OperatingSystem":  osName,
		"LocalAddress":     localAddress(r),
		"WanAddress":       localAddress(r),
		"LocalAddresses":   []any{},
		"RemoteAddresses":  []any{},
		"StartupWizardCompleted": true,
		"SupportsLibraryMonitor": false,
		"HasUpdateAvailable":     false,
	})
}

// serverInfo 完整服务器信息。
func (a *App) serverInfo(w http.ResponseWriter, r *http.Request) {
	a.serverInfoPublic(w, r)
}

func localAddress(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

// login 用户名密码登录。
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"Username"`
		Pw       string `json:"Pw"`
	}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		if err := bodyJSON(r, &body); err != nil {
			a.fail(w, 400, "请求体格式错误")
			return
		}
	} else {
		_ = r.ParseForm()
		body.Username = r.FormValue("Username")
		body.Pw = r.FormValue("Pw")
		if body.Username == "" {
			body.Username = r.FormValue("username")
			body.Pw = r.FormValue("pw")
		}
	}
	if body.Username == "" {
		a.fail(w, 400, "用户名不能为空")
		return
	}
	var u models.User
	if err := a.db.First(&u, "name = ?", body.Username).Error; err != nil {
		logx.Warn("登录失败（用户不存在）: %s", body.Username)
		a.fail(w, 401, "用户名或密码错误")
		return
	}
	if u.PasswordHash != "" && !auth.CheckPassword(u.PasswordHash, body.Pw) {
		logx.Warn("登录失败（密码错误）: %s", body.Username)
		a.fail(w, 401, "用户名或密码错误")
		return
	}
	raw, hash := models.NewToken()
	client, device, deviceID, version, _ := auth.AuthInfo(r)
	t := models.Token{
		Hash: hash, UserID: u.ID, DeviceID: deviceID, DeviceName: device,
		Client: client, AppVersion: version, IPAddress: auth.ClientIP(r),
		CreatedAt: time.Now(), LastSeen: time.Now(),
	}
	if err := a.db.Create(&t).Error; err != nil {
		a.fail(w, 500, "写入令牌失败")
		return
	}
	logx.Detail("info", "设备: "+deviceID, "用户 %s 登录成功（%s / %s）", u.Name, client, auth.ClientIP(r))
	a.json(w, 200, M{
		"User": a.userDTO(&u), "AccessToken": raw, "ServerId": a.serverID,
		"SessionInfo": a.sessionDTO(&t, &u),
	})
}

// logout 退出登录。
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	id := auth.From(r)
	if id != nil && id.Token != "" {
		a.db.Where("hash = ?", models.HashToken(id.Token)).Delete(&models.Token{})
	}
	a.noContent(w)
}

// userMe 当前用户。
func (a *App) userMe(w http.ResponseWriter, r *http.Request) {
	id := auth.From(r)
	if id == nil {
		a.fail(w, 401, "请先登录")
		return
	}
	if id.IsAPIKey || id.User == nil {
		a.fail(w, 403, "API Key 无用户身份")
		return
	}
	a.json(w, 200, a.userDTO(id.User))
}

// usersList 用户列表 / 创建（管理员）。
func (a *App) usersList(w http.ResponseWriter, r *http.Request) {
	id := auth.From(r)
	if id == nil || id.User == nil || (!id.User.IsAdmin && !id.IsAPIKey) {
		a.fail(w, 403, "需要管理员权限")
		return
	}
	if r.Method == http.MethodPost {
		a.userModify(w, r, "", http.MethodPost)
		return
	}
	var users []models.User
	a.db.Order("created_at").Find(&users)
	out := []M{}
	for i := range users {
		out = append(out, a.userDTO(&users[i]))
	}
	a.json(w, 200, out)
}

// usersRoute /users/{uid}[/...] 路由。
func (a *App) usersRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	id := auth.From(r)
	if id == nil || id.User == nil {
		a.fail(w, 401, "请先登录")
		return
	}
	uid := parts[1]
	rest := parts[2:]
	self := id.User.ID == uid
	if !self && !id.User.IsAdmin && !id.IsAPIKey {
		a.fail(w, 403, "无权访问该用户")
		return
	}

	// /users/{uid} 单条
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			var u models.User
			if err := a.db.First(&u, "id = ?", uid).Error; err != nil {
				a.fail(w, 404, "用户不存在")
				return
			}
			a.json(w, 200, a.userDTO(&u))
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			a.userModify(w, r, uid, r.Method)
		default:
			a.fail(w, 405, "方法不支持")
		}
		return
	}

	switch rest[0] {
	case "views":
		a.userViews(w, r, uid)
	case "items":
		if len(rest) >= 2 {
			switch rest[1] {
			case "root":
				a.userRoot(w, r)
			case "latest":
				a.itemsLatest(w, r)
			case "resume":
				a.itemsQuery(w, r, "resume")
			default:
				// /users/{uid}/Items/{itemId} 单条
				a.itemDetail(w, r, strings.ToLower(rest[1]))
			}
			return
		}
		a.itemsQuery(w, r, "")
	case "password":
		a.userPassword(w, r, uid)
	default:
		// 其余子路径（PlayedItems/FavoriteItems/Rating/UserData 已在上层分发）
		a.fail(w, 404, "未知端点")
	}
}

// userViews 用户媒体库视图。
func (a *App) userViews(w http.ResponseWriter, r *http.Request, uid string) {
	var libs []models.Library
	a.db.Order("sort_order, created_at").Find(&libs)
	items := []M{}
	for i := range libs {
		items = append(items, a.libDTO(&libs[i]))
	}
	a.json(w, 200, M{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
}

// userRoot 根目录。
func (a *App) userRoot(w http.ResponseWriter, r *http.Request) {
	a.json(w, 200, M{
		"Id": "root", "Name": "媒体库", "ServerId": a.serverID,
		"Type": "UserRootFolder", "IsFolder": true, "MediaType": "",
		"ChildCount": 0, "LocationType": "Virtual",
		"ImageTags": M{}, "BackdropImageTags": []any{},
		"UserData": M{"Played": false, "IsFavorite": false, "PlaybackPositionTicks": 0},
	})
}

// userModify 创建/更新/删除用户。
func (a *App) userModify(w http.ResponseWriter, r *http.Request, uid, method string) {
	id := auth.From(r)
	if !id.User.IsAdmin && !id.IsAPIKey {
		a.fail(w, 403, "需要管理员权限")
		return
	}
	switch method {
	case http.MethodPost:
		var body struct {
			Name       string `json:"Name"`
			Password   string `json:"Password"`
			IsAdmin    bool   `json:"IsAdministrator"`
			MaxDevices *int   `json:"MaxDevices"`
		}
		if err := bodyJSON(r, &body); err != nil {
			a.fail(w, 400, "请求体格式错误")
			return
		}
		if body.Name == "" {
			a.fail(w, 400, "用户名不能为空")
			return
		}
		var cnt int64
		a.db.Model(&models.User{}).Where("name = ?", body.Name).Count(&cnt)
		if cnt > 0 {
			a.fail(w, 400, "用户名已存在")
			return
		}
		u := models.User{ID: models.NewID(), Name: body.Name, IsAdmin: body.IsAdmin, Allowed: true, MaxDevices: 2, CreatedAt: time.Now()}
		if body.MaxDevices != nil && *body.MaxDevices >= 1 && *body.MaxDevices <= 100 {
			u.MaxDevices = *body.MaxDevices
		}
		if u.IsAdmin {
			u.MaxDevices = 5
		}
		if body.Password != "" {
			hash, err := auth.HashPassword(body.Password)
			if err != nil {
				a.fail(w, 500, "密码处理失败")
				return
			}
			u.PasswordHash = hash
		}
		if err := a.db.Create(&u).Error; err != nil {
			a.fail(w, 500, "创建用户失败")
			return
		}
		logx.Info("创建用户 %s（管理员=%v，设备上限=%d）", u.Name, u.IsAdmin, u.MaxDevices)
		a.json(w, 200, a.userDTO(&u))
	case http.MethodPut, http.MethodDelete:
		var u models.User
		if err := a.db.First(&u, "id = ?", uid).Error; err != nil {
			a.fail(w, 404, "用户不存在")
			return
		}
		if method == http.MethodDelete {
			if u.FirstAdmin {
				a.fail(w, 400, "初始管理员不可删除")
				return
			}
			a.db.Where("user_id = ?", uid).Delete(&models.Token{})
			a.db.Where("user_id = ?", uid).Delete(&models.UserDatum{})
			a.db.Where("user_id = ?", uid).Delete(&models.PlaySession{})
			a.db.Delete(&u)
			logx.Info("删除用户 %s", u.Name)
			a.noContent(w)
			return
		}
		// PUT 更新
		var body struct {
			Name         *string `json:"Name"`
			Password     *string `json:"Password"`
			IsAdmin      *bool   `json:"IsAdministrator"`
			Allowed      *bool   `json:"AllowPlayback"`
			MaxDevices   *int    `json:"MaxDevices"`
			EnableMediaPlayback *bool `json:"EnableMediaPlayback"`
		}
		if err := bodyJSON(r, &body); err != nil {
			a.fail(w, 400, "请求体格式错误")
			return
		}
		if body.Name != nil && *body.Name != "" {
			u.Name = *body.Name
		}
		if body.Password != nil && *body.Password != "" {
			hash, err := auth.HashPassword(*body.Password)
			if err != nil {
				a.fail(w, 500, "密码处理失败")
				return
			}
			u.PasswordHash = hash
			// 修改密码后吊销令牌
			a.db.Where("user_id = ?", uid).Delete(&models.Token{})
		}
		if body.IsAdmin != nil && !u.FirstAdmin {
			u.IsAdmin = *body.IsAdmin
		}
		if body.Allowed != nil {
			u.Allowed = *body.Allowed
		} else if body.EnableMediaPlayback != nil {
			u.Allowed = *body.EnableMediaPlayback
		}
		if body.MaxDevices != nil && *body.MaxDevices >= 1 && *body.MaxDevices <= 100 {
			u.MaxDevices = *body.MaxDevices
		}
		if !u.Allowed {
			a.db.Where("user_id = ?", uid).Delete(&models.PlaySession{})
		}
		if err := a.db.Save(&u).Error; err != nil {
			a.fail(w, 500, "保存失败")
			return
		}
		logx.Info("更新用户 %s（播放=%v，设备上限=%d）", u.Name, u.Allowed, u.MaxDevices)
		a.json(w, 200, a.userDTO(&u))
	}
}

// userPassword 修改自己的密码。
func (a *App) userPassword(w http.ResponseWriter, r *http.Request, uid string) {
	id := auth.From(r)
	if id.User.ID != uid && !id.User.IsAdmin {
		a.fail(w, 403, "无权修改该用户密码")
		return
	}
	var body struct {
		CurrentPw string `json:"CurrentPw"`
		NewPw     string `json:"NewPw"`
	}
	if err := bodyJSON(r, &body); err != nil {
		a.fail(w, 400, "请求体格式错误")
		return
	}
	var u models.User
	if err := a.db.First(&u, "id = ?", uid).Error; err != nil {
		a.fail(w, 404, "用户不存在")
		return
	}
	if u.PasswordHash != "" && !auth.CheckPassword(u.PasswordHash, body.CurrentPw) {
		a.fail(w, 400, "当前密码错误")
		return
	}
	hash, err := auth.HashPassword(body.NewPw)
	if err != nil {
		a.fail(w, 500, "密码处理失败")
		return
	}
	u.PasswordHash = hash
	a.db.Save(&u)
	a.db.Where("user_id = ?", uid).Delete(&models.Token{})
	a.noContent(w)
}

// sessions 在线会话（令牌）。
func (a *App) sessions(w http.ResponseWriter, r *http.Request) {
	id := auth.From(r)
	if id == nil || id.User == nil || !id.User.IsAdmin {
		a.json(w, 200, []any{})
		return
	}
	var tokens []models.Token
	a.db.Where("last_seen > ?", time.Now().Add(-24*time.Hour)).Order("last_seen DESC").Find(&tokens)
	out := []M{}
	for i := range tokens {
		var u models.User
		if err := a.db.First(&u, "id = ?", tokens[i].UserID).Error; err != nil {
			continue
		}
		out = append(out, a.sessionDTO(&tokens[i], &u))
	}
	a.json(w, 200, out)
}

// itemCounts 条目统计。
func (a *App) itemCounts(w http.ResponseWriter, r *http.Request) {
	var movies, series, episodes int64
	a.db.Model(&models.Item{}).Where("type = ?", "Movie").Count(&movies)
	a.db.Model(&models.Item{}).Where("type = ?", "Series").Count(&series)
	a.db.Model(&models.Item{}).Where("type = ?", "Episode").Count(&episodes)
	a.json(w, 200, M{
		"MovieCount": movies, "SeriesCount": series, "EpisodeCount": episodes,
		"AlbumCount": 0, "SongCount": 0, "GameCount": 0, "TrailerCount": 0,
		"BookCount": 0, "BoxSetCount": 0, "PersonCount": 0, "ArtistCount": 0,
	})
}

var _ = db.GetSetting
var _ = json.Marshal
