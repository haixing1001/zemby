// Package tmdb TMDB 元数据刮削客户端。
package tmdb

import (
        "context"
        "encoding/json"
        "fmt"
        "io"
        "net/http"
        "net/url"
        "os"
        "path/filepath"
        "strings"
        "time"

        "go-emby/internal/db"
        "go-emby/internal/logx"
)

const (
        apiBase    = "https://api.themoviedb.org/3"
        imgBase    = "https://image.tmdb.org/t/p"
        maxImgSize = 10 << 20
)

var client = &http.Client{Timeout: 20 * time.Second}

// Settings 刮削设置。
type Settings struct {
        APIKey      string `json:"apiKey"`
        Language    string `json:"language"` // zh-CN / en-US
        DownloadImgs bool  `json:"downloadImages"`
}

// LoadSettings 读取刮削设置。
func LoadSettings() Settings {
        s := Settings{Language: "zh-CN", DownloadImgs: true}
        if v, ok := db.GetSetting("tmdb"); ok {
                _ = json.Unmarshal([]byte(v), &s)
        }
        if s.Language == "" {
                s.Language = "zh-CN"
        }
        return s
}

// SaveSettings 保存设置。
func SaveSettings(s Settings) error {
        b, _ := json.Marshal(s)
        return db.SetSetting("tmdb", string(b))
}

// APIError TMDB 错误。
type APIError struct{ Status int; Msg string }

func (e *APIError) Error() string { return fmt.Sprintf("tmdb api %d: %s", e.Status, e.Msg) }

func get(ctx context.Context, key, path string, q url.Values, out any) error {
        q.Set("api_key", key)
        req, err := http.NewRequestWithContext(ctx, "GET", apiBase+path+"?"+q.Encode(), nil)
        if err != nil {
                return err
        }
        req.Header.Set("Accept", "application/json")
        resp, err := client.Do(req)
        if err != nil {
                return err
        }
        defer resp.Body.Close()
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
        if resp.StatusCode != 200 {
                return &APIError{Status: resp.StatusCode, Msg: strings.TrimSpace(string(body))}
        }
        return json.Unmarshal(body, out)
}

// SearchResult 搜索结果条目。
type SearchResult struct {
        ID           int      `json:"id"`
        Title        string   `json:"title"`
        Name         string   `json:"name"`
        OriginalTitle string  `json:"original_title"`
        OriginalName string   `json:"original_name"`
        Overview     string   `json:"overview"`
        ReleaseDate  string   `json:"release_date"`
        FirstAirDate string   `json:"first_air_date"`
        PosterPath   string   `json:"poster_path"`
        BackdropPath string   `json:"backdrop_path"`
        VoteAverage  float64  `json:"vote_average"`
        GenreIDs     []int    `json:"genre_ids"`
}

type searchResp struct {
        Results []SearchResult `json:"results"`
}

// SearchMovie 搜索电影。
func SearchMovie(ctx context.Context, key, query, lang string, year int) ([]SearchResult, error) {
        q := url.Values{"query": {query}, "language": {lang}}
        if year > 0 {
                q.Set("year", fmt.Sprint(year))
        }
        var r searchResp
        if err := get(ctx, key, "/search/movie", q, &r); err != nil {
                return nil, err
        }
        return r.Results, nil
}

// SearchTV 搜索剧集。
func SearchTV(ctx context.Context, key, query, lang string, year int) ([]SearchResult, error) {
        q := url.Values{"query": {query}, "language": {lang}}
        if year > 0 {
                q.Set("first_air_date_year", fmt.Sprint(year))
        }
        var r searchResp
        if err := get(ctx, key, "/search/tv", q, &r); err != nil {
                return nil, err
        }
        return r.Results, nil
}

// MovieDetail 电影详情。
type MovieDetail struct {
        ID            int      `json:"id"`
        Title         string   `json:"title"`
        OriginalTitle string   `json:"original_title"`
        Overview      string   `json:"overview"`
        ReleaseDate   string   `json:"release_date"`
        Runtime       int      `json:"runtime"`
        VoteAverage   float64  `json:"vote_average"`
        PosterPath    string   `json:"poster_path"`
        BackdropPath  string   `json:"backdrop_path"`
        IMDBID        string   `json:"imdb_id"`
        Tagline       string   `json:"tagline"`
        Status        string   `json:"status"`
        Genres        []Genre  `json:"genres"`
        ProductionCompanies []struct{ Name string `json:"name"` } `json:"production_companies"`
        ProductionCountries []struct{ Iso31661 string `json:"iso_3166_1"` } `json:"production_countries"`
}

