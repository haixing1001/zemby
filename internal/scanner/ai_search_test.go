package scanner

import (
	"context"
	"strings"
	"testing"

	"go-emby/internal/tmdb"
)

// 用户场景的关键词：豆瓣 Top250 文件名被 Top212 前缀污染，AI 提取出干净关键词。
var testKeywords = &AIKeywords{
	Title:         "7号房的礼物",
	OriginalTitle: "Miracle in Cell No 7",
	Year:          2013,
	Type:          "电影",
}

// TestSearchWithAIKeywordsMissingReleaseDateFallback 复现线上失败场景：
// TMDB 返回了正确条目但 release_date 缺失（镜像数据不全或字段为空），
// 严格过滤（年份精确相等）将其全部丢弃，导致回退到被污染的原文件名搜索。
// 修复后应按标题相似度兜底保留该结果。
func TestSearchWithAIKeywordsMissingReleaseDateFallback(t *testing.T) {
	search := func(ctx context.Context, key, query, lang string, year int) ([]tmdb.SearchResult, error) {
		if strings.Contains(query, "7号房的礼物") || strings.Contains(query, "Miracle") {
			// release_date 为空：严格过滤必杀
			return []tmdb.SearchResult{
				{ID: 268467, Title: "7号房的礼物", OriginalTitle: "7번방의 선물",
					ReleaseDate: "", PosterPath: "/a.jpg", VoteAverage: 8.2},
			}, nil
		}
		return nil, nil
	}
	got, err := searchWithAIKeywords(context.Background(), "k", "zh-CN", testKeywords, search)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("严格过滤无命中时应按标题相似度兜底保留结果，实际返回空")
	}
	if got[0].ID != 268467 {
		t.Fatalf("兜底结果错误: %+v", got[0])
	}
}

// TestSearchWithAIKeywordsStrictMatchStillWins 严格过滤命中时不受兜底干扰：
// 带正确日期的结果应直接严格命中，而不是走兜底路径。
func TestSearchWithAIKeywordsStrictMatchStillWins(t *testing.T) {
	search := func(ctx context.Context, key, query, lang string, year int) ([]tmdb.SearchResult, error) {
		return []tmdb.SearchResult{
			{ID: 268467, Title: "7号房的礼物", OriginalTitle: "7번방의 선물",
				ReleaseDate: "2013-01-23", PosterPath: "/a.jpg", VoteAverage: 8.2},
			{ID: 999999, Title: "7号房的礼物", OriginalTitle: "Miracle in Cell No. 7",
				ReleaseDate: "2019-12-05", PosterPath: "/b.jpg", VoteAverage: 7.0}, // 2019 翻拍
		}, nil
	}
	// 严格过滤分级后可保留多个命中（如原版 L2 与同名翻拍 L3），
	// 韩版（中文名+年份，L2）应排首位，翻拍（中英双名无年份，L3）随后。
	got, err := searchWithAIKeywords(context.Background(), "k", "zh-CN", testKeywords, search)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != 268467 {
		t.Fatalf("严格过滤后韩版应排首位, 实际: %+v", got)
	}
}

// TestBestEffortPrefersClosestYear 兜底排序：标题同分时，
// 发行日期与期望年份最接近的优先（防同名翻拍误配）。
func TestBestEffortPrefersClosestYear(t *testing.T) {
	results := []tmdb.SearchResult{
		{ID: 555001, Title: "7号房的礼物", OriginalTitle: "Miracle in Cell No. 7",
			ReleaseDate: "2019-12-05", VoteAverage: 7.0}, // 年份差 6
		{ID: 268467, Title: "7号房的礼物", OriginalTitle: "7번방의 선물",
			ReleaseDate: "2013-01-23", VoteAverage: 8.2}, // 年份差 0
	}
	got := bestEffortAIResults(results, testKeywords)
	if len(got) < 2 {
		t.Fatalf("兜底应保留 2 条, 实际 %d", len(got))
	}
	if got[0].ID != 268467 {
		t.Fatalf("年份更接近的 2013 版应排首位, 实际首位 ID=%d", got[0].ID)
	}
}

