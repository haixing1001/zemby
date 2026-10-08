// Package scanner 后台任务：ffprobe 探测队列与 TMDB 刮削队列。
package scanner

import (
        "path/filepath"
        "context"
        "encoding/json"
        "fmt"
	"os"
        "strings"
        "strconv"
        "sync"
        "time"

	"gorm.io/gorm"

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
        start := time.Now()

        ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
        logx.TaskLog("probe", "info", "开始提取：%s", t.path)
        r, err := probe.Probe(ctx, t.path)
        cancel()
        if err != nil {
                if batchStop.Load() {
                        return
                }
                probeFailed.Add(1)
                logx.WarnC(logx.CatProbe, "ffprobe 失败 %s: %v", filepath.Base(t.path), err)
                logx.TaskLog("probe", "warn", "提取失败 %s：%v", t.path, err)
                return
        }
        streams := probe.ToStreams(t.sourceID, t.itemID, r)
        rt := probe.RunTimeTicks(r)
        size := int64(0)
        if v, err := strconv.ParseInt(r.Format.Size, 10, 64); err == nil {
                size = v
        }
        container := firstFormat(r.Format.FormatName)
        updates := map[string]any{"run_time_ticks": rt, "size": size, "container": container}
	if err := db.DB.Transaction(func(tx *gorm.DB) error {
		// 原子替换内嵌流；失败时保留旧流信息和旧探测指纹，后续扫描可以重试。
		if err := tx.Where("item_id = ? AND is_external = ?", t.itemID, false).Delete(&models.MediaStream{}).Error; err != nil {
			return err
		}
		for i := range streams {
			if err := tx.Create(&streams[i]).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.Item{}).Where("id = ?", t.itemID).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(&models.MediaSource{}).Where("id = ?", t.sourceID).Updates(map[string]any{
			"size": size, "probed_mtime": t.mtime, "probed_size": t.size,
		}).Error
	}); err != nil {
		probeFailed.Add(1)
		logx.WarnC(logx.CatProbe, "保存探测结果失败 %s: %v", filepath.Base(t.path), err)
		logx.TaskLog("probe", "error", "保存探测结果失败 %s：%v", t.path, err)
		return
	}
        // 刷新图片修订
        var it models.Item
        if db.DB.First(&it, "id = ?", t.itemID).Error == nil {
                db.DB.Model(&it).Update("image_rev", imageRev(it.Poster, it.Backdrop, it.Thumb, it.Logo))
        }
        probeDone.Add(1)
        // 任务详细日志：流信息摘要
        var vc, ac, sc int
        var vDesc string
        for i := range streams {
                switch streams[i].Type {
                case "Video":
                        vc++
                        if vDesc == "" {
                                vDesc = fmt.Sprintf("%s %dx%d", strings.ToUpper(streams[i].Codec), streams[i].Width, streams[i].Height)
                        }
                case "Audio":
                        ac++
                case "Subtitle":
                        sc++
                }
        }
        if vDesc == "" {
                vDesc = "无视频轨"
        }
        logx.DetailC(logx.CatProbe, "info", fmt.Sprintf("%d 轨道", len(streams)), "媒体信息提取完成 %s", filepath.Base(t.path))
        logx.TaskLog("probe", "info", "提取完成 %s · %s · 内嵌 视频%d/音频%d/字幕%d 共%d轨 · 耗时 %s",
                t.path, vDesc, vc, ac, sc, len(streams), time.Since(start).Round(time.Millisecond))
        // 剧集媒体信息复用：同季缺失的集复制本次提取结果
        if LoadEnhanceConfig().EpisodeMediaReuse {
                s.reuseSeasonStreams(t.itemID)
        }
}

// reuseSeasonStreams 将 src 集的内嵌流信息复制到同季其它缺失的集。
func (s *Scanner) reuseSeasonStreams(srcItemID string) {
        var src models.Item
        if db.DB.First(&src, "id = ?", srcItemID).Error != nil || src.Type != "Episode" || src.SeriesID == "" || src.ParentIndexNumber <= 0 {
                return
        }
        if n := s.copySeasonMedia(&src); n > 0 {
                logx.InfoC(logx.CatProbe, "剧集媒体信息复用：《%s》第 %d 季复制到 %d 集", src.SeriesName, src.ParentIndexNumber, n)
        }
}

