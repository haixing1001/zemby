#!/usr/bin/env bash
# 综合验证：①首页库卡片跳转修复 ②辅助功能默认折叠 ③PathPicker 选路径
set -u
mkdir -p /tmp/webui-fix /tmp/zemby-fix
pkill -f zemby-menu-test 2>/dev/null; sleep 1
GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-home/data" MEDIA_ROOTS="/tmp/zemby-home/media" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-menu-test > /tmp/zemby-fix/srv.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done
echo "server up"

agent-browser open "http://127.0.0.1:18099/login" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-fix/login_snap.txt 2>&1
read -r UREF PREF BREF <<< "$(python3 /home/z/my-project/scripts/parse_ui.py login /tmp/webui-fix/login_snap.txt)"
agent-browser fill $UREF "admin" >/dev/null
agent-browser fill $PREF "admin123" >/dev/null
agent-browser click $BREF >/dev/null
agent-browser wait --url "/" >/dev/null 2>&1; sleep 1.5

echo "===== ① 首页点击库卡片 ====="
agent-browser errors --clear >/dev/null 2>&1
agent-browser snapshot -i > /tmp/webui-fix/home_snap.txt 2>&1
LIBREF=$(grep "电影电影电影" /tmp/webui-fix/home_snap.txt | grep -o '\[ref=[a-z0-9]*\]' | head -1 | tr -d '[]' | sed 's/ref=//')
agent-browser click $LIBREF >/dev/null 2>&1; sleep 1.5
agent-browser snapshot -i > /tmp/webui-fix/lib_snap.txt 2>&1
echo "URL: $(agent-browser eval 'location.pathname' 2>&1)"
echo "toolbar: $(agent-browser eval "document.querySelector('.toolbar') ? 'EXISTS' : 'MISSING'" 2>&1)"
echo "items: $(grep -cE 'Big Buck Bunny|Sintel' /tmp/webui-fix/lib_snap.txt)"
agent-browser errors 2>&1 | head -5
agent-browser screenshot /tmp/webui-fix/1_library_page.png >/dev/null 2>&1

echo "===== ② 辅助功能默认折叠 ====="
agent-browser open "http://127.0.0.1:18099/admin" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser snapshot -i > /tmp/webui-fix/admin_snap.txt 2>&1
N=$(grep -cE '"增强功能"|"字幕"|"AI识别"|"片头片尾"' /tmp/webui-fix/admin_snap.txt || true)
echo "collapsed children visible: $N (expect 0)"
agent-browser screenshot /tmp/webui-fix/2_admin_collapsed.png >/dev/null 2>&1
agent-browser eval "[...document.querySelectorAll('.adm-item')].find(e=>e.textContent.includes('辅助功能')).click(); 'ok'" >/dev/null 2>&1; sleep 0.5
agent-browser snapshot -i > /tmp/webui-fix/admin_open.txt 2>&1
echo "after click expand: $(grep -cE '"增强功能"|"AI识别"|"片头片尾"' /tmp/webui-fix/admin_open.txt) children"

echo "===== ③ PathPicker 选路径 ====="
agent-browser open "http://127.0.0.1:18099/admin/media" >/dev/null 2>&1
agent-browser wait --load networkidle >/dev/null 2>&1; sleep 1
agent-browser eval "[...document.querySelectorAll('button')].find(b=>b.title==='添加媒体库').click(); 'ok'" >/dev/null 2>&1; sleep 0.5
agent-browser eval "[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='浏览…').click(); 'ok'" >/dev/null 2>&1; sleep 0.8
agent-browser screenshot /tmp/webui-fix/3_picker_root.png >/dev/null 2>&1
agent-browser eval "[...document.querySelectorAll('.pp-row')].map(r=>r.textContent.trim()).join('|')" 2>&1
# 根目录 → media → movies
agent-browser eval "[...document.querySelectorAll('.pp-row')].find(r=>r.textContent.includes('media')).click(); 'ok'" >/dev/null 2>&1; sleep 0.8
echo "level2 rows: $(agent-browser eval "[...document.querySelectorAll('.pp-row')].map(r=>r.textContent.trim()).join('|')" 2>&1)"
agent-browser eval "[...document.querySelectorAll('.pp-row')].find(r=>r.textContent.includes('movies')).click(); 'ok'" >/dev/null 2>&1; sleep 0.8
agent-browser screenshot /tmp/webui-fix/4_picker_movies.png >/dev/null 2>&1
echo "picker path input: $(agent-browser eval "document.querySelector('.pp-foot input').value" 2>&1)"
echo "mkdir btn now: $(agent-browser eval "[...document.querySelectorAll('.pp-crumb button')].some(b=>b.textContent.includes('新建文件夹'))" 2>&1)"
# 选择此目录
agent-browser eval "[...document.querySelectorAll('.pp-foot button')].find(b=>b.textContent.includes('选择此目录')).click(); 'ok'" >/dev/null 2>&1; sleep 0.5
echo "newLib path input: $(agent-browser eval "document.querySelectorAll('.collapse-sec .body input')[1].value" 2>&1)"

agent-browser close >/dev/null 2>&1
echo "===== done ====="
ls -la /tmp/webui-fix/*.png