// TestBestEffortDatedBeatsUndated 兜底排序：无日期结果排在有日期结果之后。
func TestBestEffortDatedBeatsUndated(t *testing.T) {
	results := []tmdb.SearchResult{
		{ID: 1, Title: "7号房的礼物", OriginalTitle: "x", ReleaseDate: ""},
		{ID: 2, Title: "7号房的礼物", OriginalTitle: "y", ReleaseDate: "2013-01-23"},
	}
	got := bestEffortAIResults(results, testKeywords)
	if len(got) != 2 || got[0].ID != 2 {
		t.Fatalf("有日期的应排首位, 实际: %+v", got)
	}
}

// TestBestEffortRequiresTitleScore 标题相似度不足的结果兜底也不保留。
func TestBestEffortRequiresTitleScore(t *testing.T) {
	results := []tmdb.SearchResult{
		{ID: 3, Title: "完全不相关的电影", OriginalTitle: "Unrelated", ReleaseDate: "2013-01-01"},
	}
	if got := bestEffortAIResults(results, testKeywords); len(got) != 0 {
		t.Fatalf("标题不相关的结果不应兜底保留, 实际: %+v", got)
	}
}

// TestAITypeIsMovie 类型映射：纪录片/短片在 TMDB 属于电影；电视剧、综艺、
// 动画与空值走 TV 搜索（用户场景：《二十二》纪录片电影被误判走 TV 而失败）。
func TestAITypeIsMovie(t *testing.T) {
	cases := []struct {
		typ  string
		want bool
	}{
		{"电影", true},
		{"纪录片", true},
		{"纪录片电影", true},
		{"纪录", true},
		{"短片", true},
		{"动画电影", true},
		{"电视剧", false},
		{"综艺", false},
		{"动画", false},
		{"", false},
	}
	for _, c := range cases {
		if got := aiTypeIsMovie(&AIKeywords{Title: "x", Type: c.typ}); got != c.want {
			t.Errorf("aiTypeIsMovie(Type=%q) = %v, 期望 %v", c.typ, got, c.want)
		}
	}
	if aiTypeIsMovie(nil) {
		t.Error("nil 关键词应按 TV 处理")
	}
}

// TestAINonMovieSearchTVHit TV 命中：直接返回 TV 的 TMDB ID，不触发降级。
func TestAINonMovieSearchTVHit(t *testing.T) {
	s := &Scanner{}
	set := tmdb.Settings{APIKey: "k", Language: "zh-CN"}
	kw := &AIKeywords{Title: "某剧", Year: 2020, Type: "综艺"}
	tv := []tmdb.SearchResult{{ID: 555, Name: "某剧", FirstAirDate: "2020-01-01"}}
	tvID, movieRes, err := s.aiNonMovieSearch(context.Background(), set, kw, tv, 2020, tmdb.SearchMovie)
	if err != nil {
		t.Fatal(err)
	}
	if tvID != "555" || movieRes != nil {
		t.Fatalf("TV 命中应返回 tvID=555, 实际 tvID=%q movieRes=%+v", tvID, movieRes)
	}
}

// TestAINonMovieSearchFallsBackToMovie TV 无命中时降级电影搜索：
// 纪录片电影被 AI 判为非电影类型的兜底路径。
func TestAINonMovieSearchFallsBackToMovie(t *testing.T) {
	s := &Scanner{}
	set := tmdb.Settings{APIKey: "k", Language: "zh-CN"}
	kw := &AIKeywords{Title: "二十二", OriginalTitle: "Twenty Two", Year: 2015, Type: "综艺"}
	movieSearch := func(ctx context.Context, key, query, lang string, year int) ([]tmdb.SearchResult, error) {
		return []tmdb.SearchResult{
			{ID: 268, Title: "二十二", OriginalTitle: "Twenty Two", ReleaseDate: "2015-05-22", PosterPath: "/a.jpg", VoteAverage: 8.9},
		}, nil
	}
	tvID, movieRes, err := s.aiNonMovieSearch(context.Background(), set, kw, nil, 2015, movieSearch)
	if err != nil {
		t.Fatal(err)
	}
	if tvID != "" || len(movieRes) != 1 || movieRes[0].ID != 268 {
		t.Fatalf("TV 无命中应降级电影搜索并返回结果, 实际 tvID=%q movieRes=%+v", tvID, movieRes)
	}
}

