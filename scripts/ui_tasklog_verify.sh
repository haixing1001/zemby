#!/usr/bin/env bash
# 端到端验证：任务详细日志（扫描/提取/刮削 API + 三页面 UI）
set -u
mkdir -p /tmp/webui-tasklog /tmp/zemby-tl
pkill -f zemby-menu-test 2>/dev/null; sleep 1

rm -rf /tmp/zemby-tl/media /tmp/zemby-tl/data
mkdir -p "/tmp/zemby-tl/media/movies/Big Buck Bunny (2010)" "/tmp/zemby-tl/media/movies/Sintel (2010)" "/tmp/zemby-tl/media/tv/ rar"
ffmpeg -y -f lavfi -i testsrc=duration=2:size=320x240:rate=10 -loglevel error "/tmp/zemby-tl/media/movies/Big Buck Bunny (2010)/Big.Buck.Bunny.2010.mp4"
ffmpeg -y -f lavfi -i testsrc=duration=2:size=320x240:rate=10 -loglevel error "/tmp/zemby-tl/media/movies/Sintel (2010)/Sintel.2010.mp4"

GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-tl/data" MEDIA_ROOTS="/tmp/zemby-tl/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-menu-test > /tmp/zemby-tl/srv.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
echo "server up"

TOK=$(curl -s -X POST http://127.0.0.1:18099/emby/Users/AuthenticateByName -H 'Content-Type: application/json' \
  -H 'X-Emby-Authorization: MediaBrowser Client="t", Device="t", DeviceId="t", Version="1"' \
  -d '{"Username":"admin","Pw":"admin123"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['AccessToken'])")
AH="X-Emby-Token: $TOK"

# 1. 建电影库（TMDB 关）→ 扫描产生逐文件日志
curl -s -X POST http://127.0.0.1:18099/emby/admin/libraries -H "$AH" -H 'Content-Type: application/json' \
  -d '{"Name":"电影","Path":"/tmp/zemby-tl/media/movies","Type":"movies","EnableTMDB":false}' >/dev/null
for i in $(seq 1 15); do sleep 1; grep -q "扫描完成" /tmp/zemby-tl/srv.log 2>/dev/null && break; done
sleep 1

echo "== ① dashboard: scan task logs =="
curl -s http://127.0.0.1:18099/emby/admin/dashboard -H "$AH" | python3 -c "
import sys, json
d = json.load(sys.stdin)
ts = d.get('Tasks') or []
print('tasks:', len(ts))
for t in ts:
    logs = t.get('Logs') or []
    print('task', t['ID'], t['Mode'], t['State'], 'log lines:', len(logs))
    for l in logs[:6]:
        print('   ', l['Level'], l['Msg'][:80])
    print('   Message:', t.get('Message', '')[:80])
print('ScrapeState:', d.get('ScrapeState'), 'Pending:', d.get('ScrapePending'))
"

echo "== ② probe logs =="
curl -s http://127.0.0.1:18099/emby/admin/probe/status -H "$AH" | python3 -c "
import sys, json
d = json.load(sys.stdin)
logs = d.get('Logs') or []
print('probe state:', d.get('State'), 'done:', d.get('Completed'), 'logs:', len(logs))
for l in logs[:6]:
    print('   ', l['Level'], l['Msg'][:90])
"

# 2. 开启 TMDB（假 key）→ 开始刮削 → 产生刮削日志（含失败）
curl -s -X PUT http://127.0.0.1:18099/emby/admin/enhancements -H "$AH" -H 'Content-Type: application/json' \
  -d '{"tmdb":true,"favorites":true,"sortByReleaseDate":false}' >/dev/null
curl -s -X PUT http://127.0.0.1:18099/emby/admin/tmdb -H "$AH" -H 'Content-Type: application/json' \
  -d '{"APIKey":"sk-fake-key-1234567890","Language":"zh-CN","DownloadImages":true}' >/dev/null
curl -s -X POST http://127.0.0.1:18099/emby/admin/scrape/control -H "$AH" -H 'Content-Type: application/json' \
  -d '{"Action":"start"}' >/dev/null
sleep 6

echo "== ③ scrape logs =="
curl -s http://127.0.0.1:18099/emby/admin/scrape/state -H "$AH" | python3 -c "
import sys, json
d = json.load(sys.stdin)
logs = d.get('Logs') or []
print('scrape state:', d.get('State'), 'pending:', d.get('Pending'), 'failed:', d.get('Failed'), 'logs:', len(logs))
for l in logs[:12]:
    print('   ', l['Level'], l['Msg'][:90])
"

# 3. UI 验证
agent-browser open "http://127.0.0.1:18099/login" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-tasklog/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui-tasklog/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1.5

echo "== ④ UI: dashboard task logs =="
agent-browser open "http://127.0.0.1:18099/admin" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
agent-browser eval "[...document.querySelectorAll('.task-row')].find(r=>r).click(); 'ok'" >/dev/null 2>&1; sleep 0.8
echo "tlog lines on dashboard: $(agent-browser eval "document.querySelectorAll('.tlog-line').length" 2>&1)"
agent-browser eval "[...document.querySelectorAll('.task-line')].find(e=>e).click(); 'ok'" >/dev/null 2>&1; sleep 0.8
echo "after scrape line click: $(agent-browser eval "document.querySelectorAll('.tlog-line').length" 2>&1)"
agent-browser screenshot /tmp/webui-tasklog/1_dashboard_logs.png >/dev/null 2>&1

echo "== ⑤ UI: scrape page logs =="
agent-browser open "http://127.0.0.1:18099/admin/scrape" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
echo "scrape tlog lines: $(agent-browser eval "document.querySelectorAll('.tlog-line').length" 2>&1)"
agent-browser screenshot /tmp/webui-tasklog/2_scrape_logs.png >/dev/null 2>&1

echo "== ⑥ UI: media info page logs =="
agent-browser open "http://127.0.0.1:18099/admin/media-info" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
echo "probe tlog lines: $(agent-browser eval "document.querySelectorAll('.tlog-line').length" 2>&1)"
agent-browser screenshot /tmp/webui-tasklog/3_probe_logs.png >/dev/null 2>&1

agent-browser close >/dev/null 2>&1
echo "== done =="
ls -la /tmp/webui-tasklog/*.png
