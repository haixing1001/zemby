// Package api 文件管理（浏览 / 重命名 / 删除 / 新建 / 上传）。
package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-emby/internal/tmdb"
)

// fileInfo 文件信息。
type fileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// fileList GET /admin/files?path=
func (a *App) fileList(w http.ResponseWriter, r *http.Request) {
	path := q(r, "path")
	if path == "" {
		// 返回根目录列表
		roots := []fileInfo{}
		for _, root := range a.cfg.MediaRoots {
			fi, err := os.Stat(root)
			if err != nil {
				continue
			}
			roots = append(roots, fileInfo{Name: root, Path: root, IsDir: true, ModTime: fi.ModTime()})
		}
		a.json(w, 200, M{"Path": "", "Directories": roots, "Files": []any{}, "Roots": a.cfg.MediaRoots})
		return
	}
	if !a.pathAllowed(path) {
		a.fail(w, 403, "路径不在允许的媒体目录内")
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		a.fail(w, 404, "目录不存在")
		return
	}
	if !fi.IsDir() {
		a.fail(w, 400, "不是目录")
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		a.fail(w, 403, "无法读取目录")
		return
	}
	var dirs, files []fileInfo
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "@eaDir" || name == "#recycle" {
			continue
		}
		full := filepath.Join(path, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		fi := fileInfo{Name: name, Path: full, IsDir: e.IsDir(), ModTime: info.ModTime()}
		if !e.IsDir() {
			fi.Size = info.Size()
		}
		if e.IsDir() {
			dirs = append(dirs, fi)
		} else {
			files = append(files, fi)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	a.json(w, 200, M{"Path": path, "Directories": dirs, "Files": files})
}

// fileOp POST /admin/files/op  {Action, Path, Arg}
func (a *App) fileOp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"Action"` // mkdir | rename | delete
		Path   string `json:"Path"`
		Arg    string `json:"Arg"`
	}
	if err := bodyJSON(r, &body); err != nil {
		a.fail(w, 400, "请求体格式错误")
		return
	}
	if !a.pathAllowed(body.Path) {
		a.fail(w, 403, "路径不在允许的媒体目录内")
		return
	}
	switch strings.ToLower(body.Action) {
	case "mkdir":
		if body.Arg == "" {
			a.fail(w, 400, "缺少目录名")
			return
		}
		target := filepath.Join(body.Path, safeName(body.Arg))
		if err := os.MkdirAll(target, 0o755); err != nil {
			a.fail(w, 500, "创建目录失败")
			return
		}
		logxInfo("文件管理：创建目录 %s", target)
		a.json(w, 200, M{"OK": true})
	case "rename":
		if body.Arg == "" {
			a.fail(w, 400, "缺少新名称")
			return
		}
		if a.isMediaRoot(body.Path) {
			a.fail(w, 400, "不能重命名媒体根目录")
			return
		}
		target := filepath.Join(filepath.Dir(body.Path), safeName(body.Arg))
		if err := os.Rename(body.Path, target); err != nil {
			a.fail(w, 500, "重命名失败")
			return
		}
		logxInfo("文件管理：重命名 %s → %s", body.Path, target)
		a.json(w, 200, M{"OK": true})
	case "delete":
		if a.isMediaRoot(body.Path) {
			a.fail(w, 400, "不能删除媒体根目录")
			return
		}
		if err := os.RemoveAll(body.Path); err != nil {
			a.fail(w, 500, "删除失败")
			return
		}
		logxInfo("文件管理：删除 %s", body.Path)
		a.json(w, 200, M{"OK": true})
	default:
		a.fail(w, 400, "未知操作")
	}
}

// fileUpload POST /admin/files/upload?path=
func (a *App) fileUpload(w http.ResponseWriter, r *http.Request) {
	dir := q(r, "path")
	if dir == "" || !a.pathAllowed(dir) {
		a.fail(w, 403, "路径不在允许的媒体目录内")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		a.fail(w, http.StatusRequestEntityTooLarge, "上传内容超过大小上限或格式无效")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		a.fail(w, 400, "缺少文件")
		return
	}
	defer file.Close()
	target := filepath.Join(dir, safeName(header.Filename))
	out, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		a.fail(w, 500, "写入失败")
		return
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err := io.Copy(out, file); err != nil {
		_ = out.Close()
		a.fail(w, 500, "写入失败")
		return
	}
	if err := out.Close(); err != nil {
		a.fail(w, 500, "写入失败")
		return
	}
	if err := os.Rename(tmp, target); err != nil {
		a.fail(w, 500, "保存上传文件失败")
		return
	}
	logxInfo("文件管理：上传 %s（%d 字节）", target, header.Size)
	a.json(w, 200, M{"OK": true, "Path": target})
}

// safeName 防路径穿越。
func safeName(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "." || name == ".." || name == "/" || name == "" {
		return "_"
	}
	return name
}

// tmdbSettings 读取 TMDB 设置。
func tmdbSettings() (s tmdbSettingsType) {
	return tmdb.LoadSettings()
}

// saveTMDBSettings 保存。
func saveTMDBSettings(s tmdbSettingsType) error {
	return tmdb.SaveSettings(s)
}

// logxSnapshot 日志快照。
func logxSnapshot(n int) []logEntryAlias {
	return snapshotLogs(n)
}

var _ = fmt.Sprintf