// TestAINonMovieSearchAllMiss TV 与电影都无命中：报错信息包含两类搜索。
func TestAINonMovieSearchAllMiss(t *testing.T) {
	s := &Scanner{}
	set := tmdb.Settings{APIKey: "k", Language: "zh-CN"}
	kw := &AIKeywords{Title: "不存在的作品", Year: 2050, Type: "综艺"}
	movieSearch := func(ctx context.Context, key, query, lang string, year int) ([]tmdb.SearchResult, error) {
		return nil, nil
	}
	_, _, err := s.aiNonMovieSearch(context.Background(), set, kw, nil, 2050, movieSearch)
	if err == nil || !strings.Contains(err.Error(), "TV 与电影搜索均无匹配") {
		t.Fatalf("两类搜索都无命中应报错, 实际: %v", err)
	}
}

// TestAIRresultTitleScoreFieldAligned 标题相似度按字段严格对齐：
// 中文名 ↔ title，外文原名 ↔ original_title；跨字段重合不再计分。
func TestAIRresultTitleScoreFieldAligned(t *testing.T) {
	// 中文名对齐：AI 中文标题与 TMDB title 完全一致 → 100
	kw := &AIKeywords{Title: "二十二", OriginalTitle: "Twenty Two"}
	res := tmdb.SearchResult{Title: "二十二", OriginalTitle: "Twenty Two Days"}
	if got := aiResultTitleScore(res, kw); got != 100 {
		t.Fatalf("中文字段对齐应得 100, 实际 %v", got)
	}

	// 原名对齐：TMDB title 不是中文名（无翻译回退原名）时由原名侧命中
	kw2 := &AIKeywords{Title: "二十二", OriginalTitle: "Twenty Two"}
	res2 := tmdb.SearchResult{Title: "完全无关", OriginalTitle: "Twenty Two"}
	if got := aiResultTitleScore(res2, kw2); got != 100 {
		t.Fatalf("原名字段对齐应得 100, 实际 %v", got)
	}

	// 跨字段不得误配：AI 只填了外文名，TMDB 原名与之 token 重合也不计分
	// （旧实现会拿 Title 与 original_title 交叉比较产生 70 分误配）
	kw3 := &AIKeywords{Title: "Twenty Two"}
	res3 := tmdb.SearchResult{Title: "二十二", OriginalTitle: "Twenty Two Days"}
	if got := aiResultTitleScore(res3, kw3); got != 0 {
		t.Fatalf("跨字段重合不应计分, 实际 %v", got)
	}

	// TV 结果用 name/original_name 对齐
	kw4 := &AIKeywords{Title: "某剧", OriginalTitle: "Some Show"}
	res4 := tmdb.SearchResult{Name: "某剧", OriginalName: "Some Show"}
	if got := aiResultTitleScore(res4, kw4); got != 100 {
		t.Fatalf("TV 字段对齐应得 100, 实际 %v", got)
	}
}

