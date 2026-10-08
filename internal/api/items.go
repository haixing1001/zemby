// Package api 条目查询引擎（Items）。
package api

import (
        "encoding/json"
        "net/http"
        "sort"
        "strconv"
        "strings"
        "time"

        "gorm.io/gorm/clause"

        "go-emby/internal/auth"
        "go-emby/internal/models"
        "go-emby/internal/scanner"
)

// itemQuery 查询参数。
type itemQuery struct {
        UserID      string
        ParentID    string
        LibID       string
        SeriesID    string
        SeasonID    string
        IncludeItemTypes []string
        Recursive   bool
        SearchTerm  string
        Genres      []string
        Years       []int
        Filters     []string
        SortBy      string
        SortOrder   string
        Limit       int
        StartIndex  int
        ids         []string
        IsFolder    *bool
        Fields      []string
        EnableTotal bool
        Resume      bool
        Favorite    bool
        Played      *bool
        MinRating   float64
        // detail 模式
        Detail bool
}

// parseQuery 解析查询参数。
func (a *App) parseQuery(r *http.Request, mode string) *itemQuery {
        qry := &itemQuery{
                SortBy:      strings.ToLower(q(r, "SortBy")),
                SortOrder:   strings.ToLower(q(r, "SortOrder")),
                Limit:       qInt(r, "Limit", 60),
                StartIndex:  qInt(r, "StartIndex", 0),
                Recursive:   qBool(r, "Recursive"),
                SearchTerm:  strings.TrimSpace(q(r, "SearchTerm")),
                EnableTotal: !strings.EqualFold(q(r, "EnableTotalRecordCount"), "false"),
        }
        if mode == "resume" {
                qry.Resume = true
        }
        if v := q(r, "UserId"); v != "" {
                qry.UserID = strings.ToLower(v)
        }
        if id := auth.From(r); id != nil && id.User != nil && qry.UserID == "" {
                qry.UserID = id.User.ID
        }
        if v := q(r, "ParentId"); v != "" {
                qry.ParentID = strings.ToLower(v)
        }
        if v := q(r, "Ids"); v != "" {
                for _, x := range strings.Split(v, ",") {
                        x = strings.TrimSpace(strings.ToLower(x))
                        if x != "" {
                                qry.ids = append(qry.ids, x)
                        }
                }
        }
        splitParam := func(name string) []string {
                v := q(r, name)
                if v == "" {
                        return nil
                }
                var out []string
                for _, x := range strings.Split(v, ",") {
                        x = strings.TrimSpace(x)
                        if x != "" {
                                out = append(out, x)
                        }
                }
                return out
        }
        qry.IncludeItemTypes = normalizeTypes(splitParam("IncludeItemTypes"))
        for _, g := range splitParam("Genres") {
                for _, x := range strings.Split(g, "|") {
                        if x = strings.TrimSpace(x); x != "" {
                                qry.Genres = append(qry.Genres, x)
                        }
                }
        }
        for _, y := range splitParam("Years") {
                n := 0
                ok := true
                for i := 0; i < len(y); i++ {
                        if y[i] < '0' || y[i] > '9' {
                                ok = false
                                break
                        }
                        n = n*10 + int(y[i]-'0')
                }
                if ok && n > 0 {
                        qry.Years = append(qry.Years, n)
                }
        }
        for _, f := range splitParam("Filters") {
                qry.Filters = append(qry.Filters, strings.ToLower(f))
        }
        // 单独的 IsPlayed / IsFavorite 参数
        if v := strings.ToLower(q(r, "IsPlayed")); v == "true" || v == "false" {
                b := v == "true"
                qry.Played = &b
        }
        if qBool(r, "IsFavorite") {
                qry.Favorite = true
        }
        if v := strings.ToLower(q(r, "IsFolder")); v == "true" || v == "false" {
                b := v == "true"
                qry.IsFolder = &b
        }
        if v := qInt(r, "MinCommunityRating", 0); v > 0 {
                qry.MinRating = float64(v)
        }
        if f := q(r, "Fields"); f != "" {
                for _, x := range strings.Split(f, ",") {
                        qry.Fields = append(qry.Fields, strings.ToLower(strings.TrimSpace(x)))
                        switch strings.ToLower(strings.TrimSpace(x)) {
                        case "mediasources", "mediastreams", "overview", "genres", "providerids", "etag", "people", "studios", "path", "alternatemediasources", "primarimageaspectratio":
                                qry.Detail = true
                        }
                }
        }
        if qry.Limit <= 0 || qry.Limit > 1000 {
                qry.Limit = 60
        }
        return qry
}

