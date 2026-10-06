#!/usr/bin/env bash
# 演职人员过滤验证：HideActorsNoImage 开/关 时 People 输出
set -u
pkill -f zemby-test 2>/dev/null; sleep 1
MEDIA=/tmp/zemby-enh/media
# 满江红 NFO 加演员（1 带头像 1 无头像）
cat > "$MEDIA/movies1/满江红 (2023)/movie.nfo" <<'EOF'
<movie><title>满江红</title><year>2023</year>
<actor><name>沈腾</name><role>张大</role><thumb>https://example.com/shen.jpg</thumb></actor>
<actor><name>易烊千玺</name><role>孙均</role></actor>
</movie>
EOF

GEMBY_ADDR=":18099" GEMBY_DATA="/tmp/zemby-enh/data" MEDIA_ROOTS="$MEDIA" \
  GEMBY_ADMIN_PASSWORD=admin123 /tmp/zemby-test > /tmp/zemby-enh/act.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for i in $(seq 1 30); do sleep 0.5; curl -sf http://127.0.0.1:18099/health >/dev/null 2>&1 && break; done

TOK=$(python3 -c "
import json,urllib.request
req=urllib.request.Request('http://127.0.0.1:18099/emby/Users/AuthenticateByName',
  data=json.dumps({'Username':'admin','Pw':'admin123'}).encode(),
  headers={'Content-Type':'application/json'})
print(json.load(urllib.request.urlopen(req))['AccessToken'])")
AH="X-Emby-Token: $TOK"

# 触发增量扫描拾取 NFO 变化（mtime 变化 → upsert → applyNFO）
curl -s -X POST http://127.0.0.1:18099/admin/scan -H "$AH" -H 'Content-Type: application/json' -d '{"Mode":"full"}' >/dev/null
sleep 5
MID=$(curl -s "http://127.0.0.1:18099/emby/Items?Recursive=true&SearchTerm=%E6%BB%A1%E6%B1%9F%E7%BA%A2" -H "$AH" | python3 -c "import sys,json;print(json.load(sys.stdin)['Items'][0]['Id'])")
P() { curl -s "http://127.0.0.1:18099/emby/Items/$MID" -H "$AH" | python3 -c "import sys,json;d=json.load(sys.stdin);print([ (p['Name'], p.get('Thumb','')) for p in d.get('People',[]) ])"; }
echo "开关开（默认）: $(P)"
curl -s -X PUT http://127.0.0.1:18099/admin/enhancements -H "$AH" -H 'Content-Type: application/json' -d '{"HideActorsNoImage":false}' >/dev/null
echo "开关关: $(P)"
curl -s -X PUT http://127.0.0.1:18099/admin/enhancements -H "$AH" -H 'Content-Type: application/json' -d '{"HideActorsNoImage":true}' >/dev/null
echo "恢复开关开: $(P)"