// TestTmdbMatchLevel 命中规则分级：
// 中文名+英文名+年份 > 中文名+年份 > 中文名+英文名 > 中文名 > 英文名+年份 > 英文名。
func TestTmdbMatchLevel(t *testing.T) {
	cn, en := classifyRefs("告白 Confessions", "Twenty Two")
	if len(cn) != 1 || cn[0] != "告白" || len(en) != 2 {
		t.Fatalf("混合参考名拆分错误: cn=%v en=%v", cn, en)
	}
	// 中文名+英文名+年份全命中
	full := tmdb.SearchResult{Title: "告白", OriginalTitle: "Twenty Two", ReleaseDate: "2010-06-05"}
	if lv := tmdbMatchLevel(full, cn, en, 2010); lv != matchCNENYear {
		t.Fatalf("全命中应为 L1, 实际 %d", lv)
	}
	// 中文名+年份（原名是外文非英文）
	cnYear := tmdb.SearchResult{Title: "告白", OriginalTitle: "告白", ReleaseDate: "2010-06-05"}
	if lv := tmdbMatchLevel(cnYear, cn, en, 2010); lv != matchCNYear {
		t.Fatalf("中文+年份应为 L2, 实际 %d", lv)
	}
	// 同名英语翻拍：中英双名匹配但年份不符 → 不得压过年份正确的原版（L3）
	remake := tmdb.SearchResult{Title: "告白", OriginalTitle: "Twenty Two", ReleaseDate: "2020-01-01"}
	if lv := tmdbMatchLevel(remake, cn, en, 2010); lv != matchCNEN {
		t.Fatalf("双名无年份应为 L3, 实际 %d", lv)
	}
	if tmdbMatchLevel(remake, cn, en, 2010) <= tmdbMatchLevel(cnYear, cn, en, 2010) {
		t.Fatal("年份正确的原版(L2)应优先于双名无年份的翻拍(L3)")
	}
	// 仅中文名
	cnOnly := tmdb.SearchResult{Title: "告白", OriginalTitle: "別的作品", ReleaseDate: ""}
	if lv := tmdbMatchLevel(cnOnly, cn, en, 2010); lv != matchCNOnly {
		t.Fatalf("仅中文应为 L4, 实际 %d", lv)
	}
	// 英文名+年份 / 仅英文名
	enYear := tmdb.SearchResult{Title: "某某别的", OriginalTitle: "Twenty Two", ReleaseDate: "2010-06-05"}
	if lv := tmdbMatchLevel(enYear, cn, en, 2010); lv != matchENYear {
		t.Fatalf("英文+年份应为 L5, 实际 %d", lv)
	}
	enOnly := tmdb.SearchResult{Title: "某某别的", OriginalTitle: "Twenty Two", ReleaseDate: "1999-01-01"}
	if lv := tmdbMatchLevel(enOnly, cn, en, 2010); lv != matchENOnly {
		t.Fatalf("仅英文应为 L6, 实际 %d", lv)
	}
	// 无任何匹配
	none := tmdb.SearchResult{Title: "别的电影", OriginalTitle: "Unrelated", ReleaseDate: "2010-01-01"}
	if lv := tmdbMatchLevel(none, cn, en, 2010); lv != matchNone {
		t.Fatalf("无匹配应为 0, 实际 %d", lv)
	}
}

// TestYearMatchesTolerance 年份匹配允许 ±1 年误差（地区发行/首映跨年）。
func TestYearMatchesTolerance(t *testing.T) {
	if !yearMatches(1989, 1990) || !yearMatches(1990, 1990) || !yearMatches(1991, 1990) {
		t.Fatal("1989/1990/1991 都应视为 1990 的年份匹配")
	}
	if yearMatches(1988, 1990) || yearMatches(1992, 1990) {
		t.Fatal("误差超过 ±1 年不应匹配")
	}
	if yearMatches(1990, 0) {
		t.Fatal("无期望年份时不应匹配")
	}
	// 命中等级：中文名+1989 条目对搜索 1990 应算 L2（中文名+年份）
	cn, en := classifyRefs("无间道 Infernal Affairs")
	early := tmdb.SearchResult{Title: "无间道", OriginalTitle: "無間道", ReleaseDate: "1989-12-30"}
	if lv := tmdbMatchLevel(early, cn, en, 1990); lv != matchCNYear {
		t.Fatalf("±1 年内应为 L2(中文名+年份), 实际 %d", lv)
	}
}

