package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go-emby/internal/models"
)

// TestFanrenXiuxianzhuanIngestion 复现用户场景：剧集库
// 「我的视频/剧集/凡人修仙传 (2020)/Season 1/」下 6 个
// S01E001-S01E006 的 .strm 文件，验证剧集/季/集是否正常入库。
func TestFanrenXiuxianzhuanIngestion(t *testing.T) {
	d := setupDedupeDB(t)
	root := t.TempDir() // 模拟库根：…/我的视频/剧集
	seasonDir := filepath.Join(root, "凡人修仙传 (2020)", "Season 1")
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for ep := 1; ep <= 6; ep++ {
		name := fmt.Sprintf("凡人修仙传.S01E%03d.2020.2160p.WEB-DL.H264.AAC.strm", ep)
		p := filepath.Join(seasonDir, name)
		if err := os.WriteFile(p, []byte("https://example.com/fanren/"+fmt.Sprint(ep)+".mp4"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := models.Library{ID: "tvlib", Name: "剧集", Path: root, Type: "tvshows"}
	if err := d.Create(&lib).Error; err != nil {
		t.Fatal(err)
	}

	s := &Scanner{scrapeQueue: newScrapeTaskQueue()}
	if err := s.scanTVDir(&lib, root, true, seenPaths{}, nil,
		&struct{ files, newItems, updated, skipped int }{}); err != nil {
		t.Fatal(err)
	}

	var series []models.Item
	d.Where("library_id = ? AND type = ?", "tvlib", "Series").Find(&series)
	if len(series) != 1 {
		t.Fatalf("应入库 1 部剧集, 实际 %d: %+v", len(series), series)
	}
	sr := series[0]
	if sr.Name != "凡人修仙传" {
		t.Fatalf("剧集名错误: %q", sr.Name)
	}
	var seasons []models.Item
	d.Where("series_id = ? AND type = ?", sr.ID, "Season").Find(&seasons)
	if len(seasons) != 1 {
		t.Fatalf("应入库 1 个季, 实际 %d", len(seasons))
	}
	var eps []models.Item
	d.Where("series_id = ? AND type = ? AND duplicate_of = ''", sr.ID, "Episode").Order("index_number").Find(&eps)
	if len(eps) != 6 {
		t.Fatalf("应入库 6 集, 实际 %d: %+v", len(eps), eps)
	}
	for i, e := range eps {
		if e.IndexNumber != i+1 || e.ParentIndexNumber != 1 {
			t.Fatalf("第 %d 集编号错误: S%dE%d", i+1, e.ParentIndexNumber, e.IndexNumber)
		}
		var srcs []models.MediaSource
		d.Where("item_id = ?", e.ID).Find(&srcs)
		if len(srcs) != 1 || srcs[0].Path == "" {
			t.Fatalf("第 %d 集媒体源缺失: %+v", i+1, srcs)
		}
	}
}
