// Package scanner TMDB 身份唯一化（数据库层）：
//
// 同一媒体库内 (tmdb_kind, tmdb_id) 相同的条目只保留一个可见「正主」，
// 其余条目以 items.duplicate_of 软隐藏指向正主（扫描仍按 路径 维护它们，
// 不会反复重建）。媒体源与播放状态在播放层聚合为「多版本」；
// 正主被删除时自动从成员中晋升替补，悬挂引用由 GC 重新归并。
// 可见正主由部分唯一索引约束，杜绝同身份重复展示。
package scanner

import (
	"strings"

	"gorm.io/gorm"

	"go-emby/internal/db"
	"go-emby/internal/logx"
	"go-emby/internal/models"
)

// tmdbUniqueIndexDDL 部分唯一索引：仅约束可见正主行。
// movie/tv 是 TMDB 的两个编号命名空间，必须一并入键；跨媒体库各自独立。
const tmdbUniqueIndexDDL = `CREATE UNIQUE INDEX IF NOT EXISTS uniq_items_tmdb_canonical
	ON items(library_id, tmdb_kind, tmdb_id)
	WHERE duplicate_of = '' AND tmdb_id <> '' AND type IN ('Movie','Series')`

// migrateTmdbUniqueness 启动迁移：规范化身份命名空间 → 合并存量重复 → 建唯一索引。
// 必须在扫描/刮削工作线程启动前调用（main 里 scanner.New 早于 API 启动）。
func migrateTmdbUniqueness() {
	normalizeTmdbKinds()
	merged := mergeExistingDuplicates()
	gc := gcDanglingDuplicates()
	if err := db.DB.Exec(tmdbUniqueIndexDDL).Error; err != nil {
		logx.WarnC(logx.CatScan, "创建 TMDB 唯一索引失败（重复条目仍会软隐藏，仅缺少数据库级约束）：%v", err)
		return
	}
	if merged > 0 || gc > 0 {
		logx.InfoC(logx.CatScan, "TMDB 身份唯一化迁移完成：合并存量重复 %d 条，修复悬挂引用 %d 条", merged, gc)
	}
}

// normalizeTmdbKinds 旧数据规范化：有 TMDB 编号但缺命名空间的按条目类型补齐，
// 否则同一部片会因 kind 空值/有值被视为两个身份，绕过去重。
func normalizeTmdbKinds() {
	db.DB.Exec(`UPDATE items SET tmdb_kind = CASE type WHEN 'Movie' THEN 'movie' WHEN 'Series' THEN 'tv' END
		WHERE tmdb_id <> '' AND tmdb_kind = '' AND type IN ('Movie','Series')`)
}

// tmdbIdentityOf 条目的 TMDB 身份（命名空间 + 编号）；无身份或非电影/剧集返回 false。
func tmdbIdentityOf(it *models.Item) (string, string, bool) {
	if it == nil {
		return "", "", false
	}
	id := strings.TrimSpace(it.TmdbID)
	if id == "" {
		return "", "", false
	}
	kind := strings.TrimSpace(it.TmdbKind)
	if kind == "" {
		kind = models.TmdbKindForItemType(it.Type)
	}
	if kind != "movie" && kind != "tv" {
		return "", "", false
	}
	return kind, id, true
}

// findVisibleCounterpart 同库同 TMDB 身份的可见正主条目（排除自身）。
func findVisibleCounterpart(it *models.Item) (*models.Item, bool) {
	kind, id, ok := tmdbIdentityOf(it)
	if !ok {
		return nil, false
	}
	var c models.Item
	err := db.DB.Where("library_id = ? AND tmdb_kind = ? AND tmdb_id = ? AND type = ? AND duplicate_of = '' AND id <> ?",
		it.LibraryID, kind, id, it.Type, it.ID).First(&c).Error
	if err != nil {
		return nil, false
	}
	return &c, true
}

// hideDuplicate 将 dup 软隐藏到 canonical 名下，并迁移播放状态。
func hideDuplicate(dupID, canonicalID string) {
	db.DB.Model(&models.Item{}).Where("id = ?", dupID).Updates(map[string]any{
		"duplicate_of": canonicalID,
		"scrape_error": "",
	})
	mergeUserData(dupID, canonicalID)
}

