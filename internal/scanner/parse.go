// Package scanner 媒体库扫描与文件名解析。
package scanner

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 视频扩展名。
var VideoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
	".flv": true, ".ts": true, ".m2ts": true, ".mts": true, ".webm": true,
	".mpg": true, ".mpeg": true, ".vob": true, ".m4v": true, ".rmvb": true,
	".rm": true, ".3gp": true, ".ogv": true, ".divx": true, ".f4v": true,
	".strm": true, ".iso": true,
}

// NFOExts NFO 扩展名。
var NFOExts = map[string]bool{".nfo": true}

// ImageExts 图片扩展名。
var ImageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true, ".tbn": true}

// SubExts 字幕扩展名。
var SubExts = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".sup": true, ".vtt": true, ".sub": true}

// 跳过的目录名。
var skipDirs = map[string]bool{
	".AppleDouble": true, "@eaDir": true, "#recycle": true, "lost+found": true,
	".@__thumb": true, ".thumbnails": true, ".actors": true, "extrafanart": true,
	"extras": true, "trailers": true, ".git": true, "$RECYCLE.BIN": true,
	"System Volume Information": true, ".DS_Store": true, "subtitles": true,
}

var (
	epPattern    = regexp.MustCompile(`(?i)[Ss](\d{1,3})[\s._-]*[EeXx](\d{1,4})`)
	epPatternAlt = regexp.MustCompile(`(?i)第\s*(\d{1,4})\s*季\s*第\s*(\d{1,4})\s*[集話话]`)
	epPlain      = regexp.MustCompile(`(?i)^(?:[^\d]|^)(\d{1,4})\s*[集話话]`)
	yearPattern  = regexp.MustCompile(`(?i)[\(\[.]?\b((?:19|20)\d{2})\b[\)\].]?`)
	seasonDir    = regexp.MustCompile(`(?i)^season[ ._\-]*(\d{1,3})$`)
	cnSeasonDir  = regexp.MustCompile(`(?i)第\s*(\d{1,3})\s*季`)
	resTag       = regexp.MustCompile(`(?i)\b(720p|1080[pi]|2160p|4k|480p|576p|8k)\b`)
	qualityTags  = regexp.MustCompile(`(?i)\b(bluray|blu-ray|brrip|bdrip|web-?dl|webrip|hdtv|hd-?dvd|dvdrip|cam|ts|tc|remux|hdr|dvix|h\.?26[45]|x26[45]|hevc|avc|xvid|aac|flac|dts(?:-hd)?(?:\s?ma)?|ac3|eac3|truehd|atmos|ddp?5\.1|dd2\.0|10bit|60fps|dv|do_vi|dolby\s?vision)\b`)
	groupTag     = regexp.MustCompile(`[-_@\s]([A-Za-z0-9@&._-]{2,})$`)
	edgedTrash   = regexp.MustCompile(`(?i)\b(extended|uncut|remastered|unrated|directors?[ ._-]?cut|theatrical|final[ ._-]?cut|imax|edition|repack|proper|internal|limited|complete|collector)\b`)
	titleTrash   = regexp.MustCompile(`[._\-\[\]\(\)]+`)
	chineseReg   = regexp.MustCompile(`[\p{Han}]+`)
)

// Parsed 文件名解析结果。
type Parsed struct {
	Title    string
	Year     int
	Season   int
	Episode  int
	IsSample bool
}

