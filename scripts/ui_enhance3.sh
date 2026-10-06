#!/usr/bin/env bash
# 剧集详情+演员 UI 截图（满江红带演员数据）
set -u
pkill -f zemby-test 2>/dev/null; sleep 1
GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-enh/data" MEDIA_ROOTS="/tmp/zemby-enh/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-test > /tmp/zemby-enh/ui3.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done

agent-browser open http://127.0.0.1:18099/login >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui2/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui2/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1

MID=$(curl -s "http://127.0.0.1:18099/emby/Items?Recursive=true&SearchTerm=%E6%BB%A1%E6%B1%9F%E7%BA%A2" -H "X-Emby-Token: $(python3 -c "
import json,urllib.request
req=urllib.request.Request('http://127.0.0.1:18099/emby/Users/AuthenticateByName',
  data=json.dumps({'Username':'admin','Pw':'admin123'}).encode(),
  headers={'Content-Type':'application/json'})
print(json.load(urllib.request.urlopen(req))['AccessToken'])")" | python3 -c "import sys,json;print(json.load(sys.stdin)['Items'][0]['Id'])")

agent-browser open "http://127.0.0.1:18099/item/$MID" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
agent-browser screenshot /tmp/webui2/movie_detail.png >/dev/null 2>&1
agent-browser close >/dev/null 2>&1
echo done
