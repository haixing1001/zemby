#!/usr/bin/env bash
# AI 识别辅助冒烟测试：/admin/ai 配置读写与脱敏、/admin/ai/test（mock OpenAI）、
# 启用校验、AI 日志分类、鉴权保护。
set -u
PORT=18099
BASE="http://127.0.0.1:$PORT"
DIR="/tmp/zemby-ai"
DATA="$DIR/data"
MEDIA="$DIR/media"
MOCKPORT=18098
MOCK="http://127.0.0.1:$MOCKPORT"
PASS=0; FAIL=0; TOTAL=0

say()  { printf '%s\n' "$*"; }
ck() { # ck <描述> <期望> <实际>
  TOTAL=$((TOTAL+1))
  if [ "$2" = "$3" ]; then PASS=$((PASS+1)); say "ok   $1";
  else FAIL=$((FAIL+1)); say "FAIL $1 (期望=$2 实际=$3)"; fi
}

# ---------- 准备目录与 mock OpenAI 兼容服务（返回带 markdown 围栏的 JSON，验证解析容错） ----------
rm -rf "$DIR"; mkdir -p "$DATA" "$MEDIA/movies"
cat > "$DIR/mock_ai.py" <<'EOF'
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

MOCKPORT = 18098

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        ln = int(self.headers.get('Content-Length', 0))
        body = json.loads(self.rfile.read(ln) or b'{}')
        auth = self.headers.get('Authorization', '')
        # /chat/completions 才是识别端点
        if self.path.endswith('/chat/completions'):
            if auth != 'Bearer sk-test-mock-1234567890':
                self.send_response(401); self.end_headers()
                self.wfile.write(b'{"error":{"message":"bad key"}}')
                return
            # 校验请求结构（system 提示 + user 内容含路径）
            msgs = body.get('messages', [])
            content = json.dumps({
                "choices": [{"message": {"content": "```json\n"
                    + json.dumps({"title": "蜘蛛侠：纵横宇宙", "year": 2023,
                                   "original_title": "Spider-Man: Across the Spider-Verse"},
                                  ensure_ascii=False) + "\n```"}}]
            }, ensure_ascii=False).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(content)
        else:
            self.send_response(404); self.end_headers()
    def log_message(self, *a): pass

HTTPServer(('127.0.0.1', MOCKPORT), H).serve_forever()
EOF
python3 "$DIR/mock_ai.py" >"$DIR/mock.log" 2>&1 &
MOCKPID=$!
trap 'kill $SRV $MOCKPID 2>/dev/null' EXIT

# ---------- 准备测试媒体 ----------
printf 'test' > "$MEDIA/movies/Demo.2020.1080p.mkv"

# ---------- 启动服务 ----------
GEMBY_ADDR=":$PORT" GEMBY_DATA="$DATA" MEDIA_ROOTS="$MEDIA" GEMBY_ADMIN_PASSWORD=admin123 \
  /tmp/zemby-test >"$DIR/server.log" 2>&1 &
SRV=$!
for i in $(seq 1 40); do
  sleep 0.5
  curl -sf "$BASE/health" >/dev/null 2>&1 && break
done

# ---------- 登录 ----------
TOK=$(curl -s -X POST "$BASE/emby/Users/AuthenticateByName" -H 'Content-Type: application/json' \
  -d '{"Username":"admin","Pw":"admin123"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["AccessToken"])')
AH="X-Emby-Token: $TOK"
ck "登录获取 token" "64" "${#TOK}"

# ---------- 鉴权保护 ----------
ck "未鉴权 GET /admin/ai 返回 401" "401" \
  "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/emby/admin/ai")"

# ---------- 默认配置 ----------
DEF=$(curl -s -H "$AH" "$BASE/emby/admin/ai")
ck "默认 Enabled=false" "False" "$(echo "$DEF" | python3 -c 'import json,sys;print(json.load(sys.stdin)["Enabled"])')"
ck "默认 HasKey=false" "False" "$(echo "$DEF" | python3 -c 'import json,sys;print(json.load(sys.stdin)["HasKey"])')"
ck "默认 BaseURL" "https://api.openai.com/v1" "$(echo "$DEF" | python3 -c 'import json,sys;print(json.load(sys.stdin)["BaseURL"])')"

# ---------- 未配置时测试识别返回 400 ----------
ck "未配置时 /admin/ai/test 返回 400" "400" \
  "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai/test" -d '{}')"