// mergeUserData 把 fromID 条目上的用户播放状态合并到 toID：
// 目标缺记录则整体迁移（主键与 item_id 同步改写）；双方都有则字段取优后删除来源。
func mergeUserData(fromID, toID string) {
	var rows []models.UserDatum
	if err := db.DB.Where("item_id = ?", fromID).Find(&rows).Error; err != nil || len(rows) == 0 {
		return
	}
	for _, row := range rows {
		var existing models.UserDatum
		err := db.DB.First(&existing, "user_id = ? AND item_id = ?", row.UserID, toID).Error
		if err != nil {
			// 目标无记录：直接迁移（UserDatum 主键是 userID|itemID 复合串，需一并改写）
			db.DB.Model(&models.UserDatum{}).Where("id = ?", row.ID).Updates(map[string]any{
				"id":      row.UserID + "|" + toID,
				"item_id": toID,
			})
			continue
		}
		// 双方都有：合并取优，随后删除来源记录
		updates := map[string]any{}
		if !existing.Played && row.Played {
			updates["played"] = true
		}
		if row.PlayCount > existing.PlayCount {
			updates["play_count"] = row.PlayCount
		}
		if !existing.Favorite && row.Favorite {
			updates["favorite"] = true
		}
		if row.PositionTicks > existing.PositionTicks {
			updates["position_ticks"] = row.PositionTicks
		}
		if existing.LastPlayedDate == nil || (row.LastPlayedDate != nil && row.LastPlayedDate.After(*existing.LastPlayedDate)) {
			updates["last_played_date"] = row.LastPlayedDate
		}
		if len(updates) > 0 {
			db.DB.Model(&models.UserDatum{}).Where("id = ?", existing.ID).Updates(updates)
		}
		db.DB.Where("id = ?", row.ID).Delete(&models.UserDatum{})
	}
}

// mergeSeriesDuplicate 将重复剧集连同子树隐藏到 canonical 名下：
// 集 → 正主下同季同集的可见集（迁移播放状态）；无对应集时归属正主剧集；
// 季与剧集本身归属正主剧集。保留原父子结构，正主被删后可整体晋升恢复。
func mergeSeriesDuplicate(dup, canonical *models.Item) {
	var eps []models.Item
	if err := db.DB.Where("series_id = ? AND type = 'Episode'", dup.ID).Find(&eps).Error; err == nil {
		for i := range eps {
			var cEp models.Item
			if err := db.DB.Where("series_id = ? AND type = 'Episode' AND parent_index_number = ? AND index_number = ? AND duplicate_of = ''",
				canonical.ID, eps[i].ParentIndexNumber, eps[i].IndexNumber).First(&cEp).Error; err == nil {
				hideDuplicate(eps[i].ID, cEp.ID)
			} else {
				db.DB.Model(&models.Item{}).Where("id = ?", eps[i].ID).Update("duplicate_of", canonical.ID)
			}
		}
	}
	db.DB.Model(&models.Item{}).Where("series_id = ? AND type = 'Season'", dup.ID).Update("duplicate_of", canonical.ID)
	hideDuplicate(dup.ID, canonical.ID)
}

// dedupeVisibility 刮削完成、落库前归并可见性（由调用方随后 Save）：
// 同库同身份已有可见正主 → 并入正主（返回 true，跳过后续季集刮削）；
// 身份变化且此前被隐藏 → 恢复可见（duplicate_of 清空）。
func (s *Scanner) dedupeVisibility(it *models.Item) bool {
	if it == nil || (it.Type != "Movie" && it.Type != "Series") {
		return false
	}
	if c, ok := findVisibleCounterpart(it); ok {
		it.DuplicateOf = c.ID
		it.ScrapeError = ""
		if it.Type == "Series" {
			mergeSeriesDuplicate(it, c)
		} else {
			db.DB.Model(&models.Item{}).Where("id = ?", it.ID).Updates(map[string]any{
				"duplicate_of": c.ID,
				"scrape_error": "",
			})
			mergeUserData(it.ID, c.ID)
		}
		return true
	}
	it.DuplicateOf = ""
	return false
}

