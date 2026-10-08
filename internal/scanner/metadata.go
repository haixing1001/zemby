// Package scanner NFO / 本地图片 / 媒体源 / 字幕 应用逻辑。
package scanner

import (
        "encoding/json"
        "fmt"
        "os"
        "path/filepath"
        "strconv"
        "strings"
        "time"

        "go-emby/internal/db"
        "go-emby/internal/logx"
        "go-emby/internal/models"
        "go-emby/internal/nfo"
)

// applyNFO 将 NFO 信息应用到条目（电影或单集）。
func (s *Scanner) applyNFO(item *models.Item, videoPath, nfoName string) {
        candidates := []string{
                strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".nfo",
                filepath.Join(filepath.Dir(videoPath), "movie.nfo"),
        }
        if nfoName != "" {
                candidates = append(candidates, filepath.Join(filepath.Dir(videoPath), nfoName))
        }
        if item.Type == "Episode" {
                // 集的 NFO 不查 movie.nfo
                candidates = candidates[:1]
        }
        var parsed *nfo.NFO
        nfoPath := ""
        for _, c := range candidates {
                if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
                        if n, err := nfo.ParseFile(c); err == nil {
                                parsed = n
                                nfoPath = c
                                break
                        }
                }
        }
        if parsed == nil {
                return
        }
        s.tlog(item.LibraryID, "info", "读取 NFO「%s」：《%s》· %s", filepath.Base(nfoPath), parsed.Title, nfoPath)
        changed := false
        if parsed.Title != "" {
                item.Name = parsed.Title
                changed = true
        }
        if parsed.OriginalTitle != "" {
                item.OriginalTitle = parsed.OriginalTitle
                changed = true
        }
        if parsed.Plot != "" {
                item.Overview = parsed.Plot
        } else if parsed.Outline != "" {
                item.Overview = parsed.Outline
        }
        if parsed.Rating > 0 {
                item.CommunityRating = parsed.Rating
        }
        if parsed.MPAA != "" {
                item.OfficialRating = parsed.MPAA
        }
        if parsed.Year > 0 {
                item.Year = parsed.Year
        } else if t := parsed.PremiereTime(); !t.IsZero() {
                item.Year = t.Year()
                item.PremiereDate = &t
        }
        if parsed.Runtime > 0 {
                item.RunTimeTicks = int64(parsed.Runtime) * 60 * 10000000
        }
        if len(parsed.Genres) > 0 {
                item.Genres = toJSON(parsed.Genres)
        }
        if len(parsed.Studios) > 0 {
                item.Studios = toJSON(parsed.Studios)
        }
        if len(parsed.Tags) > 0 {
                item.Tags = toJSON(parsed.Tags)
        }
        var people []map[string]string
        for _, a := range parsed.Actors {
                if a.Name != "" {
                        people = append(people, map[string]string{"Name": a.Name, "Role": a.Role, "Thumb": a.Thumb})
                }
        }
        if len(parsed.Directors) > 0 {
                for _, d := range parsed.Directors {
                        people = append(people, map[string]string{"Name": d, "Role": "导演", "Type": "Director"})
                }
        }
        if len(people) > 0 {
                item.People = toJSON(people)
        }
        ids := map[string]string{}
        if item.ProviderIDs != "" {
                _ = json.Unmarshal([]byte(item.ProviderIDs), &ids)
        }
        if v := parsed.TMDBID(); v != "" {
                ids["Tmdb"] = v
        }
        if v := parsed.IMDBID(); v != "" {
                ids["Imdb"] = v
        }
        if len(ids) > 0 {
                item.ProviderIDs = toJSON(ids)
                item.TmdbID = strings.TrimSpace(ids["Tmdb"])
                item.TmdbKind = models.TmdbKindForItemType(item.Type)
                item.Scraped = true // 已有第三方 ID，无需再刮削
        }
        // NFO 内嵌流信息（若尚无流）
        applyNFOStreams(item, parsed)
        if changed || item.ProviderIDs != "" {
                db.DB.Save(item)
        }
}

