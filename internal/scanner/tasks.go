// Package scanner 后台任务：ffprobe 探测队列与 TMDB 刮削队列。
package scanner

import (
        "path/filepath"
        "context"
        "encoding/json"
        "fmt"
        "strings"
        "strconv"
        "sync"
        "time"

        "go-emby/internal/db"
        "go-emby/internal/logx"
        "go-emby/internal/models"
        "go-emby/internal/probe"
        "go-emby/internal/tmdb"
)

var probeMu sync.Mutex // ffprobe 进程级串行保护（限制同时拉起的子进程总数）

// doProbe 执行单个探测任务并计数。
func (s *Scanner) doProbe(t probeTask) {
        probeRunning.Add(1)
        defer probeRunning.Add(-1)
        defer func() { probeDedupMu.Lock(); delete(probeDedup, t.itemID); probeDedupMu.Unlock() }()

        ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
        r, err := probe.Probe(ctx, t.path)
        cancel()
        if err != nil {
                if batchStop.Load() {
                        return
                }
                probeFailed.Add(1)
                logx.WarnC(logx.CatProbe, "ffprobe 失败 %s: %v", filepath.Base(t.path), err)
                return
        }
        // 替换内嵌流（保留外挂字幕）
        db.DB.Where("item_id = ? AND is_external = ?", t.itemID, false).Delete(&models.MediaStream{})
        streams := probe.ToStreams(t.sourceID, t.itemID, r)
        for i := range streams {
                db.DB.Create(&streams[i])
        }
        rt := probe.RunTimeTicks(r)
        size := int64(0)
        if v, err := strconv.ParseInt(r.Format.Size, 10, 64); err == nil {
                size = v
        }
        container := firstFormat(r.Format.FormatName)
        updates := map[string]any{"run_time_ticks": rt, "size": size, "container": container}
        db.DB.Model(&models.Item{}).Where("id = ?", t.itemID).Updates(updates)
        db.DB.Model(&models.MediaSource{}).Where("id = ?", t.sourceID).Updates(map[string]any{"size": size})
        // 刷新图片修订
        var it models.Item
        if db.DB.First(&it, "id = ?", t.itemID).Error == nil {
                db.DB.Model(&it).Update("image_rev", imageRev(it.Poster, it.Backdrop, it.Thumb, it.Logo))
        }
        probeDone.Add(1)
        logx.DetailC(logx.CatProbe, "info", fmt.Sprintf("%d 轨道", len(streams)), "媒体信息提取完成 %s", filepath.Base(t.path))
}

// enqueueProbe 加入探测队列（计数等待数）。
func (s *Scanner) enqueueProbe(item *models.Item, src models.MediaSource, mtime int64) {
        select {
        case s.probeQueue <- probeTask{sourceID: src.ID, itemID: item.ID, path: src.Path, mtime: mtime}:
                probeWaiting.Add(1)
        default:
                logx.WarnC(logx.CatProbe, "探测队列已满，跳过 %s", src.Path)
        }
}

// enqueueScrape 加入刮削队列。
func (s *Scanner) enqueueScrape(itemID string) {
        select {
        case s.scrapeQueue <- scrapeTask{itemID: itemID}:
                probeWaiting.Add(1)
        default:
        }
}

// scrapeWorker 刮削工作线程。
func (s *Scanner) scrapeWorker() {
        for t := range s.scrapeQueue {
                probeWaiting.Add(-1)
                waitWhilePaused()
                cfg := LoadScrapeConfig()
                if !cfg.Enabled {
                        continue // 刮削总开关关闭：丢弃任务
                }
                ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
                err := s.scrapeItem(ctx, t.itemID)
                cancel()
                if err != nil {
                        db.DB.Model(&models.Item{}).Where("id = ?", t.itemID).Update("scrape_error", truncateErr(err.Error()))
                        logx.WarnC(logx.CatScrape, "刮削失败 item=%s: %v", t.itemID, err)
                } else {
                        db.DB.Model(&models.Item{}).Where("id = ? AND scrape_error != ''", t.itemID).Update("scrape_error", "")
                }
        }
}

// truncateErr 错误信息截断。
func truncateErr(s string) string {
        if len(s) > 480 {
                return s[:480]
        }
        return s
}

// ScrapeNow 立即同步刮削某条目（供 API 调用）。
func (s *Scanner) ScrapeNow(itemID string) error {
        ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
        defer cancel()
        return s.scrapeItem(ctx, itemID)
}

// scrapeItem 刮削单个条目。
func (s *Scanner) scrapeItem(ctx context.Context, itemID string) error {
        set := tmdb.LoadSettings()
        if set.APIKey == "" {
                return fmt.Errorf("未配置 TMDB API Key")
        }
        var item models.Item
        if err := db.DB.First(&item, "id = ?", itemID).Error; err != nil {
                return err
        }
        // 已有 TMDB ID（NFO 提供）则直接获取详情
        ids := map[string]string{}
        if item.ProviderIDs != "" {
                _ = json.Unmarshal([]byte(item.ProviderIDs), &ids)
        }

        switch item.Type {
        case "Movie":
                return s.scrapeMovie(ctx, &item, set, ids)
        case "Series":
                return s.scrapeSeries(ctx, &item, set, ids)
        }
        return nil
}

