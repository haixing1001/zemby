// Package scanner 后台任务：ffprobe 探测队列与 TMDB 刮削队列。
package scanner

import (
        "path/filepath"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "sort"
	"os"
        "strings"
        "strconv"
        "sync"
        "time"
        "unicode"

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
        updates := map[string]any{"run_time_ticks": rt, "container": container}
        if !probe.IsRemoteSource(t.path) {
                // 远程直链的 Content-Length 不可靠且会破坏增量扫描的
                // strm 文件大小指纹，不写回条目
                updates["size"] = size
        }
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
	// 计数在队列内部维护（waiting），不再借用探测队列的 probeWaiting，
	// 避免刮削任务把「媒体信息提取」页的等待计数顶高。
	if !s.scrapeQueue.Push(task) {
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
	if item == nil || item.ScrapeError != "" || item.DuplicateOf != "" {
		return false // 重复条目不刮削：元数据由正主管辖
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
	if err := db.DB.Select("id, poster").Where("type = ? AND scraped = ? AND scrape_error = '' AND duplicate_of = ''", "Movie", true).Find(&items).Error; err != nil {
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
		err := s.scrapeItemWithKeyword(ctx, t.itemID, t.query, t.year, t.manual && t.query == "")
		cancel()
		if err != nil {
			// 清除失败刮削写入的 TMDB 身份（含 ProviderIDs 中的 Tmdb 键）：
			// 保留会让重试跳过搜索、直连同一个（可能错误的）条目，
			// 如《龙的传人》误刮成《失落神龙传说》后永远无法纠正。
			clearFailedTmdbIdentity(t.itemID)
			db.DB.Model(&models.Item{}).Where("id = ?", t.itemID).Update("scrape_error", truncateErr(err.Error()))
			logx.WarnC(logx.CatScrape, "刮削失败 item=%s: %v", t.itemID, err)
		} else {
			db.DB.Model(&models.Item{}).Where("id = ? AND scrape_error != ''", t.itemID).Update("scrape_error", "")
		}
		}()
		s.markScrapeIdleIfDone()
	}
}

// truncateErr 错误信息截断。
func truncateErr(s string) string {
        if len(s) > 480 {
                return s[:480]
        }
        return s
}

// clearFailedTmdbIdentity 刮削失败后清除条目上的 TMDB 身份：tmdb_id / tmdb_kind
// 与 ProviderIDs 中的 Tmdb 键（其余键如 Imdb 保留）。失败刮削可能已把错误条目
// 的身份写入条目，重试时会因此跳过搜索直连同一错误条目，永远无法纠正。
func clearFailedTmdbIdentity(itemID string) {
        var it models.Item
        if err := db.DB.Select("id", "provider_ids").First(&it, "id = ?", itemID).Error; err != nil {
                return
        }
        updates := map[string]any{"tmdb_id": "", "tmdb_kind": ""}
        if it.ProviderIDs != "" {
                var ids map[string]string
                if json.Unmarshal([]byte(it.ProviderIDs), &ids) == nil && ids["Tmdb"] != "" {
                        delete(ids, "Tmdb")
                        if len(ids) == 0 {
                                updates["provider_ids"] = ""
                        } else if b, err := json.Marshal(ids); err == nil {
                                updates["provider_ids"] = string(b)
                        }
                }
        }
        db.DB.Model(&models.Item{}).Where("id = ?", itemID).Updates(updates)
}

// ClearItemMetadata 清除条目的刮削元数据，恢复未刮削状态：回退名称/年份为
// 文件路径解析的原始标题（含中文目录名优先），清空 TMDB 身份与线上元数据、
// 图片路径。媒体信息（ffprobe 提取的轨道/时长）与用户播放状态保留；
// 本地 NFO 与本地图片在下一次扫描时自动重新应用，之后可重新刮削。
// 用于错配条目的一键纠错（如《告白》被误刮成 The Confessions）。
func (s *Scanner) ClearItemMetadata(itemID string) error {
	var it models.Item
	if err := db.DB.First(&it, "id = ?", itemID).Error; err != nil {
		return err
	}
	if it.Type != "Movie" && it.Type != "Series" {
		return fmt.Errorf("仅支持电影和剧集条目")
	}
	if it.DuplicateOf != "" {
		return fmt.Errorf("重复条目由正主管辖，请清除正主条目")
	}
	updates := map[string]any{
		"tmdb_id": "", "tmdb_kind": "", "provider_ids": "",
		"original_title": "", "overview": "",
		"premiere_date": nil, "community_rating": 0, "official_rating": "",
		"genres": "", "studios": "", "people": "", "tags": "",
		"poster": "", "thumb": "", "backdrop": "", "logo": "",
		"scraped": false, "scrape_error": "",
	}
	if name, year := fallbackTitle(&it); name != "" {
		updates["name"] = name
		updates["sort_name"] = strings.ToLower(name)
		updates["initials"] = models.InitialsOf(name)
		if year > 0 {
			updates["year"] = year
		}
	}
	if err := db.DB.Model(&models.Item{}).Where("id = ?", itemID).Updates(updates).Error; err != nil {
		return err
	}
	logx.InfoC(logx.CatScrape, "已清除条目元数据（恢复未刮削状态）：《%s》· %s", it.Name, it.Path)
	return nil
}

// fallbackTitle 清除元数据后的回退标题：电影用文件路径（含中文目录名优先），
// 剧集用目录名解析；解析不出时保留原名。
func fallbackTitle(it *models.Item) (string, int) {
	if it.Path == "" {
		return it.Name, it.Year
	}
	var p Parsed
	if it.Type == "Series" {
		p = ParseName(filepath.Base(it.Path))
	} else {
		p = movieTitleFromPath(it.Path)
	}
	if p.Title == "" {
		return it.Name, it.Year
	}
	year := it.Year
	if p.Year > 0 {
		year = p.Year
	}
	return p.Title, year
}

// ClearFailedMetadata 一键清除刮削失败清单中全部条目的元数据，返回清除数。
func (s *Scanner) ClearFailedMetadata() int {
	var ids []string
	db.DB.Model(&models.Item{}).
		Where("type IN ? AND scrape_error != '' AND duplicate_of = ''", []string{"Movie", "Series"}).
		Pluck("id", &ids)
	return s.clearMetadataBatch(ids)
}

// ClearIncompleteMetadata 一键清除信息不全清单中全部条目的元数据。
func (s *Scanner) ClearIncompleteMetadata() (int, error) {
	ids, err := incompleteItemIDs()
	if err != nil {
		return 0, err
	}
	return s.clearMetadataBatch(ids), nil
}

func (s *Scanner) clearMetadataBatch(ids []string) int {
	n := 0
	for _, id := range ids {
		if s.ClearItemMetadata(id) == nil {
			n++
		}
	}
	if n > 0 {
		logx.InfoC(logx.CatScrape, "一键清除元数据：%d 个条目恢复未刮削状态", n)
	}
	return n
}

// parseManualTmdbRef 解析手动刮削中的 TMDB ID 指定：
// tmdb:123 / tmdbid:123 / tmdb-id:123（按条目类型定命名空间）、
// movie:123 / tv:123（强制电影/剧集命名空间）。
// 无前缀的纯数字不识别，避免《1917》《2012》这类纯数字片名被误当作 ID。
func parseManualTmdbRef(query string) (int, string, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	for _, prefix := range []string{"tmdb:", "tmdbid:", "tmdb-id:", "movie:", "tv:"} {
		if strings.HasPrefix(q, prefix) {
			idStr := strings.TrimSpace(q[len(prefix):])
			id, err := strconv.Atoi(idStr)
			if err != nil || id <= 0 || len(idStr) > 9 {
				return 0, "", false
			}
			kind := ""
			switch prefix {
			case "movie:", "tv:":
				kind = prefix[:len(prefix)-1]
			}
			return id, kind, true
		}
	}
	return 0, "", false
}

// rescrapeTitle 自动重刮（无手动关键词）时的搜索标题回退。已刮削过的条目，
// 其名称可能已被上次（错误的）刮削结果覆盖——用污染名搜索会反复命中同一
// 错误条目；此处回退文件路径解析出的原始标题与年份（含中文目录名优先，
// 纯英文短词文件名在 TMDB 搜索极易误配）。未刮削过的条目名称来自文件名
// 或 NFO，保持原样。
func rescrapeTitle(it *models.Item) (string, int) {
	if it == nil {
		return "", 0
	}
	if it.Scraped && it.Path != "" {
		if p := movieTitleFromPath(it.Path); p.Title != "" {
			year := it.Year
			if p.Year > 0 {
				year = p.Year
			}
			return p.Title, year
		}
	}
	return it.Name, it.Year
}

// ScrapeNow 立即同步刮削某条目（供 API 调用）。forceSearch 为 true 时忽略既有
// TMDB ID 强制走搜索链（管理员手动刷新的纠错场景）。
func (s *Scanner) ScrapeNow(itemID string, forceSearch bool) error {
        ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
        defer cancel()
        return s.scrapeItem(ctx, itemID, forceSearch)
}

// scrapeItem 刮削单个条目。
func (s *Scanner) scrapeItem(ctx context.Context, itemID string, forceSearch bool) error {
	return s.scrapeItemWithKeyword(ctx, itemID, "", 0, forceSearch)
}

func (s *Scanner) scrapeItemWithKeyword(ctx context.Context, itemID, query string, queryYear int, forceSearch bool) error {
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
        // 自动重刮回退：上次失败/误刮可能已把条目名覆盖为错误影片的标题
        // （如《龙的传人》被误刮成《失落神龙传说》），用污染名搜索会反复命中
        // 同一错误条目；回退文件路径解析出的原始标题与年份。手动关键词不回退。
        if query == "" {
                if name, year := rescrapeTitle(&item); name != item.Name || year != item.Year {
                        logx.TaskLog("scrape", "info", "重刮回退原始文件名标题：「%s」(%d)", name, year)
                        item.Name, item.Year = name, year
                }
        }
        logx.TaskLog("scrape", "info", "开始刮削%s「%s」(%d) · %s", typeLabel, item.Name, item.Year, item.Path)
        // 已有 TMDB ID（NFO 提供）则直接获取详情
        ids := map[string]string{}
        if item.ProviderIDs != "" {
                _ = json.Unmarshal([]byte(item.ProviderIDs), &ids)
        }
        if v := strings.TrimSpace(ids["Tmdb"]); v != "" {
                item.TmdbID = v
                if item.TmdbKind == "" {
                        item.TmdbKind = models.TmdbKindForItemType(item.Type)
                }
        }
        // 手动指定 TMDB ID（tmdb:123 / movie:123 / tv:123）：跳过搜索按 ID 直连刮削，
        // 用于直接纠正错配条目（如《告白》误刮成 The Confessions 后指定正确 ID）
        if query != "" {
                if id, kind, ok := parseManualTmdbRef(query); ok {
                        item.TmdbID = strconv.Itoa(id)
                        if kind != "" {
                                item.TmdbKind = kind
                        } else {
                                item.TmdbKind = models.TmdbKindForItemType(item.Type)
                        }
                        ids = map[string]string{"Tmdb": item.TmdbID}
                        query, queryYear = "", 0
                        logx.TaskLog("scrape", "info", "手动指定 TMDB 身份：%s-%d，直连刮削", item.TmdbKind, id)
                }
        }
	if query = strings.TrimSpace(query); query != "" {
                item.Name = query
                item.Year = queryYear
                ids = map[string]string{} // 手动关键词要求重新搜索，而不是沿用旧 TMDB ID。
                item.TmdbID, item.TmdbKind = "", ""
        }
        // 手动「重新刮削」（无关键词）：忽略既有 TMDB ID 强制走搜索链。
        // 错配条目的旧 ID 直连会永远拿到同一错误结果，重刮无法纠正。
        if forceSearch && query == "" {
                if item.TmdbID != "" {
                        logx.TaskLog("scrape", "info", "手动重刮：忽略既有 TMDB ID=%s，强制重新搜索", item.TmdbID)
                }
                ids = map[string]string{}
                item.TmdbID, item.TmdbKind = "", ""
        }
	// 数据库层 TMDB 去重：同库同身份已有可见正主时直接并入，跳过重复刮削
	if query == "" && item.TmdbID != "" {
	        if c, ok := findVisibleCounterpart(&item); ok {
	                if item.Type == "Series" {
	                        mergeSeriesDuplicate(&item, c)
	                } else {
	                        hideDuplicate(item.ID, c.ID)
	                }
	                logx.TaskLog("scrape", "info", "《%s》与《%s》为同 TMDB 身份，已并入正主", item.Name, c.Name)
	                return nil
	        }
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

// reuseScrapedMovieByTmdbID 已由数据库层 TMDB 去重取代：
// scrapeItemWithKeyword 在刮削前发现同身份正主即直接并入（hideDuplicate/mergeSeriesDuplicate），
// 不再复制元数据后保留两个可见条目。

// tmdbFolderName 返回 TMDB 身份目录名。保留 movie/tv 命名空间，避免不同类型相同编号互相覆盖。
func tmdbFolderName(item *models.Item) (string, error) {
        if item == nil {
                return "", fmt.Errorf("媒体条目为空")
        }
        tmdbID := strings.TrimSpace(item.TmdbID)
        if tmdbID == "" {
                return "", fmt.Errorf("条目缺少 TMDB ID")
        }
        kind := ""
        switch item.Type {
        case "Movie":
                kind = "movie"
        case "Series":
                kind = "tv"
        default:
                return "", fmt.Errorf("不支持的 TMDB 资产条目类型: %s", item.Type)
        }
        return kind + "-tmdb-" + tmdbID, nil
}

func rawJSONField(raw string) any {
        if strings.TrimSpace(raw) == "" {
                return nil
        }
        return json.RawMessage(raw)
}

// migrateTmdbAssetFolders 将旧版 tmdb-movie-* / tmdb-tv-* 目录迁到 movie-tmdb-* / tv-tmdb-*，
// 并同步修正数据库中的本地图片路径；目标目录已存在时先补齐缺失文件，成功后再清理旧目录。
func migrateTmdbAssetFolders() {
        saveDir := LoadProbeConfig().SaveDir
        entries, err := os.ReadDir(saveDir)
        if err != nil {
                return
        }
        for _, entry := range entries {
                if !entry.IsDir() {
                        continue
                }
                kind, tmdbID, ok := parseLegacyTmdbFolderName(entry.Name())
                if !ok {
                        continue
                }
                oldDir := filepath.Join(saveDir, entry.Name())
                newDir := filepath.Join(saveDir, kind+"-tmdb-"+tmdbID)
                if _, err := os.Stat(newDir); os.IsNotExist(err) {
                        if err := os.Rename(oldDir, newDir); err != nil {
                                logx.WarnC(logx.CatScrape, "迁移 TMDB 资产目录失败 %s → %s：%v", oldDir, newDir, err)
                                continue
                        }
                } else if err == nil {
                        if err := copyMissingFiles(oldDir, newDir); err != nil {
                                logx.WarnC(logx.CatScrape, "合并 TMDB 资产目录失败 %s → %s：%v", oldDir, newDir, err)
                                continue
                        }
                        if err := os.RemoveAll(oldDir); err != nil {
                                logx.WarnC(logx.CatScrape, "清理旧 TMDB 资产目录失败 %s：%v", oldDir, err)
                        }
                }
                updateTmdbItemAssetPaths(oldDir, newDir)
                logx.InfoC(logx.CatScrape, "TMDB 资产目录已迁移：%s → %s", entry.Name(), kind+"-tmdb-"+tmdbID)
        }
}

func parseLegacyTmdbFolderName(name string) (kind string, tmdbID string, ok bool) {
        if !strings.HasPrefix(name, "tmdb-movie-") && !strings.HasPrefix(name, "tmdb-tv-") {
                return "", "", false
        }
        parts := strings.Split(name, "-")
        if len(parts) != 3 || parts[2] == "" {
                return "", "", false
        }
        for _, ch := range parts[2] {
                if ch < '0' || ch > '9' {
                        return "", "", false
                }
        }
        return parts[1], parts[2], true
}

func copyMissingFiles(src, dst string) error {
        entries, err := os.ReadDir(src)
        if err != nil {
                return err
        }
        if err := os.MkdirAll(dst, 0o755); err != nil {
                return err
        }
        for _, entry := range entries {
                from := filepath.Join(src, entry.Name())
                to := filepath.Join(dst, entry.Name())
                if entry.IsDir() {
                        if err := copyMissingFiles(from, to); err != nil {
                                return err
                        }
                        continue
                }
                if _, err := os.Stat(to); err == nil {
                        continue
                }
                in, err := os.Open(from)
                if err != nil {
                        return err
                }
                out, err := os.Create(to)
                if err != nil {
                        in.Close()
                        return err
                }
                _, err = io.Copy(out, in)
                closeErr := out.Close()
                in.Close()
                if err != nil {
                        return err
                }
                if closeErr != nil {
                        return closeErr
                }
        }
        return nil
}

func updateTmdbItemAssetPaths(oldDir, newDir string) {
        var items []models.Item
        if err := db.DB.Select("id, poster, thumb, backdrop, logo").
                Where("tmdb_id <> ''").Find(&items).Error; err != nil {
                return
        }
        for i := range items {
                updates := map[string]any{}
                for field, value := range map[string]string{
                        "poster": items[i].Poster, "thumb": items[i].Thumb,
                        "backdrop": items[i].Backdrop, "logo": items[i].Logo,
                } {
                        if next, changed := remapAssetPath(value, oldDir, newDir); changed {
                                updates[field] = next
                        }
                }
                if len(updates) > 0 {
                        _ = db.DB.Model(&models.Item{}).Where("id = ?", items[i].ID).Updates(updates).Error
                }
        }
}

func remapAssetPath(path, oldDir, newDir string) (string, bool) {
        if path == "" || !strings.HasPrefix(path, oldDir+string(filepath.Separator)) {
                return path, false
        }
        return newDir + path[len(oldDir):], true
}

// writeTmdbMetadataFile 将 TMDB 元数据写入“提取媒体信息设置”的保存目录。
func (s *Scanner) writeTmdbMetadataFile(item *models.Item, filename string, data any) error {
        folder, err := tmdbFolderName(item)
        if err != nil {
                return err
        }
        dir := filepath.Join(LoadProbeConfig().SaveDir, folder)
        if err := os.MkdirAll(dir, 0o755); err != nil {
                return err
        }
        b, err := json.MarshalIndent(data, "", "  ")
        if err != nil {
                return err
        }
        target := filepath.Join(dir, filepath.Base(filename))
        tmp := target + ".tmp"
        if err := os.WriteFile(tmp, b, 0o644); err != nil {
                return err
        }
        if err := os.Rename(tmp, target); err != nil {
                _ = os.Remove(tmp)
                return err
        }
        return nil
}

// downloadTmdbImage 下载图片到当前条目的 TMDB 身份目录。
func (s *Scanner) downloadTmdbImage(ctx context.Context, item *models.Item, size, path string) (string, error) {
        folder, err := tmdbFolderName(item)
        if err != nil {
                return "", err
        }
        return tmdb.DownloadImage(ctx, LoadProbeConfig().SaveDir, folder, size, path)
}

// localizeTmdbImage 将已有本地图片复制到 TMDB 身份目录；已在目录内或复制失败时保留原路径。
func (s *Scanner) localizeTmdbImage(item *models.Item, path string) string {
        if item == nil || !hasImageFile(path) {
                return path
        }
        folder, err := tmdbFolderName(item)
        if err != nil {
                return path
        }
        dir := filepath.Join(LoadProbeConfig().SaveDir, folder)
        if rel, err := filepath.Rel(dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
                return path
        }
        data, err := os.ReadFile(path)
        if err != nil {
                return path
        }
        if err := os.MkdirAll(dir, 0o755); err != nil {
                return path
        }
        target := filepath.Join(dir, filepath.Base(path))
        if !hasImageFile(target) {
                if err := os.WriteFile(target, data, 0o644); err != nil {
                        return path
                }
        }
        return target
}

func (s *Scanner) scrapeMovie(ctx context.Context, item *models.Item, set tmdb.Settings, ids map[string]string) error {
        var detail *tmdb.MovieDetail
        if strings.TrimSpace(item.TmdbKind) == "tv" {
                return s.scrapeSeries(ctx, item, set, ids)
        }
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
                results := []tmdb.SearchResult{}
                var err error
                aiTried := false
                var aiKeywords *AIKeywords
                if preferAIEnabled() {
                        aiTried = true
                        if keywords, ok := s.aiRetrySearch(ctx, "Movie", item.Path, item.Name, item.Year, true); ok {
                                aiKeywords = keywords
                                logx.TaskLog("scrape", "info", "AI 优先关键词搜索电影：「%s」/「%s」(%d)", keywords.Title, keywords.OriginalTitle, keywords.Year)
                                if keywords.Year > 0 {
                                        matchYear = keywords.Year
                                }
                                searchFn := tmdb.SearchMovie
                                if !aiTypeIsMovie(keywords) {
                                        searchFn = tmdb.SearchTV
                                        logx.TaskLog("scrape", "info", "AI 判定类型「%s」非电影，改用 TMDB TV 搜索", keywords.Type)
                                }
                                found, searchErr := searchWithAIKeywords(ctx, set.APIKey, set.Language, keywords, searchFn)
                                if searchErr != nil {
                                        logx.TaskLog("scrape", "warn", "AI 关键词 TMDB 搜索失败，回退原文件名搜索：%v", searchErr)
                                }
                                if searchErr == nil && !aiTypeIsMovie(keywords) {
                                        tvID, movieRes, terr := s.aiNonMovieSearch(ctx, set, keywords, found, matchYear, tmdb.SearchMovie)
                                        if terr != nil {
                                                return terr
                                        }
                                        if tvID != "" {
                                                return s.scrapeSeries(ctx, item, set, map[string]string{"Tmdb": tvID})
                                        }
                                        found = movieRes // TV 无命中但电影搜索命中：按电影继续
                                }
                                if searchErr == nil {
                                        results = found
                                }
                        }
                }
                if len(results) == 0 && aiKeywords != nil && aiTypeIsMovie(aiKeywords) {
                        // AI 关键词各查询均无结果（TMDB 未返回或标题相似度都不足）：
                        // 用 AI 提取的干净标题直接搜索，由 bestMovieResult 按年份/海报/评分择优。
                        // 原文件名可能被榜单编号、字幕组等前缀污染（如 Top212.xxx），直接搜必败。
                        logx.TaskLog("scrape", "info", "AI 关键词兜底搜索电影：「%s」(%d)", aiKeywords.Title, aiKeywords.Year)
                        results, err = tmdb.SearchMovie(ctx, set.APIKey, aiKeywords.Title, set.Language, aiKeywords.Year)
                        if err != nil {
                                return err
                        }
                        if len(results) == 0 && aiKeywords.Year > 0 {
                                results, err = tmdb.SearchMovie(ctx, set.APIKey, aiKeywords.Title, set.Language, 0)
                                if err != nil {
                                        return err
                                }
                        }
                }
                if len(results) == 0 {
                        matchYear = year
                        logx.TaskLog("scrape", "info", "TMDB 搜索电影：「%s」(%d)", title, year)
                        results, err = tmdb.SearchMovie(ctx, set.APIKey, title, set.Language, year)
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
                }
                if len(results) == 0 {
                        // AI 识别辅助：从文件路径提取关键词后重试搜索
                        if !aiTried {
                        if keywords, ok := s.aiRetrySearch(ctx, "Movie", item.Path, item.Name, item.Year, false); ok {
                                logx.TaskLog("scrape", "info", "AI 关键词重搜电影：「%s」/「%s」(%d)", keywords.Title, keywords.OriginalTitle, keywords.Year)
                                if keywords.Year > 0 {
                                        matchYear = keywords.Year
                                }
				searchFn := tmdb.SearchMovie
				if !aiTypeIsMovie(keywords) {
					searchFn = tmdb.SearchTV
					logx.TaskLog("scrape", "info", "AI 判定类型「%s」非电影，改用 TMDB TV 搜索", keywords.Type)
				}
				results, err = searchWithAIKeywords(ctx, set.APIKey, set.Language, keywords, searchFn)
                                if err != nil {
                                        return err
                                }
                                if !aiTypeIsMovie(keywords) {
                                        tvID, movieRes, terr := s.aiNonMovieSearch(ctx, set, keywords, results, matchYear, tmdb.SearchMovie)
                                        if terr != nil {
                                                return terr
                                        }
                                        if tvID != "" {
                                                return s.scrapeSeries(ctx, item, set, map[string]string{"Tmdb": tvID})
                                        }
                                        results = movieRes // TV 无命中但电影搜索命中：按电影继续
                                }
                        }
                        }
                }
                logx.TaskLog("scrape", "info", "搜索结果：%d 条", len(results))
                if len(results) == 0 {
                        logx.InfoC(logx.CatTMDB, "TMDB 未找到电影: %s (%d)", title, year)
                        return fmt.Errorf("TMDB 未找到匹配: %s (%d)", title, year)
                }
				// scrapeMovie 末尾的择优（原文件名/AI 关键词直搜结果）
				{
				refNames := []string{title}
				if aiKeywords != nil {
					refNames = append(refNames, aiKeywords.Title, aiKeywords.OriginalTitle)
				}
				best := bestMovieResult(results, matchYear, refNames...)
				searchPosterPath = best.PosterPath
				logx.TaskLog("scrape", "info", "命中《%s》(首播 %s) TmdbID=%d", best.Title, best.ReleaseDate, best.ID)
				d, err := tmdb.GetMovie(ctx, set.APIKey, best.ID, set.Language)
				if err != nil {
					return err
				}
				detail = d
				}
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
		merged := s.dedupeVisibility(item) // 落库前按 TMDB 身份归并，避免撞唯一索引
        if err := db.DB.Save(item).Error; err != nil {
                return err
        }
		if merged {
			logx.InfoC(logx.CatScrape, "电影《%s》与已有条目同 TMDB 身份，已并入正主展示", item.Name)
			logx.TaskLog("scrape", "info", "电影《%s》与已有条目同 TMDB 身份，已并入正主展示", item.Name)
			return nil
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

// 命中等级（完全匹配，数值越小越优先）。
const (
	matchNone      = 0
	matchCNENYear  = 1 // 中文名+英文名+年份完全匹配
	matchCNYear    = 2 // 中文名+年份完全匹配
	matchCNEN      = 3 // 中文名+英文名完全匹配（无年份支撑）
	matchCNOnly    = 4 // 仅中文名完全匹配
	matchENYear    = 5 // 英文名+年份完全匹配
	matchENOnly    = 6 // 仅英文名完全匹配
)

// classifyRefs 参考名分类拆解。混合标题（如目录名解析出的「告白 Confessions」）
// 拆出中文段与拉丁段分别归入中文名/英文名集合；纯拉丁或其它非汉字参考名
// 整体归英文名集合（与 TMDB original_title 对齐，含韩文等外文原名）。
func classifyRefs(names ...string) (cn, en []string) {
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !hasHan(n) {
			en = append(en, n)
			continue
		}
		cn = append(cn, hanSegRe.FindAllString(n, -1)...)
		en = append(en, latinSegRe.FindAllString(n, -1)...)
	}
	return cn, en
}

// yearMatches 年份匹配（允许 ±1 年误差）：电影节首映与公映、不同地区发行
// 常跨年，如搜索 1990 时 1989/1990/1991 均视为匹配。want<=0 时视为不匹配。
func yearMatches(resultYear, want int) bool {
	if want <= 0 {
		return false
	}
	return absInt(resultYear-want) <= 1
}

// tmdbMatchLevel 命中规则分级（完全匹配，越小越优先）：
//   中文名+英文名+年份 > 中文名+年份 > 中文名+英文名 > 中文名 > 英文名+年份 > 英文名
//
// 双名匹配不带年份时次于「中文名+年份」：同名英语翻拍片的 original_title 恰为
// 英文，会与 AI 的中英双名都完全匹配，若无年份约束会压过年份正确的原版。
// 中文名比 TMDB 的 title/name，英文名比 original_title/original_name；
// 参考名集合任一完全匹配（归一化后相等）即算命中；year<=0 时不计年份维度。
func tmdbMatchLevel(result tmdb.SearchResult, cn, en []string, year int) int {
	cnOK := refExactAny(cn, firstNonEmpty(result.Title, result.Name))
	enOK := refExactAny(en, firstNonEmpty(result.OriginalTitle, result.OriginalName))
	yearOK := year > 0 && (yearMatches(parseYear(result.ReleaseDate), year) || yearMatches(parseYear(result.FirstAirDate), year))
	switch {
	case cnOK && enOK && yearOK:
		return matchCNENYear
	case cnOK && yearOK:
		return matchCNYear
	case cnOK && enOK:
		return matchCNEN
	case cnOK:
		return matchCNOnly
	case enOK && yearOK:
		return matchENYear
	case enOK:
		return matchENOnly
	}
	return matchNone
}

// refExactAny 参考名集合中任一与 got 完全匹配（归一化后相等）。
func refExactAny(refs []string, got string) bool {
	got = strings.TrimSpace(got)
	if got == "" {
		return false
	}
	gotNorm := normalizeSearchTitle(got)
	if gotNorm == "" {
		return false
	}
	for _, want := range refs {
		if n := normalizeSearchTitle(want); n != "" && n == gotNorm {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func bestMovieResult(results []tmdb.SearchResult, year int, names ...string) tmdb.SearchResult {
	if len(results) == 0 {
		return tmdb.SearchResult{}
	}
	cn, en := classifyRefs(names...)
	best := results[0]
	bestLevel := tmdbMatchLevel(best, cn, en, year)
	bestYearMatch := yearMatches(parseYear(best.ReleaseDate), year)
	for _, result := range results[1:] {
		level := tmdbMatchLevel(result, cn, en, year)
		yearMatch := yearMatches(parseYear(result.ReleaseDate), year)
		if betterPick(result, level, yearMatch, best, bestLevel, bestYearMatch) {
			best, bestLevel, bestYearMatch = result, level, yearMatch
		}
	}
	return best
}

// matchLevelRank 排序用的等级值：无匹配（0）视为最低优先级。
func matchLevelRank(level int) int {
	if level == matchNone {
		return 1 << 20
	}
	return level
}

// betterPick 命中规则择优：等级小者胜（无匹配垫底）；同级按 年份匹配 > 有海报 > 高评分。
func betterPick(result tmdb.SearchResult, level int, yearMatch bool,
	best tmdb.SearchResult, bestLevel int, bestYearMatch bool) bool {
	if level != bestLevel {
		return matchLevelRank(level) < matchLevelRank(bestLevel)
	}
	if yearMatch != bestYearMatch {
		return yearMatch
	}
	betterArtwork := best.PosterPath == "" && result.PosterPath != ""
	sameArtwork := (best.PosterPath != "") == (result.PosterPath != "")
	return betterArtwork || (sameArtwork && result.VoteAverage > best.VoteAverage)
}

func searchWithAIKeywords(ctx context.Context, key, lang string, keywords *AIKeywords, search func(context.Context, string, string, string, int) ([]tmdb.SearchResult, error)) ([]tmdb.SearchResult, error) {
	if keywords == nil {
		return nil, nil
	}
	var fallback []tmdb.SearchResult // 严格过滤无命中时按标题相似度保留的兜底结果
	for _, query := range aiSearchQueries(keywords) {
		logx.TaskLog("scrape", "info", "AI 关键词调用 TMDB：「%s」(年份 %d)", query, keywords.Year)
		found, err := search(ctx, key, query, lang, keywords.Year)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 && keywords.Year > 0 {
			found, err = search(ctx, key, query, lang, 0)
			if err != nil {
				return nil, err
			}
		}
		if matched := filterAIKeywordResults(found, keywords); len(matched) > 0 {
			logx.TaskLog("scrape", "info", "AI 关键词「%s」返回 %d 条，严格过滤后命中 %d 条", query, len(found), len(matched))
			return matched, nil
		}
		// 严格过滤（年份精确相等 + 标题相似度）无命中时不能直接丢弃：
		// TMDB 镜像可能返回缺失 release_date 的结果、或地区发行年份与文件名
		// 相差一年，全杀光后会回退到被“Top212”之类污染的原文件名搜索，必败。
		// 按标题相似度兜底保留，交由后续 bestMovieResult/bestTVResult 择优。
		if len(fallback) == 0 {
			if relaxed := bestEffortAIResults(found, keywords); len(relaxed) > 0 {
				logx.TaskLog("scrape", "info", "AI 关键词「%s」返回 %d 条，严格过滤无命中；按标题相似度保留 %d 条兜底", query, len(found), len(relaxed))
				fallback = relaxed
			} else if len(found) > 0 {
				logx.TaskLog("scrape", "warn", "AI 关键词「%s」返回 %d 条，但标题相似度均不足，全部丢弃", query, len(found))
			}
		}
	}
	return fallback, nil
}

// aiNonMovieSearch 处理 AI 判定为非电影类型的搜索结果：
//   - TV 命中 → 返回 TV 条目的 TMDB ID（调用方转剧集刮削）；
//   - TV 无命中 → 降级用电影搜索 AI 关键词。AI 判定的类型可能与 TMDB 归类
//     不一致（如纪录片电影在 TMDB 属于 movie），命中则返回电影搜索结果，
//     由调用方按电影流程继续；
//   - 两者都无命中才报错。
// movieSearch 参数便于测试注入。
func (s *Scanner) aiNonMovieSearch(ctx context.Context, set tmdb.Settings, keywords *AIKeywords,
	tvFound []tmdb.SearchResult, matchYear int,
	movieSearch func(context.Context, string, string, string, int) ([]tmdb.SearchResult, error)) (string, []tmdb.SearchResult, error) {
	if len(tvFound) > 0 {
		best := bestTVResult(tvFound, matchYear, keywords.Title, keywords.OriginalTitle)
		logx.TaskLog("scrape", "info", "AI 命中 TV《%s》(首播 %s) TmdbID=%d", best.Name, best.FirstAirDate, best.ID)
		return fmt.Sprint(best.ID), nil, nil
	}
	logx.TaskLog("scrape", "info", "AI 判定「%s」按 TV 搜索无命中，降级电影搜索：「%s」(%d)",
		keywords.Type, keywords.Title, keywords.Year)
	movieResults, err := searchWithAIKeywords(ctx, set.APIKey, set.Language, keywords, movieSearch)
	if err != nil {
		return "", nil, err
	}
	if len(movieResults) == 0 {
		return "", nil, fmt.Errorf("AI 判定为「%s」，TMDB TV 与电影搜索均无匹配: %s (%d)",
			keywords.Type, keywords.Title, keywords.Year)
	}
	return "", movieResults, nil
}

// bestEffortAIResults 严格过滤无命中时的兜底：仅按标题相似度
// （阈值与严格模式一致）保留结果，不因发行日期缺失或年份偏差丢弃；排序按
// 命中等级 → 标题相似度 → 有日期且与期望年份最接近 → 海报 → 评分，
// 降低同名翻拍误配的概率。
func bestEffortAIResults(results []tmdb.SearchResult, keywords *AIKeywords) []tmdb.SearchResult {
	if keywords == nil || len(results) == 0 {
		return nil
	}
	cn, en := classifyRefs(keywords.Title, keywords.OriginalTitle)
	type entry struct {
		result  tmdb.SearchResult
		level   int
		score   float64
		hasDate bool
		yearGap int
	}
	entries := make([]entry, 0, len(results))
	for _, result := range results {
		score := aiResultTitleScore(result, keywords)
		if score < 60 {
			continue
		}
		hasDate, yearGap := false, 1<<30
		if result.ReleaseDate != "" {
			hasDate, yearGap = true, absInt(parseYear(result.ReleaseDate)-keywords.Year)
		} else if result.FirstAirDate != "" {
			hasDate, yearGap = true, absInt(parseYear(result.FirstAirDate)-keywords.Year)
		}
		if keywords.Year <= 0 {
			yearGap = 0 // 无期望年份时不按日期排序
		}
		entries = append(entries, entry{result, tmdbMatchLevel(result, cn, en, keywords.Year), score, hasDate, yearGap})
	}
	if len(entries) == 0 {
		return nil
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].level != entries[j].level {
			return matchLevelRank(entries[i].level) < matchLevelRank(entries[j].level)
		}
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		if entries[i].hasDate != entries[j].hasDate {
			return entries[i].hasDate
		}
		return entries[i].yearGap < entries[j].yearGap
	})
	out := make([]tmdb.SearchResult, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].result)
	}
	return out
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// filterAIKeywordResults 按「完全匹配命中规则 + 标题相似度」过滤排序 AI 关键词
// 的 TMDB 结果。命中等级（越小越优先）：
//   1. 中文名+英文名完全匹配
//   2. 中文名+年份完全匹配
//   3. 中文名完全匹配
//   4. 英文名+年份完全匹配
//   5. 英文名完全匹配
//
// 无任何完全匹配时，保留「标题相似度≥60 且年份匹配」的相似结果（防误配的
// 保守语义）；两者都不满足的结果丢弃，交由 bestEffortAIResults 兜底。
func filterAIKeywordResults(results []tmdb.SearchResult, keywords *AIKeywords) []tmdb.SearchResult {
	type entry struct {
		result tmdb.SearchResult
		level  int
		score  float64
	}
	cn, en := classifyRefs(keywords.Title, keywords.OriginalTitle)
	matched := make([]entry, 0, len(results))
	for _, result := range results {
		titleScore := aiResultTitleScore(result, keywords)
		if titleScore < 60 {
			continue
		}
		level := tmdbMatchLevel(result, cn, en, keywords.Year)
		if level == matchNone && !aiResultYearMatches(result, keywords.Year) {
			continue // 无完全匹配且年份不符：不能证明相关，留给兜底
		}
		score := titleScore * 100
		if result.PosterPath != "" {
			score += 2
		}
		score += result.VoteAverage * 0.1
		matched = append(matched, entry{result, level, score})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].level != matched[j].level {
			return matchLevelRank(matched[i].level) < matchLevelRank(matched[j].level)
		}
		return matched[i].score > matched[j].score
	})
	out := make([]tmdb.SearchResult, 0, len(matched))
	for i := range matched {
		out = append(out, matched[i].result)
	}
	return out
}

func aiResultYearMatches(result tmdb.SearchResult, year int) bool {
	if year <= 0 {
		return true
	}
	return yearMatches(parseYear(result.ReleaseDate), year) || yearMatches(parseYear(result.FirstAirDate), year)
}

// aiResultTitleScore 标题相似度：按字段严格对齐匹配。TMDB（language=zh-CN）
// 返回 title/name 为中文名、original_title/original_name 为外文原名，分别与
// AI 关键词的 Title（中文）/OriginalTitle（原名）比较；不做跨字段交叉比较，
// 避免 AI 中文标题与无关条目的外文原名（或反之）局部 token 重合造成误配。
// AI 未提供对应字段时跳过该侧比较。
func aiResultTitleScore(result tmdb.SearchResult, keywords *AIKeywords) float64 {
	if keywords == nil {
		return 0
	}
	best := 0.0
	consider := func(want, got string) {
		want = strings.TrimSpace(want)
		got = strings.TrimSpace(got)
		if want == "" || got == "" {
			return
		}
		if score := searchTitleSimilarity(want, got); score > best {
			best = score
		}
	}
	// 中文名 ↔ title / name
	consider(keywords.Title, result.Title)
	consider(keywords.Title, result.Name)
	// 外文原名 ↔ original_title / original_name
	consider(keywords.OriginalTitle, result.OriginalTitle)
	consider(keywords.OriginalTitle, result.OriginalName)
	return best
}

func searchTitleSimilarity(want, got string) float64 {
	wantNorm := normalizeSearchTitle(want)
	gotNorm := normalizeSearchTitle(got)
	if wantNorm == "" || gotNorm == "" {
		return 0
	}
	if wantNorm == gotNorm {
		return 100
	}
	wantTokens := searchTitleTokens(want)
	gotTokens := searchTitleTokens(got)
	if len(wantTokens) == 0 {
		return 0
	}
	gotSet := make(map[string]struct{}, len(gotTokens))
	for _, token := range gotTokens {
		gotSet[token] = struct{}{}
	}
	matches := 0
	for _, token := range wantTokens {
		if _, ok := gotSet[token]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(wantTokens)) * 70
}

func normalizeSearchTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func searchTitleTokens(s string) []string {
	stopWords := map[string]struct{}{"the": {}, "of": {}, "a": {}, "an": {}, "and": {}, "to": {}}
	fields := strings.Fields(strings.ToLower(s))
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.TrimFunc(field, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		if token == "" {
			continue
		}
		if _, ok := stopWords[token]; ok {
			continue
		}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		if normalized := normalizeSearchTitle(s); normalized != "" {
			tokens = append(tokens, normalized)
		}
	}
	return tokens
}

func bestTVResult(results []tmdb.SearchResult, year int, names ...string) tmdb.SearchResult {
	if len(results) == 0 {
		return tmdb.SearchResult{}
	}
	cn, en := classifyRefs(names...)
	best := results[0]
	bestLevel := tmdbMatchLevel(best, cn, en, year)
	bestYearMatch := yearMatches(parseYear(best.FirstAirDate), year)
	for _, result := range results[1:] {
		level := tmdbMatchLevel(result, cn, en, year)
		yearMatch := yearMatches(parseYear(result.FirstAirDate), year)
		if betterPick(result, level, yearMatch, best, bestLevel, bestYearMatch) {
			best, bestLevel, bestYearMatch = result, level, yearMatch
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
        item.TmdbID = fmt.Sprint(d.ID)
        item.TmdbKind = "movie"
        if err := s.writeTmdbMetadataFile(item, "metadata.json", d); err != nil {
                return err
        }
        item.Poster = s.localizeTmdbImage(item, item.Poster)
        item.Backdrop = s.localizeTmdbImage(item, item.Backdrop)
        item.Scraped = true
        s.applyTmdbPeople(ctx, item, "movie", d.ID, set)

        var posterErr error
        if set.DownloadImgs {
                if d.PosterPath == "" {
                        if !hasImageFile(item.Poster) {
                                posterErr = fmt.Errorf("TMDB 未提供海报路径")
                        }
                } else if p, err := s.downloadTmdbImage(ctx, item, "w500", d.PosterPath); err == nil {
                        item.Poster = p
                } else {
                        if hasImageFile(item.Poster) {
                                logx.TaskLog("tmdb", "warn", "下载 TMDB 海报失败，保留现有封面《%s》：%v", item.Name, err)
                        } else {
                                posterErr = fmt.Errorf("下载 TMDB 海报失败：%w", err)
                        }
                }
                if d.BackdropPath != "" {
                        if b, err := s.downloadTmdbImage(ctx, item, "w1280", d.BackdropPath); err == nil {
                                item.Backdrop = b
                        } else {
                                logx.TaskLog("tmdb", "warn", "下载电影背景图失败《%s》：%v", item.Name, err)
                        }
                }
        }
        item.ImageRev = imageRev(item.Poster, item.Backdrop)
	return posterErr
}

// maxCastMembers 演员表保留上限（TMDB 按出场重要性排序，取前 N 位）。
const maxCastMembers = 20

// peopleSourceTMDB People 条目的来源标记，区别于本地 NFO 数据。
const peopleSourceTMDB = "tmdb"

// applyTmdbPeople 拉取 TMDB 演职员表写入条目。本地 NFO 已提供演员时不覆盖（本地优先）；
// 剧集仅取演员，电影额外附上 crew 中的导演；获取失败仅记日志，不影响刮削主流程。
func (s *Scanner) applyTmdbPeople(ctx context.Context, item *models.Item, kind string, tmdbID int, set tmdb.Settings) {
	if tmdbID <= 0 || peopleFromNFO(item.People) {
		return
	}
	cast, crew, err := tmdb.GetCredits(ctx, set.APIKey, kind, tmdbID, set.Language)
	if err != nil {
		logx.TaskLog("tmdb", "warn", "获取演职员表失败《%s》：%v", item.Name, err)
		return
	}
	if err := s.writeTmdbMetadataFile(item, "credits.json", map[string]any{"cast": cast, "crew": crew}); err != nil {
		logx.TaskLog("tmdb", "warn", "保存演职员表元数据失败《%s》：%v", item.Name, err)
	}
	people := make([]map[string]string, 0, maxCastMembers+2)
	for _, c := range cast {
		if len(people) >= maxCastMembers {
			break
		}
		if c.Name == "" {
			continue
		}
		p := map[string]string{"Name": c.Name, "Role": c.Character, "Source": peopleSourceTMDB}
		if u := tmdb.ProfileURL(set.ImageBaseURL, c.ProfilePath, "w185"); u != "" {
			p["Thumb"] = u
		}
		people = append(people, p)
	}
	if kind == "movie" {
		for _, c := range crew {
			if c.Job == "Director" && c.Name != "" {
				people = append(people, map[string]string{"Name": c.Name, "Role": "导演", "Type": "Director"})
			}
		}
	}
	if len(people) > 0 {
		item.People = toJSON(people)
		logx.TaskLog("tmdb", "info", "《%s》演职员表已刮削（%d 人）", item.Name, len(people))
	}
}

// parsePeople 解析 Item.People 的两种存储格式：[{"Name":..}] 与旧版 ["{...}"]。
func parsePeople(raw string) []map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var objs []map[string]string
	if err := json.Unmarshal([]byte(raw), &objs); err == nil && len(objs) > 0 {
		return objs
	}
	var strArr []string
	if json.Unmarshal([]byte(raw), &strArr) != nil {
		return nil
	}
	var out []map[string]string
	for _, s := range strArr {
		var m map[string]string
		if json.Unmarshal([]byte(s), &m) == nil && m != nil {
			out = append(out, m)
		}
	}
	return out
}

// peopleFromNFO 判断现有演员是否来自本地 NFO（无 tmdb 来源标记的演员视为本地数据，优先保留）。
func peopleFromNFO(raw string) bool {
	for _, p := range parsePeople(raw) {
		kind := strings.ToLower(strings.TrimSpace(p["Type"]))
		role := strings.ToLower(strings.TrimSpace(p["Role"]))
		if kind == "director" || kind == "导演" || role == "director" || role == "导演" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(p["Source"]), peopleSourceTMDB) {
			continue
		}
		return true
	}
	return false
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
                results := []tmdb.SearchResult{}
                var err error
                aiTried := false
                var aiKeywords *AIKeywords
                if preferAIEnabled() {
                        aiTried = true
                        if keywords, ok := s.aiRetrySearch(ctx, "Series", item.Path, item.Name, item.Year, true); ok {
                                aiKeywords = keywords
                                logx.TaskLog("scrape", "info", "AI 优先关键词搜索剧集：「%s」/「%s」(%d)", keywords.Title, keywords.OriginalTitle, keywords.Year)
                                if keywords.Year > 0 {
                                        matchYear = keywords.Year
                                }
                                found, searchErr := searchWithAIKeywords(ctx, set.APIKey, set.Language, keywords, tmdb.SearchTV)
                                if searchErr != nil {
                                        logx.TaskLog("scrape", "warn", "AI 关键词 TMDB 搜索失败，回退原文件名搜索：%v", searchErr)
                                }
                                if searchErr == nil {
                                        results = found
                                }
                        }
                }
                if len(results) == 0 && aiKeywords != nil {
                        // AI 关键词各查询均无结果：用 AI 提取的干净标题直接搜索，
                        // 原文件名可能被榜单编号等前缀污染，直接搜必败。
                        logx.TaskLog("scrape", "info", "AI 关键词兜底搜索剧集：「%s」(%d)", aiKeywords.Title, aiKeywords.Year)
                        results, err = tmdb.SearchTV(ctx, set.APIKey, aiKeywords.Title, set.Language, aiKeywords.Year)
                        if err != nil {
                                return err
                        }
                        if len(results) == 0 && aiKeywords.Year > 0 {
                                results, err = tmdb.SearchTV(ctx, set.APIKey, aiKeywords.Title, set.Language, 0)
                                if err != nil {
                                        return err
                                }
                        }
                }
                if len(results) == 0 {
                        matchYear = year
                        logx.TaskLog("scrape", "info", "TMDB 搜索剧集：「%s」(%d)", title, year)
                        results, err = tmdb.SearchTV(ctx, set.APIKey, title, set.Language, year)
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
                }
                if len(results) == 0 {
                        // AI 识别辅助：从文件路径提取关键词后重试搜索
                        if !aiTried {
                        if keywords, ok := s.aiRetrySearch(ctx, "Series", item.Path, item.Name, item.Year, false); ok {
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
                }
                logx.TaskLog("scrape", "info", "搜索结果：%d 条", len(results))
                if len(results) == 0 {
                        logx.InfoC(logx.CatTMDB, "TMDB 未找到剧集: %s", title)
                        return fmt.Errorf("TMDB 未找到匹配: %s", title)
                }
		refNames := []string{title}
		if aiKeywords != nil {
			refNames = append(refNames, aiKeywords.Title, aiKeywords.OriginalTitle)
		}
		best := bestTVResult(results, matchYear, refNames...)
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
        item.TmdbID = fmt.Sprint(detail.ID)
        item.TmdbKind = "tv"
        if err := s.writeTmdbMetadataFile(item, "metadata.json", detail); err != nil {
                return err
        }
        item.Poster = s.localizeTmdbImage(item, item.Poster)
        item.Backdrop = s.localizeTmdbImage(item, item.Backdrop)
        item.Scraped = true
        s.applyTmdbPeople(ctx, item, "tv", detail.ID, set)
        if set.DownloadImgs {
                if p, err := s.downloadTmdbImage(ctx, item, "w500", detail.PosterPath); err == nil {
                        item.Poster = p
                } else {
                        logx.TaskLog("tmdb", "warn", "下载剧集海报失败《%s》：%v", item.Name, err)
                }
                if detail.BackdropPath != "" {
                    if b, err := s.downloadTmdbImage(ctx, item, "w1280", detail.BackdropPath); err == nil {
                        item.Backdrop = b
                    } else {
                        logx.TaskLog("tmdb", "warn", "下载剧集背景图失败《%s》：%v", item.Name, err)
                    }
                }
        }
        item.ImageRev = imageRev(item.Poster, item.Backdrop)
		merged := s.dedupeVisibility(item) // 落库前按 TMDB 身份归并，避免撞唯一索引
        if err := db.DB.Save(item).Error; err != nil {
                return err
        }
		if merged {
			// 已并入正主：季集元数据由正主管辖，跳过重复请求
			logx.InfoC(logx.CatScrape, "剧集《%s》与已有条目同 TMDB 身份，已并入正主展示", item.Name)
			logx.TaskLog("scrape", "info", "剧集《%s》与已有条目同 TMDB 身份，已并入正主展示", item.Name)
			return nil
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
                if err := s.writeTmdbMetadataFile(series, fmt.Sprintf("season-%d.json", season.ParentIndexNumber), resp); err != nil {
                        logx.TaskLog("scrape", "warn", "保存季元数据失败《%s》第 %d 季：%v", series.Name, season.ParentIndexNumber, err)
                }
                if set.DownloadImgs && resp.PosterPath != "" {
                        if p, err := s.downloadTmdbImage(ctx, series, "w500", resp.PosterPath); err == nil {
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
                                        if err := s.writeTmdbMetadataFile(series, fmt.Sprintf("episode-s%02de%02d.json", e.SeasonNumber, e.EpisodeNumber), e); err != nil {
                                                logx.TaskLog("scrape", "warn", "保存集元数据失败《%s》 S%02dE%02d：%v", series.Name, e.SeasonNumber, e.EpisodeNumber, err)
                                        }
                                        if e.AirDate != "" && len(e.AirDate) >= 10 {
                                                if t, err := time.Parse("2006-01-02", e.AirDate[:10]); err == nil {
                                                        ep.PremiereDate = &t
                                                }
                                        }
                                        if e.Runtime > 0 {
                                                ep.RunTimeTicks = int64(e.Runtime) * 60 * 10000000
                                        }
                                        if set.DownloadImgs && e.StillPath != "" {
                                                if p, err := s.downloadTmdbImage(ctx, series, "w300", e.StillPath); err == nil {
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

// metaDir 元数据目录：与「提取媒体信息设置」的保存目录保持一致。
func (s *Scanner) metaDir() string { return LoadProbeConfig().SaveDir }

func mpaa(status string) string { return "" }