// applyNFOStreams 将 NFO streamdetails 写入流表（无 ffprobe 时的兜底）。
func applyNFOStreams(item *models.Item, n *nfo.NFO) {
        var cnt int64
        db.DB.Model(&models.MediaStream{}).Where("item_id = ? AND is_external = ?", item.ID, false).Count(&cnt)
        if cnt > 0 {
                return
        }
        var src models.MediaSource
        if err := db.DB.Where("item_id = ?", item.ID).First(&src).Error; err != nil {
                return
        }
        sd := n.StreamDetails
        idx := 0
        if sd.Video.Codec != "" {
                db.DB.Create(&models.MediaStream{SourceID: src.ID, ItemID: item.ID, Index: idx,
                        Type: "Video", Codec: strings.ToLower(sd.Video.Codec), Width: sd.Video.Width, Height: sd.Video.Height, Aspect: sd.Video.AspectRatio})
                idx++
                if item.RunTimeTicks == 0 && sd.Video.DurationSeconds > 0 {
                        item.RunTimeTicks = int64(sd.Video.DurationSeconds * 10000000)
                }
        }
        for _, a := range sd.Audio {
                db.DB.Create(&models.MediaStream{SourceID: src.ID, ItemID: item.ID, Index: idx,
                        Type: "Audio", Codec: strings.ToLower(a.Codec), Language: langAlias(a.Language), Channels: a.Channels})
                idx++
        }
        for _, sub := range sd.Subtitle {
                db.DB.Create(&models.MediaStream{SourceID: src.ID, ItemID: item.ID, Index: idx,
                        Type: "Subtitle", Codec: strings.ToLower(sub.Codec), Language: langAlias(sub.Language)})
                idx++
        }
}

func langAlias(l string) string {
        if l == "" {
                return ""
        }
        l = strings.ToLower(l)
        for k, v := range map[string]string{"zh": "chi", "zho": "chi", "chs": "chi", "cht": "chi", "cn": "chi", "en": "eng", "jp": "jpn", "ja": "jpn", "ko": "kor"} {
                if l == k {
                        return v
                }
        }
        return l
}

// applyNFOToSeries 将 tvshow.nfo 应用到剧集条目。
func applyNFOToSeries(series *models.Item, n *nfo.NFO) {
        if n.Title != "" {
                series.Name = n.Title
        }
        if n.OriginalTitle != "" {
                series.OriginalTitle = n.OriginalTitle
        }
        if n.Plot != "" {
                series.Overview = n.Plot
        }
        if n.Rating > 0 {
                series.CommunityRating = n.Rating
        }
        if n.MPAA != "" {
                series.OfficialRating = n.MPAA
        }
        if n.Year > 0 {
                series.Year = n.Year
        } else if t := n.PremiereTime(); !t.IsZero() {
                series.Year = t.Year()
                series.PremiereDate = &t
        }
        if len(n.Genres) > 0 {
                series.Genres = toJSON(n.Genres)
        }
        if len(n.Studios) > 0 {
                series.Studios = toJSON(n.Studios)
        }
        var people []map[string]string
        for _, a := range n.Actors {
                if a.Name != "" {
                        people = append(people, map[string]string{"Name": a.Name, "Role": a.Role, "Thumb": a.Thumb})
                }
        }
        if len(people) > 0 {
                series.People = toJSON(people)
        }
        ids := map[string]string{}
        if series.ProviderIDs != "" {
                _ = json.Unmarshal([]byte(series.ProviderIDs), &ids)
        }
        if v := n.TMDBID(); v != "" {
                ids["Tmdb"] = v
        }
        if v := n.IMDBID(); v != "" {
                ids["Imdb"] = v
        }
        if len(ids) > 0 {
                series.ProviderIDs = toJSON(ids)
                series.TmdbID = strings.TrimSpace(ids["Tmdb"])
                series.TmdbKind = models.TmdbKindForItemType(series.Type)
        }
}

