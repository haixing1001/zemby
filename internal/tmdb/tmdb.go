// Package tmdb TMDB 元数据刮削客户端。
package tmdb

import (
        "context"
        "encoding/json"
        "fmt"
        "io"
	"mime"
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
	defaultAPIBase = "https://api.themoviedb.org/3"
	defaultImgBase = "https://image.tmdb.org/t/p"
        maxImgSize = 10 << 20
)

var client = &http.Client{Timeout: 20 * time.Second}

// Settings 刮削设置。
type Settings struct {
	APIKey       string `json:"apiKey"`
	Language     string `json:"language"` // zh-CN / en-US
	DownloadImgs bool   `json:"downloadImages"`
	APIBaseURL   string `json:"apiBaseUrl"`
	ImageBaseURL string `json:"imageBaseUrl"`
}

// LoadSettings 读取刮削设置。
func LoadSettings() Settings {
	s := Settings{Language: "zh-CN", DownloadImgs: true, APIBaseURL: defaultAPIBase, ImageBaseURL: defaultImgBase}
        if v, ok := db.GetSetting("tmdb"); ok {
                _ = json.Unmarshal([]byte(v), &s)
        }
        if s.Language == "" {
                s.Language = "zh-CN"
        }
	if s.APIBaseURL, _ = normalizeBaseURL(s.APIBaseURL, defaultAPIBase, false); s.APIBaseURL == "" {
		s.APIBaseURL = defaultAPIBase
	}
	if s.ImageBaseURL, _ = normalizeBaseURL(s.ImageBaseURL, defaultImgBase, true); s.ImageBaseURL == "" {
		s.ImageBaseURL = defaultImgBase
	}
        return s
}

