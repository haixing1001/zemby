#!/usr/bin/env bash
# UI 验证：辅助功能折叠项交互（展开/收起/子路由自动展开）
set -u
mkdir -p /tmp/webui-menu /tmp/zemby-menu
pkill -f zemby-menu 2>/dev/null; sleep 1

GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-menu/data" MEDIA_ROOTS="/tmp/zemby-menu/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-menu-test > /tmp/zemby-menu/srv.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
echo "server up"

agent-browser open http://127.0.0.1:18099/login >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-menu/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui-menu/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1

# 1. 控制台页：辅助功能默认展开
agent-browser open http://127.0.0.1:18099/admin >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser snapshot -i > /tmp/webui-menu/admin_snap.txt 2>&1
echo "== snapshot: aux item & children =="
grep -E "辅助功能|增强功能|AI识别|片头片尾|代理" /tmp/webui-menu/admin_snap.txt
agent-browser screenshot /tmp/webui-menu/1_default_expanded.png >/dev/null 2>&1

# 2. 点击辅助功能 → 收起
AUXREF=$(grep "辅助功能" /tmp/webui-menu/admin_snap.txt | grep -o '\[ref=[a-z0-9]*\]' | head -1 | tr -d '[]' | sed 's/ref=//')
echo "aux ref: $AUXREF"
agent-browser click $AUXREF >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-menu/collapsed_snap.txt 2>&1
echo "== collapsed: children should be gone =="
grep -cE "增强功能|AI识别" /tmp/webui-menu/collapsed_snap.txt || echo "0 children found (collapsed OK)"
agent-browser screenshot /tmp/webui-menu/2_collapsed.png >/dev/null 2>&1

# 3. 再点击 → 展开
agent-browser click $AUXREF >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-menu/reopened_snap.txt 2>&1
echo "== reopened: children count =="
grep -cE "增强功能|字幕|TMDB|AI识别|Bot|片头片尾|代理" /tmp/webui-menu/reopened_snap.txt

# 4. 直达子路由 /admin/ai：折叠态应自动展开且 AI识别 高亮
agent-browser click $AUXREF >/dev/null 2>&1; sleep 0.3  # 先收起
agent-browser open http://127.0.0.1:18099/admin/ai >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser snapshot -i > /tmp/webui-menu/ai_snap.txt 2>&1
echo "== /admin/ai: group auto-expanded? =="
grep -E "增强功能|片头片尾|AI识别" /tmp/webui-menu/ai_snap.txt
agent-browser screenshot /tmp/webui-menu/3_ai_autoexpand.png >/dev/null 2>&1

agent-browser close >/dev/null 2>&1
echo "== done =="
ls -la /tmp/webui-menu/*.png
