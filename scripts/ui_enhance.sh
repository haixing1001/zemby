#!/usr/bin/env bash
# UI 验证：登录 → 增强功能页 → 开关交互 → 首页收藏库 → 媒体库角标
set -u
pkill -f zemby-test 2>/dev/null; sleep 1
mkdir -p /tmp/webui2

GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-enh/data" MEDIA_ROOTS="/tmp/zemby-enh/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-test > /tmp/zemby-enh/ui.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
echo "== server ready =="

agent-browser open http://127.0.0.1:18099/login >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5

# 动态解析登录表单 refs
agent-browser snapshot -i > /tmp/webui2/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui2/login_snap.txt)"
echo "refs: $UREF $PREF $BREF"
agent-browser fill $UREF "admin" >/dev/null 2>&1
agent-browser fill $PREF "admin123" >/dev/null 2>&1
agent-browser click $BREF >/dev/null 2>&1
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1
echo "== logged in =="

# 1. 增强功能页
agent-browser open http://127.0.0.1:18099/admin/settings >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser screenshot /tmp/webui2/enhance_top.png >/dev/null 2>&1
echo "== enhance page top captured =="
agent-browser eval "window.scrollTo(0, document.body.scrollHeight*0.45)" >/dev/null 2>&1; sleep 0.5
agent-browser screenshot /tmp/webui2/enhance_mid.png >/dev/null 2>&1
agent-browser eval "window.scrollTo(0, document.body.scrollHeight)" >/dev/null 2>&1; sleep 0.5
agent-browser screenshot /tmp/webui2/enhance_bottom.png >/dev/null 2>&1
echo "== enhance page scrolled captured =="

# 2. 开启收藏功能开关（找到 开启收藏功能 行的 switch）
agent-browser snapshot -i > /tmp/webui2/enh_snap.txt 2>&1
FAVREF=$(python3 /home/z/my-project/scripts/parse_ui.py favswitch /tmp/webui2/enh_snap.txt)
echo "fav switch ref: $FAVREF"
if [ -n "$FAVREF" ]; then
  agent-browser click $FAVREF >/dev/null 2>&1
  sleep 1.5
  agent-browser screenshot /tmp/webui2/enhance_fav_on.png >/dev/null 2>&1
  echo "== favorites toggled on, captured =="
fi

# 3. 首页（收藏库出现）
agent-browser open http://127.0.0.1:18099/ >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser screenshot /tmp/webui2/home.png >/dev/null 2>&1
echo "== home captured =="

# 4. 媒体库页（集数角标）
agent-browser open http://127.0.0.1:18099/admin >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui2/admin_snap.txt 2>&1
LIBURL=$(python3 -c "
import re
s=open('/tmp/webui2/admin_snap.txt').read()
m=re.search(r'/library/([a-f0-9]{32})', s)
print(m.group(0) if m else '')")
echo "lib url: $LIBURL"
if [ -n "$LIBURL" ]; then
  agent-browser open "http://127.0.0.1:18099$LIBURL" >/dev/null 2>&1
  agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1.5
  agent-browser screenshot /tmp/webui2/library.png >/dev/null 2>&1
  echo "== library captured =="
fi

# 5. 详情页（演职人员区：如无 People 则跳过）
echo "== done =="
ls -la /tmp/webui2/*.png
