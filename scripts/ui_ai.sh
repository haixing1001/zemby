#!/usr/bin/env bash
# UI 验证（单次运行完成全部流程）：登录 → 侧边栏 → AI 页填配置 → 保存 → 识别测试 → 日志 AI 分类
set -u
pkill -f zemby-test 2>/dev/null; pkill -f mock_ai 2>/dev/null; sleep 1
mkdir -p /tmp/webui3
DIR=/tmp/zemby-ai

cat > "$DIR/mock_ai.py" <<'EOF'
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

MOCKPORT = 18098

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path.endswith('/chat/completions'):
            content = json.dumps({"choices": [{"message": {"content": "```json\n"
                + json.dumps({"title": "蜘蛛侠：纵横宇宙", "year": 2023,
                               "original_title": "Spider-Man: Across the Spider-Verse"},
                              ensure_ascii=False) + "\n```"}}]}, ensure_ascii=False).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(content)
        else:
            self.send_response(404); self.end_headers()
    def log_message(self, *a): pass

HTTPServer(('127.0.0.1', MOCKPORT), H).serve_forever()
EOF
python3 "$DIR/mock_ai.py" >/tmp/webui3/mock.log 2>&1 &
MOCKPID=$!

GEMBY_ADDR=":18099" GEMBY_DATA="$DIR/data" MEDIA_ROOTS="$DIR/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-test > /tmp/webui3/ui.log 2>&1 &
SRV=$!
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
echo "== server ready =="

# 登录
agent-browser open http://127.0.0.1:18099/login >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui3/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui3/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null 2>&1
agent-browser fill $PREF "admin123" >/dev/null 2>&1
agent-browser click $BREF >/dev/null 2>&1
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1
echo "== logged in =="

# 1. 侧边栏截图（控制台页，含辅助功能分组）
agent-browser open http://127.0.0.1:18099/admin >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser screenshot /tmp/webui3/1_sidebar.png >/dev/null 2>&1
S=$(agent-browser snapshot 2>/dev/null)
echo "$S" | rg -q '设置 · 字幕 · TMDB · 更多' && echo "FAIL 折叠项仍存在" || echo "ok 折叠项已删除"
echo "$S" | rg -q '"辅助功能"' && echo "ok 辅助功能分组" || echo "FAIL 无辅助功能"
echo "$S" | rg -q 'AI识别' && echo "ok AI识别菜单" || echo "FAIL 无AI识别"

# 2. AI 识别页：填配置并保存
agent-browser open http://127.0.0.1:18099/admin/ai >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser screenshot /tmp/webui3/2_ai_page.png >/dev/null 2>&1
SNAP=$(agent-browser snapshot -i 2>/dev/null)
URLREF=$(echo "$SNAP" | rg -o 'textbox "https[^"]*" \[ref=(e[0-9]+)\]' -r '$1' | head -1)
KEYREF=$(echo "$SNAP" | rg -o 'textbox "sk-\.\.\." \[ref=(e[0-9]+)\]' -r '$1' | head -1)
SAVEREF=$(echo "$SNAP" | rg -o 'button "保存" \[ref=(e[0-9]+)\]' -r '$1' | head -1)
agent-browser fill @$URLREF "http://127.0.0.1:18098/v1" >/dev/null 2>&1
agent-browser fill @$KEYREF "sk-test-mock-1234567890" >/dev/null 2>&1
agent-browser click @$SAVEREF >/dev/null 2>&1
sleep 1.2
T=$(agent-browser snapshot 2>/dev/null | rg '已保存|失败' | head -1)
echo "保存提示: $T"
agent-browser screenshot /tmp/webui3/3_ai_saved.png >/dev/null 2>&1

# 3. 识别测试
SNAP=$(agent-browser snapshot -i 2>/dev/null)
PATHREF=$(echo "$SNAP" | rg -o 'textbox "/media/电影/[^"]*" \[ref=(e[0-9]+)\]' -r '$1' | head -1)
TESTREF=$(echo "$SNAP" | rg -o 'button "测试识别" \[ref=(e[0-9]+)\]' -r '$1' | head -1)
agent-browser fill @$PATHREF "/media/downloads/Spider.Man.Across.the.Spider.Verse.2023.1080p.BluRay.x264-GROUP/蜘蛛侠 纵横宇宙 2023 1080p.mkv" >/dev/null 2>&1
agent-browser click @$TESTREF >/dev/null 2>&1
sleep 2
R=$(agent-browser snapshot 2>/dev/null)
echo "$R" | rg -q '蜘蛛侠：纵横宇宙' && echo "ok 识别名称正确" || echo "FAIL 未识别到名称"
echo "$R" | rg -q 'Spider-Man: Across the Spider-Verse' && echo "ok 识别原名正确" || echo "FAIL 未识别到原名"
echo "$R" | rg -q '2023' && echo "ok 识别年份正确" || echo "FAIL 未识别到年份"
echo "$R" | rg -q 'AI 配置可用' && echo "ok 成功提示" || echo "FAIL 无成功提示"
agent-browser screenshot /tmp/webui3/4_ai_test.png >/dev/null 2>&1

# 4. 日志管理页 AI 分类 chip
agent-browser open http://127.0.0.1:18099/admin/logs >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser screenshot /tmp/webui3/5_logs.png >/dev/null 2>&1
L=$(agent-browser snapshot 2>/dev/null)
echo "$L" | rg -q 'AI识别' && echo "ok 日志页 AI识别 chip" || echo "FAIL 日志页无 AI chip"
echo "$L" | rg -q 'AI 识别辅助配置已更新' && echo "ok AI 日志条目存在" || echo "FAIL 无 AI 日志条目"

echo "== done =="
