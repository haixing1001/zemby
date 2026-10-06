// Package api 增强功能：配置接口、收藏库封面管理。
package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"

	"go-emby/internal/logx"
	"go-emby/internal/scanner"
)

// favLibID 虚拟收藏库固定 ID。
const favLibID = "favorites"

// favCoverPath 收藏库封面文件路径。
func (a *App) favCoverPath() string {
	return filepath.Join(a.cfg.DataDir, "favorites", "poster.jpg")
}

// adminEnhance GET/PUT /admin/enhancements 增强功能配置。
func (a *App) adminEnhance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := scanner.LoadEnhanceConfig()
		a.json(w, 200, M{
			"Favorites":                    cfg.Favorites,
			"TMDB":                         cfg.TMDB,
			"SortByReleaseDate":            cfg.SortByReleaseDate,
			"EpisodeMediaReuse":            cfg.EpisodeMediaReuse,
			"PosterEpisodeBadge":           cfg.PosterEpisodeBadge,
			"HideActorsNoImage":            cfg.HideActorsNoImage,
			"MergeVersionsInLibrary":       cfg.MergeVersionsInLibrary,
			"MergeVersionsAcrossLibraries": cfg.MergeVersionsAcrossLibraries,
			"SearchByInitials":             cfg.SearchByInitials,
			"WatchEnabled":                 cfg.WatchEnabled,
			"WatchDelaySeconds":            cfg.WatchDelaySeconds,
			"FastPath":                     cfg.FastPath,
			"FastPathWaitSec":              cfg.FastPathWaitSec,
			"HasFavoriteCover":             fileExists(a.favCoverPath()),
		})
		return
	case http.MethodPut, http.MethodPost:
	default:
		a.fail(w, 405, "方法不支持")
		return
	}
	var b struct {
		Favorites                    *bool `json:"Favorites"`
		TMDB                         *bool `json:"TMDB"`
		SortByReleaseDate            *bool `json:"SortByReleaseDate"`
		EpisodeMediaReuse            *bool `json:"EpisodeMediaReuse"`
		PosterEpisodeBadge           *bool `json:"PosterEpisodeBadge"`
		HideActorsNoImage            *bool `json:"HideActorsNoImage"`
		MergeVersionsInLibrary       *bool `json:"MergeVersionsInLibrary"`
		MergeVersionsAcrossLibraries *bool `json:"MergeVersionsAcrossLibraries"`
		SearchByInitials             *bool `json:"SearchByInitials"`
		WatchEnabled                 *bool `json:"WatchEnabled"`
		WatchDelaySeconds            *int  `json:"WatchDelaySeconds"`
		FastPath                     *bool `json:"FastPath"`
		FastPathWaitSec              *int  `json:"FastPathWaitSec"`
	}
	if err := bodyJSON(r, &b); err != nil {
		a.fail(w, 400, "请求体格式错误")
		return
	}
	if b.Favorites == nil && b.TMDB == nil && b.SortByReleaseDate == nil && b.EpisodeMediaReuse == nil &&
		b.PosterEpisodeBadge == nil && b.HideActorsNoImage == nil && b.MergeVersionsInLibrary == nil &&
		b.MergeVersionsAcrossLibraries == nil && b.SearchByInitials == nil && b.WatchEnabled == nil &&
		b.WatchDelaySeconds == nil && b.FastPath == nil && b.FastPathWaitSec == nil {
		a.fail(w, 400, "缺少增强功能设置")
		return
	}
	if b.WatchDelaySeconds != nil && (*b.WatchDelaySeconds < 10 || *b.WatchDelaySeconds > 86400) {
		a.fail(w, 400, "媒体变动延时范围 10–86400 秒")
		return
	}
	if b.FastPathWaitSec != nil && (*b.FastPathWaitSec < 1 || *b.FastPathWaitSec > 60) {
		a.fail(w, 400, "快速路径等待上限范围 1–60 秒")
		return
	}
	cfg := scanner.LoadEnhanceConfig()
	if b.Favorites != nil {
		cfg.Favorites = *b.Favorites
	}
	if b.TMDB != nil {
		cfg.TMDB = *b.TMDB
	}
	if b.SortByReleaseDate != nil {
		cfg.SortByReleaseDate = *b.SortByReleaseDate
	}
	if b.EpisodeMediaReuse != nil {
		cfg.EpisodeMediaReuse = *b.EpisodeMediaReuse
	}
	if b.PosterEpisodeBadge != nil {
		cfg.PosterEpisodeBadge = *b.PosterEpisodeBadge
	}
	if b.HideActorsNoImage != nil {
		cfg.HideActorsNoImage = *b.HideActorsNoImage
	}
	if b.MergeVersionsInLibrary != nil {
		cfg.MergeVersionsInLibrary = *b.MergeVersionsInLibrary
	}
	if b.MergeVersionsAcrossLibraries != nil {
		cfg.MergeVersionsAcrossLibraries = *b.MergeVersionsAcrossLibraries
	}
	if b.SearchByInitials != nil {
		cfg.SearchByInitials = *b.SearchByInitials
	}
	if b.WatchEnabled != nil {
		cfg.WatchEnabled = *b.WatchEnabled
	}
	if b.WatchDelaySeconds != nil {
		cfg.WatchDelaySeconds = *b.WatchDelaySeconds
	}
	if b.FastPath != nil {
		cfg.FastPath = *b.FastPath
	}
	if b.FastPathWaitSec != nil {
		cfg.FastPathWaitSec = *b.FastPathWaitSec
	}
	scanner.SaveEnhanceConfig(cfg)
	a.scanner.ApplyEnhanceSideEffects()
	logx.Info("增强功能设置已更新（收藏=%v TMDB=%v 发行日期排序=%v 文件监听=%v 快速路径=%v）",
		cfg.Favorites, cfg.TMDB, cfg.SortByReleaseDate, cfg.WatchEnabled, cfg.FastPath)
	a.json(w, 200, M{"OK": true})
}

// adminFavCover POST（multipart 上传）/ DELETE（移除）收藏库封面。
func (a *App) adminFavCover(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			a.fail(w, 400, "上传内容无效")
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			a.fail(w, 400, "缺少文件字段 file")
			return
		}
		defer f.Close()
		p := a.favCoverPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			a.fail(w, 500, "保存目录创建失败")
			return
		}
		dst, err := os.CreateTemp(filepath.Dir(p), ".cover-")
		if err != nil {
			a.fail(w, 500, "封面保存失败")
			return
		}
		tmp := dst.Name()
		_, werr := io.Copy(dst, f)
		cerr := dst.Close()
		if werr != nil || cerr != nil {
			os.Remove(tmp)
			a.fail(w, 500, "封面保存失败")
			return
		}
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(tmp)
			a.fail(w, 500, "封面保存失败")
			return
		}
		logx.Info("收藏库封面已上传")
		a.json(w, 200, M{"OK": true})
	case http.MethodDelete:
		if err := os.Remove(a.favCoverPath()); err != nil && !os.IsNotExist(err) {
			a.fail(w, 500, "封面移除失败")
			return
		}
		logx.Info("收藏库封面已移除")
		a.json(w, 200, M{"OK": true})
	default:
		a.fail(w, 405, "方法不支持")
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
