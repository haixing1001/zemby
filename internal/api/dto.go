// Package api Emby 兼容 DTO 构建。
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go-emby/internal/models"
)

// M 快捷 map。
type M = map[string]any

func strSlice(s string) []string {
	if s == "" {
		return []string{}
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func idSlice(s string) []string { return strSlice(s) }

// itemTag 图片标签。
func itemTag(it *models.Item) string {
	if it.ImageRev == "" {
		return "0"
	}
	return it.ImageRev
}

// formatDate Emby 风格时间。
func embyTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.0000000Z")
}

// libDTO 媒体库（CollectionFolder）DTO。
func (a *App) libDTO(lib *models.Library) M {
	return M{
		"Id": lib.ID, "ItemId": lib.ID, "Name": lib.Name,
		"Type": "CollectionFolder", "CollectionType": lib.Type,
		"IsFolder": true,
		"SortName": lib.Name, "ForcedSortName": lib.Name,
		"ExternalUrls": []any{}, "ProviderIds": M{}, "RemoteTrailers": []any{},
		"Taglines": []any{}, "LockedFields": []any{}, "LockData": false,
		"CanDelete": false, "CanDownload": false,
		"PresentationUniqueKey": lib.ID, "ServerId": a.serverID,
		"ImageTags": M{"Primary": lib.ID}, "ParentId": "root",
		"LocationType": "Virtual",
		"ChildCount":   0, "RecursiveItemCount": 0,
		"PrimaryImageAspectRatio": 0.6666666666666666,
		"BackdropImageTags":       []any{},
		"UserData":                M{"Played": false, "IsFavorite": false, "PlaybackPositionTicks": 0},
		"DisplayPreferencesId":    lib.ID,
		"DefaultSort":             lib.DefaultSort,
	}
}

// itemDTO 条目 DTO。
func (a *App) itemDTO(it *models.Item, detail bool, userID string) M {
	d := M{
		"Id": it.ID, "ServerId": a.serverID, "Name": it.Name,
		"SortName": it.SortName, "Type": it.Type,
		"IsFolder": it.Type == "Series" || it.Type == "Season",
		"MediaType":        "Video",
		"LocationType":     "FileSystem",
		"DateCreated":      embyTime(it.DateCreated),
		"ImageTags":        M{},
		"BackdropImageTags": []any{},
	}
	if it.OriginalTitle != "" {
		d["OriginalTitle"] = it.OriginalTitle
	}
	if it.Year > 0 {
		d["ProductionYear"] = it.Year
		d["PremiereDate"] = embyTime(firstTime(it.PremiereDate))
	}
	if it.Overview != "" {
		d["Overview"] = it.Overview
	}
	if it.CommunityRating > 0 {
		d["CommunityRating"] = it.CommunityRating
	}
	if it.OfficialRating != "" {
		d["OfficialRating"] = it.OfficialRating
	}
	if it.RunTimeTicks > 0 {
		d["RunTimeTicks"] = it.RunTimeTicks
	}
	genres := strSlice(it.Genres)
	if len(genres) > 0 {
		d["Genres"] = genres
	}
	if tags := strSlice(it.Tags); len(tags) > 0 {
		d["Tags"] = tags
	}
	if studios := strSlice(it.Studios); len(studios) > 0 {
		var s []M
		for _, name := range studios {
			s = append(s, M{"Name": name, "Id": studioID(name)})
		}
		d["Studios"] = s
	}
	if people := strSlice(it.People); len(people) > 0 {
		var p []M
		for _, raw := range people {
			var pm map[string]string
			_ = json.Unmarshal([]byte(raw), &pm)
			if pm == nil || pm["Name"] == "" {
				continue
			}
			person := M{"Id": personID(pm["Name"]), "Name": pm["Name"], "Type": "Actor"}
			if pm["Role"] != "" {
				person["Role"] = pm["Role"]
			}
			if t, ok := pm["Type"]; ok && t != "" {
				person["Type"] = t
			}
			p = append(p, person)
		}
		if len(p) > 0 {
			d["People"] = p
		}
	}
	ids := M{}
	if it.ProviderIDs != "" {
		_ = json.Unmarshal([]byte(it.ProviderIDs), &ids)
	}
	if len(ids) > 0 {
		d["ProviderIds"] = ids
	}

	// 图片标签
	if it.Poster != "" {
		d["ImageTags"] = M{"Primary": itemTag(it)}
	}
	if it.Backdrop != "" {
		d["BackdropImageTags"] = []string{itemTag(it)}
	}
	d["PrimaryImageAspectRatio"] = 0.6666666666666666
	if it.Type == "Episode" {
		d["PrimaryImageAspectRatio"] = 1.7777777777777777
	}

	// 层级关系
	switch it.Type {
	case "Series":
		d["ChildCount"] = 0
	case "Season":
		d["SeriesId"] = it.SeriesID
		d["SeriesName"] = it.SeriesName
		d["IndexNumber"] = it.ParentIndexNumber
		d["ParentId"] = it.ParentID
	case "Episode":
		d["SeriesId"] = it.SeriesID
		d["SeriesName"] = it.SeriesName
		d["SeasonId"] = it.SeasonID
		d["ParentId"] = it.SeasonID
		d["IndexNumber"] = it.IndexNumber
		d["ParentIndexNumber"] = it.ParentIndexNumber
		if it.Thumb != "" {
			d["ImageTags"].(M)["Thumb"] = itemTag(it)
		}
	default:
		if it.ParentID != "" {
			d["ParentId"] = it.ParentID
		}
	}

	// 媒体源
	if it.Type == "Movie" || it.Type == "Episode" {
		sources := a.sources(it, detail)
		if len(sources) > 0 {
			d["MediaSources"] = sources
			d["MediaSourceCount"] = len(sources)
		}
	}

	// UserData
	d["UserData"] = a.userData(userID, it)
	return d
}

func firstTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// sources 媒体源列表。
func (a *App) sources(it *models.Item, detail bool) []M {
	var srcs []models.MediaSource
	a.db.Where("item_id = ?", it.ID).Order("\"default\" DESC, created_at").Find(&srcs)
	var out []M
	for i, s := range srcs {
		out = append(out, a.sourceDTO(it, &s, detail, i == 0))
	}
	return out
}

// sourceDTO MediaSourceInfo。
func (a *App) sourceDTO(it *models.Item, s *models.MediaSource, detail bool, isDefault bool) M {
	name := s.Name
	if name == "" {
		name = it.Name
	}
	if !isDefault && it.Type == "Movie" {
		// 多版本时以文件名区分
		if base := shortName(s.Path); base != "" {
			name = base
		}
	}
	ms := M{
		"Id": s.ID, "Name": name, "ItemId": it.ID,
		"Protocol": "File", "Type": "Default",
		"IsDefault": isDefault, "IsRemote": false,
		"SupportsDirectPlay":    true,
		"SupportsDirectStream":  true,
		"SupportsTranscoding":   false,
		"RequiresOpening":       false,
		"RequiresClosing":       false,
		"RequiresLooping":       false,
		"SupportsProbing":       true,
		"AddApiKeyToDirectStreamUrl": false,
	}
	if s.Container != "" {
		ms["Container"] = s.Container
	}
	if s.Size > 0 {
		ms["Size"] = s.Size
	}
	if it.RunTimeTicks > 0 {
		ms["RunTimeTicks"] = it.RunTimeTicks
	}
	// 远程流（strm）
	if strings.HasPrefix(s.Path, "http://") || strings.HasPrefix(s.Path, "https://") {
		ms["Protocol"] = "Http"
		ms["IsRemote"] = true
		ms["Path"] = s.Path
		if s.Container == "" {
			ms["Container"] = containerFromURL(s.Path)
		}
	}
	// 直连地址
	ext := s.Container
	if ext == "" {
		ext = containerFromURL(s.Path)
	}
	direct := fmt.Sprintf("Videos/%s/stream.%s?Static=true&MediaSourceId=%s", it.ID, ext, s.ID)
	ms["DirectStreamUrl"] = direct
	// 详情模式附流信息
	if detail {
		var streams []models.MediaStream
		a.db.Where("source_id = ?", s.ID).Order("`index`").Find(&streams)
		var ss []M
		for _, st := range streams {
			ss = append(ss, streamDTO(&st))
		}
		if ss == nil {
			ss = []M{}
		}
		ms["MediaStreams"] = ss
	}
	return ms
}

// streamDTO MediaStream。
func streamDTO(st *models.MediaStream) M {
	d := M{
		"Index": st.Index, "Type": st.Type, "Codec": st.Codec,
		"IsTextSubtitleStream": st.Type == "Subtitle" && isTextSub(st.Codec),
		"IsExternal":           st.IsExternal,
		"SupportsExternalStream": st.IsExternal,
	}
	if st.Language != "" && st.Language != "und" {
		d["Language"] = st.Language
	}
	if st.DisplayTitle != "" {
		d["DisplayTitle"] = st.DisplayTitle
	}
	if st.Title != "" {
		d["Title"] = st.Title
	}
	switch st.Type {
	case "Video":
		d["Width"] = st.Width
		d["Height"] = st.Height
		if st.Aspect != "" {
			d["AspectRatio"] = st.Aspect
		}
		if st.FrameRate > 0 {
			d["AverageFrameRate"] = st.FrameRate
			d["RealFrameRate"] = st.FrameRate
		}
		if st.BitRate > 0 {
			d["BitRate"] = st.BitRate
		}
		if st.BitDepth > 0 {
			d["BitDepth"] = st.BitDepth
		}
		if st.PixelFormat != "" {
			d["PixelFormat"] = st.PixelFormat
		}
		if st.Profile != "" {
			d["Profile"] = st.Profile
		}
		if st.VideoRange != "" {
			d["VideoRange"] = st.VideoRange
		}
		d["IsInterlaced"] = false
	case "Audio":
		d["Channels"] = st.Channels
		if st.SampleRate > 0 {
			d["SampleRate"] = st.SampleRate
		}
		if st.BitRate > 0 {
			d["BitRate"] = st.BitRate
		}
		d["IsDefault"] = st.IsDefault
		d["IsForced"] = st.IsForced
	case "Subtitle":
		d["IsDefault"] = st.IsDefault
		d["IsForced"] = st.IsForced
		if st.Path != "" {
			d["Path"] = st.Path
		}
	}
	return d
}

func isTextSub(c string) bool {
	switch c {
	case "srt", "ass", "ssa", "vtt", "sub":
		return true
	}
	return false
}

// userData 用户数据。
func (a *App) userData(userID string, it *models.Item) M {
	ud := M{
		"Played": false, "IsFavorite": false, "PlayCount": 0,
		"PlaybackPositionTicks": 0, "Key": it.ID,
		"UnplayedItemCount": 0,
	}
	if userID == "" {
		return ud
	}
	var rec models.UserDatum
	if err := a.db.Where("user_id = ? AND item_id = ?", userID, it.ID).First(&rec).Error; err == nil {
		ud["Played"] = rec.Played
		ud["IsFavorite"] = rec.Favorite
		ud["PlayCount"] = rec.PlayCount
		ud["PlaybackPositionTicks"] = rec.PositionTicks
		if rec.LastPlayedDate != nil {
			ud["LastPlayedDate"] = embyTime(*rec.LastPlayedDate)
		}
	}
	// 剧集聚合：任一集已看
	if it.Type == "Series" {
		var played int64
		a.db.Model(&models.UserDatum{}).Where("user_id = ? AND played = ? AND item_id IN (?)",
			userID, true, a.db.Model(&models.Item{}).Select("id").Where("series_id = ?", it.ID)).Count(&played)
		if played > 0 {
			ud["Played"] = true
			ud["UnplayedItemCount"] = 0
		}
	}
	return ud
}

// shortName 版本文件名。
func shortName(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i >= 0 {
		path = path[i+1:]
	}
	if j := strings.LastIndexByte(path, '.'); j > 0 {
		path = path[:j]
	}
	return path
}

// containerFromURL 从 URL/路径推断容器。
func containerFromURL(p string) string {
	// 去掉 query
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimPrefix(p, "http://")
	p = strings.TrimPrefix(p, "https://")
	ext := ""
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		ext = strings.ToLower(p[i+1:])
	}
	switch ext {
	case "mkv", "mp4", "avi", "mov", "wmv", "flv", "ts", "webm", "m2ts", "m4v", "iso", "rmvb":
		return ext
	case "":
		return "mkv"
	}
	if len(ext) > 5 {
		return "mkv"
	}
	return ext
}

// personID 人物 ID（稳定）。
func personID(name string) string {
	return "person-" + stableHash(name)
}

func studioID(name string) string {
	return "studio-" + stableHash(name)
}

func genreID(name string) string {
	return "genre-" + stableHash(name)
}

// longHash 32 位十六进制稳定哈希。
func longHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// stableHash 稳定哈希。
func stableHash(s string) string {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}
