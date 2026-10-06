// Package api Web 静态资源（嵌入 Vue 构建产物）。
package api

import (
        "embed"
        "io/fs"
        "net/http"
        "path/filepath"
        "strings"
)

//go:embed dist
var webFS embed.FS

// staticHandler 单例。
var staticHandler http.Handler

// initStatic 初始化静态资源。
func initStatic() {
        sub, err := fs.Sub(webFS, "dist")
        if err != nil {
                panic(err)
        }
        staticHandler = http.FileServer(http.FS(sub))
}

// StaticHandler 返回 Web UI 静态处理器（SPA 回退 index.html）。
func StaticHandler() http.Handler {
        if staticHandler == nil {
                initStatic()
        }
        sub, err := fs.Sub(webFS, "dist")
        if err != nil {
                panic(err)
        }
        subFS := sub
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                p := strings.TrimPrefix(r.URL.Path, "/")
                if p == "" {
                        p = "index.html"
                }
                if _, err := fs.Stat(subFS, p); err != nil {
                        // SPA 回退
                        r2 := new(http.Request)
                        *r2 = *r
                        r2.URL.Path = "/"
                        http.StripPrefix("/", staticHandler).ServeHTTP(w, r2)
                        return
                }
                if strings.HasPrefix(p, "assets/") {
                        w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
                }
                http.StripPrefix("/", staticHandler).ServeHTTP(w, r)
        })
}

var _ = filepath.Join
