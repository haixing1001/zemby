package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-emby/internal/models"
)

// TestLooksLikeSample 样本/预告识别：独立分词命中才跳过，
// 多词标题的首词（如《Trailer Park Boys》）不误杀。
func TestLooksLikeSample(t *testing.T) {
	cases := []struct {
		name   string
		sample bool
	}{
		{"Movie.2023.1080p.BluRay-sample", true},
		{"Movie 2023 TRAILER", true},
		{"sample", true},
		{"Trailer", true},
		{"Big Movie [sample]", true},
		{"Trailer Park Boys The Movie 2009", false}, // 首词是标题一部分
		{"The Sampler 2016", false},                 // 分词不等
		{"ASampleMovie 2020", false},                 // 连写不构成独立分词
		{"电锯惊魂 Saw 2004", false},
	}
	for _, c := range cases {
		if got := looksLikeSample(c.name); got != c.sample {
			t.Errorf("looksLikeSample(%q) = %v, 期望 %v", c.name, got, c.sample)
		}
	}
}

// TestParseNameEpisodePatterns 电影库跳过依据：集号模式解析必须可靠。
func TestParseNameEpisodePatterns(t *testing.T) {
	cases := []struct {
		name    string
		episode int
	}{
		{"Show S01E02 1080p BluRay", 2},
		{"剧集 第1季第3集", 3},
		{"某剧 第2集", 2},
		{"2001太空漫游", 0},
		{"S.W.A.T.2017", 0},
		{"Se7en 1995", 0},
		{"第9节课 2022", 0},
	}
	for _, c := range cases {
		if got := ParseName(c.name).Episode; got != c.episode {
			t.Errorf("ParseName(%q).Episode = %d, 期望 %d", c.name, got, c.episode)
		}
	}
}

// TestMovieLibrarySkipsEpisodeAndSampleFiles 电影库扫描：剧集与样本文件跳过，
// 正常电影照常入库。
func TestMovieLibrarySkipsEpisodeAndSampleFiles(t *testing.T) {
	d := setupDedupeDB(t)
	dir := t.TempDir()
	for _, name := range []string{
		"Show S01E01.mkv",
		"Show S01E02.mkv",
		"剧集 第1季第5集.mkv",
		"Big Movie 2020-sample.mkv",
		"Movie 2019.mkv",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := models.Library{ID: "lib1", Name: "电影", Path: dir, Type: "movies"}
	if err := d.Create(&lib).Error; err != nil {
		t.Fatal(err)
	}
	s := &Scanner{scrapeQueue: newScrapeTaskQueue()}
	cnt := &struct{ files, newItems, updated, skipped int }{}
	if err := s.scanMoviesDir(&lib, dir, true, seenPaths{}, nil, cnt); err != nil {
		t.Fatal(err)
	}
	var items []models.Item
	if err := d.Where("library_id = ? AND type = ?", "lib1", "Movie").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("应只入库 1 部电影, 实际 %d: %+v", len(items), items)
	}
	if items[0].Name != "Movie" || items[0].Year != 2019 {
		t.Fatalf("入库电影错误: name=%q year=%d", items[0].Name, items[0].Year)
	}
	if cnt.skipped != 4 {
		t.Fatalf("应跳过 4 个文件, 实际 %d", cnt.skipped)
	}
}

// TestMovieLibraryCleansExistingMisclassifiedItem 历史上误建为电影的剧集条目，
// 重扫时自动清理且不再重建。
func TestMovieLibraryCleansExistingMisclassifiedItem(t *testing.T) {
	d := setupDedupeDB(t)
	dir := t.TempDir()
	epPath := filepath.Join(dir, "Old Show S01E01.mkv")
	if err := os.WriteFile(epPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := models.Library{ID: "lib1", Name: "电影", Path: dir, Type: "movies"}
	if err := d.Create(&lib).Error; err != nil {
		t.Fatal(err)
	}
	// 预置误建条目（模拟旧版本把剧集文件当电影入库）
	stale := models.Item{ID: "stale1", LibraryID: "lib1", Type: "Movie",
		Name: "Old Show", Path: epPath, TmdbID: "777", TmdbKind: "movie",
		Scraped: true, DateCreated: time.Now(), UpdatedAt: time.Now()}
	if err := d.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&models.MediaSource{ID: "src-stale", ItemID: "stale1", Path: epPath, Default: true}).Error; err != nil {
		t.Fatal(err)
	}

	s := &Scanner{scrapeQueue: newScrapeTaskQueue()}
	for pass := 0; pass < 2; pass++ { // 扫两遍：第二遍验证不重建
		if err := s.scanMoviesDir(&lib, dir, true, seenPaths{}, nil,
			&struct{ files, newItems, updated, skipped int }{}); err != nil {
			t.Fatal(err)
		}
	}
	var cnt int64
	d.Model(&models.Item{}).Where("library_id = ? AND type = ?", "lib1", "Movie").Count(&cnt)
	if cnt != 0 {
		t.Fatalf("误建条目应被清理且不重建, 残留 %d", cnt)
	}
	var srcCnt int64
	d.Model(&models.MediaSource{}).Where("item_id = ?", "stale1").Count(&srcCnt)
	if srcCnt != 0 {
		t.Fatalf("误建条目的媒体源应级联清理, 残留 %d", srcCnt)
	}
}

// TestTVLibraryStillIngestsEpisodes 剧集库行为不受影响：SxxExx 文件正常入库。
func TestTVLibraryStillIngestsEpisodes(t *testing.T) {
	d := setupDedupeDB(t)
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Some Show")
	seasonDir := filepath.Join(seriesDir, "Season 1")
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ep := filepath.Join(seasonDir, "Some Show S01E01.mkv")
	if err := os.WriteFile(ep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := models.Library{ID: "tlib", Name: "剧集", Path: root, Type: "tvshows"}
	if err := d.Create(&lib).Error; err != nil {
		t.Fatal(err)
	}
	s := &Scanner{scrapeQueue: newScrapeTaskQueue()}
	if err := s.scanTVDir(&lib, root, true, seenPaths{}, nil,
		&struct{ files, newItems, updated, skipped int }{}); err != nil {
		t.Fatal(err)
	}
	var eps int64
	d.Model(&models.Item{}).Where("library_id = ? AND type = ?", "tlib", "Episode").Count(&eps)
	if eps != 1 {
		t.Fatalf("剧集库应正常入库 1 集, 实际 %d", eps)
	}
}