// ensureSource 确保条目有媒体源，返回之。
func (s *Scanner) ensureSource(item *models.Item, vf videoFile) models.MediaSource {
        var src models.MediaSource
        err := db.DB.Where("item_id = ?", item.ID).First(&src).Error
        realPath := vf.path
        // strm 内容解析
        if strings.EqualFold(filepath.Ext(vf.path), ".strm") {
                if u := readSTRM(vf.path); u != "" {
                        realPath = u
                }
        }
        if err != nil {
                src = models.MediaSource{
                        ID: models.NewID(), ItemID: item.ID, Path: realPath,
                        Container: containerOf(realPath, vf.path), Size: vf.size,
                        Name: filepath.Base(vf.path), Default: true, CreatedAt: time.Now(),
                }
                db.DB.Create(&src)
        } else if src.Path != realPath || src.Size != vf.size {
                src.Path, src.Size, src.Container = realPath, vf.size, containerOf(realPath, vf.path)
                db.DB.Save(&src)
        }
        return src
}

// readSTRM 读取 .strm 内容中的 URL。
func readSTRM(path string) string {
        b, err := os.ReadFile(path)
        if err != nil || len(b) > 64<<10 {
                return ""
        }
        for _, line := range strings.Split(string(b), "\n") {
                line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
                if line == "" || strings.HasPrefix(line, "#") {
                        continue
                }
                return line
        }
        return ""
}

// containerOf 推断容器格式。
func containerOf(realPath, filePath string) string {
        ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(realPath)), ".")
        if ext == "" || len(ext) > 5 {
                ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(filePath)), ".")
        }
        switch ext {
        case "mkv": return "mkv"
        case "mp4", "m4v": return "mp4"
        case "avi": return "avi"
        case "mov": return "mov"
        case "wmv": return "wmv"
        case "flv": return "flv"
        case "ts", "m2ts", "mts": return "ts"
        case "webm": return "webm"
        case "rmvb", "rm": return "rmvb"
        case "iso": return "iso"
        case "strm": return ""
        }
        return ext
}

// applySubtitles 匹配并写入外挂字幕。
func (s *Scanner) applySubtitles(item *models.Item, src models.MediaSource, videoPath string) {
        // 清掉旧的外挂字幕
        db.DB.Where("item_id = ? AND is_external = ?", item.ID, true).Delete(&models.MediaStream{})
        subs := MatchSubs(videoPath)
        var maxIdx int
        db.DB.Model(&models.MediaStream{}).Where("item_id = ?", item.ID).Select("COALESCE(MAX(index),-1)").Scan(&maxIdx)
        for i, sub := range subs {
                st := models.MediaStream{
                        SourceID: src.ID, ItemID: item.ID, Index: maxIdx + 1 + i,
                        Type: "Subtitle", Codec: sub.Format, Language: sub.Language,
                        DisplayTitle: DisplayName(sub), Title: sub.Title,
                        IsExternal: true, IsForced: sub.IsForced, IsDefault: sub.IsDefault,
                        Path: sub.Path,
                }
                db.DB.Create(&st)
        }
        if len(subs) > 0 {
                logx.InfoC(logx.CatSubtitle, "《%s》匹配到 %d 个外挂字幕", item.Name, len(subs))
                s.tlog(item.LibraryID, "info", "《%s》匹配到 %d 个外挂字幕：%s", item.Name, len(subs), filepath.Base(videoPath))
        }
}

// posterNames 常见海报文件名。
var posterNames = []string{"poster.jpg", "poster.png", "poster.webp", "folder.jpg", "folder.png", "cover.jpg", "default.jpg"}
var backdropNames = []string{"fanart.jpg", "fanart.png", "backdrop.jpg", "backdrop.png", "background.jpg", "landscape.jpg"}
var logoNames = []string{"logo.png", "logo.jpg", "clearlogo.png"}