// normalizeTypes 类型名规范化。
func normalizeTypes(in []string) []string {
        var out []string
        for _, t := range in {
                switch strings.ToLower(t) {
                case "movie": out = append(out, "Movie")
                case "series", "show": out = append(out, "Series")
                case "season": out = append(out, "Season")
                case "episode": out = append(out, "Episode")
                case "folder", "collectionfolder", "foldertype", "userrootfolder", "userview", "collection":
                        out = append(out, "CollectionFolder")
                case "person": out = append(out, "Person")
                case "genre": out = append(out, "Genre")
                case "boxset": out = append(out, "Movie")
                }
        }
        return out
}

// queryItems 执行查询，返回条目与总数。
func (a *App) queryItems(q *itemQuery) ([]models.Item, int) {
        dbq := a.db.Model(&models.Item{}).Where("type IN ?", []string{"Movie", "Series", "Season", "Episode"})

        // Ids
        if len(q.ids) > 0 {
                dbq = dbq.Where("id IN ?", q.ids)
        }
        // 收藏虚拟库：按当前用户收藏过滤（电影+剧集）
        if q.ParentID == favLibID {
                q.Favorite = true
                q.Recursive = true
                dbq = dbq.Where("type IN ?", []string{"Movie", "Series"})
        } else if q.ParentID != "" && q.ParentID != "root" && q.ParentID != a.serverID {
                var lib models.Library
                if err := a.db.First(&lib, "id = ?", q.ParentID).Error; err == nil {
                        q.LibID = lib.ID
                        dbq = dbq.Where("library_id = ?", lib.ID)
                        if !q.Recursive {
                                // 非递归：返回库直属内容（Series/Movie）
                                if lib.Type == "tvshows" {
                                        dbq = dbq.Where("type = ?", "Series")
                                } else {
                                        dbq = dbq.Where("type = ?", "Movie")
                                }
                        } else if len(q.IncludeItemTypes) > 0 {
                                // 递归 + 类型过滤
                                dbq = dbq.Where("type IN ?", q.IncludeItemTypes)
                        }
                } else {
                        // 是 Series / Season / 其他条目
                        var parent models.Item
                        if err := a.db.First(&parent, "id = ?", q.ParentID).Error; err == nil {
                                switch parent.Type {
                                case "Series":
                                        q.SeriesID = parent.ID
                                        if q.Recursive {
                                                dbq = dbq.Where("series_id = ?", parent.ID)
                                                if len(q.IncludeItemTypes) > 0 {
                                                        dbq = dbq.Where("type IN ?", q.IncludeItemTypes)
                                                }
                                        } else {
                                                dbq = dbq.Where("parent_id = ?", parent.ID).Where("type = ?", "Season")
                                        }
                                case "Season":
                                        q.SeasonID = parent.ID
                                        dbq = dbq.Where("season_id = ?", parent.ID).Where("type = ?", "Episode")
                                default:
                                        dbq = dbq.Where("parent_id = ?", parent.ID)
                                }
                        } else {
                                // 未知 ParentId：空结果
                                return []models.Item{}, 0
                        }
                }
        } else if q.ParentID == "root" || q.ParentID == a.serverID {
                // 返回库列表
                var libs []models.Library
                a.db.Order("sort_order, created_at").Find(&libs)
                // 以 CollectionFolder 名义返回，用 Item 类型 hack：转字符串让调用方处理
                // 这里通过查询标记直接构造（items() 内处理）
                q.IncludeItemTypes = append(q.IncludeItemTypes, "__libraries__")
        }

        // 类型过滤（无 ParentId 时）
        if len(q.IncludeItemTypes) > 0 && q.LibID == "" {
                dbq = dbq.Where("type IN ?", q.IncludeItemTypes)
        }

        if q.LibID != "" {
                dbq = dbq.Where("library_id = ?", q.LibID)
        }
        if q.SeriesID != "" {
                dbq = dbq.Where("series_id = ?", q.SeriesID)
        }
        if q.SeasonID != "" {
                dbq = dbq.Where("season_id = ?", q.SeasonID)
        }
        if q.SearchTerm != "" {
                like := "%" + likeEscape(q.SearchTerm) + "%"
                if scanner.LoadEnhanceConfig().SearchByInitials && isInitialsQuery(q.SearchTerm) {
                        // 首字母搜索：中文名按拼音首字母前缀匹配，同时保留原名包含匹配
                        dbq = dbq.Where("name LIKE ? ESCAPE '\\' OR initials LIKE ? ESCAPE '\\'",
                                like, likeEscape(strings.ToLower(q.SearchTerm))+"%")
                } else {
                        dbq = dbq.Where("name LIKE ? ESCAPE '\\'", like)
                }
        }
        for _, g := range q.Genres {
                dbq = dbq.Where("genres LIKE ?", "%\""+likeEscape(g)+"\"%")
        }
        if len(q.Years) > 0 {
                dbq = dbq.Where("year IN ?", q.Years)
        }
        if q.MinRating > 0 {
                dbq = dbq.Where("community_rating >= ?", q.MinRating)
        }
        if q.IsFolder != nil {
                if *q.IsFolder {
                        dbq = dbq.Where("type IN ?", []string{"Series", "Season"})
                } else {
                        dbq = dbq.Where("type IN ?", []string{"Movie", "Episode"})
                }
        }
        // Filters
        for _, f := range q.Filters {
                switch {
                case strings.Contains(f, "isresumable"):
                        // 继续播放：有进度且未看完
                        var ids []string
                        a.db.Model(&models.UserDatum{}).Where("user_id = ? AND position_ticks > 0", q.UserID).Select("item_id").Scan(&ids)
                        if len(ids) > 0 {
                                dbq = dbq.Where("id IN ?", ids)
                        } else {
                                return []models.Item{}, 0
                        }
                case strings.Contains(f, "isplayed"):
                        ids := a.userPlayedIDs(q.UserID, true)
                        dbq = dbq.Where("id IN ?", orEmpty(ids))
                case strings.Contains(f, "isunplayed"):
                        ids := a.userPlayedIDs(q.UserID, true)
                        dbq = dbq.Where("id NOT IN ?", orEmpty(ids))
                }
        }
        if q.Played != nil {
                ids := a.userPlayedIDs(q.UserID, true)
                if *q.Played {
                        dbq = dbq.Where("id IN ?", orEmpty(ids))
                } else {
                        dbq = dbq.Where("id NOT IN ?", orEmpty(ids))
                }
        }
        if q.Favorite {
                var ids []string
                a.db.Model(&models.UserDatum{}).Where("user_id = ? AND favorite = ?", q.UserID, true).Select("item_id").Scan(&ids)
                dbq = dbq.Where("id IN ?", orEmpty(ids))
        }
        // Resume 模式
        if q.Resume {
                var ids []string
                a.db.Model(&models.UserDatum{}).Where("user_id = ? AND position_ticks > 0 AND played = ?", q.UserID, false).Select("item_id").Scan(&ids)
                dbq = dbq.Where("id IN ?", orEmpty(ids))
                dbq = dbq.Where("type IN ?", []string{"Movie", "Episode"})
        }

        // 计数
        total := 0
        if q.EnableTotal {
                var cnt int64
                dbq.Count(&cnt)
                total = int(cnt)
        }

        // 排序
        orderBy := a.sortClause(q)
        if q.Resume && q.SortBy == "" {
                // 继续观看未显式指定排序时按 Emby 标准行为：最近播放的排前面（按用户数据中的 last_played_date 倒序）。
                // 注意 GORM 的 Order() 不支持 clause.Expr（会静默忽略），须经 Statement.AddClause 传参化表达式
                dbq.Statement.AddClause(clause.OrderBy{Expression: clause.Expr{
                        SQL:  "(SELECT ud.last_played_date FROM user_data ud WHERE ud.user_id = ? AND ud.item_id = items.id) DESC",
                        Vars: []interface{}{q.UserID},
                }})
        } else {
                dbq = dbq.Order(orderBy)
        }

        // 多版本合并：开启开关时全量取回内存分组后分页（仅影响电影）
        if a.mergeEnabledFor(q) {
                var all []models.Item
                if err := dbq.Limit(20000).Find(&all).Error; err != nil {
                        return []models.Item{}, 0
                }
                merged, total := a.groupVersions(all)
                start := q.StartIndex
                if start > len(merged) {
                        start = len(merged)
                }
                end := len(merged)
                if q.Limit > 0 && start+q.Limit < end {
                        end = start + q.Limit
                }
                return merged[start:end], total
        }

        var items []models.Item
        if q.Limit > 0 {
                dbq = dbq.Limit(q.Limit).Offset(q.StartIndex)
        }
        if err := dbq.Find(&items).Error; err != nil {
                return []models.Item{}, 0
        }
        return items, total
}