// TestBestMovieResultMatchLevels 择优按命中等级：正确条目（中文名+年份）
// 应胜过排在前面的无关高评分条目。
func TestBestMovieResultMatchLevels(t *testing.T) {
	results := []tmdb.SearchResult{
		{ID: 900, Title: "LEGO DC Super Hero Girls: Galactic Wonder", OriginalTitle: "LEGO DC Super Hero Girls: Galactic Wonder", ReleaseDate: "2017-01-01", PosterPath: "/x.jpg", VoteAverage: 9.9},
		{ID: 420803, Title: "奇迹男孩", OriginalTitle: "Wonder", ReleaseDate: "2017-11-17", PosterPath: "/w.jpg", VoteAverage: 8.0},
	}
	best := bestMovieResult(results, 2017, "奇迹男孩 Wonder")
	if best.ID != 420803 {
		t.Fatalf("命中规则应选出《奇迹男孩》, 实际 ID=%d (%s)", best.ID, best.Title)
	}
}

// TestParseManualTmdbRef 手动 TMDB ID 指定的解析规则。
func TestParseManualTmdbRef(t *testing.T) {
	cases := []struct {
		in   string
		id   int
		kind string
		ok   bool
	}{
		{"tmdb:671", 671, "", true},
		{"TMDB:671", 671, "", true},
		{" tmdb-id:12345 ", 12345, "", true},
		{"movie:671", 671, "movie", true},
		{"tv:456", 456, "tv", true},
		{"1917", 0, "", false},             // 纯数字不识别，防《1917》片名误判
		{"tmdb:abc", 0, "", false},         // 非数字
		{"tmdb:0", 0, "", false},           // 非正数
		{"tmdb:123456789012", 0, "", false}, // 超长
	}
	for _, c := range cases {
		id, kind, ok := parseManualTmdbRef(c.in)
		if ok != c.ok || (c.ok && (id != c.id || kind != c.kind)) {
			t.Errorf("parseManualTmdbRef(%q) = (%d,%q,%v), 期望 (%d,%q,%v)", c.in, id, kind, ok, c.id, c.kind, c.ok)
		}
	}
}

func TestAISearchQueriesChineseFirst(t *testing.T) {
	q := aiSearchQueries(testKeywords)
	want := []string{"7号房的礼物", "Miracle in Cell No 7", "7号房的礼物 (Miracle in Cell No 7)"}
	if len(q) != len(want) {
		t.Fatalf("查询数 = %d (%+v), 期望 %d", len(q), q, len(want))
	}
	for i := range want {
		if q[i] != want[i] {
			t.Fatalf("查询[%d] = %q, 期望 %q（中文优先）: %+v", i, q[i], want[i], q)
		}
	}
}

// TestSearchWithAIKeywordsStrictWinsOverFallback 多查询时严格命中优先：
// 第一个查询（中文）只返回低相似结果（进兜底），第二个查询（外文原名）返回
// 可严格命中的结果，最终应采用严格命中的。
func TestSearchWithAIKeywordsStrictWinsOverFallback(t *testing.T) {
	search := func(ctx context.Context, key, query, lang string, year int) ([]tmdb.SearchResult, error) {
		if strings.Contains(query, "7号房的礼物") && !strings.Contains(query, "Miracle") { // 第一个查询（中文）
			return []tmdb.SearchResult{
				{ID: 100, Title: "某个无关的其它电影", OriginalTitle: "Something Else", ReleaseDate: "2010-01-01"},
			}, nil
		}
		if strings.Contains(query, "Miracle") { // 后续查询
			return []tmdb.SearchResult{
				{ID: 268467, Title: "7号房的礼物", OriginalTitle: "7번방의 선물",
					ReleaseDate: "2013-01-23", PosterPath: "/a.jpg", VoteAverage: 8.2},
			}, nil
		}
		return nil, nil
	}
	got, err := searchWithAIKeywords(context.Background(), "k", "zh-CN", testKeywords, search)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 268467 {
		t.Fatalf("严格命中应优先于兜底结果, 实际: %+v", got)
	}
}
