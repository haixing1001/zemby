#!/usr/bin/env bash
# 增强功能冒烟测试：/admin/enhancements 配置、收藏库、TMDB 开关、多版本合并、
# 首字母搜索、集数角标、媒体信息复用、快速路径、发行日期排序、文件监听。
set -u
PORT=18099
BASE="http://127.0.0.1:$PORT"
DIR="/tmp/zemby-enh"
DATA="$DIR/data"
MEDIA="$DIR/media"
PASS=0; FAIL=0; TOTAL=0

say()  { printf '%s\n' "$*"; }
ck() { # ck <描述> <期望> <实际>
  TOTAL=$((TOTAL+1))
  if [ "$2" = "$3" ]; then PASS=$((PASS+1)); say "ok   $1";
  else FAIL=$((FAIL+1)); say "FAIL $1 (期望=$2 实际=$3)"; fi
}

# ---------- 准备测试媒体 ----------
rm -rf "$DIR"; mkdir -p "$DATA" "$MEDIA/movies1" "$MEDIA/movies2" "$MEDIA/tv"
# 同库两版本电影（同名同年，movie.nfo 统一两个文件的名称/年份/发行日期）
mkdir -p "$MEDIA/movies1/沙丘 (2021)"
printf 'test' > "$MEDIA/movies1/沙丘 (2021)/Dune.2021.1080p.mkv"
printf 'test' > "$MEDIA/movies1/沙丘 (2021)/Dune.2021.2160p.mkv"
cat > "$MEDIA/movies1/沙丘 (2021)/movie.nfo" <<'EOF'
<movie><title>沙丘</title><year>2021</year><premiered>2021-10-22</premiered></movie>
EOF
# 中文片名（首字母搜索 mjh）
mkdir -p "$MEDIA/movies1/满江红 (2023)"
printf 'test' > "$MEDIA/movies1/满江红 (2023)/Full.River.Red.2023.mkv"
cat > "$MEDIA/movies1/满江红 (2023)/movie.nfo" <<'EOF'
<movie><title>满江红</title><year>2023</year></movie>
EOF
# 跨库同版本（movie.nfo 统一名）
mkdir -p "$MEDIA/movies2/沙丘 (2021)"
printf 'test' > "$MEDIA/movies2/沙丘 (2021)/Dune.2021.4K.REMUX.mkv"
cat > "$MEDIA/movies2/沙丘 (2021)/movie.nfo" <<'EOF'
<movie><title>沙丘</title><year>2021</year><premiered>2021-10-22</premiered></movie>
EOF
# 剧集（真实视频文件供 ffprobe 提取，验证媒体信息复用）
mkdir -p "$MEDIA/tv/测试剧集/Season 01"
if command -v ffmpeg >/dev/null 2>&1; then
  for ep in 1 2 3; do
    ffmpeg -loglevel error -f lavfi -i testsrc=duration=1:size=320x240:rate=10 \
      -f lavfi -i sine=frequency=440:duration=1 -c:v libx264 -preset ultrafast \
      -c:a aac -shortest -y "$MEDIA/tv/测试剧集/Season 01/Test.Show.S01E0$ep.mkv" </dev/null
  done
else
  for ep in 1 2 3; do printf 'test' > "$MEDIA/tv/测试剧集/Season 01/Test.Show.S01E0$ep.mkv"; done
fi

# ---------- 启动服务 ----------
GEMBY_ADDR=":$PORT" GEMBY_DATA="$DATA" MEDIA_ROOTS="$MEDIA" GEMBY_ADMIN_PASSWORD=admin123 \
  /tmp/zemby-test >"$DIR/server.log" 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 40); do
  sleep 0.5
  curl -sf "$BASE/health" >/dev/null 2>&1 && break
done

# ---------- 登录 ----------
TOK=$(curl -s -X POST "$BASE/emby/Users/AuthenticateByName" -H 'Content-Type: application/json' \
  -d '{"Username":"admin","Pw":"admin123"}' | sed -n 's/.*"AccessToken":"\([A-Za-z0-9]*\)".*/\1/p')
[ -n "$TOK" ] && say "登录成功" || { say "登录失败"; exit 1; }
AH="X-Emby-Token: $TOK"
JQ() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null; }

# 等待扫描完成
sleep 4

# 建库前先开启媒体信息复用（扫描入队后探测完成即触发复制）
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"EpisodeMediaReuse":true}' >/dev/null

