# Go Emby Server

Go 语言编写的 **Emby 兼容媒体服务器**。提供 Web 管理界面与常用媒体库管理能力，播放采用**重定向 / 直连媒体源**方式，不进行任何视频转码。

> 本项目参考 [sd87671067/go-emby](https://github.com/sd87671067/go-emby) 的接口兼容思路重新实现，技术栈为 Go + SQLite（纯 Go 驱动，无 CGO）+ Vue3。

## 功能特性

- **Emby 兼容接口**：实现标准 Emby REST API（`/emby` 前缀可选），Infuse、Fileball、Emby 官方客户端等均可直连
- **Web 媒体库**：海报墙、影视详情、季/集浏览、网页播放器、继续观看、最近添加
- **媒体库管理**：全量扫描、增量刷新、目录管理，支持电影库与剧集库两种类型
- **元数据读取**：Kodi 风格 NFO（`movie.nfo` / `tvshow.nfo` / `<文件名>.nfo`）、本地海报（`poster.jpg` / `folder.jpg` / `<名>-poster.jpg` 等）
- **媒体信息提取**：调用 `ffprobe` 提取编码、分辨率、音轨、字幕轨、时长、HDR 信息等
- **TMDB 刮削**：后台配置 API Key 后自动刮削电影/剧集元数据（含演职员表，本地 NFO 已有演员时优先保留），下载海报与背景图，支持语言切换
- **AI 识别辅助**：TMDB 标题搜索（有年份及无年份）均无结果时调用 OpenAI 兼容模型，抽取名称/原名/类型后重搜；**可保存多个 AI 供应商并随时切换使用**；仅发送文件名与最近两级目录
- **外挂字幕增强**：自动匹配 `<视频名>[.语言][.forced].srt/.ass/.ssa/.sup` 字幕，语言识别（chs/cht/eng…），网页播放器可直接挂载
- **用户与权限**：多用户、bcrypt 密码、播放权限开关、**同时播放设备数量限制**（180 秒租约心跳）
- **文件管理**：浏览器内浏览 / 重命名 / 删除 / 新建目录 / 上传，路径限制在媒体根目录内
- **实时日志**：SSE 实时推送服务端日志（扫描 / 播放 / 错误），后台可视查看
- **部署**：Docker Compose 一键部署，GitHub Actions 自动构建 **amd64 / arm64 双架构镜像**
- **播放策略**：本地文件以 HTTP Range 直连，远程流（`.strm` 内容为 URL）302 重定向直连，转码请求一律拒绝
- **MoviePilot 风格管理后台**：侧边栏式控制台，包含控制台、媒体管理、文件管理、刮削管理、用户管理、媒体排序、媒体信息、增强功能（设置/字幕/TMDB）与 API 文档页

## 管理后台功能

| 页面 | 功能 |
|------|------|
| 控制台 | 电影/电视剧/剧集/用户统计、进程 CPU/内存/运行时长、正在播放、任务状态、播放活跃记录 |
| 媒体管理 | 媒体库卡片与操作菜单：全量扫描 / 刷新扫描 / 添加媒体文件夹 / 插入封面 / ffmpeg 生成封面 / 移除封面 / 隐藏 / 重命名 / 删除 |
| 文件管理 | 面包屑导航、搜索、排序、上传、新建文件夹、重命名、删除 |
| 刮削管理 | 开启刮削 / 实时监控（每 2 分钟增量）/ 自动刷新 / 手动刮削开关，覆盖策略（跳过已有 / 覆盖），任务控制（扫描 / 开始 / 暂停 / 停止），**刮削失败清单与一键重试**，SSE 实时任务日志 |
| 用户管理 | 用户增删、播放权限、同时播放设备上限、改密 |
| 媒体排序 | 按媒体库设置海报墙默认排序（名称 / 添加时间 / 年份 / 评分 × 升降序），对全站生效 |
| 媒体信息 | 浏览时提取 / 预加载下一集 / 持久化媒体信息开关，批量提取（进度统计：等待 / 提取中 / 完成 / 跳过 / 失败），提取并发 1-8 可调 |
| 增强功能 | 服务器名称与密码修改、外挂字幕清单、TMDB 刮削器配置；Bot / 片头片尾 / 代理 预留开发中 |
| API | Emby 兼容接口分组文档与调用示例 |

## 快速开始（Docker Compose）

```bash
git clone https://github.com/haixing1001/zemby.git
cd zemby

# 准备媒体目录
mkdir -p media config

# 首次启动前，在 .env 中设置至少 12 个字符的管理员密码：ADMIN_PASSWORD=你的强密码
docker compose up -d
```

访问 `http://服务器IP:8097`，管理员账号为 `admin`，密码为首次启动前设置的 `ADMIN_PASSWORD`。如果直接运行二进制文件，则首次启动前必须设置 `GEMBY_ADMIN_PASSWORD`（至少 12 个字符）；密码不会写入日志。

登录后进入「后台管理 → 媒体库」添加媒体目录（如 `/media/movies`），创建后自动开始扫描；在「后台管理 → TMDB 刮削」填入 [TMDB API Key](https://www.themoviedb.org/settings/api) 即可自动刮削。若服务器无法直连 TMDB，可在同页分别填写 API 与图片镜像基础地址；图片镜像支持普通 TMDB 路径前缀和带 `url` 参数的代理（例如 `https://wsrv.nl/?url=https://image.tmdb.org`），留空时使用官方地址。

### 配置说明

| 环境变量 | 说明 | 默认值 |
| --- | --- | --- |
| `GEMBY_ADDR` / `HTTP_PORT` | 监听地址 / 端口 | `:8097` |
| `GEMBY_DATA` | 数据目录（数据库 / 元数据 / 图片） | `/config` |
| `MEDIA_ROOTS` | 允许访问的媒体根目录（逗号分隔，扫描与文件管理都限制在其内） | `/media` |
| `GEMBY_SERVER_NAME` | 服务器名称 | `Go Emby Server` |
| `GEMBY_ADMIN_PASSWORD` | 首次启动必填的 admin 密码（至少 12 个字符） | 无默认值 |
| `DEVICE_LEASE_SECONDS` | 设备租约秒数（超过无心跳视为离线） | `180` |
| `GEMBY_MAX_UPLOAD_BYTES` | 文件管理单次上传上限（字节） | `21474836480`（20 GiB） |
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

依赖：Go 1.27+、Node.js 24+（前端构建）、ffprobe（运行时，可选）

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

GitHub Actions 工作流（`.github/workflows/docker.yml`）在 push 到 `main`、`codex` 或打 tag 时自动构建 `linux/amd64` 与 `linux/arm64` 镜像并推送到 GHCR。也可以在 GitHub Actions 页面手动运行该工作流；分支运行会生成同名镜像标签（例如 `codex`），默认分支生成 `latest`。构建使用 `CGO_ENABLED=0` 纯静态编译（SQLite 驱动为纯 Go 实现），无需目标平台 QEMU 编译工具链。

Compose 默认拉取 `latest`，可通过 `.env` 中的 `IMAGE_TAG=codex` 固定到 codex 分支镜像。容器停止时会先给服务器最多 30 秒完成优雅关闭和正在传输的请求。

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
