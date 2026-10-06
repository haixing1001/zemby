// Package models 定义数据库模型。
package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
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
	RunTimeTicks     int64     `json:"runTimeTicks,omitempty"`
	Path             string    `gorm:"index;size:768" json:"-"` // 文件或目录
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
	Message   string    `json:"message"`
	Detail    string    `json:"detail,omitempty"`
	Time      time.Time `json:"time"`
}

func init() {}
