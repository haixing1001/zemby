// Package api 播放：PlaybackInfo / 直连流 / 重定向 / 设备占用。
package api

import (
	"context"
	"errors"
        "fmt"
	"net"
	"net/netip"
        "net/http"
	"net/url"
        "os"
        "path/filepath"
        "strings"
        "sync"
        "time"

	"gorm.io/gorm"

        "go-emby/internal/auth"
        "go-emby/internal/logx"
        "go-emby/internal/models"
        "go-emby/internal/scanner"
)

var errDeviceLimit = errors.New("simultaneous device limit reached")

// reserve 设备占用检查（租约 180s）。
func (a *App) reserve(w http.ResponseWriter, r *http.Request) (ok bool, msg string) {
        id := auth.From(r)
        if id == nil || id.User == nil || id.IsAPIKey {
                return true, "" // API Key 或无用户：不限制
        }
        now := time.Now()
        lease := time.Duration(a.cfg.DeviceLeaseSeconds) * time.Second
        // 权限
        if !id.User.Allowed {
                return false, "管理员已禁止该用户播放"
        }
	if id.User.MaxDevices < 1 {
                return false, fmt.Sprintf("已达到同时播放设备上限（%d 台）", id.User.MaxDevices)
        }
	// 在同一个事务内清理租约、检查上限并保存新租约，避免同时启动两个播放
	// 请求时都读到旧计数，从而超过设备上限。
	err := a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("updated_at < ?", now.Add(-lease)).Delete(&models.PlaySession{}).Error; err != nil {
			return err
		}
		var cnt int64
		if err := tx.Model(&models.PlaySession{}).Where("user_id = ? AND device_id <> ?", id.User.ID, id.DeviceID).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt >= int64(id.User.MaxDevices) {
			return errDeviceLimit
		}
		ps := models.PlaySession{ID: id.User.ID + "|" + id.DeviceID, UserID: id.User.ID, DeviceID: id.DeviceID, UpdatedAt: now}
		return tx.Save(&ps).Error
	})
	if errors.Is(err, errDeviceLimit) {
		return false, fmt.Sprintf("已达到同时播放设备上限（%d 台）", id.User.MaxDevices)
	}
	if err != nil {
		logx.Warn("无法写入播放设备租约: %v", err)
		return false, "无法检查播放设备限制，请稍后重试"
	}
        return true, ""
}

// releaseDevice 释放设备。
func (a *App) releaseDevice(userID, deviceID string) {
        a.db.Where("user_id = ? AND device_id = ?", userID, deviceID).Delete(&models.PlaySession{})
}

// playbackInfo POST /items/{id}/playbackinfo
func (a *App) playbackInfo(w http.ResponseWriter, r *http.Request, id string) {
        var it models.Item
        if err := a.db.First(&it, "id = ?", strings.ToLower(id)).Error; err != nil {
                a.fail(w, 404, "条目不存在")
                return
        }
        if idn := auth.From(r); idn != nil && idn.User != nil {
                if ok, msg := a.reserve(w, r); !ok {
                        a.fail(w, 403, msg)
                        return
                }
        }
        playSession := longHash(it.ID + time.Now().String())
        a.json(w, 200, M{
                "MediaSources":  a.mergedSources(&it, true),
                "PlaySessionId": playSession,
                "ErrorCode":     "None",
        })
}