// mergeExistingDuplicates 合并数据库中已存在的同身份可见重复条目（启动迁移用）。
// 每组按「已刮削 > 有海报 > 最早入库」选出正主，其余隐藏。
func mergeExistingDuplicates() int {
	type identity struct {
		libraryID, kind, tmdbID string
	}
	group := func(items []models.Item) map[identity][]*models.Item {
		out := map[identity][]*models.Item{}
		for i := range items {
			kind, id, ok := tmdbIdentityOf(&items[i])
			if !ok {
				continue
			}
			k := identity{items[i].LibraryID, kind, id}
			out[k] = append(out[k], &items[i])
		}
		return out
	}

	n := 0
	var series []models.Item
	if err := db.DB.Where("type = 'Series' AND tmdb_id <> '' AND duplicate_of = ''").
		Order("date_created").Find(&series).Error; err == nil {
		for _, members := range group(series) {
			best := pickBestMember(members)
			for _, m := range members {
				if m.ID != best.ID {
					mergeSeriesDuplicate(m, best)
					n++
				}
			}
		}
	}
	var movies []models.Item
	if err := db.DB.Where("type = 'Movie' AND tmdb_id <> '' AND duplicate_of = ''").
		Order("date_created").Find(&movies).Error; err == nil {
		for _, members := range group(movies) {
			best := pickBestMember(members)
			for _, m := range members {
				if m.ID != best.ID {
					hideDuplicate(m.ID, best.ID)
					n++
				}
			}
		}
	}
	return n
}

// pickBestMember 选正主：已刮削 > 有海报 > 最早入库。
func pickBestMember(members []*models.Item) *models.Item {
	if len(members) == 0 {
		return nil
	}
	best := members[0]
	for _, c := range members[1:] {
		switch {
		case c.Scraped && !best.Scraped:
			best = c
		case c.Scraped == best.Scraped && c.Poster != "" && best.Poster == "":
			best = c
		case c.Scraped == best.Scraped && (c.Poster != "") == (best.Poster != "") && c.DateCreated.Before(best.DateCreated):
			best = c
		}
	}
	return best
}

// promoteMembers 可见正主被删除前，从其重复成员中晋升一个替补并转移正主数据：
// 事务内先隐藏正主释放唯一身份 → 晋升替补 → 其余成员改挂替补；
// 替补元数据缺失时从正主复制，避免海报/简介丢失。随后调用方删除正主行。
func promoteMembers(victim *models.Item) {
	if victim == nil {
		return
	}
	var members []models.Item
	if err := db.DB.Where("duplicate_of = ?", victim.ID).Order("date_created").Find(&members).Error; err != nil || len(members) == 0 {
		return
	}
	refs := make([]*models.Item, len(members))
	for i := range members {
		refs[i] = &members[i]
	}
	best := pickBestMember(refs)
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		// 先隐藏正主（此时同身份无可见行），再晋升替补，避免撞唯一索引
		if err := tx.Model(&models.Item{}).Where("id = ?", victim.ID).Update("duplicate_of", best.ID).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Item{}).Where("id = ?", best.ID).Update("duplicate_of", "").Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Item{}).Where("duplicate_of = ? AND id <> ?", victim.ID, best.ID).
			Update("duplicate_of", best.ID).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		logx.WarnC(logx.CatScan, "晋升重复成员失败（《%s》）：%v", victim.Name, err)
		return
	}
	// 正主已刮削而替补缺失时，转移元数据
	if victim.Scraped && (!best.Scraped || best.Poster == "") {
		best.Name, best.OriginalTitle = victim.Name, victim.OriginalTitle
		best.Overview, best.Year, best.PremiereDate = victim.Overview, victim.Year, victim.PremiereDate
		best.CommunityRating, best.OfficialRating = victim.CommunityRating, victim.OfficialRating
		best.Genres, best.Studios, best.People, best.Tags = victim.Genres, victim.Studios, victim.People, victim.Tags
		best.ProviderIDs, best.TmdbID, best.TmdbKind = victim.ProviderIDs, victim.TmdbID, victim.TmdbKind
		best.Poster, best.Thumb, best.Backdrop, best.Logo = victim.Poster, victim.Thumb, victim.Backdrop, victim.Logo
		best.RunTimeTicks, best.Scraped = victim.RunTimeTicks, true
		best.ImageRev = imageRev(best.Poster, best.Backdrop, best.Thumb, best.Logo)
		if err := db.DB.Save(best).Error; err != nil {
			logx.WarnC(logx.CatScan, "晋升成员保存元数据失败（《%s》）：%v", best.Name, err)
		}
	}
	mergeUserData(victim.ID, best.ID)
	logx.InfoC(logx.CatScan, "正主《%s》已删除，同 TMDB 身份的《%s》晋升为展示条目", victim.Name, best.Name)
}