func (s *Scanner) scrapeMovie(ctx context.Context, item *models.Item, set tmdb.Settings, ids map[string]string) error {
        var detail *tmdb.MovieDetail
        if v := ids["Tmdb"]; v != "" {
                id := atoi(v)
                d, err := tmdb.GetMovie(ctx, set.APIKey, id, set.Language)
                if err == nil {
                        detail = d
                }
        }
        if detail == nil {
                title, year := item.Name, item.Year
                results, err := tmdb.SearchMovie(ctx, set.APIKey, title, set.Language, year)
                if err != nil {
                        return err
                }
                if len(results) == 0 && year > 0 {
                        results, err = tmdb.SearchMovie(ctx, set.APIKey, title, set.Language, 0)
                        if err != nil {
                                return err
                        }
                }
                if len(results) == 0 {
                        logx.InfoC(logx.CatTMDB, "TMDB 未找到电影: %s (%d)", title, year)
                        return fmt.Errorf("TMDB 未找到匹配: %s (%d)", title, year)
                }
                best := results[0]
                if year > 0 {
                        for _, r := range results {
                                if ry := parseYear(r.ReleaseDate); ry == year {
                                        best = r
                                        break
                                }
                        }
                }
                d, err := tmdb.GetMovie(ctx, set.APIKey, best.ID, set.Language)
                if err != nil {
                        return err
                }
                detail = d
        }
        s.applyMovieDetail(item, detail, set)
        if err := db.DB.Save(item).Error; err != nil {
                return err
        }
        logx.InfoC(logx.CatScrape, "刮削电影《%s》(%d) 完成", item.Name, item.Year)
        return nil
}

func (s *Scanner) applyMovieDetail(item *models.Item, d *tmdb.MovieDetail, set tmdb.Settings) {
        item.Name = d.Title
        item.OriginalTitle = d.OriginalTitle
        item.Overview = d.Overview
        if d.ReleaseDate != "" {
                if t, err := time.Parse("2006-01-02", d.ReleaseDate[:min(10, len(d.ReleaseDate))]); err == nil {
                        item.PremiereDate = &t
                        item.Year = t.Year()
                }
        }
        if d.Runtime > 0 {
                item.RunTimeTicks = int64(d.Runtime) * 60 * 10000000
        }
        item.CommunityRating = d.VoteAverage
        item.OfficialRating = mpaa(d.Status)
        if len(d.Genres) > 0 {
                var gs []string
                for _, g := range d.Genres {
                        gs = append(gs, g.Name)
                }
                item.Genres = toJSON(gs)
        }
        var studios []string
        for _, c := range d.ProductionCompanies {
                studios = append(studios, c.Name)
        }
        if len(studios) > 0 {
                item.Studios = toJSON(studios)
        }
        ids := map[string]string{"Tmdb": fmt.Sprint(d.ID)}
        if d.IMDBID != "" {
                ids["Imdb"] = d.IMDBID
        }
        item.ProviderIDs = toJSON(ids)
        item.Scraped = true

        // 图片
        if set.DownloadImgs {
                if p, err := tmdb.DownloadImage(s.metaDir(), item.ID, "w500", d.PosterPath); err == nil {
                        item.Poster = p
                }
                if b, err := tmdb.DownloadImage(s.metaDir(), item.ID, "w1280", d.BackdropPath); err == nil {
                        item.Backdrop = b
                }
        }
        item.ImageRev = imageRev(item.Poster, item.Backdrop)
}

// firstFormat 取 ffprobe format_name 首段。
func firstFormat(f string) string {
        f = strings.SplitN(f, ",", 2)[0]
        if f == "" || len(f) > 5 {
                return ""
        }
        return f
}

func parseYear(d string) int {
        if len(d) >= 4 {
                return atoi(d[:4])
        }
        return 0
}

func min(a, b int) int {
        if a < b {
                return a
        }
        return b
}

