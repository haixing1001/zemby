#!/usr/bin/env bash
set -u
mkdir -p /tmp/webui-tasklog
pkill -f zemby-menu-test 2>/dev/null; sleep 1
GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-tl/data4" MEDIA_ROOTS="/tmp/zemby-tl/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-menu-test > /tmp/zemby-tl/srv7.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
TOK=$(curl -s -X POST http://127.0.0.1:18099/emby/Users/AuthenticateByName -H 'Content-Type: application/json' \
  -H 'X-Emby-Authorization: MediaBrowser Client="t", Device="t", DeviceId="t", Version="1"' \
  -d '{"Username":"admin","Pw":"admin123"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['AccessToken'])")
AH="X-Emby-Token: $TOK"
curl -s -X POST http://127.0.0.1:18099/emby/admin/libraries -H "$AH" -H 'Content-Type: application/json' \
  -d '{"Name":"电影","Path":"/tmp/zemby-tl/media/movies","Type":"movies","EnableTMDB":false}' >/dev/null
for i in $(seq 1 15); do sleep 1; grep -q "扫描完成" /tmp/zemby-tl/srv7.log 2>/dev/null && break; done
sleep 1

agent-browser open "http://127.0.0.1:18099/login" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-tasklog/login2.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui-tasklog/login2.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1

# 控制台：展开扫描任务日志
agent-browser open "http://127.0.0.1:18099/admin" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
agent-browser eval "[...document.querySelectorAll('.task-row')].find(r=>r).click(); 'ok'" >/dev/null 2>&1; sleep 0.6
agent-browser eval "document.querySelector('.tlog').scrollIntoView({block:'center'}); 'ok'" >/dev/null 2>&1; sleep 0.4
agent-browser screenshot /tmp/webui-tasklog/4_dashboard_tasklog.png >/dev/null 2>&1

# 刮削管理：日志卡片
agent-browser open "http://127.0.0.1:18099/admin/scrape" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
agent-browser eval "document.querySelector('.tlog') && document.querySelector('.tlog').scrollIntoView({block:'center'}); 'ok'" >/dev/null 2>&1; sleep 0.4
agent-browser screenshot /tmp/webui-tasklog/5_scrape_tasklog.png >/dev/null 2>&1

agent-browser close >/dev/null 2>&1
echo done
