#!/bin/bash
# API Key 管理全链路冒烟测试
set -e
BIN=/tmp/zemby-apikey
DATA=/tmp/zemby-keytest
PORT=18099
BASE=http://127.0.0.1:$PORT

rm -rf "$DATA"; mkdir -p "$DATA/media"
GEMBY_ADDR=":$PORT" GEMBY_DATA="$DATA" HTTP_PORT=$PORT "$BIN" >"$DATA/server.log" 2>&1 &
SRV=$!
trap "kill $SRV 2>/dev/null" EXIT

# 等待就绪
for i in $(seq 1 30); do
  curl -sf "$BASE/health" >/dev/null 2>&1 && break
  sleep 0.5
done
echo "== 服务器已就绪 =="

PASS=0; FAIL=0
chk() { # chk <name> <expected> <actual>
  if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "PASS $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (期望 $2 实际 $3)"; fi
}

# 1. 管理员登录
LOGIN=$(curl -s -X POST "$BASE/emby/Users/AuthenticateByName" -H 'Content-Type: application/json' \
  -H 'X-Emby-Authorization: MediaBrowser Client="test", Device="t", DeviceId="t1", Version="1.0"' \
  -d '{"Username":"admin","Pw":"admin123"}')
TOKEN=$(echo "$LOGIN" | python3 -c 'import json,sys; print(json.load(sys.stdin)["AccessToken"])')
USERID=$(echo "$LOGIN" | python3 -c 'import json,sys; print(json.load(sys.stdin)["User"]["Id"])')
[ -n "$TOKEN" ] && echo "PASS 管理员登录" || { echo "FAIL 登录"; exit 1; }
AH="X-Emby-Token: $TOKEN"

# 2. 初始密钥列表为空
N=$(curl -s -H "$AH" "$BASE/admin/apikeys" | python3 -c 'import json,sys; print(json.load(sys.stdin)["TotalRecordCount"])')
chk "初始密钥数为 0" 0 "$N"

# 3. 生成密钥（模拟截图中的 "aa"）
KEYJSON=$(curl -s -X POST -H "$AH" -H 'Content-Type: application/json' -d '{"Name":"aa"}' "$BASE/admin/apikeys")
KEY=$(echo "$KEYJSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Key"])')
KEYID=$(echo "$KEYJSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["ID"])')
MASKED=$(echo "$KEYJSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Masked"])')
echo "   密钥: ${KEY:0:6}...${KEY: -4}  掩码: $MASKED"
[ ${#KEY} -ge 32 ] && chk "密钥长度>=32" yes yes || chk "密钥长度>=32" yes no
chk "掩码格式 前6+点+后4" "${KEY:0:6}••••••••••${KEY: -4}" "$MASKED"

# 4. 列表可见掩码
LIST=$(curl -s -H "$AH" "$BASE/admin/apikeys")
echo "$LIST" | rg -q "$MASKED" && chk "列表展示掩码" yes yes || chk "列表展示掩码" yes no
echo "$LIST" | rg -q '"Key"' && chk "列表不泄露明文" no yes || chk "列表不泄露明文" no no

# 5. Emby 兼容调用：api_key 参数
C1=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/emby/System/Info?api_key=$KEY")
chk "api_key 参数调用 System/Info" 200 "$C1"

# 6. X-Emby-Token 头
C2=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Emby-Token: $KEY" "$BASE/emby/System/Info")
chk "X-Emby-Token 头调用" 200 "$C2"

# 7. X-MediaBrowser-Token 头（老客户端兼容）
C2b=$(curl -s -o /dev/null -w '%{http_code}' -H "X-MediaBrowser-Token: $KEY" "$BASE/emby/Items?Limit=1")
chk "X-MediaBrowser-Token 调用 Items" 200 "$C2b"

# 8. Items 查询
C3=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/emby/Items?api_key=$KEY&Recursive=true&Limit=5")
chk "api_key 调用 Items 查询" 200 "$C3"

# 9. 用户列表（Emby 集成常用）
C4=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/emby/Users?api_key=$KEY")
chk "api_key 调用 /Users" 200 "$C4"

# 10. 管理接口（等同管理员）
C5=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Emby-Token: $KEY" "$BASE/admin/dashboard")
chk "api_key 调用 /admin/dashboard" 200 "$C5"

# 11. Views（用户维度，绑定合成管理员身份）
C6=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/emby/Users/$USERID/Views?api_key=$KEY")
chk "api_key 调用 Views" 200 "$C6"

# 12. 标记已看（MoviePilot 类工具集成）——不存在条目应 404（说明权限已通过，而非 403）
C7=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/emby/Users/$USERID/PlayedItems/nonexistent?api_key=$KEY")
chk "api_key 标记已看权限可达(非403)" 404 "$C7"

# 13. 未鉴权调用 /admin/apikeys 应被拒
C8=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/admin/apikeys")
chk "未鉴权访问密钥列表 401" 401 "$C8"

# 14. 删除密钥
C9=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AH" "$BASE/admin/apikeys/$KEYID")
chk "删除密钥 204" 204 "$C9"

# 15. 删除后密钥立即失效
C10=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/emby/System/Info?api_key=$KEY")
chk "撤销后调用 401" 401 "$C10"

# 16. 空名称创建走默认名
K2=$(curl -s -X POST -H "$AH" -H 'Content-Type: application/json' -d '{}' "$BASE/admin/apikeys" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Name"])')
chk "空名称默认 未命名密钥" "未命名密钥" "$K2"
# 清理
curl -s -o /dev/null -X DELETE -H "$AH" "$BASE/admin/apikeys/2"

echo "== 结果: $PASS 通过 / $FAIL 失败 =="
[ $FAIL -eq 0 ] && echo ALL_PASS || exit 1
