// Package scanner 外挂字幕匹配。
package scanner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 语言标签映射。
var langAliases = map[string]string{
	"chs": "chi", "sc": "chi", "zh": "chi", "zho": "chi", "cn": "chi", "gb": "chi",
	"cht": "chi", "tc": "chi", "big5": "chi", "hk": "chi", "tw": "chi",
	"eng": "eng", "en": "eng", "english": "eng",
	"jpn": "jpn", "ja": "jpn", "jp": "jpn",
	"kor": "kor", "ko": "kor", "kr": "kor",
	"fre": "fre", "fr": "fre", "fra": "fre",
	"ger": "ger", "de": "ger", "deu": "ger",
	"spa": "spa", "es": "spa",
	"rus": "rus", "ru": "rus",
	"und": "und",
}

// Format 字幕格式展示名。
func Format(ext string) string {
	switch strings.ToLower(ext) {
	case ".srt": return "srt"
	case ".ass": return "ass"
	case ".ssa": return "ssa"
	case ".sup": return "pgs"
	case ".vtt": return "vtt"
	case ".sub": return "sub"
	}
	return strings.TrimPrefix(strings.ToLower(ext), ".")
}

var (
	langSuffix  = regexp.MustCompile(`(?i)[._\s-]+((?:chs|cht|sc|tc|zh|zho|cn|gb|en|eng|jp|jpn|ja|ko|kor|kr|fr|fre|fra|de|ger|deu|es|spa|ru|rus|big5|gbk)(?:[-_.](?:chs|cht|sc|tc|simplified|traditional))?)`)
	forcedTag   = regexp.MustCompile(`(?i)[._\s-]+forced`)
	simpleTag   = regexp.MustCompile(`(?i)(简体|繁体|简中|繁中|中英|中文|双语|英文|chinese|english)`)
	defaultTag  = regexp.MustCompile(`(?i)[._\s-]+default`)
)

// SubEntry 匹配到的外挂字幕。
type SubEntry struct {
	Path     string
	Format   string // srt/ass/...
	Language string
	Title    string
	IsForced bool
	IsDefault bool
}

// MatchSubs 为视频文件匹配同目录外挂字幕。
// 规则：<视频名>[.lang][.forced].ext，且文件名中含视频名前缀即视为匹配。
func MatchSubs(videoPath string) []SubEntry {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SubEntry
	lowerBase := strings.ToLower(base)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fn := e.Name()
		ext := strings.ToLower(filepath.Ext(fn))
		if !SubExts[ext] {
			continue
		}
		stem := fn[:len(fn)-len(ext)]
		lowerStem := strings.ToLower(stem)
		if !strings.HasPrefix(lowerStem, lowerBase) {
			continue
		}
		rest := lowerStem[len(lowerBase):]
		// rest 应该为空或以分隔符/语言开始
		if rest != "" && rest[0] != '.' && rest[0] != '-' && rest[0] != '_' && rest[0] != ' ' {
			continue
		}
		se := SubEntry{Path: filepath.Join(dir, fn), Format: Format(ext), IsDefault: false}
		// 语言
		if m := langSuffix.FindStringSubmatch(stem); m != nil {
			code := strings.ToLower(m[1])
			if l, ok := langAliases[code]; ok {
				se.Language = l
			} else {
				se.Language = code
			}
		} else if simpleTag.MatchString(stem) {
			se.Language = "chi"
		}
		if se.Language == "" {
			se.Language = "und"
		}
		se.IsForced = forcedTag.MatchString(stem)
		if defaultTag.MatchString(stem) {
			se.IsDefault = true
		}
		// 标题描述
		if m := simpleTag.FindString(stem); m != "" {
			se.Title = m
		}
		out = append(out, se)
	}
	// 默认：中文优先
	if len(out) > 0 {
		best := 0
		for i, s := range out {
			if s.IsDefault {
				best = i
				break
			}
			if s.Language == "chi" && out[best].Language != "chi" {
				best = i
			}
		}
		out[best].IsDefault = true
	}
	return out
}

// DisplayName 字幕显示名。
func DisplayName(s SubEntry) string {
	name := map[string]string{"chi": "中文", "eng": "英语", "jpn": "日语", "kor": "韩语", "und": ""}[s.Language]
	if name == "" {
		name = strings.ToUpper(s.Language)
	}
	t := s.Title
	if t == "" {
		t = strings.ToUpper(s.Format)
	}
	if s.IsForced {
		t += "（强制）"
	}
	if name != "" && t != "" {
		return name + " " + t
	}
	return t
}
