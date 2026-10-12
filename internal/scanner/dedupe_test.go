package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"go-emby/internal/db"
	"go-emby/internal/models"
)

// setupDedupeDB 为去重测试准备独立的 SQLite 数据库。
func setupDedupeDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := d.AutoMigrate(
		&models.User{}, &models.Library{}, &models.Item{},
		&models.MediaSource{}, &models.MediaStream{}, &models.UserDatum{},
		&models.PlaySession{}, &models.Setting{}, &models.ScanTask{},
		&models.ApiKey{},
	); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}
	db.DB = d
	return d
}

func mkMovie(id, lib, tmdbID, name, poster string, scraped bool, created time.Time) *models.Item {
	return &models.Item{ID: id, LibraryID: lib, Type: "Movie", Name: name,
		TmdbID: tmdbID, TmdbKind: "movie", Scraped: scraped, Poster: poster,
		Path: "/media/" + id + ".mkv", DateCreated: created, UpdatedAt: created}
}

func mkSeries(id, lib, tmdbID, name string, scraped bool) *models.Item {
	return &models.Item{ID: id, LibraryID: lib, Type: "Series", Name: name,
		TmdbID: tmdbID, TmdbKind: "tv", Scraped: scraped,
		Path: "/media/series-" + id, DateCreated: time.Now(), UpdatedAt: time.Now()}
}

func visibleCount(d *gorm.DB, typ string) int64 {
	var n int64
	d.Model(&models.Item{}).Where("type = ? AND duplicate_of = ''", typ).Count(&n)
	return n
}

func itemByID(t *testing.T, d *gorm.DB, id string) models.Item {
	t.Helper()
	var it models.Item
	if err := d.First(&it, "id = ?", id).Error; err != nil {
		t.Fatalf("条目 %s 不存在: %v", id, err)
	}
	return it
}

func datumByItem(d *gorm.DB, userID, itemID string) (models.UserDatum, bool) {
	var ud models.UserDatum
	err := d.First(&ud, "user_id = ? AND item_id = ?", userID, itemID).Error
	return ud, err == nil
}

// TestMigrateUniquenessMergesMovies 验证启动迁移：同库同身份的电影只留一个可见正主，
// 播放状态迁移，且唯一索引阻止新的可见重复。
func TestMigrateUniquenessMergesMovies(t *testing.T) {
	d := setupDedupeDB(t)
	lib := "lib1"
	base := time.Now().Add(-2 * time.Hour)
	// 三部同身份电影：第二部已刮削有海报，另两部没有
	if err := d.Create(mkMovie("m1", lib, "100", "沙丘 A 版", "", false, base)).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(mkMovie("m2", lib, "100", "沙丘 B 版", "/img/m2.jpg", true, base.Add(time.Hour))).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(mkMovie("m3", lib, "100", "沙丘 C 版", "", false, base.Add(2*time.Hour))).Error; err != nil {
		t.Fatal(err)
	}
	// m1 上有用户播放状态
	if err := d.Create(&models.UserDatum{ID: "u1|m1", UserID: "u1", ItemID: "m1", Played: true, PositionTicks: 500}).Error; err != nil {
		t.Fatal(err)
	}
	// 不同库的同身份不受影响
	if err := d.Create(mkMovie("x1", "lib2", "100", "沙雪", "/img/x1.jpg", true, base)).Error; err != nil {
		t.Fatal(err)
	}

	migrateTmdbUniqueness()

	if n := visibleCount(d, "Movie"); n != 2 { // lib1 一个正主 + lib2 一个
		t.Fatalf("可见电影数 = %d, 期望 2", n)
	}
	m2 := itemByID(t, d, "m2")
	if m2.DuplicateOf != "" {
		t.Fatalf("已刮削有海报的 m2 应为正主, duplicate_of=%q", m2.DuplicateOf)
	}
	for _, id := range []string{"m1", "m3"} {
		it := itemByID(t, d, id)
		if it.DuplicateOf != "m2" {
			t.Fatalf("%s 应软隐藏指向 m2, 实际指向 %q", id, it.DuplicateOf)
		}
	}
	// m1 的播放状态迁移到 m2
	if ud, ok := datumByItem(d, "u1", "m2"); !ok || !ud.Played || ud.PositionTicks != 500 {
		t.Fatalf("m1 的播放状态未迁移到 m2: %+v (found=%v)", ud, ok)
	}
	if _, ok := datumByItem(d, "u1", "m1"); ok {
		t.Fatal("m1 的播放状态应已删除")
	}
	// 唯一索引：再插一个可见同身份行必须失败
	err := d.Create(mkMovie("m4", lib, "100", "沙丘 D 版", "", false, time.Now())).Error
	if err == nil {
		t.Fatal("唯一索引未生效：可见重复条目创建成功")
	}
	// 软隐藏行不受索引约束（刮削落库前已确定 duplicate_of，不会以可见形态带新身份落库）
	hidden5 := mkMovie("m5", lib, "100", "沙丘 E 版", "", false, time.Now())
	hidden5.DuplicateOf = "m2"
	if err := d.Create(hidden5).Error; err != nil {
		t.Fatalf("软隐藏行为应不受索引约束: %v", err)
	}
	_ = itemByID(t, d, "m5")
}

