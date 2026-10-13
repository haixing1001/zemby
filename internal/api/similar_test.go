package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"go-emby/internal/db"
	"go-emby/internal/models"
)

func setupSimilarDB(t *testing.T) *App {
	t.Helper()
	d, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := d.AutoMigrate(
		&models.User{}, &models.Library{}, &models.Item{},
		&models.MediaSource{}, &models.MediaStream{}, &models.UserDatum{},
		&models.Setting{}, &models.ScanTask{},
	); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}
	db.DB = d // scanner.LoadEnhanceConfig 等走全局 DB
	return &App{db: d, serverID: "srv1"}
}

// TestItemSimilarTmdbOnlyAndCastRank 推荐影片：
// 1) 只包含有 TMDB 身份的条目（裸条目没有 /item/movie-xxx 稳定路由）；
// 2) 按共演演员数优先排序。
func TestItemSimilarTmdbOnlyAndCastRank(t *testing.T) {
	app := setupSimilarDB(t)
	d := app.db

	mk := func(id string, tmdbID, people string) models.Item {
		return models.Item{ID: id, LibraryID: "lib1", Type: "Movie", Name: id,
			TmdbID: tmdbID, TmdbKind: "movie", People: people,
			Path: "/m/" + id + ".mkv", CommunityRating: 5.0,
			DateCreated: time.Now(), UpdatedAt: time.Now()}
	}
	// 详情条目：两位主演
	base := mk("base", "100", `[{"Name":"周星驰","Role":"主演"},{"Name":"吴孟达","Role":"配角"}]`)
	base.ID = "base"
	if err := d.Create(&base).Error; err != nil {
		t.Fatal(err)
	}
	// c1：共演 2 人，有 TMDB
	c1 := mk("c1", "201", `[{"Name":"周星驰","Role":"主演"},{"Name":"吴孟达","Role":"配角"},{"Name":"徐锦江","Role":"配角"}]`)
	if err := d.Create(&c1).Error; err != nil {
		t.Fatal(err)
	}
	// c2：共演 1 人，有 TMDB
	c2 := mk("c2", "202", `[{"Name":"吴孟达","Role":"主演"},{"Name":"梁朝伟","Role":"主演"}]`)
	if err := d.Create(&c2).Error; err != nil {
		t.Fatal(err)
	}
	// c3：共演 2 人但未刮削（无 TMDB）——不应出现
	c3 := mk("c3", "", `[{"Name":"周星驰","Role":"主演"},{"Name":"吴孟达","Role":"配角"}]`)
	if err := d.Create(&c3).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/Items/base/Similar?Limit=8", nil)
	w := httptest.NewRecorder()
	app.itemSimilar(w, req, "base")

	var resp struct {
		Items []struct {
			Id      string `json:"Id"`
			RouteId string `json:"RouteId"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v %s", err, w.Body.String())
	}
	if len(resp.Items) == 0 {
		t.Fatal("应有推荐结果")
	}
	// 未刮削条目（c3）不得出现
	for _, it := range resp.Items {
		if it.Id == "c3" {
			t.Fatal("未刮削条目不应出现在推荐列表")
		}
		if it.RouteId == "" || it.RouteId == it.Id {
			t.Fatalf("推荐条目 RouteId 应为 TMDB 形式, 实际 %q (id=%s)", it.RouteId, it.Id)
		}
	}
	// 共演 2 人的 c1 应排在共演 1 人的 c2 之前
	if len(resp.Items) < 2 || resp.Items[0].Id != "c1" || resp.Items[1].Id != "c2" {
		t.Fatalf("共演多的应排前, 实际顺序: %+v", resp.Items)
	}
}