// SaveSettings 保存设置。
func SaveSettings(s Settings) error {
	var err error
	if s.APIBaseURL, err = normalizeBaseURL(s.APIBaseURL, defaultAPIBase, false); err != nil {
		return fmt.Errorf("TMDB API 镜像地址无效：%w", err)
	}
	if s.ImageBaseURL, err = normalizeBaseURL(s.ImageBaseURL, defaultImgBase, true); err != nil {
		return fmt.Errorf("TMDB 图片镜像地址无效：%w", err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
        return db.SetSetting("tmdb", string(b))
}

func normalizeBaseURL(raw, fallback string, allowImageProxy bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("请输入有效的 http 或 https 基础地址")
	}
	if u.RawQuery != "" {
		if !allowImageProxy {
			return "", fmt.Errorf("API 镜像地址不能包含查询参数")
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", fmt.Errorf("镜像查询参数格式无效：%w", err)
		}
		values := query["url"]
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return "", fmt.Errorf("图片代理地址必须包含一个非空 url 参数")
		}
		origin, err := url.Parse(values[0])
		if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil || origin.Fragment != "" {
			return "", fmt.Errorf("图片代理 url 参数必须是有效的 http 或 https 图片源地址")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func imageRequestURL(base, size, imagePath string) (string, error) {
	proxy, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if proxy.RawQuery == "" {
		return strings.TrimRight(base, "/") + "/" + size + imagePath, nil
	}
	query, err := url.ParseQuery(proxy.RawQuery)
	if err != nil {
		return "", err
	}
	origin, err := url.Parse(query.Get("url"))
	if err != nil || origin.Host == "" {
		return "", fmt.Errorf("图片代理缺少有效的 url 参数")
	}
	originPath := strings.TrimRight(origin.Path, "/")
	if originPath != "/t/p" && !strings.HasSuffix(originPath, "/t/p") {
		originPath += "/t/p"
	}
	origin.Path = originPath + "/" + size + "/" + strings.TrimLeft(imagePath, "/")
	origin.RawPath = ""
	query.Set("url", origin.String())
	proxy.RawQuery = query.Encode()
	return proxy.String(), nil
}

// APIError TMDB 错误。
type APIError struct{ Status int; Msg string }

func (e *APIError) Error() string { return fmt.Sprintf("tmdb api %d: %s", e.Status, e.Msg) }

func get(ctx context.Context, key, path string, q url.Values, out any) error {
        q.Set("api_key", key)
	base := LoadSettings().APIBaseURL
	req, err := http.NewRequestWithContext(ctx, "GET", base+path+"?"+q.Encode(), nil)
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

// GetMoviePoster returns the best available poster path when movie details do
// not include one. This happens for localized details even when TMDB has images.
func GetMoviePoster(ctx context.Context, key string, id int, lang string) (string, error) {
	var result struct {
		Posters []struct {
			FilePath string `json:"file_path"`
			Language string `json:"iso_639_1"`
		} `json:"posters"`
	}
	q := url.Values{"include_image_language": {lang + ",null"}}
	if err := get(ctx, key, fmt.Sprintf("/movie/%d/images", id), q, &result); err != nil {
		return "", err
	}
	preferred := strings.SplitN(lang, "-", 2)[0]
	for _, poster := range result.Posters {
		if poster.FilePath != "" && poster.Language == preferred {
			return poster.FilePath, nil
		}
	}
	for _, poster := range result.Posters {
		if poster.FilePath != "" {
			return poster.FilePath, nil
		}
	}
	return "", nil
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

// CreditMember 演职员条目（cast 与 crew 共用结构）。
type CreditMember struct {
        Name        string `json:"name"`
        Character   string `json:"character"`
        Job         string `json:"job"`
        ProfilePath string `json:"profile_path"`
        Order       int    `json:"order"`
}

type creditsResp struct {
        Cast []CreditMember `json:"cast"`
        Crew []CreditMember `json:"crew"`
}

// GetCredits 获取演职员表，kind 为 "movie" 或 "tv"。
func GetCredits(ctx context.Context, key, kind string, id int, lang string) ([]CreditMember, []CreditMember, error) {
        var r creditsResp
        q := url.Values{"language": {lang}}
        if err := get(ctx, key, fmt.Sprintf("/%s/%d/credits", kind, id), q, &r); err != nil {
                return nil, nil, err
        }
        return r.Cast, r.Crew, nil
}

// ProfileURL 将 TMDB 头像路径转成完整 URL（遵循图片镜像配置），路径为空时返回空串。
func ProfileURL(base, profilePath, size string) string {
        if profilePath == "" {
                return ""
        }
        u, err := imageRequestURL(base, size, profilePath)
        if err != nil {
                return ""
        }
        return u
}

// DownloadImage 下载 TMDB 图片到本地 metaDir/itemID/ 下，返回本地路径。
func DownloadImage(ctx context.Context, metaDir, itemID string, size, path string) (string, error) {
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
        if validCachedImage(local) {
                return local, nil
        }
        _ = os.Remove(local)
	u, err := imageRequestURL(LoadSettings().ImageBaseURL, size, path)
	if err != nil {
		return "", err
	}
        var lastErr error
        for attempt := 0; attempt < 3; attempt++ {
                req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
                if err != nil {
                        return "", err
                }
                resp, err := client.Do(req)
                if err != nil {
                        lastErr = err
                } else if resp.StatusCode != http.StatusOK {
                        body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
                        resp.Body.Close()
                        lastErr = fmt.Errorf("image %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
                        if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
                                return "", lastErr
                        }
                } else {
                        data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxImgSize+1))
                        resp.Body.Close()
                        if readErr != nil {
                                lastErr = readErr
                        } else if len(data) > maxImgSize {
                                return "", fmt.Errorf("image exceeds %d bytes", maxImgSize)
                        } else if !isImageData(data) {
                                lastErr = fmt.Errorf("invalid image response from %s", u)
                        } else {
                                if err := writeImageAtomically(dir, local, data); err != nil {
                                        return "", err
                                }
                                logx.DetailC(logx.CatTMDB, "info", u, "已下载 TMDB 图片 %s", filepath.Base(local))
                                return local, nil
                        }
                }
                if attempt < 2 {
                        delay := time.Duration(attempt+1) * 300 * time.Millisecond
                        timer := time.NewTimer(delay)
                        select {
                        case <-ctx.Done():
                                timer.Stop()
                                return "", ctx.Err()
                        case <-timer.C:
                        }
                }
        }
        return "", lastErr
}

func validCachedImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxImgSize {
		return false
	}
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	return isImageData(buf[:n]) && (err == nil || err == io.EOF)
}

func isImageData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if len(data) > 512 {
		data = data[:512]
	}
	contentType, _, err := mime.ParseMediaType(http.DetectContentType(data))
	return err == nil && strings.HasPrefix(contentType, "image/")
}

func writeImageAtomically(dir, local string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmdb-image-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, local)
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