// copySeasonMedia 复制同季流信息，返回复制条数。
func (s *Scanner) copySeasonMedia(src *models.Item) int {
        var sibs []models.Item
        db.DB.Where("series_id = ? AND parent_index_number = ? AND type = 'Episode' AND id <> ?",
                src.SeriesID, src.ParentIndexNumber, src.ID).Find(&sibs)
        n := 0
        for i := range sibs {
                sib := &sibs[i]
                var cnt int64
                db.DB.Model(&models.MediaStream{}).Where("item_id = ? AND is_external = ?", sib.ID, false).Count(&cnt)
                if cnt > 0 {
                        continue // 已有自己的内嵌流
                }
                var sibSrc models.MediaSource
                if db.DB.Where("item_id = ?", sib.ID).Order("\"default\" DESC").First(&sibSrc).Error != nil {
                        continue // 无媒体源无法挂载流信息
                }
                var streams []models.MediaStream
                db.DB.Where("item_id = ? AND is_external = ?", src.ID, false).Find(&streams)
                if len(streams) == 0 {
                        continue
                }
                for _, st := range streams {
                        st.ID = 0
                        st.ItemID = sib.ID
                        st.SourceID = sibSrc.ID
                        db.DB.Create(&st)
                }
                n++
        }
        return n
}

// tryReuseOnBrowse 浏览时尝试同季复用（命中返回 true，无需再探测）。
func (s *Scanner) tryReuseOnBrowse(item *models.Item) bool {
        if !LoadEnhanceConfig().EpisodeMediaReuse || item.Type != "Episode" || item.SeriesID == "" || item.ParentIndexNumber <= 0 {
                return false
        }
        // 同季是否存在已提取的兄弟集
        var cnt int64
        db.DB.Raw(`SELECT COUNT(1) FROM items i INNER JOIN media_streams ms ON ms.item_id = i.id AND ms.is_external = ?
                WHERE i.series_id = ? AND i.parent_index_number = ? AND i.type = 'Episode' AND i.id <> ?`,
                false, item.SeriesID, item.ParentIndexNumber, item.ID).Scan(&cnt)
        if cnt == 0 {
                return false
        }
        if s.copySeasonMedia(item) > 0 {
                logx.InfoC(logx.CatProbe, "剧集媒体信息复用：《%s》第 %d 季（浏览时命中）", item.SeriesName, item.ParentIndexNumber)
                return true
        }
        return false
}

// enqueueProbe 加入探测队列（计数等待数）。
func (s *Scanner) enqueueProbe(item *models.Item, src models.MediaSource, mtime int64) bool {
        return s.enqueueProbeTask(probeTask{sourceID: src.ID, itemID: item.ID, path: src.Path, mtime: mtime, size: item.Size})
}

func (s *Scanner) enqueueProbeTask(task probeTask) bool {
        probeWaiting.Add(1)
        select {
		case s.probeQueue <- task:
                return true
        default:
                probeWaiting.Add(-1)
                logx.WarnC(logx.CatProbe, "探测队列已满，跳过 %s", task.path)
                logx.TaskLog("probe", "warn", "探测队列已满，跳过 %s", task.path)
                return false
        }
}

// enqueueScrape 加入刮削队列。
func (s *Scanner) enqueueScrape(itemID string) bool {
	return s.enqueueScrapeTask(scrapeTask{itemID: itemID})
}

func (s *Scanner) enqueueScrapeTask(task scrapeTask) bool {
	probeWaiting.Add(1)
	if !s.scrapeQueue.Push(task) {
                probeWaiting.Add(-1)
		return false
        }
	return true
}

// QueueScrape queues an explicit single-item refresh, optionally searching by
// a caller-provided title and year instead of the item's existing TMDB ID.
func (s *Scanner) QueueScrape(itemID, query string, year int) (bool, error) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) > 200 {
		return false, fmt.Errorf("关键词不能超过 200 个字符")
	}
	if year < 0 || year > 2100 {
		return false, fmt.Errorf("年份范围无效")
	}
	if query == "" && year != 0 {
		return false, fmt.Errorf("设置年份时必须同时提供关键词")
	}
	var item models.Item
	if err := db.DB.Select("id", "type").First(&item, "id = ?", itemID).Error; err != nil {
		return false, fmt.Errorf("媒体条目不存在")
	}
	if item.Type != "Movie" && item.Type != "Series" {
		return false, fmt.Errorf("仅支持电影和剧集刮削")
	}
	if !s.enqueueScrapeTask(scrapeTask{itemID: itemID, query: query, year: year, manual: true}) {
		return false, nil
	}
	SetScrapeState("running")
	if query != "" {
		logx.TaskLog("scrape", "info", "手动关键词刮削已加入队列：「%s」(%d)", query, year)
	} else {
		logx.TaskLog("scrape", "info", "条目重新刮削已加入队列：%s", itemID)
	}
	return true, nil
}