// gcDanglingDuplicates 重新归并指向已不存在条目的 duplicate_of（正主被清理后的遗留）。
// 按 剧集 → 电影 → 季 → 集 的顺序处理：先定剧集可见性，季集才能正确归属；
// 顺序处理时同组多条竞争由「先恢复可见者成为新正主」自然解决。
func gcDanglingDuplicates() int {
	var rows []models.Item
	if err := db.DB.Where("duplicate_of <> '' AND duplicate_of NOT IN (SELECT id FROM items)").
		Order("date_created").Find(&rows).Error; err != nil || len(rows) == 0 {
		return 0
	}
	for pass := 0; pass < 4; pass++ {
		for i := range rows {
			if danglingPass(rows[i].Type) == pass {
				resolveDangling(&rows[i])
			}
		}
	}
	return len(rows)
}

func danglingPass(typ string) int {
	switch typ {
	case "Series":
		return 0
	case "Movie":
		return 1
	case "Season":
		return 2
	default: // Episode 及其它
		return 3
	}
}

// resolveDangling 为一条悬挂引用重新找归属。
func resolveDangling(it *models.Item) {
	switch it.Type {
	case "Movie":
		if c, ok := findVisibleCounterpart(it); ok {
			hideDuplicate(it.ID, c.ID)
			return
		}
		db.DB.Model(&models.Item{}).Where("id = ?", it.ID).Update("duplicate_of", "")
	case "Series":
		if c, ok := findVisibleCounterpart(it); ok {
			mergeSeriesDuplicate(it, c)
			return
		}
		db.DB.Model(&models.Item{}).Where("id = ?", it.ID).Update("duplicate_of", "")
	case "Season", "Episode":
		resolveDanglingChild(it)
	}
}

// resolveDanglingChild 季/集悬挂：优先同剧集内可见的同位条目；
// 所属剧集可见则本条恢复可见；剧集仍隐藏则归属其正主（集优先匹配正主的同季同集）。
func resolveDanglingChild(it *models.Item) {
	if c, ok := findVisibleSibling(it); ok {
		db.DB.Model(&models.Item{}).Where("id = ?", it.ID).Update("duplicate_of", c.ID)
		return
	}
	var series models.Item
	if err := db.DB.First(&series, "id = ?", it.SeriesID).Error; err != nil {
		// 所属剧集已不存在：保持隐藏等待空季/空集清理，避免孤儿重新露出
		return
	}
	if series.DuplicateOf == "" {
		db.DB.Model(&models.Item{}).Where("id = ?", it.ID).Update("duplicate_of", "")
		return
	}
	var canonical models.Item
	if err := db.DB.First(&canonical, "id = ?", series.DuplicateOf).Error; err != nil {
		return // 正主也悬挂：剧集轮次已先行处理，通常不可达
	}
	if it.Type == "Episode" {
		var cEp models.Item
		if err := db.DB.Where("series_id = ? AND type = 'Episode' AND parent_index_number = ? AND index_number = ? AND duplicate_of = ''",
			canonical.ID, it.ParentIndexNumber, it.IndexNumber).First(&cEp).Error; err == nil {
			db.DB.Model(&models.Item{}).Where("id = ?", it.ID).Update("duplicate_of", cEp.ID)
			return
		}
	}
	db.DB.Model(&models.Item{}).Where("id = ?", it.ID).Update("duplicate_of", canonical.ID)
}

// findVisibleSibling 同剧集内可见的同位条目（同季，或同季同集）。
func findVisibleSibling(it *models.Item) (*models.Item, bool) {
	q := db.DB.Where("series_id = ? AND type = ? AND duplicate_of = '' AND id <> ?", it.SeriesID, it.Type, it.ID)
	if it.Type == "Season" {
		q = q.Where("parent_index_number = ?", it.ParentIndexNumber)
	} else {
		q = q.Where("parent_index_number = ? AND index_number = ?", it.ParentIndexNumber, it.IndexNumber)
	}
	var c models.Item
	if err := q.First(&c).Error; err != nil {
		return nil, false
	}
	return &c, true
}
