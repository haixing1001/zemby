// Package nfo 解析 Kodi/Emby 风格的 NFO 元数据文件。
package nfo

import (
        "encoding/xml"
        "io"
        "os"
        "strconv"
        "strings"
        "time"
)

// Person NFO 中的人物。
type Person struct {
        Name  string `xml:"name"`
        Role  string `xml:"role"`
        Thumb string `xml:"thumb"`
}

// UniqueID 唯一标识（tmdb/imdb）。
type UniqueID struct {
        Type  string `xml:"type,attr"`
        Value string `xml:",chdata"`
}

// StreamDetails NFO 内嵌流信息。
type StreamDetails struct {
        Video struct {
                Codec             string  `xml:"codec"`
                Width             int     `xml:"width"`
                Height            int     `xml:"height"`
                DurationSeconds   float64 `xml:"durationinseconds"`
                AspectRatio       string  `xml:"aspect"`
        } `xml:"video"`
        Audio []struct {
                Codec    string `xml:"codec"`
                Language string `xml:"language"`
                Channels int    `xml:"channels"`
        } `xml:"audio"`
        Subtitle []struct {
                Codec    string `xml:"codec"`
                Language string `xml:"language"`
        } `xml:"subtitle"`
}

// NFO 解析后的 NFO 数据（movie 与 tvshow 通用超集）。
type NFO struct {
        Title         string     `xml:"title"`
        OriginalTitle string     `xml:"originaltitle"`
        SortTitle     string     `xml:"sorttitle"`
        Plot          string     `xml:"plot"`
        Outline       string     `xml:"outline"`
        Tagline       string     `xml:"tagline"`
        Rating        float64    `xml:"rating"`
        Year          int        `xml:"year"`
        Season        int        `xml:"season"`
        Episode       int        `xml:"episode"`
        Premiered     string     `xml:"premiered"`
        Releasedate   string     `xml:"releasedate"`
        Runtime       int        `xml:"runtime"` // 分钟
        MPAA          string     `xml:"mpaa"`
        Genres        []string   `xml:"genre"`
        Studios       []string   `xml:"studio"`
        Countries     []string   `xml:"country"`
        Tags          []string   `xml:"tag"`
        Directors     []string   `xml:"director"`
        Writers       []string   `xml:"credits"`
        Actors        []Person   `xml:"actor"`
        UniqueIDs     []UniqueID `xml:"uniqueid"`
        TmdbID        string     `xml:"tmdbid"`
        ImdbID        string     `xml:"id"`
        ShowTitle     string     `xml:"showtitle"`
        StreamDetails StreamDetails `xml:"fileinfo>streamdetails"`
}

// ParseFile 解析 NFO 文件。
func ParseFile(path string) (*NFO, error) {
        f, err := os.Open(path)
        if err != nil {
                return nil, err
        }
        defer f.Close()
        return Parse(f)
}

// Parse 解析 NFO 流。容错：XML 前可能存在 HTML 或 BOM。
func Parse(r io.Reader) (*NFO, error) {
        raw, err := io.ReadAll(io.LimitReader(r, 4<<20))
        if err != nil {
                return nil, err
        }
        data := stripBOM(raw)
        // 截取首个 <tag 到最后一个 </...>，避免前导垃圾导致解析失败
        if i := strings.IndexAny(string(data), "<"); i > 0 {
                data = data[i:]
        }
        var n NFO
        if err := xml.Unmarshal(data, &n); err != nil {
                // 尝试宽松处理未转义字符：取 <movie> 或 <tvshow> 段
                var alt NFO
                if err2 := xml.Unmarshal(data, &alt); err2 != nil {
                        return nil, err
                }
                return &alt, nil
        }
        return &n, nil
}

// TMDBID 返回 TMDB ID（优先 uniqueid）。
func (n *NFO) TMDBID() string {
        for _, u := range n.UniqueIDs {
                if strings.EqualFold(u.Type, "tmdb") && u.Value != "" {
                        return u.Value
                }
        }
        return n.TmdbID
}

// IMDBID 返回 IMDB ID（优先 uniqueid）。
func (n *NFO) IMDBID() string {
        for _, u := range n.UniqueIDs {
                if strings.EqualFold(u.Type, "imdb") && u.Value != "" {
                        return u.Value
                }
        }
        if strings.HasPrefix(strings.ToLower(n.ImdbID), "tt") {
                return n.ImdbID
        }
        return ""
}

// PremiereTime 解析首播日期，失败返回零值。
func (n *NFO) PremiereTime() time.Time {
        s := strings.TrimSpace(n.Premiered)
        if s == "" {
                s = strings.TrimSpace(n.Releasedate)
        }
        if s == "" {
                return time.Time{}
        }
        for _, layout := range []string{"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02", "2006/01/02", "20060102"} {
                if t, err := time.Parse(layout, s); err == nil {
                        return t
                }
        }
        return time.Time{}
}

func stripBOM(b []byte) []byte {
        if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
                return b[3:]
        }
        return b
}

// Atoi 宽松转 int。
func Atoi(s string) int {
        n, _ := strconv.Atoi(strings.TrimSpace(s))
        return n
}