// videoStream 直连流：本地文件 http.ServeFile（支持 Range），远程流 302 重定向。
func (a *App) videoStream(w http.ResponseWriter, r *http.Request, id string) {
        var it models.Item
        if err := a.db.First(&it, "id = ?", strings.ToLower(id)).Error; err != nil {
                a.fail(w, 404, "条目不存在")
                return
        }
        if ok, msg := a.reserve(w, r); !ok {
                a.fail(w, 403, msg)
                return
        }
        // 选媒体源
        var src models.MediaSource
        srcID := strings.ToLower(q(r, "MediaSourceId"))
        if srcID != "" {
                if err := a.db.Where("item_id = ? AND id = ?", it.ID, srcID).First(&src).Error; err != nil {
                        // 多版本合并：源可能属于同身份的其它条目
                        var alt models.MediaSource
                        if err2 := a.db.First(&alt, "id = ?", srcID).Error; err2 == nil && alt.ItemID != it.ID {
                                var owner models.Item
                                if err3 := a.db.First(&owner, "id = ?", alt.ItemID).Error; err3 == nil &&
                                        owner.Type == "Movie" && owner.Year == it.Year &&
                                        strings.EqualFold(strings.TrimSpace(owner.Name), strings.TrimSpace(it.Name)) {
                                        src = alt
                                }
                        }
                }
                if src.ID == "" {
                        a.fail(w, 404, "媒体源不存在")
                        return
                }
        } else {
                if err := a.db.Where("item_id = ?", it.ID).Order("\"default\" DESC, created_at").First(&src).Error; err != nil {
                        a.fail(w, 404, "无可用媒体源")
                        return
                }
        }

        clientInfo := ""
        userName := ""
        if idn := auth.From(r); idn != nil {
                clientInfo = idn.Client
                if clientInfo == "" {
                        clientInfo = auth.ClientIP(r)
                }
                if idn.User != nil {
                        userName = idn.User.Name
                }
        }
        logx.Playback("用户开始播放《%s》[%s]", it.Name, clientInfo)
        a.recordActivity(userName, it.ID, it.Name, clientInfo, "start", 0)
        if it.Type == "Episode" {
                go a.scanner.PreloadNext(it.SeriesID, it.ParentIndexNumber, it.IndexNumber)
        }

        // 远程流：302 重定向直连
        if strings.HasPrefix(src.Path, "http://") || strings.HasPrefix(src.Path, "https://") {
                target := src.Path
                if cfg := scanner.LoadEnhanceConfig(); cfg.FastPath {
                        // 快速路径：限时解析最终直链，失败回退原始地址（不改变普通播放）
                        target = a.resolveFast(src.Path, cfg.FastPathWaitSec)
                }
                logx.DetailC(logx.CatRedirect, "info", target+" · "+clientInfo, "302 重定向直连《%s》", it.Name)
                w.Header().Set("Location", target)
                w.Header().Set("Cache-Control", "no-store")
                w.WriteHeader(http.StatusFound)
                return
        }
        // 本地文件：检查安全
        if !a.pathAllowed(src.Path) {
                a.fail(w, 403, "文件不在允许的媒体目录内")
                return
        }
        f, err := os.Open(src.Path)
        if err != nil {
                a.fail(w, 404, "文件不可访问")
                return
        }
        defer f.Close()
        fi, err := f.Stat()
        if err != nil {
                a.fail(w, 404, "文件信息不可读取")
                return
        }
        if !fi.Mode().IsRegular() {
                a.fail(w, 400, "不是有效文件")
                return
        }
        name := filepath.Base(src.Path)
        w.Header().Set("Content-Type", contentTypeFor(name, src.Container))
        w.Header().Set("Accept-Ranges", "bytes")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range, Content-Type, ETag, Last-Modified")
        // http.ServeContent 处理 Range/If-Range
        http.ServeContent(w, r, name, fi.ModTime(), f)
}

// fastLink 快速路径解析缓存条目。
type fastLink struct {
        location string
        until    time.Time
}

var fastCache = struct {
        sync.Mutex
        m map[string]fastLink
}{m: map[string]fastLink{}}