// isInitialsQuery 是否为纯字母/数字搜索词（首字母匹配模式）。
func isInitialsQuery(s string) bool {
        if s == "" {
                return false
        }
        for _, ch := range s {
                if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
                        return false
                }
        }
        return true
}

// mergeEnabledFor 当前查询是否需要多版本合并分组。
func (a *App) mergeEnabledFor(q *itemQuery) bool {
        enh := scanner.LoadEnhanceConfig()
        if !enh.MergeVersionsInLibrary && !enh.MergeVersionsAcrossLibraries {
                return false
        }
        // 仅电影参与分组；类型过滤排除电影时不启用
        if len(q.IncludeItemTypes) > 0 {
                ok := false
                for _, t := range q.IncludeItemTypes {
                        if t == "Movie" {
                                ok = true
                        }
                }
                if !ok {
                        return false
                }
        }
        return true
}

// movieVersionKey 电影版本分组键：优先 TMDB ID；无 TMDB ID 时降级同名同年。
func movieVersionKey(it *models.Item, across bool) string {
        if tmdbID := strings.TrimSpace(it.TmdbID); tmdbID != "" {
                tmdbKind := strings.TrimSpace(it.TmdbKind)
                if tmdbKind == "" {
                        tmdbKind = "movie"
                }
                key := "tmdb:" + tmdbKind + ":" + tmdbID
                if !across {
                        key = it.LibraryID + "|" + key
                }
                return key
        }
        n := strings.ToLower(strings.TrimSpace(it.Name))
        if n == "" {
                return ""
        }
        key := n + "|" + strconv.Itoa(it.Year)
        if !across {
                key = it.LibraryID + "|" + key
        }
        return key
}