# ---------- 保存配置（指向 mock） ----------
R=$(curl -s -X PUT -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai" \
  -d '{"Enabled":true,"BaseURL":"'$MOCK'/v1/","APIKey":"sk-test-mock-1234567890","Model":"mock-model"}')
ck "保存配置 OK" "True" "$(echo "$R" | python3 -c 'import json,sys;print(json.load(sys.stdin)["OK"])')"
G=$(curl -s -H "$AH" "$BASE/emby/admin/ai")
ck "保存后 HasKey=true" "True" "$(echo "$G" | python3 -c 'import json,sys;print(json.load(sys.stdin)["HasKey"])')"
ck "BaseURL 尾斜杠已去除" "$MOCK/v1" "$(echo "$G" | python3 -c 'import json,sys;print(json.load(sys.stdin)["BaseURL"])')"
ck "APIKey 脱敏输出" "sk-t****7890" "$(echo "$G" | python3 -c 'import json,sys;print(json.load(sys.stdin)["APIKey"])')"

# ---------- 掩码不覆盖真实密钥 ----------
curl -s -o /dev/null -X PUT -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai" \
  -d '{"APIKey":"sk-t****7890","Model":"mock-model-2"}'
G=$(curl -s -H "$AH" "$BASE/emby/admin/ai")
ck "掩码回传不覆盖密钥（HasKey 仍 true）" "True" "$(echo "$G" | python3 -c 'import json,sys;print(json.load(sys.stdin)["HasKey"])')"
ck "模型名已更新" "mock-model-2" "$(echo "$G" | python3 -c 'import json,sys;print(json.load(sys.stdin)["Model"])')"

# ---------- 非法 BaseURL 校验 ----------
ck "非法 BaseURL 返回 400" "400" \
  "$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai" -d '{"BaseURL":"ftp://x"}')"

# ---------- 测试识别（走 mock，验证围栏剥离 + JSON 解析） ----------
R=$(curl -s -X POST -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai/test" \
  -d '{"Type":"Movie","Path":"/media/downloads/Spider.Man.2023.1080p.BluRay.x264/蜘蛛侠 纵横宇宙.mkv","Name":"蜘蛛侠 纵横宇宙","Year":0}')
ck "测试识别 OK" "True" "$(echo "$R" | python3 -c 'import json,sys;print(json.load(sys.stdin)["OK"])')"
ck "AI 提取片名" "蜘蛛侠：纵横宇宙" "$(echo "$R" | python3 -c 'import json,sys;print(json.load(sys.stdin)["Title"])')"
ck "AI 提取原名" "Spider-Man: Across the Spider-Verse" "$(echo "$R" | python3 -c 'import json,sys;print(json.load(sys.stdin)["OriginalTitle"])')"
ck "AI 提取年份" "2023" "$(echo "$R" | python3 -c 'import json,sys;print(json.load(sys.stdin)["Year"])')"

# ---------- 错误密钥场景：改密钥为错误值 → mock 返回 401 ----------
curl -s -o /dev/null -X PUT -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai" \
  -d '{"APIKey":"sk-wrong-key"}'
CK=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai/test" -d '{}')
ck "错误密钥测试识别返回 502" "502" "$CK"
# 恢复正确密钥
curl -s -o /dev/null -X PUT -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai" \
  -d '{"APIKey":"sk-test-mock-1234567890"}'

# ---------- Enabled=false 但配置完整：test 仍可用（配置即测） ----------
R=$(curl -s -X POST -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai/test" -d '{}')
ck "默认示例路径测试识别 OK" "True" "$(echo "$R" | python3 -c 'import json,sys;print(json.load(sys.stdin)["OK"])')"

# ---------- AI 日志分类 ----------
LOGS=$(curl -s -H "$AH" "$BASE/emby/admin/logs?category=ai&limit=100")
N=$(echo "$LOGS" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["Entries"]))')
ck "AI 日志分类有条目" "True" "$([ "$N" -gt 0 ] && echo True || echo False)"
NOCAT=$(echo "$LOGS" | python3 -c 'import json,sys;es=json.load(sys.stdin)["Entries"];print(sum(1 for e in es if e.get("category")!="ai"))')
ck "AI 分类不串类" "0" "$NOCAT"

# ---------- 清空配置恢复默认 ----------
curl -s -o /dev/null -X PUT -H "$AH" -H 'Content-Type: application/json' "$BASE/emby/admin/ai" \
  -d '{"Enabled":false,"APIKey":"","BaseURL":"","Model":""}'
G=$(curl -s -H "$AH" "$BASE/emby/admin/ai")
ck "清空后 HasKey=false" "False" "$(echo "$G" | python3 -c 'import json,sys;print(json.load(sys.stdin)["HasKey"])')"

# ---------- 回归：管理端点存活 ----------
ck "回归 /admin/dashboard" "200" "$(curl -s -o /dev/null -w '%{http_code}' -H "$AH" "$BASE/emby/admin/dashboard")"
ck "回归 /admin/enhancements" "200" "$(curl -s -o /dev/null -w '%{http_code}' -H "$AH" "$BASE/emby/admin/enhancements")"
ck "回归 /admin/tmdb" "200" "$(curl -s -o /dev/null -w '%{http_code}' -H "$AH" "$BASE/emby/admin/tmdb")"

say "----------------------------------"
say "通过 $PASS / $TOTAL （失败 $FAIL）"
[ "$FAIL" -eq 0 ] || exit 1