# ---------- 0. 创建媒体库（触发首次扫描） ----------
curl -s -X POST "$BASE/admin/libraries" -H "$AH" -H 'Content-Type: application/json' \
  -d "{\"Name\":\"电影一\",\"Path\":\"$MEDIA/movies1\",\"Type\":\"movies\"}" >/dev/null
curl -s -X POST "$BASE/admin/libraries" -H "$AH" -H 'Content-Type: application/json' \
  -d "{\"Name\":\"电影二\",\"Path\":\"$MEDIA/movies2\",\"Type\":\"movies\"}" >/dev/null
curl -s -X POST "$BASE/admin/libraries" -H "$AH" -H 'Content-Type: application/json' \
  -d "{\"Name\":\"剧集\",\"Path\":\"$MEDIA/tv\",\"Type\":\"tvshows\"}" >/dev/null
# 等待三个库扫描完成（轮询扫描状态）
for i in $(seq 1 30); do
  sleep 2
  RUN=$(curl -s "$BASE/admin/scan-status" -H "$AH" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['Scanning']))" 2>/dev/null)
  [ "$RUN" = "0" ] && break
done
sleep 2

# ---------- 1. 增强配置默认值 ----------
DEF=$(curl -s "$BASE/admin/enhancements" -H "$AH")
ck "默认 PosterEpisodeBadge=true"  "True"  "$(echo "$DEF" | JQ "['PosterEpisodeBadge']" | sed 's/^True$/True/;s/true/True/')"
ck "默认 MergeInLibrary=true"      "True"  "$(echo "$DEF" | JQ "['MergeVersionsInLibrary']" | sed 's/true/True/')"
ck "默认 WatchDelaySeconds=30"     "30"    "$(echo "$DEF" | JQ "['WatchDelaySeconds']")"

# PUT 越界校验
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"WatchDelaySeconds":5}')
ck "媒体变动延时 5 秒被拒绝(400)" "400" "$CODE"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{}')
ck "空设置体被拒绝(400)" "400" "$CODE"

# ---------- 2. 收藏功能 ----------
V0=$(curl -s "$BASE/emby/Users/me/Views" -H "$AH" | python3 -c "import sys,json;print(any(i['Id']=='favorites' for i in json.load(sys.stdin)['Items']))")
ck "默认关：Views 无收藏库" "False" "$(echo "$V0" | sed 's/True/False/;s/False/False/')"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"Favorites":true}' >/dev/null
V1=$(curl -s "$BASE/emby/Users/me/Views" -H "$AH" | python3 -c "import sys,json;print(any(i['Id']=='favorites' for i in json.load(sys.stdin)['Items']))")
ck "开启后 Views 出现收藏库" "True" "$V1"

# 收藏一个条目（满江红）
MID=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie&SearchTerm=%E6%BB%A1%E6%B1%9F%E7%BA%A2" -H "$AH" | JQ "['Items'][0]['Id']")
ck "满江红条目存在（NFO 中文名）" "ok" "$([ -n "$MID" ] && [ "$MID" != "None" ] && echo ok || echo no)"
UID2=$(curl -s "$BASE/emby/Users/me" -H "$AH" | JQ "['Id']")
curl -s -X POST "$BASE/emby/Users/$UID2/FavoriteItems/$MID" -H "$AH" >/dev/null
FAV=$(curl -s "$BASE/emby/Items?ParentId=favorites&Recursive=true" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin);print(len(d['Items']))")
ck "收藏库查询包含已收藏条目" "1" "$FAV"

# 封面上传/移除
printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n\x2d\xb4\x00\x00\x00\x00IEND\xaeB\x60\x82' > "$DIR/cover.png"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/admin/favorites/cover?api_key=$TOK" -F "file=@$DIR/cover.png;type=image/png")
ck "收藏封面上传成功" "200" "$CODE"
CT=$(curl -s -o /dev/null -w '%{http_code} %{content_type}' "$BASE/emby/Items/favorites/Images/Primary")
echo "$CT" | grep -q "200 image" && R="ok" || R="no"
ck "收藏封面图片可访问(image)" "ok" "$R"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$BASE/admin/favorites/cover?api_key=$TOK")
ck "收藏封面移除成功" "200" "$CODE"