// groupVersions 查询结果内存分组：每组保留排序首位作代表，总数按分组后计。
func (a *App) groupVersions(all []models.Item) ([]models.Item, int) {
        enh := scanner.LoadEnhanceConfig()
        across := enh.MergeVersionsAcrossLibraries
        seen := map[string]bool{}
        out := make([]models.Item, 0, len(all))
        for i := range all {
                it := &all[i]
                key := ""
                if it.Type == "Movie" {
                        key = movieVersionKey(it, across)
                }
                if key == "" {
                        out = append(out, *it)
                        continue
                }
                if seen[key] {
                        continue // 后续版本不输出（由 DTO 聚合 MediaSources）
                }
                seen[key] = true
                out = append(out, *it)
        }
        return out, len(out)
}

// versionMembers 同身份的其它电影条目（跨库或库内，按开关）。
func (a *App) versionMembers(it *models.Item) []models.Item {
        enh := scanner.LoadEnhanceConfig()
        if it.Type != "Movie" || (!enh.MergeVersionsInLibrary && !enh.MergeVersionsAcrossLibraries) {
                return nil
        }
        if tmdbID := strings.TrimSpace(it.TmdbID); tmdbID != "" {
                tmdbKind := strings.TrimSpace(it.TmdbKind)
                if tmdbKind == "" {
                        tmdbKind = "movie"
                }
                q := a.db.Where("type = 'Movie' AND id <> ? AND tmdb_id = ? AND tmdb_kind = ?", it.ID, tmdbID, tmdbKind)
                if !enh.MergeVersionsAcrossLibraries {
                        q = q.Where("library_id = ?", it.LibraryID)
                }
                var out []models.Item
                q.Order("date_created").Limit(100).Find(&out)
                return out
        }
        name := strings.ToLower(strings.TrimSpace(it.Name))
        if name == "" {
                return nil
        }
        q := a.db.Where("type = 'Movie' AND id <> ? AND lower(trim(name)) = ? AND year = ?",
                it.ID, name, it.Year)
        if !enh.MergeVersionsAcrossLibraries {
                q = q.Where("library_id = ?", it.LibraryID)
        }
        var out []models.Item
        q.Order("date_created").Limit(100).Find(&out)
        return out
}

