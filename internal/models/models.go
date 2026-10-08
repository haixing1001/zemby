// Package models 定义数据库模型。
package models

import (
        "crypto/rand"
        "crypto/sha256"
        "encoding/hex"
        "strings"
        "time"

        "github.com/mozillazg/go-pinyin"
        "gorm.io/gorm"
)

func randHex(n int) string {
        b := make([]byte, n)
        _, _ = rand.Read(b)
        return hex.EncodeToString(b)
}

// NewID 生成 32 位十六进制随机 ID（模拟 Emby GUID）。
func NewID() string { return randHex(16) }

// NewToken 生成 64 位访问令牌，数据库只存哈希。
func NewToken() (raw string, hash string) {
        raw = randHex(16) + randHex(16)
        h := sha256.Sum256([]byte(raw))
        return raw, hex.EncodeToString(h[:])
}

// HashToken 对令牌做 SHA-256，落库用。
func HashToken(t string) string {
        h := sha256.Sum256([]byte(t))
        return hex.EncodeToString(h[:])
}

// User 用户。
type User struct {
        ID           string    `gorm:"primaryKey;size:64" json:"id"`
        Name         string    `gorm:"uniqueIndex;size:64" json:"name"`
        PasswordHash string    `gorm:"size:128" json:"-"`
        IsAdmin      bool      `json:"isAdministrator"`
        Allowed      bool      `gorm:"default:true" json:"allowPlayback"` // 播放权限
        MaxDevices   int       `gorm:"default:2" json:"maxDevices"`
        FirstAdmin   bool      `json:"-"` // 初始管理员不可删除
        CreatedAt    time.Time `json:"dateCreated"`
        UpdatedAt    time.Time `json:"-"`
}

// Token 登录令牌（与设备绑定）。
type Token struct {
        Hash       string    `gorm:"primaryKey;size:64"`
        UserID     string    `gorm:"index;size:64"`
        DeviceID   string    `gorm:"index;size:128"`
        DeviceName string    `gorm:"size:128"`
        Client     string    `gorm:"size:128"`
        AppVersion string    `gorm:"size:64"`
        IPAddress  string    `gorm:"size:64"`
        CreatedAt  time.Time `json:"dateCreated"`
        LastSeen   time.Time
}

// Library 媒体库。
type Library struct {
        ID          string    `gorm:"primaryKey;size:64" json:"id"`
        Name        string    `gorm:"size:128" json:"name"`
        Path        string    `gorm:"size:512" json:"path"` // 可为多个目录，分号分隔
        Type        string    `gorm:"size:16" json:"type"`  // movies | tvshows
        EnableTMDB  bool      `gorm:"default:true" json:"enableTmdb"`
        Language    string    `gorm:"size:8;default:zh-CN" json:"language"`
        SortOrder   int       `json:"sortOrder"`
        Hidden      bool      `json:"hidden"`             // 隐藏媒体库
        Poster      string    `gorm:"size:768" json:"-"` // 媒体库封面图路径
        DefaultSort string    `gorm:"size:64" json:"-"`  // 默认排序，如 SortName|Ascending
        Scanning    bool      `json:"-"`
        LastScan    *time.Time `json:"lastScanned"`
        CreatedAt   time.Time `json:"dateCreated"`
}

// Item 媒体条目（电影 / 剧集 / 季 / 集）。
type Item struct {
        ID               string    `gorm:"primaryKey;size:64" json:"id"`
        LibraryID        string    `gorm:"index;size:64" json:"libraryId"`
        Type             string    `gorm:"index;size:16" json:"type"` // Movie|Series|Season|Episode
        Name             string    `gorm:"index;size:512" json:"name"`
        SortName         string    `gorm:"size:512" json:"sortName"`
        OriginalTitle    string    `gorm:"size:512" json:"originalTitle,omitempty"`
        Overview         string    `gorm:"type:text" json:"overview,omitempty"`
        Year             int       `gorm:"index" json:"productionYear,omitempty"`
        PremiereDate     *time.Time `json:"premiereDate,omitempty"`
        CommunityRating  float64   `json:"communityRating,omitempty"`
        OfficialRating   string    `gorm:"size:32" json:"officialRating,omitempty"`
        Genres           string    `gorm:"type:text" json:"-"` // JSON array
        Studios          string    `gorm:"type:text" json:"-"` // JSON array
        People           string    `gorm:"type:text" json:"-"` // JSON array [{Name,Role}]
        Tags             string    `gorm:"type:text" json:"-"`
        ProviderIDs      string    `gorm:"size:512" json:"-"` // JSON {Tmdb,Imdb}
        TmdbID           string    `gorm:"index;size:32" json:"-"` // TMDB 编号
        TmdbKind         string    `gorm:"index;size:16" json:"-"` // movie | tv | season | episode
        RunTimeTicks     int64     `json:"runTimeTicks,omitempty"`
        Path             string    `gorm:"index;size:768" json:"-"` // 文件或目录
        Initials         string    `gorm:"index;size:512" json:"-"` // 拼音首字母检索串（小写）
        Container        string    `gorm:"size:32" json:"-"`
        Size             int64     `json:"-"`
        Mtime            int64     `json:"-"` // 增量扫描用：文件修改时间戳
        ParentID         string    `gorm:"index;size:64" json:"-"` // Season 的父=Series; Episode 的父=Season
        SeriesID         string    `gorm:"index;size:64" json:"-"`
        SeriesName       string    `gorm:"size:512" json:"-"`
        SeasonID         string    `gorm:"index;size:64" json:"-"`
        IndexNumber      int       `json:"indexNumber,omitempty"`      // 集
        ParentIndexNumber int      `json:"parentIndexNumber,omitempty"` // 季
        Scraped          bool      `json:"-"`
        ScrapeError      string    `gorm:"size:512" json:"-"` // 最近一次刮削失败原因
        Poster           string    `gorm:"size:768" json:"-"` // 本地或已下载图片路径
        Thumb            string    `gorm:"size:768" json:"-"`
        Backdrop         string    `gorm:"size:768" json:"-"`
        Logo             string    `gorm:"size:768" json:"-"`
        ImageRev         string    `gorm:"size:40" json:"-"` // 图片修订（etag/tag 用）
        DateCreated      time.Time `json:"dateCreated"`
        UpdatedAt        time.Time `json:"-"`
}