func (s *Scanner) scrapeSeries(ctx context.Context, item *models.Item, set tmdb.Settings, ids map[string]string) error {
        var detail *tmdb.TVDetail
        if v := ids["Tmdb"]; v != "" {
                d, err := tmdb.GetTV(ctx, set.APIKey, atoi(v), set.Language)
                if err == nil {
                        detail = d
                }
        }
        if detail == nil {
                title, year := item.Name, item.Year
                results, err := tmdb.SearchTV(ctx, set.APIKey, title, set.Language, year)
                if err != nil {
                        return err
                }
                if len(results) == 0 && year > 0 {
                        results, err = tmdb.SearchTV(ctx, set.APIKey, title, set.Language, 0)
                        if err != nil {
                                return err
                        }
                }
                if len(results) == 0 {
                        logx.InfoC(logx.CatTMDB, "TMDB 未找到剧集: %s", title)
                        return fmt.Errorf("TMDB 未找到匹配: %s", title)
                }
                best := results[0]
                if year > 0 {
                        for _, r := range results {
                                if parseYear(r.FirstAirDate) == year {
                                        best = r
                                        break
                                }
                        }
                }
                d, err := tmdb.GetTV(ctx, set.APIKey, best.ID, set.Language)
                if err != nil {
                        return err
                }
                detail = d
        }
        // 应用剧集信息
        item.Name = detail.Name
        item.OriginalTitle = detail.OriginalName
        item.Overview = detail.Overview
        if detail.FirstAirDate != "" {
                if t, err := time.Parse("2006-01-02", detail.FirstAirDate[:min(10, len(detail.FirstAirDate))]); err == nil {
                        item.PremiereDate = &t
                        item.Year = t.Year()
                }
        }
        item.CommunityRating = detail.VoteAverage
        var gs []string
        for _, g := range detail.Genres {
                gs = append(gs, g.Name)
        }
        if len(gs) > 0 {
                item.Genres = toJSON(gs)
        }
        var nets []string
        for _, n := range detail.Networks {
                nets = append(nets, n.Name)
        }
        if len(nets) > 0 {
                item.Studios = toJSON(nets)
        }
        item.ProviderIDs = toJSON(map[string]string{"Tmdb": fmt.Sprint(detail.ID)})
        item.Scraped = true
        if set.DownloadImgs {
                if p, err := tmdb.DownloadImage(s.metaDir(), item.ID, "w500", detail.PosterPath); err == nil {
                        item.Poster = p
                }
                if b, err := tmdb.DownloadImage(s.metaDir(), item.ID, "w1280", detail.BackdropPath); err == nil {
                        item.Backdrop = b
                }
        }
        item.ImageRev = imageRev(item.Poster, item.Backdrop)
        if err := db.DB.Save(item).Error; err != nil {
                return err
        }
        logx.InfoC(logx.CatScrape, "刮削剧集《%s》完成（共 %d 季）", item.Name, detail.NumberOfSeasons)

        // 刮削各季与各集
        s.scrapeSeasons(ctx, item, set, detail.ID)
        return nil
}

// scrapeSeasons 刮削季与集。
func (s *Scanner) scrapeSeasons(ctx context.Context, series *models.Item, set tmdb.Settings, tvID int) {
        // 找到该剧集的所有季与集
        var seasons []models.Item
        db.DB.Where("series_id = ? AND type = 'Season'", series.ID).Order("parent_index_number").Find(&seasons)
        for _, season := range seasons {
                resp, err := tmdb.GetSeason(ctx, set.APIKey, tvID, season.ParentIndexNumber, set.Language)
                if err != nil {
                        logx.WarnC(logx.CatScrape, "获取季详情失败 %s S%d: %v", series.Name, season.ParentIndexNumber, err)
                        continue
                }
                season.Name = resp.Name
                if season.Name == "" {
                        season.Name = fmt.Sprintf("第 %d 季", season.ParentIndexNumber)
                }
                season.Overview = resp.Overview
                if set.DownloadImgs && resp.PosterPath != "" {
                        if p, err := tmdb.DownloadImage(s.metaDir(), season.ID, "w500", resp.PosterPath); err == nil {
                                season.Poster = p
                        }
                }
                season.Scraped = true
                db.DB.Save(&season)

                // 更新集信息
                var episodes []models.Item
                db.DB.Where("season_id = ?", season.ID).Order("index_number").Find(&episodes)
                for i := range resp.Episodes {
                        e := resp.Episodes[i]
                        for j := range episodes {
                                if episodes[j].IndexNumber == e.EpisodeNumber {
                                        ep := &episodes[j]
                                        ep.Name = e.Name
                                        if ep.Name == "" {
                                                ep.Name = fmt.Sprintf("第 %d 集", e.EpisodeNumber)
                                        }
                                        ep.Overview = e.Overview
                                        if e.AirDate != "" && len(e.AirDate) >= 10 {
                                                if t, err := time.Parse("2006-01-02", e.AirDate[:10]); err == nil {
                                                        ep.PremiereDate = &t
                                                }
                                        }
                                        if e.Runtime > 0 {
                                                ep.RunTimeTicks = int64(e.Runtime) * 60 * 10000000
                                        }
                                        if set.DownloadImgs && e.StillPath != "" {
                                                if p, err := tmdb.DownloadImage(s.metaDir(), ep.ID, "w300", e.StillPath); err == nil {
                                                        ep.Thumb = p
                                                }
                                        }
                                        ep.Scraped = true
                                        db.DB.Save(ep)
                                }
                        }
                }
        }
}

// metaDir 元数据目录。
func (s *Scanner) metaDir() string { return s.cfg.MetaDir() }

func mpaa(status string) string { return "" }