func shouldQueueScrape(item *models.Item) bool {
	if item == nil || item.ScrapeError != "" {
		return false
	}
	if !item.Scraped {
		return true
	}
	if item.Type != "Movie" || !LoadEnhanceConfig().TMDB || !tmdb.LoadSettings().DownloadImgs {
		return false
	}
	if item.Poster == "" {
		return true
	}
	info, err := os.Stat(item.Poster)
	return err != nil || info.IsDir()
}

func (s *Scanner) enqueueMissingMoviePosters() {
	if !LoadEnhanceConfig().TMDB || !LoadScrapeConfig().Enabled {
		return
	}
	settings := tmdb.LoadSettings()
	if settings.APIKey == "" || !settings.DownloadImgs {
		return
	}
	var items []models.Item
	if err := db.DB.Select("id, poster").Where("type = ? AND scraped = ? AND scrape_error = ''", "Movie", true).Find(&items).Error; err != nil {
		logx.WarnC(logx.CatScrape, "检查缺失电影海报失败：%v", err)
		return
	}
	queued := 0
	for i := range items {
		if !hasImageFile(items[i].Poster) && s.enqueueScrape(items[i].ID) {
			queued++
		}
	}
	if queued > 0 {
		logx.InfoC(logx.CatScrape, "启动时发现 %d 部电影缺少海报，已加入修复队列", queued)
	}
}

