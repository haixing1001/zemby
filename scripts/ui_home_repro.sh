#!/usr/bin/env bash
# 复现首页「我的媒体库」问题：建库 → 首页 → 点击库卡片 → 观察跳转
set -u
mkdir -p /tmp/webui-home /tmp/zemby-home/data
pkill -f zemby-menu-test 2>/dev/null; pkill -f zemby-home-test 2>/dev/null; sleep 1

# 测试媒体
rm -rf /tmp/zemby-home/media
mkdir -p "/tmp/zemby-home/media/movies/Big Buck Bunny (2010)" "/tmp/zemby-home/media/movies/Sintel (2010)"
ffmpeg -y -f lavfi -i testsrc=duration=2:size=320x240:rate=10 -loglevel error "/tmp/zemby-home/media/movies/Big Buck Bunny (2010)/Big.Buck.Bunny.2010.mp4"
ffmpeg -y -f lavfi -i testsrc=duration=2:size=320x240:rate=10 -loglevel error "/tmp/zemby-home/media/movies/Sintel (2010)/Sintel.2010.mp4"

GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-home/data" MEDIA_ROOTS="/tmp/zemby-home/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-menu-test > /tmp/zemby-home/srv.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
echo "server up"

# API 建库 + 等扫描
TOK=$(curl -s -X POST http://127.0.0.1:18099/emby/Users/AuthenticateByName -H 'Content-Type: application/json' \
  -H 'X-Emby-Authorization: MediaBrowser Client="t", Device="t", DeviceId="t", Version="1"' \
  -d '{"Username":"admin","Pw":"admin123"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['AccessToken'])")
curl -s -X POST http://127.0.0.1:18099/emby/admin/libraries -H "X-Emby-Token: $TOK" -H 'Content-Type: application/json' \
  -d '{"Name":"电影","Path":"/tmp/zemby-home/media/movies","Type":"movies","EnableTMDB":false}' 
echo ""
for i in $(seq 1 20); do sleep 1; N=$(curl -s "http://127.0.0.1:18099/emby/Items?Recursive=true&IncludeItemTypes=Movie" -H "X-Emby-Token: $TOK" | python3 -c "import sys,json;print(json.load(sys.stdin).get('TotalRecordCount',0))" 2>/dev/null); [ "$N" = "2" ] && break; done
echo "items: $N"

# Views API 返回什么
echo "== Views =="
curl -s "http://127.0.0.1:18099/emby/Users/$(curl -s http://127.0.0.1:18099/emby/Users -H "X-Emby-Token: $TOK" | python3 -c "import sys,json;print(json.load(sys.stdin)[0]['Id'])")/Views" -H "X-Emby-Token: $TOK" | python3 -m json.tool | head -30

# 浏览器验证
agent-browser open http://127.0.0.1:18099/login >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-home/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui-home/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1.5
agent-browser snapshot -i > /tmp/webui-home/home_snap.txt 2>&1
echo "== home snapshot: 我的媒体库 section =="
grep -A6 "我的媒体库" /tmp/webui-home/home_snap.txt || echo "SECTION NOT FOUND"
grep -E "电影|Big Buck|Sintel" /tmp/webui-home/home_snap.txt | head -8
agent-browser screenshot /tmp/webui-home/home.png >/dev/null 2>&1

# 点击库卡片
LIBREF=$(grep "电影电影电影" /tmp/webui-home/home_snap.txt | grep -o '\[ref=[a-z0-9]*\]' | head -1 | tr -d '[]' | sed 's/ref=//')
echo "lib card ref: $LIBREF"
agent-browser click $LIBREF >/dev/null 2>&1; sleep 1.5
agent-browser snapshot -i > /tmp/webui-home/lib_snap.txt 2>&1
echo "== after click: url & content =="
agent-browser url 2>&1 | tail -1
grep -E "没有找到条目|Big Buck|Sintel|电影" /tmp/webui-home/lib_snap.txt | head -8
agent-browser screenshot /tmp/webui-home/library.png >/dev/null 2>&1

agent-browser close >/dev/null 2>&1
echo "== done =="
ls -la /tmp/webui-home/*.png