// resolveFast 快速路径：限时探测 STRM 地址可达性并跟随重定向取最终直链；
// 超时/失败回退原始地址，结果缓存 5 秒。
func (a *App) resolveFast(raw string, waitSec int) string {
	if waitSec < 1 {
		waitSec = 1
	} else if waitSec > 60 {
		waitSec = 60
	}
        fastCache.Lock()
        hit, ok := fastCache.m[raw]
        fastCache.Unlock()
        if ok && time.Now().Before(hit.until) {
                return hit.location
        }
        loc := raw
        reachable := false
	checkCtx, cancelCheck := context.WithTimeout(context.Background(), time.Duration(waitSec)*time.Second)
	defer cancelCheck()
	if err := validatePublicURL(checkCtx, raw); err != nil {
		logx.InfoC(logx.CatRedirect, "快速路径跳过非公网地址，回退原始 STRM 地址")
		return raw
	}
	transport := &http.Transport{
		Proxy: nil, // 不允许环境代理绕过目标地址校验
		DialContext: safePublicDialContext,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   time.Duration(waitSec) * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return validatePublicURL(req.Context(), req.URL.String())
		},
	}
        do := func(method string) (string, bool) {
				req, err := http.NewRequestWithContext(checkCtx, method, raw, nil)
                if err != nil {
                        return "", false
                }
                req.Header.Set("User-Agent", "zemby/fastpath")
                if method == http.MethodGet {
                        req.Header.Set("Range", "bytes=0-0")
                }
                resp, err := client.Do(req)
                if err != nil {
                        return "", false
                }
                defer resp.Body.Close()
                if resp.StatusCode >= 400 {
                        return "", false
                }
                if resp.Request != nil && resp.Request.URL != nil {
                        return resp.Request.URL.String(), true
                }
                return "", false
        }
        if l, ok := do(http.MethodHead); ok {
                loc, reachable = l, true
        } else if l, ok := do(http.MethodGet); ok {
                loc, reachable = l, true
        }
        if reachable {
                if loc != raw {
                        logx.InfoC(logx.CatRedirect, "快速路径解析成功（限时 %d 秒）", waitSec)
                } else {
                        logx.InfoC(logx.CatRedirect, "快速路径可达（限时 %d 秒），无重定向直连", waitSec)
                }
        } else {
                logx.InfoC(logx.CatRedirect, "快速路径解析失败，回退原始 STRM 地址")
        }
        fastCache.Lock()
        if len(fastCache.m) >= 512 {
                for k := range fastCache.m {
                        delete(fastCache.m, k)
                        break
                }
        }
        fastCache.m[raw] = fastLink{loc, time.Now().Add(5 * time.Second)}
        fastCache.Unlock()
        return loc
}

var blockedFastPathPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func isPublicFastPathIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, prefix := range blockedFastPathPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func validatePublicURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("invalid HTTP URL")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !isPublicFastPathIP(ip) {
			return fmt.Errorf("non-public IP address")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("cannot resolve host")
	}
	for _, ip := range ips {
		if !isPublicFastPathIP(ip) {
			return fmt.Errorf("host resolves to a non-public IP address")
		}
	}
	return nil
}