// TestDedupeVisibilityMovie 刮削落库前的归并：有正主则并入，身份变化则恢复可见。
func TestDedupeVisibilityMovie(t *testing.T) {
	d := setupDedupeDB(t)
	s := &Scanner{}
	if err := d.Create(mkMovie("c1", "lib1", "200", "正主", "/img/c1.jpg", true, time.Now())).Error; err != nil {
		t.Fatal(err)
	}

	// 同身份新条目 → 并入正主
	dup := mkMovie("d1", "lib1", "200", "副本", "", true, time.Now())
	if merged := s.dedupeVisibility(dup); !merged {
		t.Fatal("应并入正主")
	}
	if err := d.Create(dup).Error; err != nil {
		t.Fatal(err)
	}
	if dup.DuplicateOf != "c1" {
		t.Fatalf("副本应指向 c1, 实际 %q", dup.DuplicateOf)
	}

	// 身份变化（重新刮削匹配到别的片）且原为隐藏 → 恢复可见
	if err := d.Create(&models.UserDatum{ID: "u1|d1", UserID: "u1", ItemID: "d1", Played: true}).Error; err != nil {
		t.Fatal(err)
	}
	dup2 := itemByID(t, d, "d1")
	dup2.TmdbID = "300"
	if merged := s.dedupeVisibility(&dup2); merged {
		t.Fatal("身份变化后不应并入正主")
	}
	if dup2.DuplicateOf != "" {
		t.Fatalf("身份变化后应恢复可见, duplicate_of=%q", dup2.DuplicateOf)
	}
	if err := d.Save(&dup2).Error; err != nil {
		t.Fatal(err)
	}
	// 保存后可见，且播放状态仍在
	if _, ok := datumByItem(d, "u1", "d1"); !ok {
		t.Fatal("播放状态不应丢失")
	}
}