// mergedSources 条目媒体源（多版本合并开启且 detail 时聚合同身份成员的源）。
func (a *App) mergedSources(it *models.Item, detail bool) []M {
        base := a.sources(it, detail)
        if !detail {
                return base
        }
        members := a.versionMembers(it)
        for i := range members {
                base = append(base, a.sources(&members[i], detail)...)
        }
        return base
}

func orEmpty(ids []string) []string {
        if len(ids) == 0 {
                return []string{""}
        }
        return ids
}

func likeEscape(s string) string {
        s = strings.ReplaceAll(s, "\\", "\\\\")
        s = strings.ReplaceAll(s, "%", "\\%")
        s = strings.ReplaceAll(s, "_", "\\_")
        return s
}

// userPlayedIDs 已看条目 ID。
func (a *App) userPlayedIDs(userID string, played bool) []string {
        var ids []string
        a.db.Model(&models.UserDatum{}).Where("user_id = ? AND played = ?", userID, played).Select("item_id").Scan(&ids)
        return ids
}

// sortClause 排序子句。
func (a *App) sortClause(q *itemQuery) string {
        order := "ASC"
        if strings.HasPrefix(q.SortOrder, "desc") {
                order = "DESC"
        }
        col := "name"
        for _, s := range strings.Split(q.SortBy, ",") {
                s = strings.TrimSpace(s)
                switch s {
                case "sortname", "name":
                        col = "name"
                case "productionyear", "year":
                        col = "year"
                case "datecreated", "dateadded":
                        col = "date_created"
                case "premieredate":
                        col = "premiere_date"
                case "indexnumber":
                        col = "index_number"
                case "parentindexnumber", "parentindexnumber,indexnumber":
                        col = "parent_index_number"
                case "communityrating", "rating":
                        col = "community_rating"
                case "runtime", "runtimeticks":
                        col = "run_time_ticks"
                case "random":
                        col = "RANDOM()"
                }
                if col != "" {
                        break
                }
        }
        // 剧集内按 季/集 排
        if q.SeriesID != "" || q.SeasonID != "" {
                return "parent_index_number ASC, index_number ASC, name " + order
        }
        if col == "RANDOM()" {
                return col
        }
        if col == "premiere_date" {
                // 无发行日期的内容排在最后（升序/降序一致）
                return "(CASE WHEN premiere_date IS NULL THEN 1 ELSE 0 END) ASC, premiere_date " + order + ", id"
        }
        return col + " " + order + ", id"
}

