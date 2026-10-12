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
	got, err := searchWithAIKeywords(context.Background(), "k", "zh-CN", testKeywords, search)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 268467 {
		t.Fatalf("严格过滤应只保留 2013 韩版(ID=268467), 实际: %+v", got)
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

// TestAISearchQueriesChineseFirst 查询顺序：中文名称优先，外文原名随后，组合第三。
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
// 第一个查询（中文）只返回无日期结果（进兜底），第二个查询（外文原名）返回
// 可严格命中的结果，最终应采用严格命中的。
func TestSearchWithAIKeywordsStrictWinsOverFallback(t *testing.T) {
	search := func(ctx context.Context, key, query, lang string, year int) ([]tmdb.SearchResult, error) {
		if strings.Contains(query, "7号房的礼物") && !strings.Contains(query, "Miracle") { // 第一个查询（中文）
			return []tmdb.SearchResult{
				{ID: 100, Title: "7号房的礼物", OriginalTitle: "7번방의 선물", ReleaseDate: ""},
			}, nil
		}
		if strings.Contains(query, "Miracle") { // 后续查询
			return []tmdb.SearchResult{
				{ID: 268467, Title: "7号房的礼物", OriginalTitle: "7번방의 선物",
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