// TestSeriesMergeWithEpisodeMapping 剧集合并：集按 季+集号 映射到正主的集，
// 无对应的集归属正主剧集，季与剧集本身也隐藏。
func TestSeriesMergeWithEpisodeMapping(t *testing.T) {
	d := setupDedupeDB(t)
	c := mkSeries("s1", "lib1", "500", "正主剧集", true)
	if err := d.Create(c).Error; err != nil {
		t.Fatal(err)
	}
	// 正主的第 1 季第 1 集
	if err := d.Create(&models.Item{ID: "c-s1", LibraryID: "lib1", Type: "Season", Name: "第 1 季",
		SeriesID: "s1", ParentIndexNumber: 1, Path: "/m/c/S1", DateCreated: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&models.Item{ID: "c-e1", LibraryID: "lib1", Type: "Episode", Name: "第 1 集",
		SeriesID: "s1", SeasonID: "c-s1", ParentIndexNumber: 1, IndexNumber: 1,
		Path: "/m/c/S1/e1.mkv", DateCreated: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	// 重复剧集：有 S1E1（对应正主的集）和 S1E2（正主缺失）
	dd := mkSeries("s2", "lib1", "500", "重复剧集", true)
	if err := d.Create(dd).Error; err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct{ id string; idx int }{
		{"d-e1", 1}, {"d-e2", 2},
	} {
		if err := d.Create(&models.Item{ID: e.id, LibraryID: "lib1", Type: "Episode",
			Name: "集", SeriesID: "s2", ParentIndexNumber: 1, IndexNumber: e.idx,
			Path: "/m/d/S1/e" + e.id + ".mkv", DateCreated: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 重复剧集第 1 集有播放进度
	if err := d.Create(&models.UserDatum{ID: "u1|d-e1", UserID: "u1", ItemID: "d-e1", PositionTicks: 900}).Error; err != nil {
		t.Fatal(err)
	}

	mergeSeriesDuplicate(dd, c)

	dEp1 := itemByID(t, d, "d-e1")
	if dEp1.DuplicateOf != "c-e1" {
		t.Fatalf("d-e1 应指向正主的 c-e1, 实际 %q", dEp1.DuplicateOf)
	}
	dEp2 := itemByID(t, d, "d-e2")
	if dEp2.DuplicateOf != "s1" {
		t.Fatalf("正主缺失对应集时 d-e2 应归属正主剧集 s1, 实际 %q", dEp2.DuplicateOf)
	}
	dSeries := itemByID(t, d, "s2")
	if dSeries.DuplicateOf != "s1" {
		t.Fatalf("重复剧集应指向 s1, 实际 %q", dSeries.DuplicateOf)
	}
	// 播放进度迁移到正主的集
	if ud, ok := datumByItem(d, "u1", "c-e1"); !ok || ud.PositionTicks != 900 {
		t.Fatalf("d-e1 的播放进度未迁移到 c-e1: %+v (found=%v)", ud, ok)
	}
}

// TestPromoteOnCanonicalDelete 正主删除时从成员晋升，元数据与播放状态随之转移。
func TestPromoteOnCanonicalDelete(t *testing.T) {
	d := setupDedupeDB(t)
	s := &Scanner{}
	if err := d.Create(mkMovie("k1", "lib1", "700", "正主", "/img/k1.jpg", true, time.Now())).Error; err != nil {
		t.Fatal(err)
	}
	dup := mkMovie("k2", "lib1", "700", "副本", "", false, time.Now())
	dup.DuplicateOf = "k1"
	if err := d.Create(dup).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&models.MediaSource{ID: "src-k2", ItemID: "k2", Path: "/media/k2.mkv", Default: true}).Error; err != nil {
		t.Fatal(err)
	}
	// 正主上的播放状态
	if err := d.Create(&models.UserDatum{ID: "u1|k1", UserID: "u1", ItemID: "k1", Played: true, PositionTicks: 777}).Error; err != nil {
		t.Fatal(err)
	}

	s.deleteItemCascade("k1")

	// 正主行已删，副本晋升可见并继承元数据与播放状态
	var gone models.Item
	if err := d.First(&gone, "id = ?", "k1").Error; err == nil {
		t.Fatal("正主行应已删除")
	}
	k2 := itemByID(t, d, "k2")
	if k2.DuplicateOf != "" {
		t.Fatalf("k2 应晋升为可见正主, duplicate_of=%q", k2.DuplicateOf)
	}
	if !k2.Scraped || k2.Name != "正主" || k2.Poster != "/img/k1.jpg" {
		t.Fatalf("晋升成员未继承元数据: name=%q poster=%q scraped=%v", k2.Name, k2.Poster, k2.Scraped)
	}
	if ud, ok := datumByItem(d, "u1", "k2"); !ok || !ud.Played || ud.PositionTicks != 777 {
		t.Fatalf("播放状态未随晋升转移: %+v (found=%v)", ud, ok)
	}
	// 副本的媒体源仍在，播放不受影响
	var cnt int64
	d.Model(&models.MediaSource{}).Where("item_id = ?", "k2").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("晋升成员的媒体源丢失: %d", cnt)
	}
}

// TestGcDanglingReferences 正主被清掉后，悬挂引用重新归并：
// 同剧集下无同位集 → 恢复可见；有同位集 → 挂到该集。
func TestGcDanglingReferences(t *testing.T) {
	d := setupDedupeDB(t)
	c := mkSeries("cs", "lib1", "900", "可见剧集", true)
	if err := d.Create(c).Error; err != nil {
		t.Fatal(err)
	}
	// 可见剧集的 S1E1
	if err := d.Create(&models.Item{ID: "c-e1", LibraryID: "lib1", Type: "Episode", Name: "第 1 集",
		SeriesID: "cs", ParentIndexNumber: 1, IndexNumber: 1, Path: "/m/c/e1.mkv", DateCreated: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	// 一个悬挂的剧集行（正主已删）+ 其两集：S1E1 与可见集同位，S1E2 无对应
	if err := d.Create(&models.Item{ID: "ds", LibraryID: "lib1", Type: "Series", Name: "悬挂剧集",
		TmdbID: "900", TmdbKind: "tv", Path: "/m/d", DuplicateOf: "gone-series", DateCreated: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct{ id string; idx int }{
		{"d-e1", 1}, {"d-e2", 2},
	} {
		if err := d.Create(&models.Item{ID: e.id, LibraryID: "lib1", Type: "Episode", Name: "集",
			SeriesID: "ds", ParentIndexNumber: 1, IndexNumber: e.idx, DuplicateOf: "gone-ep",
			Path: "/m/d/e" + e.id + ".mkv", DateCreated: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}

	n := gcDanglingDuplicates()
	if n != 3 {
		t.Fatalf("应修复 3 条悬挂引用, 实际 %d", n)
	}
	// 悬挂剧集并入可见正主剧集
	ds := itemByID(t, d, "ds")
	if ds.DuplicateOf != "cs" {
		t.Fatalf("悬挂剧集应并入 cs, 实际 %q", ds.DuplicateOf)
	}
	// d-e1 挂到正主剧集的 c-e1；d-e2 归属正主剧集
	if it := itemByID(t, d, "d-e1"); it.DuplicateOf != "c-e1" {
		t.Fatalf("d-e1 应指向 c-e1, 实际 %q", it.DuplicateOf)
	}
	if it := itemByID(t, d, "d-e2"); it.DuplicateOf != "cs" {
		t.Fatalf("d-e2 应归属 cs, 实际 %q", it.DuplicateOf)
	}
}

// TestGCRevivesSeriesWithoutCanonical 正主彻底消失且无替代者：悬挂剧集恢复可见。
func TestGCRevivesSeriesWithoutCanonical(t *testing.T) {
	d := setupDedupeDB(t)
	if err := d.Create(&models.Item{ID: "ls", LibraryID: "lib1", Type: "Series", Name: "孤剧",
		TmdbID: "950", TmdbKind: "tv", Path: "/m/l", DuplicateOf: "dead", DateCreated: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	gcDanglingDuplicates()
	ls := itemByID(t, d, "ls")
	if ls.DuplicateOf != "" {
		t.Fatalf("无正主时悬挂剧集应恢复可见, duplicate_of=%q", ls.DuplicateOf)
	}
}

// TestUpsertMoviePreservesDuplicateOf 扫描器按路径维护条目：软隐藏的重复条目
// 在重新扫描（upsert）后必须保留 duplicate_of，否则会反复重建、去重失效。
func TestUpsertMoviePreservesDuplicateOf(t *testing.T) {
	d := setupDedupeDB(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "Test Movie 2020.mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := models.Library{ID: "lib1", Name: "电影", Path: dir, Type: "movies"}
	if err := d.Create(&lib).Error; err != nil {
		t.Fatal(err)
	}
	s := &Scanner{scrapeQueue: newScrapeTaskQueue()}
	vf := videoFile{path: video, mtime: time.Now().UnixNano(), size: 1}

	if err := s.upsertMovie(&lib, vf); err != nil {
		t.Fatalf("首次入库失败: %v", err)
	}
	var it models.Item
	if err := d.Where("library_id = ? AND type = ?", "lib1", "Movie").First(&it).Error; err != nil {
		t.Fatalf("条目未创建: %v", err)
	}
	// 模拟刮削归并：标记为软隐藏
	if err := d.Model(&models.Item{}).Where("id = ?", it.ID).Update("duplicate_of", "master-1").Error; err != nil {
		t.Fatal(err)
	}

	// 重新扫描（增量/全量走同一 upsert）
	if err := s.upsertMovie(&lib, vf); err != nil {
		t.Fatalf("重扫失败: %v", err)
	}
	it2 := itemByID(t, d, it.ID)
	if it2.DuplicateOf != "master-1" {
		t.Fatalf("重扫后软隐藏状态丢失: duplicate_of=%q", it2.DuplicateOf)
	}
	// 且没有产生第二个条目
	var cnt int64
	d.Model(&models.Item{}).Where("library_id = ? AND type = ?", "lib1", "Movie").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("同路径应只有一个条目, 实际 %d", cnt)
	}
}

// TestShouldQueueScrapeSkipsHidden 软隐藏条目不再进入自动刮削队列。
func TestShouldQueueScrapeSkipsHidden(t *testing.T) {
	setupDedupeDB(t)
	hidden := mkMovie("h1", "lib1", "800", "隐藏", "", false, time.Now())
	hidden.DuplicateOf = "c1"
	if shouldQueueScrape(hidden) {
		t.Fatal("软隐藏条目不应入刮削队列")
	}
	fresh := mkMovie("f1", "lib1", "", "新条目", "", false, time.Now())
	if !shouldQueueScrape(fresh) {
		t.Fatal("未刮削的可见条目应入刮削队列")
	}
}