// librariesAsItems 库列表伪装。
func (a *App) librariesAsItems(libs []models.Library) ([]models.Item, int) {
        // 由调用方通过 itemsDTO 处理库特殊化；这里返回空集合并用标记
        return []models.Item{}, len(libs)
}

// itemsQuery /Items 查询。
func (a *App) itemsQuery(w http.ResponseWriter, r *http.Request, mode string) {
        q := a.parseQuery(r, mode)
        // 特殊：ParentId=root 直接返回库
        if q.ParentID == "root" || q.ParentID == a.serverID {
                var libs []models.Library
                a.db.Order("sort_order, created_at").Find(&libs)
                items := []M{}
                for i := range libs {
                        items = append(items, a.libDTO(&libs[i]))
                }
                a.json(w, 200, M{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
                return
        }
        items, total := a.queryItems(q)
        out := a.itemDTOs(items, q.Detail, q.UserID)
        a.json(w, 200, M{
                "Items": out, "TotalRecordCount": total,
                "StartIndex": q.StartIndex,
        })
}

// itemsLatest 最新添加（裸数组）。
func (a *App) itemsLatest(w http.ResponseWriter, r *http.Request) {
        q := a.parseQuery(r, "")
        limit := q.Limit
        if limit == 60 {
                limit = qInt(r, "Limit", 16)
        }
        var items []models.Item
        dbq := a.db.Where("type IN ?", []string{"Movie", "Series"}).Order("date_created DESC, id DESC").Limit(limit)
        if q.ParentID != "" && q.ParentID != "root" {
                dbq = dbq.Where("library_id = ?", q.ParentID)
        }
        dbq.Find(&items)
        out := a.itemDTOs(items, false, q.UserID)
        a.json(w, 200, out)
}

// itemDetail 单条目详情。
func (a *App) itemDetail(w http.ResponseWriter, r *http.Request, id string) {
        id = strings.ToLower(id)
        if id == "root" {
                a.userRoot(w, r)
                return
        }
        var it models.Item
        if err := a.db.First(&it, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "条目不存在")
                return
        }
        // 浏览时提取：缺媒体信息则后台补齐（异步，不阻塞响应）
        if it.Type == "Movie" || it.Type == "Episode" {
                go a.scanner.TryProbeOnBrowse(it.ID)
        }
        uid := ""
        if idn := auth.From(r); idn != nil && idn.User != nil {
                uid = idn.User.ID
        }
        a.json(w, 200, a.itemDTO(&it, true, uid))
}

// itemSimilar returns same-library items ranked by genre, year, and rating.
func (a *App) itemSimilar(w http.ResponseWriter, r *http.Request, id string) {
        var it models.Item
        if err := a.db.First(&it, "id = ?", strings.ToLower(id)).Error; err != nil {
                a.fail(w, 404, "条目不存在")
                return
        }
	limit := qInt(r, "Limit", 8)
	if limit <= 0 {
		limit = 8
	}
	if limit > 24 {
		limit = 24
	}
	var items []models.Item
	dbq := a.db.Select("id, library_id, type, name, sort_name, year, genres, community_rating, poster, image_rev, date_created").
		Where("type = ? AND id <> ?", it.Type, it.ID).
		Order("community_rating DESC").Limit(500)
        if it.LibraryID != "" {
                dbq = dbq.Where("library_id = ?", it.LibraryID)
        }
        dbq.Find(&items)

	baseGenres := make(map[string]struct{})
	for _, genre := range strSlice(it.Genres) {
		baseGenres[genre] = struct{}{}
	}
	type rankedItem struct {
		item  models.Item
		score float64
		yearGap int
	}
	ranked := make([]rankedItem, 0, len(items))
	for i := range items {
		candidate := items[i]
		sharedGenres := 0
		for _, genre := range strSlice(candidate.Genres) {
			if _, ok := baseGenres[genre]; ok {
				sharedGenres++
			}
		}
		yearGap := 9999
		yearScore := 0.0
		if it.Year > 0 && candidate.Year > 0 {
			yearGap = it.Year - candidate.Year
			if yearGap < 0 {
				yearGap = -yearGap
			}
			yearScore = 10 - float64(yearGap)
			if yearScore < 0 {
				yearScore = 0
			}
		}
		ranked = append(ranked, rankedItem{
			item: candidate,
			score: float64(sharedGenres*100) + yearScore + candidate.CommunityRating*0.1,
			yearGap: yearGap,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].yearGap != ranked[j].yearGap {
			return ranked[i].yearGap < ranked[j].yearGap
		}
		if ranked[i].item.CommunityRating != ranked[j].item.CommunityRating {
			return ranked[i].item.CommunityRating > ranked[j].item.CommunityRating
		}
		return ranked[i].item.Name < ranked[j].item.Name
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	items = make([]models.Item, len(ranked))
	for i := range ranked {
		items[i] = ranked[i].item
	}
        out := a.itemDTOs(items, false, "")
        a.json(w, 200, M{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0})
}

// showSeasons 剧集季列表。
func (a *App) showSeasons(w http.ResponseWriter, r *http.Request, seriesID string) {
        var seasons []models.Item
        a.db.Where("series_id = ? AND type = 'Season'", strings.ToLower(seriesID)).
                Order("parent_index_number ASC").Find(&seasons)
        uid := ""
        if idn := auth.From(r); idn != nil && idn.User != nil {
                uid = idn.User.ID
        }
        out := a.itemDTOs(seasons, false, uid)
        a.json(w, 200, M{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0})
}

// showEpisodes 剧集集列表。
func (a *App) showEpisodes(w http.ResponseWriter, r *http.Request, seriesID string) {
        seasonID := strings.ToLower(q(r, "SeasonId"))
        seasonNum := qInt(r, "Season", 0)
        dbq := a.db.Model(&models.Item{}).Where("series_id = ? AND type = 'Episode'", strings.ToLower(seriesID))
        if seasonID != "" {
                dbq = dbq.Where("season_id = ?", seasonID)
        } else if seasonNum > 0 {
                dbq = dbq.Where("parent_index_number = ?", seasonNum)
        }
        limit := qInt(r, "Limit", 10000)
        if limit <= 0 || limit > 10000 {
                limit = 10000
        }
        var episodes []models.Item
        dbq.Order("parent_index_number ASC, index_number ASC").Limit(limit).Offset(qInt(r, "StartIndex", 0)).Find(&episodes)
        uid := ""
        if idn := auth.From(r); idn != nil && idn.User != nil {
                uid = idn.User.ID
        }
        out := a.itemDTOs(episodes, true, uid)
        a.json(w, 200, M{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0})
}

// persons 人物列表。
func (a *App) persons(w http.ResponseWriter, r *http.Request) {
        var items []models.Item
        a.db.Where("people <> '' AND people IS NOT NULL").Limit(500).Find(&items)
        seen := map[string]M{}
        var order []string
        for i := range items {
                var people []map[string]string
                _ = json.Unmarshal([]byte(items[i].People), &people)
                for _, p := range people {
                        if p["Name"] == "" {
                                continue
                        }
                        if _, ok := seen[p["Name"]]; !ok {
                                seen[p["Name"]] = M{
                                        "Id": personID(p["Name"]), "Name": p["Name"], "Type": "Person",
                                        "IsFolder": false, "ImageTags": M{}, "BackdropImageTags": []any{},
                                        "PrimaryImageAspectRatio": 0.6666666666666666,
                                        "UserData":                M{"Played": false, "IsFavorite": false, "PlaybackPositionTicks": 0},
                                        "MovieCount":              1,
                                }
                                order = append(order, p["Name"])
                        } else {
                                m := seen[p["Name"]]
                                m["MovieCount"] = m["MovieCount"].(int) + 1
                        }
                }
        }
        limit := qInt(r, "Limit", 60)
        out := []M{}
        start := qInt(r, "StartIndex", 0)
        for i, name := range order {
                if i < start {
                        continue
                }
                if len(out) >= limit {
                        break
                }
                out = append(out, seen[name])
        }
        sort.Slice(out, func(i, j int) bool {
                return out[i]["Name"].(string) < out[j]["Name"].(string)
        })
        a.json(w, 200, M{"Items": out, "TotalRecordCount": len(order), "StartIndex": start})
}

// personDetail 人物详情与作品。
func (a *App) personDetail(w http.ResponseWriter, r *http.Request, nameOrID string) {
        a.json(w, 200, M{
                "Id": personID(nameOrID), "Name": nameOrID, "Type": "Person",
                "IsFolder": false, "ImageTags": M{}, "BackdropImageTags": []any{},
                "UserData": M{"Played": false, "IsFavorite": false, "PlaybackPositionTicks": 0},
        })
}

// genres 类型列表。
func (a *App) genres(w http.ResponseWriter, r *http.Request) {
        var items []models.Item
        a.db.Where("genres <> '' AND genres IS NOT NULL AND type IN ?", []string{"Movie", "Series"}).Find(&items)
        counts := map[string]int{}
        for i := range items {
                for _, g := range strSlice(items[i].Genres) {
                        counts[g]++
                }
        }
        out := []M{}
        for name, c := range counts {
                out = append(out, M{
                        "Id": genreID(name), "Name": name, "Type": "Genre", "IsFolder": true,
                        "ImageTags": M{}, "BackdropImageTags": []any{},
                        "UserData": M{"Played": false, "IsFavorite": false, "PlaybackPositionTicks": 0},
                })
                _ = c
        }
        sort.Slice(out, func(i, j int) bool { return out[i]["Name"].(string) < out[j]["Name"].(string) })
        a.json(w, 200, M{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0})
}

// moviesRecommendations 电影推荐。
func (a *App) moviesRecommendations(w http.ResponseWriter, r *http.Request) {
        a.json(w, 200, []any{})
}

// searchHints 搜索建议。
func (a *App) searchHints(w http.ResponseWriter, r *http.Request) {
        term := q(r, "SearchTerm")
        if term == "" {
                a.json(w, 200, M{"SearchHints": []any{}, "TotalRecordCount": 0})
                return
        }
        qry := &itemQuery{UserID: "", SearchTerm: term, Limit: qInt(r, "Limit", 20), EnableTotal: false}
        if idn := auth.From(r); idn != nil && idn.User != nil {
                qry.UserID = idn.User.ID
        }
        items, _ := a.queryItems(qry)
        out := []M{}
        for i := range items {
                it := &items[i]
                out = append(out, M{
                        "Id": it.ID, "Name": it.Name, "Type": it.Type,
                        "MediaType": "Video", "ProductionYear": it.Year,
                        "PrimaryImageTag": itemTag(it),
                })
        }
        a.json(w, 200, M{"SearchHints": out, "TotalRecordCount": len(out)})
}

// itemRefresh 触发条目刮削。
func (a *App) itemRefresh(w http.ResponseWriter, r *http.Request, id string) {
        a.adminGuard(w, r, func() {
                go func() {
                        _ = a.scanner.ScrapeNow(strings.ToLower(id))
                        logxScan("条目刷新完成: %s", id)
                }()
                a.noContent(w)
        })
}

var _ = time.Now
