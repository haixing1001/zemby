#!/usr/bin/env bash
set -u
mkdir -p /tmp/webui-home
pkill -f zemby-menu-test 2>/dev/null; sleep 1
GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-home/data" MEDIA_ROOTS="/tmp/zemby-home/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-menu-test > /tmp/zemby-home/srv5.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done

agent-browser open "http://127.0.0.1:18099/login" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-home/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui-home/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1.5

agent-browser errors --clear >/dev/null 2>&1
agent-browser console --clear >/dev/null 2>&1
agent-browser snapshot -i > /tmp/webui-home/home2_snap.txt 2>&1
LIBREF=$(grep "电影电影电影" /tmp/webui-home/home2_snap.txt | grep -o '\[ref=[a-z0-9]*\]' | head -1 | tr -d '[]' | sed 's/ref=//')
echo "click ref: $LIBREF"
agent-browser click $LIBREF >/dev/null 2>&1
sleep 2
echo "== pathname =="
agent-browser eval "location.pathname"
echo "== body innerHTML (first 500) =="
agent-browser eval "document.body.innerHTML.slice(0,500)"
echo "== #app exists =="
agent-browser eval "!!document.getElementById('app')"
echo "== page errors =="
agent-browser errors 2>&1 | head -15
echo "== console =="
agent-browser console 2>&1 | head -15
agent-browser close >/dev/null 2>&1