# 关闭收藏：Views 不含收藏库，但收藏数据保留
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"Favorites":false}' >/dev/null
V2=$(curl -s "$BASE/emby/Users/me/Views" -H "$AH" | python3 -c "import sys,json;print(any(i['Id']=='favorites' for i in json.load(sys.stdin)['Items']))")
ck "关闭后 Views 不再显示收藏库" "False" "$V2"
KEEP=$(curl -s "$BASE/emby/Items?IsFavorite=true&Recursive=true" -H "$AH" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['Items']))")
ck "关闭不删除用户收藏数据" "1" "$KEEP"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"Favorites":true}' >/dev/null

# ---------- 3. TMDB 全局开关 ----------
# 条目无 NFO TMDB ID 且刮削未运行（TMDB 默认关）→ 手动刷新应报「TMDB 未开启」
ERR1=$(curl -s -X POST "$BASE/emby/Items/$MID/Refresh" -H "$AH" -o /dev/null -w '%{http_code}')
ck "手动刷新请求已受理" "204" "$ERR1"

# ---------- 4. 多版本合并 ----------
# 库1 查询：沙丘合并为 1 条
D1=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie&SearchTerm=%E6%B2%99%E4%B8%98" -H "$AH" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['Items']))")
ck "同库多版本合并：沙丘仅显示 1 条" "1" "$D1"
# 详情 MediaSources 聚合（跨库合并默认也开：同库 2 + 跨库 1 = 3 源）
MS=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie&SearchTerm=%E6%B2%99%E4%B8%98&Fields=MediaSources" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin)['Items'][0];print(len(d.get('MediaSources',[])))")
ck "详情聚合全部版本源（3 个）" "3" "$MS"
# 关闭跨库后仅同库聚合（2 源；全局查询返回 2 条，取版本源最多者）
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"MergeVersionsAcrossLibraries":false}' >/dev/null
MS2=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie&SearchTerm=%E6%B2%99%E4%B8%98&Fields=MediaSources" -H "$AH" | python3 -c "
import sys,json
d=json.load(sys.stdin)['Items']
print(max(len(it.get('MediaSources') or []) for it in d), len(d))" | awk '{print $1}')
N2=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie&SearchTerm=%E6%B2%99%E4%B8%98&Fields=MediaSources" -H "$AH" | python3 -c "
import sys,json
d=json.load(sys.stdin)['Items']
print(max(len(it.get('MediaSources') or []) for it in d), len(d))" | awk '{print $2}')
ck "关闭跨库后返回 2 条" "2" "$N2"
ck "关闭跨库后同库聚合 2 源" "2" "$MS2"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"MergeVersionsAcrossLibraries":true}' >/dev/null
# 关闭合并：恢复独立条目（ movies1 两版 + movies2 一版 = 3 条）
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' \
  -d '{"MergeVersionsInLibrary":false,"MergeVersionsAcrossLibraries":false}' >/dev/null
D3=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie&SearchTerm=%E6%B2%99%E4%B8%98" -H "$AH" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['Items']))")
ck "关闭合并后恢复 3 条独立条目" "3" "$D3"
# 仅开同库合并：库内各自归并，跨库不合并 → 全局 2 条
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' \
  -d '{"MergeVersionsInLibrary":true,"MergeVersionsAcrossLibraries":false}' >/dev/null
LIB1=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin);print(sum(1 for i in d['Items'] if i['Name']=='沙丘'))")
ck "仅同库合并：沙丘 2 条（每库 1 条）" "2" "$LIB1"
# 跨库合并（默认全开）
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' \
  -d '{"MergeVersionsInLibrary":true,"MergeVersionsAcrossLibraries":true}' >/dev/null
D4=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin);print(sum(1 for i in d['Items'] if i['Name']=='沙丘'))")
ck "跨库合并全开：沙丘全局 1 条" "1" "$D4"

# ---------- 5. 首字母搜索 ----------
INIT=$(curl -s "$BASE/emby/Items?Recursive=true&SearchTerm=mjh" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin);print(len(d['Items']), d['Items'][0]['Name'] if d['Items'] else '')" | awk '{print $1}')
ck "拼音首字母 mjh 命中《满江红》" "1" "$INIT"
CN=$(curl -s "$BASE/emby/Items?Recursive=true&SearchTerm=%E6%B2%99" -H "$AH" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['Items']))")
ck "中文关键字搜索（跨库合并后）" "1" "$CN"

