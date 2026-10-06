#!/bin/bash
# 日志管理（分类）冒烟测试
set -e
BIN=/tmp/zemby-logs
DATA=/tmp/zemby-logtest
PORT=18098
BASE=http://127.0.0.1:$PORT

pkill -f zemby-logs 2>/dev/null || true
rm -rf "$DATA"; mkdir -p "$DATA/media"
GEMBY_ADDR=":$PORT" GEMBY_DATA="$DATA" MEDIA_ROOTS="$DATA/media" "$BIN" >"$DATA/server.log" 2>&1 &
trap "pkill -f zemby-logs 2>/dev/null" EXIT
for i in $(seq 1 30); do curl -sf "$BASE/health" >/dev/null 2>&1 && break; sleep 0.5; done
echo "== 服务器就绪 =="

PASS=0; FAIL=0
chk() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "PASS $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (期望 $2 实际 $3)"; fi }

TOKEN=$(curl -s -X POST "$BASE/emby/Users/AuthenticateByName" -H 'Content-Type: application/json' \
  -H 'X-Emby-Authorization: MediaBrowser Client="t", Device="t", DeviceId="t1", Version="1"' \
  -d '{"Username":"admin","Pw":"admin123"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["AccessToken"])')
AH="X-Emby-Token: $TOKEN"
[ -n "$TOKEN" ] && echo "PASS 登录" || { echo FAIL 登录; exit 1; }

# 触发多类日志：扫描（创建库）、刮削配置、探测配置、302（无内容可跳过）、系统
LIB=$(curl -s -X POST -H "$AH" -H 'Content-Type: application/json' -d '{"Name":"测试库","Type":"movies","Path":"'"$DATA"'/media"}' "$BASE/admin/libraries")
curl -s -o /dev/null -X PUT -H "$AH" -H 'Content-Type: application/json' -d '{"Enabled":true}' "$BASE/admin/scrape/config"
curl -s -o /dev/null -X PUT -H "$AH" -H 'Content-Type: application/json' -d '{"Concurrency":4}' "$BASE/admin/probe/config"
sleep 1

# 1. 全部日志含 category 字段
ALL=$(curl -s -H "$AH" "$BASE/admin/logs?limit=500")
echo "$ALL" | rg -q '"category"' && chk "日志含 category 字段" yes yes || chk "日志含 category 字段" yes no

# 2. 分类计数返回
echo "$ALL" | rg -q '"Counts"' && chk "返回 Counts 计数" yes yes || chk "返回 Counts 计数" yes no

# 3. 扫描分类过滤
SCAN_N=$(curl -s -H "$AH" "$BASE/admin/logs?category=scan&limit=500" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["Total"])')
[ "$SCAN_N" -ge 1 ] && chk "scan 分类过滤有结果($SCAN_N)" yes yes || chk "scan 分类过滤有结果" yes no

# 4. 刮削分类过滤
SCRAPE_N=$(curl -s -H "$AH" "$BASE/admin/logs?category=scrape&limit=500" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["Total"])')
[ "$SCRAPE_N" -ge 1 ] && chk "scrape 分类过滤有结果($SCRAPE_N)" yes yes || chk "scrape 分类过滤有结果" yes no

# 5. 探测分类
PROBE_N=$(curl -s -H "$AH" "$BASE/admin/logs?category=probe&limit=500" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["Total"])')
[ "$PROBE_N" -ge 1 ] && chk "probe 分类过滤有结果($PROBE_N)" yes yes || chk "probe 分类过滤有结果" yes no

# 6. 错误日志作为特殊分类（level=error）
curl -s -o /dev/null -H "$AH" "$BASE/admin/logs?category=error"
ERR_OK=$(curl -s -H "$AH" "$BASE/admin/logs?category=error&limit=500" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Total"] >= 0 and "ok")')
chk "error 特殊分类查询可用" ok "$ERR_OK"

# 7. 分类过滤互斥性：scan 结果不含 scrape 条目
MIX=$(curl -s -H "$AH" "$BASE/admin/logs?category=scan&limit=500" | python3 -c '
import json,sys
d=json.load(sys.stdin)
bad=[e for e in d["Entries"] if e.get("category") not in ("scan",)]
print(len(bad))')
chk "scan 过滤无串类" 0 "$MIX"

# 8. SSE 流含 category
SSE=$(timeout 3 curl -s -N -H "$AH" "$BASE/admin/logs/stream" | head -40)
echo "$SSE" | rg -q '"category"' && chk "SSE 推送含 category" yes yes || chk "SSE 推送含 category" yes no

# 9. 清空日志
C=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AH" "$BASE/admin/logs")
chk "DELETE 清空 204" 204 "$C"
N=$(curl -s -H "$AH" "$BASE/admin/logs?limit=500" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["Total"])')
[ "$N" -le 1 ] && chk "清空后仅剩清空日志($N)" yes yes || chk "清空后条数<=1" yes no

echo "== 结果: $PASS 通过 / $FAIL 失败 =="
[ $FAIL -eq 0 ] && echo ALL_PASS || exit 1