// TVDetail 剧集详情。
type TVDetail struct {
        ID            int     `json:"id"`
        Name          string  `json:"name"`
        OriginalName  string  `json:"original_name"`
        Overview      string  `json:"overview"`
        FirstAirDate  string  `json:"first_air_date"`
        PosterPath    string  `json:"poster_path"`
        BackdropPath  string  `json:"backdrop_path"`
        Tagline       string  `json:"tagline"`
        NumberOfSeasons int   `json:"number_of_seasons"`
        VoteAverage   float64 `json:"vote_average"`
        Genres        []Genre `json:"genres"`
        Networks      []struct{ Name string `json:"name"` } `json:"networks"`
        Seasons       []TVSeason `json:"seasons"`
}

// Genre 类型。
type Genre struct{ ID int; Name string }

func (g *Genre) UnmarshalJSON(b []byte) error {
        var raw struct {
                ID   int    `json:"id"`
                Name string `json:"name"`
        }
        if err := json.Unmarshal(b, &raw); err != nil {
                return err
        }
        g.ID, g.Name = raw.ID, raw.Name
        return nil
}

// TVSeason 剧集季概要。
type TVSeason struct {
        ID          int    `json:"id"`
        Name        string `json:"name"`
        SeasonNumber int   `json:"season_number"`
        EpisodeCount int   `json:"episode_count"`
        AirDate     string `json:"air_date"`
        PosterPath  string `json:"poster_path"`
}

// GetMovie 电影详情。
func GetMovie(ctx context.Context, key string, id int, lang string) (*MovieDetail, error) {
        var d MovieDetail
        q := url.Values{"language": {lang}}
        if err := get(ctx, key, fmt.Sprintf("/movie/%d", id), q, &d); err != nil {
                return nil, err
        }
        return &d, nil
}

// GetTV 剧集详情。
func GetTV(ctx context.Context, key string, id int, lang string) (*TVDetail, error) {
        var d TVDetail
        q := url.Values{"language": {lang}}
        if err := get(ctx, key, fmt.Sprintf("/tv/%d", id), q, &d); err != nil {
                return nil, err
        }
        return &d, nil
}

// EpisodeDetail 集详情。
type EpisodeDetail struct {
        ID           int     `json:"id"`
        Name         string  `json:"name"`
        Overview     string  `json:"overview"`
        EpisodeNumber int    `json:"episode_number"`
        SeasonNumber  int    `json:"season_number"`
        AirDate      string  `json:"air_date"`
        StillPath    string  `json:"still_path"`
        Runtime      int     `json:"runtime"`
        VoteAverage  float64 `json:"vote_average"`
}

type seasonResp struct {
        Episodes []EpisodeDetail `json:"episodes"`
        Name     string          `json:"name"`
        Overview string          `json:"overview"`
        PosterPath string        `json:"poster_path"`
        AirDate  string          `json:"air_date"`
}

// GetSeason 获取某一季的集列表。
func GetSeason(ctx context.Context, key string, tvID, season int, lang string) (*seasonResp, error) {
        var r seasonResp
        q := url.Values{"language": {lang}}
        if err := get(ctx, key, fmt.Sprintf("/tv/%d/season/%d", tvID, season), q, &r); err != nil {
                return nil, err
        }
        return &r, nil
}

// DownloadImage 下载 TMDB 图片到本地 metaDir/itemID/ 下，返回本地路径。
func DownloadImage(metaDir, itemID string, size, path string) (string, error) {
        if path == "" {
                return "", fmt.Errorf("empty image path")
        }
        if !strings.HasPrefix(path, "/") {
                path = "/" + path
        }
        dir := filepath.Join(metaDir, itemID)
        if err := os.MkdirAll(dir, 0o755); err != nil {
                return "", err
        }
        local := filepath.Join(dir, sanitize(size+"-"+filepath.Base(path)))
        if _, err := os.Stat(local); err == nil {
                return local, nil
        }
        u := imgBase + "/" + size + path
        req, err := http.NewRequest("GET", u, nil)
        if err != nil {
                return "", err
        }
        resp, err := client.Do(req)
        if err != nil {
                return "", err
        }
        defer resp.Body.Close()
        if resp.StatusCode != 200 {
                return "", fmt.Errorf("image %d", resp.StatusCode)
        }
        data, err := io.ReadAll(io.LimitReader(resp.Body, maxImgSize))
        if err != nil {
                return "", err
        }
        if err := os.WriteFile(local, data, 0o644); err != nil {
                return "", err
        }
        logx.Detail("info", u, "已下载 TMDB 图片 %s", filepath.Base(local))
        return local, nil
}

func sanitize(name string) string {
        name = strings.Map(func(r rune) rune {
                switch r {
                case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
                        return '_'
                }
                return r
        }, name)
        if name == "" {
                return "img"
        }
        return name
}