// scrapeWorker 刮削工作线程。
func (s *Scanner) scrapeWorker() {
	for {
		t := s.scrapeQueue.Pop()
                probeWaiting.Add(-1)
		func() {
			defer s.scrapeQueue.Done(t.itemID)
                waitWhilePaused()
                cfg := LoadScrapeConfig()
			if !cfg.Enabled && !t.manual {
				return // 刮削总开关关闭：丢弃任务
                }
			if !LoadEnhanceConfig().TMDB && !t.manual {
				return // 增强功能「启动TMDB」关闭：静默跳过（开启后可重新批量刮削）
                }
                ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		err := s.scrapeItemWithKeyword(ctx, t.itemID, t.query, t.year)
                cancel()
                if err != nil {
                        db.DB.Model(&models.Item{}).Where("id = ?", t.itemID).Update("scrape_error", truncateErr(err.Error()))
                        logx.WarnC(logx.CatScrape, "刮削失败 item=%s: %v", t.itemID, err)
                } else {
                        db.DB.Model(&models.Item{}).Where("id = ? AND scrape_error != ''", t.itemID).Update("scrape_error", "")
                }
		}()
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
	return s.scrapeItemWithKeyword(ctx, itemID, "", 0)
}

func (s *Scanner) scrapeItemWithKeyword(ctx context.Context, itemID, query string, queryYear int) error {
        set := tmdb.LoadSettings()
        if !LoadEnhanceConfig().TMDB {
                return fmt.Errorf("TMDB 未开启（设置-增强功能-启动TMDB）")
        }
        if set.APIKey == "" {
                return fmt.Errorf("未配置 TMDB API Key")
        }
        var item models.Item
        if err := db.DB.First(&item, "id = ?", itemID).Error; err != nil {
                return err
        }
        typeLabel := "条目"
        switch item.Type {
        case "Movie":
                typeLabel = "电影"
        case "Series":
                typeLabel = "剧集"
        }
        logx.TaskLog("scrape", "info", "开始刮削%s「%s」(%d) · %s", typeLabel, item.Name, item.Year, item.Path)
        // 已有 TMDB ID（NFO 提供）则直接获取详情
        ids := map[string]string{}
        if item.ProviderIDs != "" {
                _ = json.Unmarshal([]byte(item.ProviderIDs), &ids)
        }
	if query = strings.TrimSpace(query); query != "" {
		item.Name = query
		item.Year = queryYear
		ids = map[string]string{} // 手动关键词要求重新搜索，而不是沿用旧 TMDB ID。
	}
        var err error
        switch item.Type {
        case "Movie":
                err = s.scrapeMovie(ctx, &item, set, ids)
        case "Series":
                err = s.scrapeSeries(ctx, &item, set, ids)
        default:
                return nil
        }
        if err != nil {
                logx.TaskLog("scrape", "error", "刮削失败「%s」：%v", item.Name, err)
        }
        return err
}

func (s *Scanner) scrapeMovie(ctx context.Context, item *models.Item, set tmdb.Settings, ids map[string]string) error {
        var detail *tmdb.MovieDetail
	searchPosterPath := ""
        if v := ids["Tmdb"]; v != "" {
                id := atoi(v)
                d, err := tmdb.GetMovie(ctx, set.APIKey, id, set.Language)
                if err == nil {
                        detail = d
                }
        }
        if detail == nil {
                title, year := item.Name, item.Year
                matchYear := year
                logx.TaskLog("scrape", "info", "TMDB 搜索电影：「%s」(%d)", title, year)
                results, err := tmdb.SearchMovie(ctx, set.APIKey, title, set.Language, year)
                if err != nil {
                        return err
                }
                if len(results) == 0 && year > 0 {
                        logx.TaskLog("scrape", "info", "带年份无结果，降级无年份重搜「%s」", title)
                        results, err = tmdb.SearchMovie(ctx, set.APIKey, title, set.Language, 0)
                        if err != nil {
                                return err
                        }
                }
                if len(results) == 0 {
                        // AI 识别辅助：从文件路径提取关键词后重试搜索
                        if keywords, ok := s.aiRetrySearch(ctx, "Movie", item.Path, item.Name, item.Year); ok {
                                logx.TaskLog("scrape", "info", "AI 关键词重搜电影：「%s」/「%s」(%d)", keywords.Title, keywords.OriginalTitle, keywords.Year)
                                if keywords.Year > 0 {
                                        matchYear = keywords.Year
                                }
                                results, err = searchWithAIKeywords(ctx, set.APIKey, set.Language, keywords, tmdb.SearchMovie)
                                if err != nil {
                                        return err
                                }
                        }
                }
                logx.TaskLog("scrape", "info", "搜索结果：%d 条", len(results))
                if len(results) == 0 {
                        logx.InfoC(logx.CatTMDB, "TMDB 未找到电影: %s (%d)", title, year)
                        return fmt.Errorf("TMDB 未找到匹配: %s (%d)", title, year)
                }
				best := bestMovieResult(results, matchYear)
				searchPosterPath = best.PosterPath
                logx.TaskLog("scrape", "info", "命中《%s》(首播 %s) TmdbID=%d", best.Title, best.ReleaseDate, best.ID)
                d, err := tmdb.GetMovie(ctx, set.APIKey, best.ID, set.Language)
                if err != nil {
                        return err
                }
                detail = d
        }
		if detail.PosterPath == "" {
			detail.PosterPath = searchPosterPath
		}
		if detail.PosterPath == "" && set.DownloadImgs {
			if posterPath, err := tmdb.GetMoviePoster(ctx, set.APIKey, detail.ID, set.Language); err == nil {
				detail.PosterPath = posterPath
			} else {
				logx.TaskLog("tmdb", "warn", "获取电影海报列表失败《%s》：%v", item.Name, err)
			}
		}
		imageErr := s.applyMovieDetail(ctx, item, detail, set)
        if err := db.DB.Save(item).Error; err != nil {
                return err
        }
		if imageErr != nil {
			logx.TaskLog("scrape", "error", "电影《%s》元数据已保存，但海报不可用：%v", item.Name, imageErr)
			return imageErr
		}
        logx.InfoC(logx.CatScrape, "刮削电影《%s》(%d) 完成", item.Name, item.Year)
        logx.TaskLog("scrape", "info", "电影《%s》(%d) 刮削完成 · 评分 %.1f · 类型 %s · 海报=%v 背景=%v",
                item.Name, item.Year, item.CommunityRating, item.Genres, item.Poster != "", item.Backdrop != "")
        return nil
}

func bestMovieResult(results []tmdb.SearchResult, year int) tmdb.SearchResult {
	if len(results) == 0 {
		return tmdb.SearchResult{}
	}
	best := results[0]
	bestYearMatch := year > 0 && parseYear(best.ReleaseDate) == year
	for _, result := range results {
		resultYearMatch := year > 0 && parseYear(result.ReleaseDate) == year
		sameYearMatch := resultYearMatch == bestYearMatch
		betterArtwork := best.PosterPath == "" && result.PosterPath != ""
		sameArtworkAndHigherRating := (best.PosterPath != "") == (result.PosterPath != "") && result.VoteAverage > best.VoteAverage
		if resultYearMatch && !bestYearMatch || sameYearMatch && (betterArtwork || sameArtworkAndHigherRating) {
			best = result
			bestYearMatch = resultYearMatch
		}
	}
	return best
}

func searchWithAIKeywords(ctx context.Context, key, lang string, keywords *AIKeywords, search func(context.Context, string, string, string, int) ([]tmdb.SearchResult, error)) ([]tmdb.SearchResult, error) {
	if keywords == nil {
		return nil, nil
	}
	titles := []string{strings.TrimSpace(keywords.Title), strings.TrimSpace(keywords.OriginalTitle)}
	seenTitles := make(map[string]struct{}, len(titles))
	seenResults := make(map[int]struct{})
	var results []tmdb.SearchResult
	for _, title := range titles {
		if title == "" {
			continue
		}
		keyTitle := strings.ToLower(title)
		if _, ok := seenTitles[keyTitle]; ok {
			continue
		}
		seenTitles[keyTitle] = struct{}{}
		found, err := search(ctx, key, title, lang, keywords.Year)
		if err != nil {
			if len(results) > 0 {
				return results, nil
			}
			return nil, err
		}
		if len(found) == 0 && keywords.Year > 0 {
			found, err = search(ctx, key, title, lang, 0)
			if err != nil {
				if len(results) > 0 {
					return results, nil
				}
				return nil, err
			}
		}
		for _, result := range found {
			if _, ok := seenResults[result.ID]; ok {
				continue
			}
			seenResults[result.ID] = struct{}{}
			results = append(results, result)
		}
	}
	return results, nil
}

func bestTVResult(results []tmdb.SearchResult, year int) tmdb.SearchResult {
	if len(results) == 0 {
		return tmdb.SearchResult{}
	}
	best := results[0]
	bestYearMatch := year > 0 && parseYear(best.FirstAirDate) == year
	for _, result := range results {
		resultYearMatch := year > 0 && parseYear(result.FirstAirDate) == year
		sameYearMatch := resultYearMatch == bestYearMatch
		betterArtwork := best.PosterPath == "" && result.PosterPath != ""
		sameArtworkAndHigherRating := (best.PosterPath != "") == (result.PosterPath != "") && result.VoteAverage > best.VoteAverage
		if resultYearMatch && !bestYearMatch || sameYearMatch && (betterArtwork || sameArtworkAndHigherRating) {
			best = result
			bestYearMatch = resultYearMatch
		}
	}
	return best
}

func (s *Scanner) applyMovieDetail(ctx context.Context, item *models.Item, d *tmdb.MovieDetail, set tmdb.Settings) error {
	if !hasImageFile(item.Poster) {
		item.Poster = ""
	}
	if !hasImageFile(item.Backdrop) {
		item.Backdrop = ""
	}
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

        var posterErr error
        if set.DownloadImgs {
                if d.PosterPath == "" {
                        if !hasImageFile(item.Poster) {
                                posterErr = fmt.Errorf("TMDB 未提供海报路径")
                        }
                } else if p, err := tmdb.DownloadImage(ctx, s.metaDir(), item.ID, "w500", d.PosterPath); err == nil {
                        item.Poster = p
                } else {
                        if hasImageFile(item.Poster) {
                                logx.TaskLog("tmdb", "warn", "下载 TMDB 海报失败，保留现有封面《%s》：%v", item.Name, err)
                        } else {
                                posterErr = fmt.Errorf("下载 TMDB 海报失败：%w", err)
                        }
                }
                if d.BackdropPath != "" {
                        if b, err := tmdb.DownloadImage(ctx, s.metaDir(), item.ID, "w1280", d.BackdropPath); err == nil {
                                item.Backdrop = b
                        } else {
                                logx.TaskLog("tmdb", "warn", "下载电影背景图失败《%s》：%v", item.Name, err)
                        }
                }
        }
        item.ImageRev = imageRev(item.Poster, item.Backdrop)
	return posterErr
}

func hasImageFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
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
                matchYear := year
                logx.TaskLog("scrape", "info", "TMDB 搜索剧集：「%s」(%d)", title, year)
                results, err := tmdb.SearchTV(ctx, set.APIKey, title, set.Language, year)
                if err != nil {
                        return err
                }
                if len(results) == 0 && year > 0 {
                        logx.TaskLog("scrape", "info", "带年份无结果，降级无年份重搜「%s」", title)
                        results, err = tmdb.SearchTV(ctx, set.APIKey, title, set.Language, 0)
                        if err != nil {
                                return err
                        }
                }
                if len(results) == 0 {
                        // AI 识别辅助：从文件路径提取关键词后重试搜索
                        if keywords, ok := s.aiRetrySearch(ctx, "Series", item.Path, item.Name, item.Year); ok {
                                logx.TaskLog("scrape", "info", "AI 关键词重搜剧集：「%s」/「%s」(%d)", keywords.Title, keywords.OriginalTitle, keywords.Year)
                                if keywords.Year > 0 {
                                        matchYear = keywords.Year
                                }
                                results, err = searchWithAIKeywords(ctx, set.APIKey, set.Language, keywords, tmdb.SearchTV)
                                if err != nil {
                                        return err
                                }
                        }
                }
                logx.TaskLog("scrape", "info", "搜索结果：%d 条", len(results))
                if len(results) == 0 {
                        logx.InfoC(logx.CatTMDB, "TMDB 未找到剧集: %s", title)
                        return fmt.Errorf("TMDB 未找到匹配: %s", title)
                }
                best := bestTVResult(results, matchYear)
                logx.TaskLog("scrape", "info", "命中《%s》(首播 %s) TmdbID=%d", best.Name, best.FirstAirDate, best.ID)
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
                if p, err := tmdb.DownloadImage(ctx, s.metaDir(), item.ID, "w500", detail.PosterPath); err == nil {
                        item.Poster = p
                } else {
                        logx.TaskLog("tmdb", "warn", "下载剧集海报失败《%s》：%v", item.Name, err)
                }
                if detail.BackdropPath != "" {
                    if b, err := tmdb.DownloadImage(ctx, s.metaDir(), item.ID, "w1280", detail.BackdropPath); err == nil {
                        item.Backdrop = b
                    } else {
                        logx.TaskLog("tmdb", "warn", "下载剧集背景图失败《%s》：%v", item.Name, err)
                    }
                }
        }
        item.ImageRev = imageRev(item.Poster, item.Backdrop)
        if err := db.DB.Save(item).Error; err != nil {
                return err
        }
        logx.InfoC(logx.CatScrape, "刮削剧集《%s》完成（共 %d 季）", item.Name, detail.NumberOfSeasons)
        logx.TaskLog("scrape", "info", "剧集《%s》刮削完成 · 共 %d 季 · 评分 %.1f · 海报=%v 背景=%v，继续刮削各季各集",
                item.Name, detail.NumberOfSeasons, item.CommunityRating, item.Poster != "", item.Backdrop != "")

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
                        if p, err := tmdb.DownloadImage(ctx, s.metaDir(), season.ID, "w500", resp.PosterPath); err == nil {
                                season.Poster = p
                        } else {
                                logx.TaskLog("tmdb", "warn", "下载季海报失败《%s》第 %d 季：%v", series.Name, season.ParentIndexNumber, err)
                        }
                }
                season.Scraped = true
                db.DB.Save(&season)

                // 更新集信息
                var episodes []models.Item
                db.DB.Where("season_id = ?", season.ID).Order("index_number").Find(&episodes)
                logx.TaskLog("scrape", "info", "《%s》第 %d 季「%s」刮削完成 · 本季 %d 集", series.Name, season.ParentIndexNumber, season.Name, len(episodes))
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
                                                if p, err := tmdb.DownloadImage(ctx, s.metaDir(), ep.ID, "w300", e.StillPath); err == nil {
                                                        ep.Thumb = p
                                                } else {
                                                        logx.TaskLog("tmdb", "warn", "下载剧集缩略图失败《%s》：%v", ep.Name, err)
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
