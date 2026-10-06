#!/usr/bin/env bash
set -u
mkdir -p /tmp/webui-home
pkill -f zemby-menu-test 2>/dev/null; sleep 1
GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-home/data" MEDIA_ROOTS="/tmp/zemby-home/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-menu-test > /tmp/zemby-home/srv3.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
TOK=$(curl -s -X POST http://127.0.0.1:18099/emby/Users/AuthenticateByName -H 'Content-Type: application/json' \
  -H 'X-Emby-Authorization: MediaBrowser Client="t", Device="t", DeviceId="t", Version="1"' \
  -d '{"Username":"admin","Pw":"admin123"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['AccessToken'])")
LIBID=$(curl -s "http://127.0.0.1:18099/emby/Users/x/Views" -H "X-Emby-Token: $TOK" | python3 -c "import sys,json;d=json.load(sys.stdin)['Items'];print([i['Id'] for i in d if i.get('CollectionType')=='movies'][0])")

agent-browser open "http://127.0.0.1:18099/login" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-home/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui-home/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1

agent-browser errors --clear >/dev/null 2>&1
agent-browser open "http://127.0.0.1:18099/library/$LIBID" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
echo "== location =="
agent-browser eval "location.pathname"
echo "== app html length =="
agent-browser eval "document.getElementById('app').innerHTML.length"
echo "== app html head =="
agent-browser eval "document.getElementById('app').innerHTML.slice(0,400)"
echo "== localStorage token present =="
agent-browser eval "!!localStorage.getItem('gemby_token')"
echo "== errors =="
agent-browser errors 2>&1 | head -10
agent-browser close >/dev/null 2>&1