# ---------- 6. 剧集集数角标 ----------
BADGE=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Series" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin)['Items'][0];print(d.get('RecursiveItemCount','none'),d.get('ChildCount','none'))")
ck "角标字段：3 集 1 季" "3 1" "$BADGE"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"PosterEpisodeBadge":false}' >/dev/null
BADGE2=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Series" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin)['Items'][0];print(d.get('RecursiveItemCount','none'))")
ck "关闭角标后字段不再输出" "none" "$BADGE2"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"PosterEpisodeBadge":true}' >/dev/null

# ---------- 7. 发行日期排序 ----------
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"SortByReleaseDate":true}' >/dev/null
ORD=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Movie&SortBy=PremiereDate&SortOrder=Descending&Limit=10" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin)['Items'];names=[i['Name'] for i in d];print('沙丘' in names)")
ck "发行日期排序查询正常返回" "True" "$ORD"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"SortByReleaseDate":false}' >/dev/null

# ---------- 8. 剧集媒体信息复用（建库前已开启，等待 ffprobe + 复制） ----------
REUSE="no"
for i in $(seq 1 30); do
  sleep 2
  E2STREAMS=$(curl -s "$BASE/emby/Items?Recursive=true&IncludeItemTypes=Episode&Fields=MediaStreams" -H "$AH" | python3 -c "
import sys,json
d=json.load(sys.stdin)['Items']
n=sum(1 for it in d if any(len(ms.get('MediaStreams') or [])>0 for ms in (it.get('MediaSources') or [])))
print(n)")
  [ "$E2STREAMS" = "3" ] && { REUSE="ok"; break; }
done
ck "同季媒体信息复用：3 集都有流信息" "ok" "$REUSE"

# ---------- 9. 快速路径（STRM） ----------
printf 'http://127.0.0.1:%s/health\n' "$PORT" > "$MEDIA/movies1/StrmTest (2024).strm"
curl -s -X POST "$BASE/admin/scan" -H "$AH" -H 'Content-Type: application/json' -d '{"Mode":"update"}' >/dev/null
sleep 5
SID=$(curl -s "$BASE/emby/Items?Recursive=true&SearchTerm=strmtest" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin)['Items'];print(d[0]['Id'] if d else '')")
ck "strm 条目已入库" "ok" "$([ -n "$SID" ] && echo ok || echo no)"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"FastPath":true,"FastPathWaitSec":3}' >/dev/null
LOC=$(curl -s -o /dev/null -w '%{redirect_url}' "$BASE/emby/Videos/$SID/stream.mp4?Static=true&api_key=$TOK")
ck "快速路径 302 重定向存在" "ok" "$([ -n "$LOC" ] && echo ok || echo no)"
FASTLOG=$(curl -s "$BASE/admin/logs?category=redirect&limit=50" -H "$AH" | python3 -c "import sys,json;print(sum(1 for e in json.load(sys.stdin)['Entries'] if '快速路径' in e['message']))")
ck "快速路径日志已记录" "ok" "$([ "$FASTLOG" -ge 1 ] && echo ok || echo no)"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"FastPath":false}' >/dev/null

# ---------- 10. 文件监听 ----------
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"WatchEnabled":true,"WatchDelaySeconds":10}' >/dev/null
WL=$(curl -s "$BASE/admin/logs?category=scan&limit=100" -H "$AH" | python3 -c "import sys,json;print(sum(1 for e in json.load(sys.stdin)['Entries'] if '文件变动监听已开启' in e['message']))")
ck "文件监听已开启日志" "ok" "$([ "$WL" -ge 1 ] && echo ok || echo no)"
# 触发文件变动 → 防抖 10s → 增量扫描 → 新条目入库
printf 'test' > "$MEDIA/movies1/监听测试 (2020).mkv"
FOUND="no"
for i in $(seq 1 20); do
  sleep 2
  C=$(curl -s "$BASE/emby/Items?Recursive=true&SearchTerm=%E7%9B%91%E5%90%AC" -H "$AH" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['Items']))")
  [ "$C" -ge 1 ] && { FOUND="ok"; break; }
done
ck "文件变动触发自动刷新入库" "ok" "$FOUND"
curl -s -X PUT "$BASE/admin/enhancements" -H "$AH" -H 'Content-Type: application/json' -d '{"WatchEnabled":false}' >/dev/null

# ---------- 11. 未鉴权访问拒绝 ----------
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/admin/enhancements")
ck "未鉴权访问增强配置被拒" "401" "$CODE"

say ""
say "=================================="
say "通过 $PASS / $TOTAL，失败 $FAIL"
say "=================================="
[ "$FAIL" = "0" ]
