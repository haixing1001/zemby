package scanner

import (
	"strings"
	"testing"
	"time"

	"go-emby/internal/models"
)

// TestClearFailedTmdbIdentity 刮削失败清除 TMDB 身份：Tmdb 键移除、
// Imdb 等其余键保留；只剩 Tmdb 键时 provider_ids 整体清空。
func TestClearFailedTmdbIdentity(t *testing.T) {
	d := setupDedupeDB(t)
	it := models.Item{ID: "c1", LibraryID: "lib1", Type: "Movie", Name: "失落神龙传说",
		TmdbID: "999", TmdbKind: "movie", ProviderIDs: `{"Tmdb":"999","Imdb":"tt123"}`,
		Scraped: true, Path: "/m/x.mkv", DateCreated: time.Now(), UpdatedAt: time.Now()}
	if err := d.Create(&it).Error; err != nil {
		t.Fatal(err)
	}

	clearFailedTmdbIdentity("c1")

	got := itemByID(t, d, "c1")
	if got.TmdbID != "" || got.TmdbKind != "" {
		t.Fatalf("tmdb_id/tmdb_kind 应清空, 实际 %q/%q", got.TmdbID, got.TmdbKind)
	}
	if !strings.Contains(got.ProviderIDs, "tt123") || strings.Contains(got.ProviderIDs, "999") {
		t.Fatalf("ProviderIDs 应保留 Imdb 删除 Tmdb, 实际 %q", got.ProviderIDs)
	}

	// 只剩 Tmdb 键时整体清空
	if err := d.Model(&models.Item{}).Where("id = ?", "c1").
		Updates(map[string]any{"tmdb_id": "7", "tmdb_kind": "movie", "provider_ids": `{"Tmdb":"7"}`}).Error; err != nil {
		t.Fatal(err)
	}
	clearFailedTmdbIdentity("c1")
	got = itemByID(t, d, "c1")
	if got.TmdbID != "" || got.ProviderIDs != "" {
		t.Fatalf("仅 Tmdb 键时应整体清空, 实际 tmdb_id=%q provider_ids=%q", got.TmdbID, got.ProviderIDs)
	}

	// 不存在的条目：no-op 不报错
	clearFailedTmdbIdentity("missing-item")
}

// TestRescrapeTitleFallsBackToFileName 复现《龙的传人》场景：条目名已被上次
// 误刮覆盖为《失落神龙传说》，自动重刮时应回退文件路径解析的原始标题与年份。
func TestRescrapeTitleFallsBackToFileName(t *testing.T) {
	it := &models.Item{Type: "Movie", Scraped: true,
		Name: "失落神龙传说", Year: 2005,
		Path: "/media/龍的傳人.Legend.of.the.Dragon.1990.BluRay.1080p.x265.mkv"}
	name, year := rescrapeTitle(it)
	if year != 1990 {
		t.Fatalf("年份应回退为文件名的 1990, 实际 %d", year)
	}
	if !strings.Contains(name, "龍的傳人") {
		t.Fatalf("标题应回退为文件名解析结果（含原始名）, 实际 %q", name)
	}
}

// TestRescrapeTitleKeepsFreshName 未刮削过的条目（名称来自文件名/NFO）不回退；
// 无路径时保持原样；nil 安全。
func TestRescrapeTitleKeepsFreshName(t *testing.T) {
	fresh := &models.Item{Type: "Movie", Scraped: false, Name: "NFO 提供的标题", Year: 2020, Path: "/m/别的名字 2020.mkv"}
	if name, _ := rescrapeTitle(fresh); name != "NFO 提供的标题" {
		t.Fatalf("未刮削条目不应回退, 实际 %q", name)
	}
	noPath := &models.Item{Type: "Movie", Scraped: true, Name: "原名", Year: 2020, Path: ""}
	if name, _ := rescrapeTitle(noPath); name != "原名" {
		t.Fatalf("无路径时保持原名, 实际 %q", name)
	}
	if name, _ := rescrapeTitle(nil); name != "" {
		t.Fatalf("nil 应返回空, 实际 %q", name)
	}
}
