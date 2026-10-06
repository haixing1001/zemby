# Go Emby Server

Go 语言编写的 **Emby 兼容媒体服务器**。提供 Web 管理界面与常用媒体库管理能力，播放采用**重定向 / 直连媒体源**方式，不进行任何视频转码。

> 本项目参考 [sd87671067/go-emby](https://github.com/sd87671067/go-emby) 的接口兼容思路重新实现，技术栈为 Go + SQLite（纯 Go 驱动，无 CGO）+ Vue3。

## 功能特性

- **Emby 兼容接口**：实现标准 Emby REST API（`/emby` 前缀可选），Infuse、Fileball、Emby 官方客户端等均可直连
- **Web 媒体库**：海报墙、影视详情、季/集浏览、网页播放器、继续观看、最近添加
- **媒体库管理**：全量扫描、增量刷新、目录管理，支持电影库与剧集库两种类型
- **元数据读取**：Kodi 风格 NFO（`movie.nfo` / `tvshow.nfo` / `<文件名>.nfo`）、本地海报（`poster.jpg` / `folder.jpg` / `<名>-poster.jpg` 等）
- **媒体信息提取**：调用 `ffprobe` 提取编码、分辨率、音轨、字幕轨、时长、HDR 信息等
- **TMDB 刮削**：后台配置 API Key 后自动刮削电影/剧集元数据，下载海报与背景图，支持语言切换
- **外挂字幕增强**：自动匹配 `<视频名>[.语言][.forced].srt/.ass/.ssa/.sup` 字幕，语言识别（chs/cht/eng…），网页播放器可直接挂载
- **用户与权限**：多用户、bcrypt 密码、播放权限开关、**同时播放设备数量限制**（180 秒租约心跳）
- **文件管理**：浏览器内浏览 / 重命名 / 删除 / 新建目录 / 上传，路径限制在媒体根目录内
- **实时日志**：SSE 实时推送服务端日志（扫描 / 播放 / 错误），后台可视查看
- **部署**：Docker Compose 一键部署，GitHub Actions 自动构建 **amd64 / arm64 双架构镜像**
- **播放策略**：本地文件以 HTTP Range 直连，远程流（`.strm` 内容为 URL）302 重定向直连，转码请求一律拒绝

## 快速开始（Docker Compose）

```bash
mkdir -p go-emby && cd go-emby
# 下载 compose 文件（或直接使用仓库中的 docker-compose.yml）
curl -fLO https://raw.githubusercontent.com/your-name/go-emby/main/docker-compose.yml

# 准备媒体目录
mkdir -p media config

# 启动
docker compose up -d
```

访问 `http://服务器IP:8097`，默认账号 `admin`，默认密码 `admin123`（可用环境变量 `GEMBY_ADMIN_PASSWORD` 修改，**请尽快在后台修改密码**）。

登录后进入「后台管理 → 媒体库」添加媒体目录（如 `/media/movies`），创建后自动开始扫描；在「后台管理 → TMDB 刮削」填入 [TMDB API Key](https://www.themoviedb.org/settings/api) 即可自动刮削。

### 配置说明

| 环境变量 | 说明 | 默认值 |
| --- | --- | --- |
| `GEMBY_ADDR` / `HTTP_PORT` | 监听地址 / 端口 | `:8097` |
| `GEMBY_DATA` | 数据目录（数据库 / 元数据 / 图片） | `/config` |
| `MEDIA_ROOTS` | 允许访问的媒体根目录（逗号分隔，扫描与文件管理都限制在其内） | `/media` |
| `GEMBY_SERVER_NAME` | 服务器名称 | `Go Emby Server` |
| `GEMBY_ADMIN_PASSWORD` | 首次启动的 admin 密码 | `admin123` |
| `DEVICE_LEASE_SECONDS` | 设备租约秒数（超过无心跳视为离线） | `180` |
| `TZ` | 时区 | - |

### 目录结构

```text
go-emby/
├── docker-compose.yml
├── config/          # data.db + metadata/（TMDB 下载的图片）
└── media/           # 媒体文件（可挂载任意宿主机目录）
```

## 媒体库组织建议

电影库：

```text
/movies
├── The.Matrix.1999.1080p.BluRay.x264.mkv
├── 黑客帝国 (1999)/
│   ├── movie.nfo            # 可选：本地元数据
│   ├── poster.jpg           # 可选：本地海报
│   └── The.Matrix.1999.mkv
└── 沙丘 (2021)/dune.strm    # strm 内容为一行 http(s) 直链，播放时 302 重定向
```

剧集库：

```text
/tv
└── 白莲花度假村/
    ├── tvshow.nfo           # 剧集元数据
    ├── poster.jpg
    ├── Season 1/
    │   ├── White.Lotus.S01E01.1080p.mkv
    │   ├── White.Lotus.S01E01.chs.ass   # 外挂字幕（自动匹配）
    │   └── White.Lotus.S01E01.eng.srt
    └── Season 2/
        └── ...
```

NFO 支持 `title / originaltitle / plot / rating / year / runtime / genre* / studio* / actor / director / mpaa / uniqueid(type=tmdb) / tmdbid / fileinfo>streamdetails` 等字段；含 `tmdbid` 的 NFO 将跳过搜索直接拉取详情。

## Emby 客户端连接

任何支持 Emby 的客户端均可连接：

1. 服务器地址：`http://服务器IP:8097`
2. 账号密码：即本服务器的用户

已适配的客户端关键调用：`System/Info/Public` 探测、`AuthenticateByName` 登录（兼容 JSON 与表单）、`Users/{id}/Views` 首页媒体库、`Items` 海报墙查询（SortBy/Filters/Genres/SearchTerm/分页）、`Shows/{id}/Seasons|Episodes` 剧集导航、`PlaybackInfo` 播放协商（仅 DirectPlay）、`Videos/{id}/stream` 直连、`Sessions/Playing/*` 进度上报、`UserData` 已看/收藏标记。

## 从源码构建

依赖：Go 1.23+、Node.js 20+（前端构建）、ffprobe（运行时，可选）

```bash
# 1. 构建前端
cd web && npm install && npm run build && cd ..

# 2. 构建后端（嵌入 web 产物）
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o go-emby .

# 3. 运行
GEMBY_DATA=./data MEDIA_ROOTS=./media ./go-emby
```

无 ffprobe 时服务正常运行，仅媒体信息（编码/分辨率/音轨）不可用；NFO 中的 streamdetails 仍会作为兜底。

## Docker 多架构镜像

GitHub Actions 工作流（`.github/workflows/docker.yml`）在 push 到 `main` 或打 tag 时自动构建 `linux/amd64` 与 `linux/arm64` 镜像并推送到 GHCR。构建使用 `CGO_ENABLED=0` 纯静态编译（SQLite 驱动为纯 Go 实现），无需目标平台 QEMU 编译工具链。

手动构建多架构镜像：

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t your/go-emby:latest --push .
```

## 项目结构

```text
go-emby/
├── main.go                    # 入口：配置、数据库、路由装配
├── internal/
│   ├── config/                # 环境变量配置
│   ├── db/                    # SQLite（GORM + 纯 Go 驱动）初始化与迁移
│   ├── models/                # 数据模型（用户/媒体库/条目/媒体源/流/播放状态…）
│   ├── auth/                  # 登录、令牌（SHA-256 落库）、X-Emby-Authorization 解析
│   ├── logx/                  # 环形日志缓冲 + SSE 广播
│   ├── scanner/               # 扫描引擎：全量/增量、电影/剧集识别、NFO/图片/字幕
│   ├── probe/                 # ffprobe 封装与流信息映射
│   ├── tmdb/                  # TMDB 客户端（搜索/详情/季/图片下载）
│   ├── api/                   # Emby 兼容 API + 管理后台 API + 嵌入式 Web
│   │   ├── router.go          #   路由分发（/emby 前缀可选）
│   │   ├── dto.go             #   BaseItemDto / MediaSourceInfo / MediaStream
│   │   ├── items.go           #   条目查询引擎
│   │   ├── play.go            #   PlaybackInfo / 直连流 / 302 重定向
│   │   ├── system.go          #   认证 / 用户 / 会话
│   │   ├── library.go         #   媒体库 / TMDB 设置 / 扫描
│   │   ├── filemgr.go         #   文件管理
│   │   └── dist/              #   Vue 构建产物（go:embed）
│   └── ...
└── web/                       # Vue3 + Vite 前端源码
    └── src/
        ├── views/             # 登录 / 首页 / 海报墙 / 详情 / 播放器 / 后台管理
        └── api/client.js      # API 客户端
```

## 设计说明

- **不转码**：`MediaSourceInfo.SupportsTranscoding` 恒为 false，用户策略禁用转码位，`/Videos/{id}/master.m3u8` 等转码端点返回 400；播放信息中提供 `DirectStreamUrl`，客户端自动选择 Direct Play
- **设备限制**：播放时按 `用户|设备` 记录租约（默认 180 秒，Progress 上报即心跳），超过用户设备上限返回 403；`/Sessions/Playing/Stopped` 释放设备
- **令牌安全**：AccessToken 仅返回一次，数据库只存 SHA-256；登录支持 JSON 与 `application/x-www-form-urlencoded`（兼容老客户端）
- **增量扫描**：以文件 `mtime + size` 判定变化，未变化文件仅轻量刷新字幕与本地图片；文件消失时级联清理条目/媒体源/流/播放状态
- **纯 Go 依赖**：SQLite 使用 `modernc.org` 系纯 Go 驱动，全项目 `CGO_ENABLED=0` 可构建，天然支持交叉编译多架构镜像