// MediaSource 媒体源（一个条目可有多个版本/文件）。
type MediaSource struct {
        ID        string    `gorm:"primaryKey;size:64"`
        ItemID    string    `gorm:"index;size:64"`
        Path      string    `gorm:"size:768"` // 文件路径或 http(s) URL
        Container string    `gorm:"size:32"`
        Size      int64
	ProbedMtime int64 // 最近一次成功探测时文件的修改时间（纳秒）
	ProbedSize  int64 // 最近一次成功探测时文件的大小
        Name      string    `gorm:"size:512"`
        Default   bool      `gorm:"default:true"`
        CreatedAt time.Time
}

// MediaStream 流信息（视频/音频/字幕轨道），含外挂字幕。
type MediaStream struct {
        ID           uint   `gorm:"primaryKey;autoIncrement"`
        SourceID     string `gorm:"index;size:64"`
        ItemID       string `gorm:"index;size:64"`
        Index        int
        Type         string `gorm:"size:16"` // Video|Audio|Subtitle
        Codec        string `gorm:"size:32"`
        Language     string `gorm:"size:16"`
        DisplayTitle string `gorm:"size:256"`
        Title        string `gorm:"size:256"`
        Width        int
        Height       int
        Aspect       string `gorm:"size:16"`
        Channels     int
        SampleRate   int
        BitRate      int
        BitDepth     int
        FrameRate    float64
        PixelFormat  string `gorm:"size:32"`
        Profile      string `gorm:"size:64"`
        VideoRange   string `gorm:"size:16"`
        IsDefault    bool
        IsForced     bool
        IsExternal   bool   `gorm:"default:false"`
        Path         string `gorm:"size:768"` // 外挂字幕文件路径
}

// UserDatum 用户播放状态。
type UserDatum struct {
        ID           string `gorm:"primaryKey;size:64"` // userID|itemID
        UserID       string `gorm:"index;size:64"`
        ItemID       string `gorm:"index;size:64"`
        Played       bool
        PlayCount    int
        Favorite     bool
        PositionTicks int64
        LastPlayedDate *time.Time
}

// PlaySession 播放中的设备（租约制，用于设备数量限制）。
type PlaySession struct {
        ID        string `gorm:"primaryKey;size:64"` // userID|deviceID
        UserID    string `gorm:"index;size:64"`
        DeviceID  string `gorm:"size:128"`
        ItemID    string `gorm:"size:64"`
        UpdatedAt time.Time
}

// Setting 键值配置（TMDB Key、刮削语言等）。
type Setting struct {
        Key   string `gorm:"primaryKey;size:64"`
        Value string `gorm:"type:text"` // JSON
}

// ApiKey 后台「API 管理」签发的 Emby 兼容密钥（库中只存哈希，等同管理员权限）。
type ApiKey struct {
        ID        uint       `gorm:"primaryKey;autoIncrement" json:"id"`
        Name      string     `gorm:"size:128" json:"name"`
        Hash      string     `gorm:"uniqueIndex;size:64" json:"-"`
        Prefix    string     `gorm:"size:8" json:"prefix"` // 明文前 6 位，用于掩码展示
        Suffix    string     `gorm:"size:8" json:"suffix"` // 明文后 4 位
        LastSeen  *time.Time `json:"lastSeen"`
        CreatedAt time.Time  `json:"dateCreated"`
}

