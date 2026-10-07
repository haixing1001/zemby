// go-emby：Go 语言编写的 Emby 兼容媒体服务器。
// 播放采用直连/重定向方式，不进行视频转码。
package main

import (
        "fmt"
        "net"
        "net/http"
        "os"
        "path/filepath"
        "strings"

        "go-emby/internal/api"
        "go-emby/internal/config"
        "go-emby/internal/db"
        "go-emby/internal/logx"
        "go-emby/internal/probe"
        "go-emby/internal/scanner"
)

func main() {
        cfg := config.Load()
        // 初始化目录
        for _, d := range []string{cfg.DataDir, cfg.MetaDir()} {
                if err := os.MkdirAll(d, 0o755); err != nil {
                        fmt.Fprintf(os.Stderr, "创建目录失败 %s: %v\n", d, err)
                        os.Exit(1)
                }
        }
        if len(cfg.MediaRoots) > 0 {
                for _, r := range cfg.MediaRoots {
                        _ = os.MkdirAll(r, 0o755)
                }
        }

        logx.Info("Go Emby Server 启动中（数据目录 %s）", cfg.DataDir)

        // 数据库
        if err := db.Open(cfg.DBPath()); err != nil {
                fmt.Fprintf(os.Stderr, "数据库初始化失败: %v\n", err)
                os.Exit(1)
        }
        logx.Info("数据库就绪: %s", filepath.Base(cfg.DBPath()))

        // ffprobe
        if probe.HasFFprobe() {
                logx.Info("检测到 ffprobe，媒体信息提取已启用")
        } else {
                logx.Warn("未检测到 ffprobe，媒体信息提取将不可用（请安装 ffmpeg）")
        }

        // 扫描器
        sc := scanner.New(cfg)

        // API
        app := api.New(cfg, db.DB, sc)

        // 路由：API 走鉴权处理器，静态资源直接放行（SPA 自带登录页）
        mux := http.NewServeMux()
        apiHandler := app.Handler()
        mux.Handle("/emby/", apiHandler)
        mux.Handle("/emby", apiHandler)
        mux.Handle("/health", apiHandler)
        mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                p := strings.TrimPrefix(r.URL.Path, "/emby")
                p = strings.TrimPrefix(p, "/")
                lower := strings.ToLower(p)
                apiPrefixes := []string{
                        "system", "users", "items", "shows", "library", "libraries", "sessions",
                        "videos", "audio", "persons", "genres", "movies", "displaypreferences",
                        "search", "branding", "auth", "admin", "api",
                }
                isAPI := false
                if p != "" {
                        if lower == "admin" {
                                // SPA 后台页面（/admin 精确路径），API 为 /admin/*
                                isAPI = false
                        } else {
                                for _, pre := range apiPrefixes {
                                        if lower == pre || strings.HasPrefix(lower, pre+"/") {
                                                isAPI = true
                                                break
                                        }
                                }
                                if strings.Contains(lower, "/images/") {
                                        isAPI = true
                                }
                        }
                }
                // 浏览器文档导航（刷新/直达 SPA 路由，如 /library/{id}）回退静态页：
                // 以 Sec-Fetch-Mode/Sec-Fetch-Dest 为主信号（现代浏览器刷新必带、fetch 永不带），
                // Accept: text/html 为老浏览器兜底；fetch/XHR API 调用不受影响。
                // 否则 SPA 路由与 API 前缀同名（library）时，刷新会被鉴权中间件拦成 401「请先登录」
                if isAPI && api.IsDocumentNav(r) {
                        isAPI = false
                }
                if isAPI {
                        apiHandler.ServeHTTP(w, r)
                        return
                }
                api.StaticHandler().ServeHTTP(w, r)
        }))

        addr := cfg.Addr
        ln, err := net.Listen("tcp", addr)
        if err != nil {
                fmt.Fprintf(os.Stderr, "监听失败 %s: %v\n", addr, err)
                os.Exit(1)
        }
        logx.Info("服务已启动: http://%s （账号 admin）", ln.Addr().String())
        if err := http.Serve(ln, mux); err != nil {
                fmt.Fprintf(os.Stderr, "服务器退出: %v\n", err)
                os.Exit(1)
        }
}
