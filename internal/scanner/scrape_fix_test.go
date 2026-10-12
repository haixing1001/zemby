package scanner

import (
	"os"
	"path/filepath"
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

// TestClearItemMetadata 清除元数据：恢复未刮削状态，名称/年份回退文件路径
// （中文目录名优先），媒体源与播放状态保留。
func TestClearItemMetadata(t *testing.T) {
	d := setupDedupeDB(t)
	s := &Scanner{}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "奇迹男孩.Wonder.2017.BluRay.1080p"), 0o755); err != nil {
		t.Fatal(err)
	}
	it := models.Item{ID: "w1", LibraryID: "lib1", Type: "Movie",
		Name: "LEGO DC Super Hero Girls: Galactic Wonder", Year: 2017,
		TmdbID: "442841", TmdbKind: "movie", ProviderIDs: `{"Tmdb":"442841","Imdb":"tt123"}`,
		Overview: "错误条目的简介", People: `[{"Name":"X"}]`, Poster: "/img/wrong.jpg",
		Scraped: true, CommunityRating: 7.1,
		Path: filepath.Join(dir, "奇迹男孩.Wonder.2017.BluRay.1080p", "Wonder.2017.BluRay.1080p.x265.strm"),
		DateCreated: time.Now(), UpdatedAt: time.Now()}
	if err := d.Create(&it).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&models.MediaSource{ID: "src-w1", ItemID: "w1", Path: it.Path, Default: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&models.UserDatum{ID: "u1|w1", UserID: "u1", ItemID: "w1", PositionTicks: 88}).Error; err != nil {
		t.Fatal(err)
	}

	if err := s.ClearItemMetadata("w1"); err != nil {
		t.Fatal(err)
	}
	got := itemByID(t, d, "w1")
	if got.Scraped || got.TmdbID != "" || got.TmdbKind != "" || got.ProviderIDs != "" {
		t.Fatalf("刮削状态与 TMDB 身份应清空: %+v", got)
	}
	if got.Overview != "" || got.People != "" || got.Poster != "" || got.CommunityRating != 0 {
		t.Fatalf("线上元数据应清空: overview=%q people=%q poster=%q", got.Overview, got.People, got.Poster)
	}
	if !strings.Contains(got.Name, "奇迹男孩") || got.Year != 2017 {
		t.Fatalf("名称应回退中文目录名解析: name=%q year=%d", got.Name, got.Year)
	}
	// 媒体源与播放状态保留
	var srcCnt int64
	d.Model(&models.MediaSource{}).Where("item_id = ?", "w1").Count(&srcCnt)
	if srcCnt != 1 {
		t.Fatalf("媒体源不应被清除: %d", srcCnt)
	}
	if ud, ok := datumByItem(d, "u1", "w1"); !ok || ud.PositionTicks != 88 {
		t.Fatalf("播放状态应保留: %+v", ud)
	}
	if !shouldQueueScrape(&got) {
		t.Fatal("清除后的条目应可自动重新刮削")
	}
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