// PlayActivity 播放活动记录（后台活跃状态展示）。
type PlayActivity struct {
        ID        uint      `gorm:"primaryKey;autoIncrement"`
        UserName  string    `gorm:"size:64;index"`
        ItemID    string    `gorm:"size:64;index"`
        ItemName  string    `gorm:"size:512"`
        Device    string    `gorm:"size:128"`
        Action    string    `gorm:"size:16"` // start | progress | stop
        Position  int64
        CreatedAt time.Time `gorm:"index"`
}

// ScanTask 扫描/刮削任务记录。
type ScanTask struct {
        ID        uint      `gorm:"primaryKey;autoIncrement"`
        LibraryID string    `gorm:"index;size:64"`
        Mode      string    `gorm:"size:16"` // full | update | scrape
        State     string    `gorm:"size:16"` // running|done|error
        Message   string    `gorm:"size:512"`
        Total     int
        Done      int
        StartedAt time.Time
        EndedAt   *time.Time
}

// LogEntry 实时日志条目。
type LogEntry struct {
        Seq       int64     `json:"id"`
        Level     string    `json:"level"` // info|warn|error|scan|playback
        Category  string    `json:"category,omitempty"` // system|scan|scrape|tmdb|probe|subtitle|playback|redirect
        Message   string    `json:"message"`
        Detail    string    `json:"detail,omitempty"`
        Time      time.Time `json:"time"`
}

// pyArgs 拼音首字母参数。
var pyArgs = pinyin.NewArgs()

// InitialsOf 生成检索串：汉字转拼音首字母，字母/数字保留小写原样，其余字符忽略。
// 例：《满江红》→ "mjh"；"Top Gun 2024" → "topgun2024"。
func InitialsOf(name string) string {
        var b strings.Builder
        for _, ch := range name {
                switch {
                case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
                        b.WriteRune(ch)
                case ch >= 'A' && ch <= 'Z':
                        b.WriteRune(ch + ('a' - 'A'))
                case ch >= 0x3400 && ch <= 0x9fff: // CJK 统一表意文字
                        vals := pinyin.SinglePinyin(ch, pyArgs)
                        if len(vals) > 0 && vals[0] != "" {
                                b.WriteByte(vals[0][0])
                        }
                }
        }
        return b.String()
}

// TmdbKindForItemType 返回 TMDB ID 的命名空间，避免电影/剧集编号碰撞。
func TmdbKindForItemType(itemType string) string {
        switch itemType {
        case "Movie":
                return "movie"
        case "Series":
                return "tv"
        case "Season":
                return "season"
        case "Episode":
                return "episode"
        default:
                return ""
        }
}

// TmdbRouteID 返回前台详情页使用的稳定身份。电影和剧集使用 TMDB 的独立命名空间，
// 其余层级继续使用内部 ID，避免季/集的父级 TMDB 编号与影片本身混淆。
func (i *Item) TmdbRouteID() string {
        if i == nil {
                return ""
        }
        tmdbID := strings.TrimSpace(i.TmdbID)
        if tmdbID == "" {
                return i.ID
        }
        var kind string
        switch i.Type {
        case "Movie":
                kind = "movie"
        case "Series":
                kind = "tv"
        default:
                return i.ID
        }
        return kind + "-" + tmdbID
}

// ParseTmdbRouteID 解析前台 TMDB 路由身份，并兼容上一版 tmdb-movie-* / tmdb-tv-* 链接。
func ParseTmdbRouteID(routeID string) (kind string, tmdbID string, ok bool) {
        routeID = strings.ToLower(strings.TrimSpace(routeID))
        if kind, tmdbID, ok = parseCurrentTmdbRouteID(routeID); ok {
                return kind, tmdbID, true
        }
        return parseLegacyTmdbRouteID(routeID)
}

func parseCurrentTmdbRouteID(routeID string) (kind string, tmdbID string, ok bool) {
        if !strings.HasPrefix(routeID, "movie-") && !strings.HasPrefix(routeID, "tv-") {
                return "", "", false
        }
        parts := strings.SplitN(routeID, "-", 2)
        return parts[0], parts[1], validTmdbRouteID(parts[0], parts[1])
}

func parseLegacyTmdbRouteID(routeID string) (kind string, tmdbID string, ok bool) {
        const prefix = "tmdb-"
        if !strings.HasPrefix(routeID, prefix) {
                return "", "", false
        }
        parts := strings.Split(strings.TrimPrefix(routeID, prefix), "-")
        if len(parts) != 2 {
                return "", "", false
        }
        return parts[0], parts[1], validTmdbRouteID(parts[0], parts[1])
}

func validTmdbRouteID(kind, tmdbID string) bool {
        if (kind != "movie" && kind != "tv") || tmdbID == "" {
                return false
        }
        for _, ch := range tmdbID {
                if ch < '0' || ch > '9' {
                        return false
                }
        }
        return true
}

// BeforeSave GORM 钩子：任何保存都同步刷新首字母检索串。
func (i *Item) BeforeSave(tx *gorm.DB) error {
        i.Initials = InitialsOf(i.Name)
        return nil
}

func init() {}
