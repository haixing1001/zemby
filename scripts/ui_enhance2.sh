#!/usr/bin/env bash
# UI 验证 2：媒体库集数角标 + 详情页
set -u
pkill -f zemby-test 2>/dev/null; sleep 1

GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-enh/data" MEDIA_ROOTS="/tmp/zemby-enh/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-test > /tmp/zemby-enh/ui2.log 2>&1 &
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

# 剧集库 ID（从 Views API 拿）
TOK=$(grep -o '"AccessToken":"[A-Za-z0-9]*"' /tmp/webui2/login_resp.json 2>/dev/null | head -1)
LIBID=$(curl -s "http://127.0.0.1:18099/emby/Items?Recursive=true&IncludeItemTypes=Series" \
  -H "X-Emby-Token: $(python3 -c "
import json,urllib.request,urllib.parse
req=urllib.request.Request('http://127.0.0.1:18099/emby/Users/AuthenticateByName',
  data=json.dumps({'Username':'admin','Pw':'admin123'}).encode(),
  headers={'Content-Type':'application/json'})
r=json.load(urllib.request.urlopen(req))
print(r['AccessToken'])")" | python3 -c "import sys,json;d=json.load(sys.stdin)['Items'];print(d[0]['Id'] if d else '')")
echo "series id: $LIBID"

if [ -n "$LIBID" ]; then
  agent-browser open "http://127.0.0.1:18099/item/$LIBID" >/dev/null 2>&1
  agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
  agent-browser screenshot /tmp/webui2/series_detail.png >/dev/null 2>&1
  echo "== series detail captured =="
fi

agent-browser close >/dev/null 2>&1
ls -la /tmp/webui2/series_detail.png