// ParseName 解析文件名（不含扩展名），提取标题/年份/季/集。
func ParseName(base string) Parsed {
	name := strings.TrimSpace(base)
	p := Parsed{}

	// 剔除扩展名残影
	name = strings.TrimSuffix(name, filepath.Ext(name))

	// 采样/预告
	low := strings.ToLower(name)
	if strings.Contains(low, "sample") || strings.HasPrefix(low, "trailer") {
		p.IsSample = true
	}

	// 季集
	if m := epPattern.FindStringSubmatch(name); m != nil {
		p.Season, _ = strconv.Atoi(m[1])
		p.Episode, _ = strconv.Atoi(m[2])
	} else if m := epPatternAlt.FindStringSubmatch(name); m != nil {
		p.Season, _ = strconv.Atoi(m[1])
		p.Episode, _ = strconv.Atoi(m[2])
	} else if m := epPlain.FindStringSubmatch(name); m != nil {
		p.Season = 1
		p.Episode, _ = strconv.Atoi(m[1])
	}

	// 年份：优先取标题尾部
	year := 0
	// 找最后一个 4 位年份
	locs := yearPattern.FindAllStringSubmatchIndex(name, -1)
	if len(locs) > 0 {
		last := locs[len(locs)-1]
		year, _ = strconv.Atoi(name[last[2]:last[3]])
		// 截断年份后的部分（通常为 quality/group）
		if year >= 1900 && year <= 2100 {
			name = strings.TrimSpace(name[:last[0]])
		} else {
			year = 0
		}
	}
	p.Year = year

	// 分集名处理：SxxExx 之前的部分是标题
	if p.Episode > 0 {
		if m := epPattern.FindStringIndex(strings.ToUpper(base)); m != nil && m[0] > 0 {
			name = strings.TrimSpace(base[:m[0]])
		} else if m := epPatternAlt.FindStringIndex(base); m != nil && m[0] > 0 {
			name = strings.TrimSpace(base[:m[0]])
		}
	}

	// 清理质量标签、组名等
	name = resTag.ReplaceAllString(name, " ")
	name = qualityTags.ReplaceAllString(name, " ")
	name = edgedTrash.ReplaceAllString(name, " ")
	if idx := strings.LastIndex(name, "-"); idx > 0 {
		tail := name[idx+1:]
		if !chineseReg.MatchString(tail) && !strings.ContainsAny(tail, "0123456789") && len(tail) <= 12 {
			name = name[:idx] // 组名
		}
	}
	name = titleTrash.ReplaceAllString(name, " ")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.TrimSpace(name)

	// 去掉常见站点水印
	for _, junk := range []string{"精修", "中英字幕", "中字", "内嵌", "双语", "简繁", "4K", "HDR", "杜比"} {
		name = strings.ReplaceAll(name, junk, " ")
	}
	name = strings.TrimSpace(name)

	if name == "" {
		name = strings.TrimSpace(base)
	}
	p.Title = name
	return p
}

// ParseSeasonDir 解析季目录名，返回季号（0 表示非季目录）。
func ParseSeasonDir(name string) int {
	if m := seasonDir.FindStringSubmatch(strings.TrimSpace(name)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if m := cnSeasonDir.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// IsVideo 判断是否视频文件。
func IsVideo(name string) bool { return VideoExts[strings.ToLower(filepath.Ext(name))] }

// IsSubtitle 判断是否字幕文件。
func IsSubtitle(name string) bool { return SubExts[strings.ToLower(filepath.Ext(name))] }

// IsImage 判断是否图片。
func IsImage(name string) bool { return ImageExts[strings.ToLower(filepath.Ext(name))] }

// IsNFO 判断是否 NFO。
func IsNFO(name string) bool { return strings.ToLower(filepath.Ext(name)) == ".nfo" }

// FindSeriesDir 向上回溯，找到剧集所在根目录。
func FindSeriesDir(path, libRoot string) string {
	dir := filepath.Dir(path)
	for {
		parent := filepath.Dir(dir)
		if dir == parent || len(dir) <= len(libRoot) {
			return dir
		}
		// 若父目录中含 tvshow.nfo 或父目录不是季目录，则当前即剧集目录
		base := filepath.Base(dir)
		if ParseSeasonDir(base) == 0 {
			// 当前目录不是季目录：如果父目录是季目录，继续向上
			return dir
		}
		// 当前是季目录，继续向上
		dir = parent
	}
}
