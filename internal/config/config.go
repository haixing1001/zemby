// Package config 负责服务端配置加载。
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config 全局配置。
type Config struct {
	// HTTP 监听地址
	Addr string
	// 数据目录（数据库、元数据、图片缓存都在这里）
	DataDir string
	// 允许访问的媒体根目录（逗号分隔），文件管理与扫描都限制在其内
	MediaRoots []string
	// 服务器名称
	ServerName string
	// 播放设备租约秒数，超过该时间无心跳视为离线
	DeviceLeaseSeconds int
	// MaxUploadBytes 请求文件管理上传的上限，防止无界占用磁盘。
	MaxUploadBytes int64
}

// Load 从环境变量加载配置。
func Load() *Config {
	c := &Config{
		Addr:               env("GEMBY_ADDR", ":"+env("HTTP_PORT", "8097")),
		DataDir:            env("GEMBY_DATA", "/config"),
		ServerName:         env("GEMBY_SERVER_NAME", "Go Emby Server"),
		DeviceLeaseSeconds: 180,
		MaxUploadBytes:     20 << 30,
	}
	if n, err := strconv.Atoi(env("DEVICE_LEASE_SECONDS", "180")); err == nil && n >= 30 {
		c.DeviceLeaseSeconds = n
	}
	if n, err := strconv.ParseInt(env("GEMBY_MAX_UPLOAD_BYTES", ""), 10, 64); err == nil && n > 0 {
		c.MaxUploadBytes = n
	}
	roots := env("MEDIA_ROOTS", "/media")
	for _, r := range splitList(roots) {
		if abs, err := filepath.Abs(r); err == nil {
			c.MediaRoots = append(c.MediaRoots, abs)
		}
	}
	if c.ServerName == "" {
		c.ServerName = "Go Emby Server"
	}
	return c
}

// DBPath 返回 SQLite 数据库文件路径。
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "data.db") }

// MetaDir 返回元数据（下载的 TMDB 图片等）目录。
func (c *Config) MetaDir() string { return filepath.Join(c.DataDir, "metadata") }

// inRoots 判断路径是否位于允许的媒体根目录下。
func (c *Config) inRoots(path string) bool {
	if len(c.MediaRoots) == 0 {
		return true
	}
	for _, r := range c.MediaRoots {
		if r == path || (len(path) > len(r) && path[:len(r)] == r && path[len(r)] == filepath.Separator) {
			return true
		}
	}
	return false
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	cur := ""
	for _, part := range splitAny(s, ",;") {
		part = trimSpace(part)
		if part != "" && part != cur {
			out = append(out, part)
			cur = part
		}
	}
	return out
}

func splitAny(s, seps string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if containsByte(seps, s[i]) {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func containsByte(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