// applyLocalImages 应用本地图片（海报/背景/缩略图）。
func (s *Scanner) applyLocalImages(item *models.Item, dir, base string) {
        changed := false
        for _, imagePath := range []*string{&item.Poster, &item.Backdrop, &item.Thumb, &item.Logo} {
                if *imagePath == "" {
                        continue
                }
                if info, err := os.Stat(*imagePath); err != nil || info.IsDir() {
                        *imagePath = ""
                        changed = true
                }
        }
        if item.Poster == "" {
                var cands []string
                if base != "" {
                        cands = append(cands,
                                filepath.Join(dir, base+"-poster.jpg"), filepath.Join(dir, base+"-poster.png"),
                                filepath.Join(dir, base+"-thumb.jpg"), filepath.Join(dir, base+".jpg"), filepath.Join(dir, base+".png"),
                        )
                }
                for _, n := range posterNames {
                        cands = append(cands, filepath.Join(dir, n))
                }
                for _, c := range cands {
                        if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
                                item.Poster = c
                                changed = true
                                break
                        }
                }
        }
        if item.Backdrop == "" {
                for _, n := range backdropNames {
                        c := filepath.Join(dir, n)
                        if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
                                item.Backdrop = c
                                changed = true
                                break
                        }
                }
        }
        if item.Logo == "" {
                for _, n := range logoNames {
                        c := filepath.Join(dir, n)
                        if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
                                item.Logo = c
                                changed = true
                                break
                        }
                }
        }
        if item.Thumb == "" && item.Type == "Episode" {
                c := filepath.Join(dir, strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))+"-thumb.jpg")
                if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
                        item.Thumb = c
                        changed = true
                }
        }
        if changed {
                if item.Type == "Movie" && item.Poster != "" {
                        item.ScrapeError = ""
                }
                item.ImageRev = imageRev(item.Poster, item.Backdrop, item.Thumb, item.Logo)
                db.DB.Model(&models.Item{}).Where("id = ?", item.ID).Updates(map[string]any{
                        "poster": item.Poster, "backdrop": item.Backdrop,
                        "thumb": item.Thumb, "logo": item.Logo, "image_rev": item.ImageRev,
                        "scrape_error": item.ScrapeError,
                })
                var found []string
                if item.Poster != "" {
                        found = append(found, "海报")
                }
                if item.Backdrop != "" {
                        found = append(found, "背景")
                }
                if item.Logo != "" {
                        found = append(found, "Logo")
                }
                if item.Thumb != "" {
                        found = append(found, "缩略图")
                }
                s.tlog(item.LibraryID, "info", "本地图片「%s」：%s", filepath.Base(item.Path), strings.Join(found, "/"))
        }
}

// toJSON 序列化为 JSON 字符串。
func toJSON(v any) string {
        b, err := json.Marshal(v)
        if err != nil {
                return ""
        }
        return string(b)
}

func atoi(s string) int {
        n, _ := strconv.Atoi(strings.TrimSpace(s))
        return n
}

var _ = fmt.Sprintf

// refreshLight 未变化文件的轻量刷新（字幕与本地图片）。
func (s *Scanner) refreshLight(item *models.Item, vf videoFile) {
        s.tlog(item.LibraryID, "info", "未变化，轻量刷新：%s", vf.path)
        var src models.MediaSource
        if err := db.DB.Where("item_id = ?", item.ID).First(&src).Error; err != nil {
                return
        }
        s.applySubtitles(item, src, vf.path)
        if item.Type == "Movie" {
                s.applyLocalImages(item, filepath.Dir(vf.path), strings.TrimSuffix(filepath.Base(vf.path), filepath.Ext(vf.path)))
        } else if item.Type == "Episode" {
                s.applyLocalImages(item, filepath.Dir(vf.path), strings.TrimSuffix(filepath.Base(vf.path), filepath.Ext(vf.path)))
        }
}
