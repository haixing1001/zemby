package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-emby/internal/models"
)

// TestScrapeQueueLenAndIdleState 刮削任务状态修复的回归：
// 队列计数（Push/Pop/Drain）与「队列清空后 running 自动归位 idle」。
func TestScrapeQueueLenAndIdleState(t *testing.T) {
	setupDedupeDB(t)
	q := newScrapeTaskQueue()
	if q.Len() != 0 {
		t.Fatalf("初始队列长度应为 0, 实际 %d", q.Len())
	}
	q.Push(scrapeTask{itemID: "a"})
	q.Push(scrapeTask{itemID: "b"})
	q.Push(scrapeTask{itemID: "c"})
	if q.Len() != 3 {
		t.Fatalf("入队后应为 3, 实际 %d", q.Len())
	}
	if got := q.Pop(); got.itemID != "a" {
		t.Fatalf("FIFO 顺序错误: %q", got.itemID)
	}
	if q.Len() != 2 {
		t.Fatalf("取出后应为 2, 实际 %d", q.Len())
	}
	if n := q.Drain(); n != 2 || q.Len() != 0 {
		t.Fatalf("清空应移除 2 并归零, 实际 n=%d len=%d", n, q.Len())
	}

	s := &Scanner{scrapeQueue: q}
	// 队列空 + running → 自动归位 idle
	SetScrapeState("running")
	s.markScrapeIdleIfDone()
	if GetScrapeState() != "idle" {
		t.Fatalf("队列清空后应归位 idle, 实际 %s", GetScrapeState())
	}
	// 队列有任务 + running → 保持 running
	SetScrapeState("running")
	q.Push(scrapeTask{itemID: "x"})
	s.markScrapeIdleIfDone()
	if GetScrapeState() != "running" {
		t.Fatal("队列有任务时不应归位 idle")
	}
	q.Pop()
	// paused 状态不被归位
	SetScrapeState("paused")
	s.markScrapeIdleIfDone()
	if GetScrapeState() != "paused" {
		t.Fatal("paused 状态不应被改动")
	}
	SetScrapeState("idle")
}

// TestScrapeFailedCountExcludesDuplicates 失败计数与失败清单同口径：
// 排除软隐藏的重复条目。
func TestScrapeFailedCountExcludesDuplicates(t *testing.T) {
	d := setupDedupeDB(t)
	mk := func(id, dup string) {
		it := models.Item{ID: id, LibraryID: "lib1", Type: "Movie", Name: id,
			ScrapeError: "x", DuplicateOf: dup, Path: "/m/" + id,
			DateCreated: time.Now(), UpdatedAt: time.Now()}
		if err := d.Create(&it).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("v1", "")
	mk("h1", "v1")
	if got := ScrapeFailedCount(); got != 1 {
		t.Fatalf("失败计数应排除软隐藏条目, 实际 %d", got)
	}
}

// TestNeedsProbeRemoteSwitch 远程流探测开关：开启时 strm 直链参与探测，
// 关闭时跳过。
func TestNeedsProbeRemoteSwitch(t *testing.T) {
	d := setupDedupeDB(t)
	orig := hasFFprobe
	hasFFprobe = true
	defer func() { hasFFprobe = orig }()

	SaveProbeConfig(ProbeConfig{ProbeRemote: true, Concurrency: 2, SaveDir: t.TempDir()})
	mk := func(id, srcPath string) models.Item {
		it := models.Item{ID: id, LibraryID: "lib1", Type: "Movie", Name: id,
			Path: "/m/" + id + ".strm", Mtime: 100, Size: 10,
			DateCreated: time.Now(), UpdatedAt: time.Now()}
		if err := d.Create(&it).Error; err != nil {
			t.Fatal(err)
		}
		if err := d.Create(&models.MediaSource{ID: "src-" + id, ItemID: id, Path: srcPath, Default: true}).Error; err != nil {
			t.Fatal(err)
		}
		return it
	}

	remote := mk("r1", "https://cdn.example.com/r1.mp4")
	s := &Scanner{}
	if !s.needsProbe(&remote) {
		t.Fatal("开启远程探测时 strm 直链应需要探测")
	}

	SaveProbeConfig(ProbeConfig{ProbeRemote: false, Concurrency: 2, SaveDir: t.TempDir()})
	if s.needsProbe(&remote) {
		t.Fatal("关闭远程探测时 strm 直链应跳过")
	}

	// 本地文件不受开关影响
	SaveProbeConfig(ProbeConfig{ProbeRemote: false, Concurrency: 2, SaveDir: t.TempDir()})
	local := models.Item{ID: "l1", LibraryID: "lib1", Type: "Movie", Name: "l1",
		Path: "/m/l1.mkv", Mtime: 100, Size: 10, DateCreated: time.Now(), UpdatedAt: time.Now()}
	if err := d.Create(&local).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&models.MediaSource{ID: "src-l1", ItemID: "l1", Path: "/m/l1.mkv", Default: true}).Error; err != nil {
		t.Fatal(err)
	}
	if !s.needsProbe(&local) {
		t.Fatal("本地文件的探测不受远程开关影响")
	}
}

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