// safePublicDialContext resolves at connection time and pins the connection to
// a validated public IP, preventing redirects or DNS rebinding from reaching
// local/private services.
func safePublicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve host")
	}
	dialer := net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		if !isPublicFastPathIP(ip) {
			return nil, fmt.Errorf("host resolves to a non-public IP address")
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// subtitleStream 外挂字幕流：/videos/{id}/subtitles/{index}/stream.{fmt}
func (a *App) subtitleStream(w http.ResponseWriter, r *http.Request, parts []string) {
        // parts: videos/{id}/subtitles/{index}/stream.{fmt} 或 videos/{id}/{msid}/subtitles/{index}/stream.{fmt}
        // 找到 index 与格式
        id := parts[1]
        // 兼容两种结构
        idx := -1
        format := "vtt"
        for i := 0; i < len(parts); i++ {
                if parts[i] == "subtitles" && i+1 < len(parts) {
                        fmt.Sscanf(parts[i+1], "%d", &idx)
                }
                if strings.HasPrefix(parts[i], "stream.") {
                        format = strings.TrimPrefix(parts[i], "stream.")
                }
        }
        if idx < 0 {
                a.fail(w, 404, "字幕不存在")
                return
        }
        var st models.MediaStream
        q := a.db.Where("item_id = ? AND type = 'Subtitle' AND is_external = ?", strings.ToLower(id), true)
        q = q.Order("index").Offset(idx).Limit(1)
        if err := q.First(&st).Error; err != nil {
                a.fail(w, 404, "字幕不存在")
                return
        }
	if !a.pathAllowed(st.Path) {
		a.fail(w, http.StatusForbidden, "字幕文件不在允许的媒体目录内")
		return
	}
        data, err := os.ReadFile(st.Path)
        if err != nil {
                a.fail(w, 404, "字幕文件不可读")
                return
        }
        ct := "application/x-subrip; charset=utf-8"
        switch strings.ToLower(format) {
        case "vtt":
                ct = "text/vtt; charset=utf-8"
                // srt → vtt 转换
                if strings.EqualFold(st.Codec, "srt") {
                        data = srtToVTT(data)
                }
        case "srt":
                ct = "application/x-subrip; charset=utf-8"
        case "ass", "ssa":
                ct = "text/plain; charset=utf-8"
        }
        w.Header().Set("Content-Type", ct)
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Write(data)
}

// srtToVTT SRT 转 VTT。
func srtToVTT(b []byte) []byte {
        s := string(b)
        s = strings.ReplaceAll(s, "\r\n", "\n")
        if !strings.HasPrefix(s, "WEBVTT") {
                // 逗号毫秒分隔符 → 点
                out := strings.Builder{}
                out.WriteString("WEBVTT\n\n")
                for _, block := range strings.Split(s, "\n\n") {
                        lines := strings.Split(block, "\n")
                        for i, l := range lines {
                                if strings.Contains(l, "-->") {
                                        lines[i] = strings.ReplaceAll(l, ",", ".")
                                }
                        }
                        out.WriteString(strings.Join(lines, "\n"))
                        out.WriteString("\n\n")
                }
                return []byte(out.String())
        }
        return b
}

// transcodeRejected 转码请求一律拒绝。
func (a *App) transcodeRejected(w http.ResponseWriter, r *http.Request) {
        a.fail(w, http.StatusBadRequest, "本服务器不支持转码或 HLS，请使用直接播放（Direct Play）")
}

// itemDownload 条目文件下载（管理员）。
func (a *App) itemDownload(w http.ResponseWriter, r *http.Request, id string) {
	identity := auth.From(r)
	if identity == nil || (!identity.IsAPIKey && (identity.User == nil || !identity.User.IsAdmin)) {
		a.fail(w, http.StatusForbidden, "需要管理员权限")
		return
	}
        var src models.MediaSource
        if err := a.db.Where("item_id = ?", strings.ToLower(id)).Order("\"default\" DESC").First(&src).Error; err != nil {
                a.fail(w, 404, "无可用媒体源")
                return
        }
        if strings.HasPrefix(src.Path, "http") {
                w.Header().Set("Location", src.Path)
                w.WriteHeader(http.StatusFound)
                return
        }
        if !a.pathAllowed(src.Path) {
                a.fail(w, 403, "文件不在允许的媒体目录内")
                return
        }
        w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", urlEscape(filepath.Base(src.Path))))
        http.ServeFile(w, r, src.Path)
}

func urlEscape(s string) string {
        var b strings.Builder
        for i := 0; i < len(s); i++ {
                c := s[i]
                if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
                        c == '-' || c == '_' || c == '.' || c == '~' {
                        b.WriteByte(c)
                } else {
                        fmt.Fprintf(&b, "%%%02X", c)
                }
        }
        return b.String()
}

// contentTypeFor 内容类型。
func contentTypeFor(name, container string) string {
        ext := strings.ToLower(filepath.Ext(name))
        if ext == "" || ext == "." {
                ext = "." + container
        }
        switch ext {
        case ".mp4", ".m4v": return "video/mp4"
        case ".mkv": return "video/x-matroska"
        case ".avi": return "video/x-msvideo"
        case ".mov": return "video/quicktime"
        case ".webm": return "video/webm"
        case ".ts", ".m2ts", ".mts": return "video/mp2t"
        case ".flv": return "video/x-flv"
        case ".wmv": return "video/x-ms-wmv"
        case ".iso": return "application/octet-stream"
        case ".strm": return "video/mp4"
        }
        return "video/mp4"
}

// pathAllowed 路径是否在媒体根目录内。
func (a *App) pathAllowed(path string) bool {
	if len(a.cfg.MediaRoots) == 0 {
		return false
	}
	resolved, err := canonicalExistingPath(path)
	if err != nil {
		return false
	}
	for _, root := range a.cfg.MediaRoots {
		resolvedRoot, err := canonicalExistingPath(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

// canonicalExistingPath normalizes a path and resolves symlinks before checking
// media-root containment. Callers use this for paths that must already exist.
func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// isMediaRoot reports whether path resolves to one of the configured roots.
func (a *App) isMediaRoot(path string) bool {
	resolved, err := canonicalExistingPath(path)
	if err != nil {
		return false
	}
	for _, root := range a.cfg.MediaRoots {
		resolvedRoot, err := canonicalExistingPath(root)
		if err == nil && resolved == resolvedRoot {
			return true
		}
	}
	return false
}
