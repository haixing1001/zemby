#!/bin/bash
# zemby 新后台端点冒烟测试（单进程内完成）
set -u
B=http://127.0.0.1:18097/emby
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  ✔ $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  ✘ $1  <<< $2"; }
check() { # name, actual, expect-substr
  if echo "$2" | grep -q "$3"; then ok "$1"; else bad "$1" "$2"; fi
}

pkill -f zemby-test 2>/dev/null
rm -rf /tmp/ztest/config; mkdir -p /tmp/ztest/config

cd /tmp/ztest
setsid env GEMBY_DATA=/tmp/ztest/config GEMBY_ADDR=:18097 MEDIA_ROOTS=/tmp/ztest/media GEMBY_SERVER_NAME=ZembyTest /tmp/zemby-test > server.log 2>&1 &
SRV=$!
sleep 2

echo "== 登录 =="
LOGIN=$(curl -s -X POST $B/Users/AuthenticateByName -H "Content-Type: application/json" -H 'X-Emby-Authorization: MediaBrowser Client="t", Device="t", DeviceId="t1", Version="1"' -d '{"Username":"admin","Pw":"admin123"}')
check "登录" "$LOGIN" "AccessToken"
TOK=$(echo "$LOGIN" | python3 -c "import json,sys;print(json.load(sys.stdin)['AccessToken'])" 2>/dev/null)
AH="X-Emby-Token: $TOK"

echo "== 建库与扫描 =="
R1=$(curl -s -X POST $B/admin/libraries -H "$AH" -H "Content-Type: application/json" -d '{"Name":"电影","Path":"/tmp/ztest/media/movies","Type":"movies"}')
check "创建电影库" "$R1" "ID"
R2=$(curl -s -X POST $B/admin/libraries -H "$AH" -H "Content-Type: application/json" -d '{"Name":"剧集","Path":"/tmp/ztest/media/tv","Type":"tvshows"}')
check "创建剧集库" "$R2" "ID"
sleep 6
LIBS=$(curl -s $B/admin/libraries -H "$AH")
check "电影库条目>=2" "$LIBS" '"ItemCount":2'
MOVIE_LIB=$(echo "$LIBS" | python3 -c "import json,sys;d=json.load(sys.stdin);print([l['ID'] for l in d if l['Name']=='电影'][0])")
TV_LIB=$(echo "$LIBS" | python3 -c "import json,sys;d=json.load(sys.stdin);print([l['ID'] for l in d if l['Name']=='剧集'][0])")

echo "== 控制台 =="
DASH=$(curl -s $B/admin/dashboard -H "$AH")
check "仪表盘-电影数" "$DASH" '"Movies":2'
check "仪表盘-剧集数" "$DASH" '"Episodes":1'
check "仪表盘-运行时长" "$DASH" '"Uptime"'
check "仪表盘-CPU" "$DASH" '"CPUPercent"'
check "仪表盘-内存" "$DASH" '"MemMB"'
check "仪表盘-正在播放" "$DASH" '"NowPlaying"'
check "仪表盘-任务" "$DASH" '"Tasks"'
check "仪表盘-活跃" "$DASH" '"Activity"'

echo "== 刮削管理 =="
SC=$(curl -s $B/admin/scrape/config -H "$AH")
check "刮削配置读取" "$SC" '"enabled"'
SCN=$(curl -s -X PUT $B/admin/scrape/config -H "$AH" -H "Content-Type: application/json" -d '{"Enabled":true,"Realtime":false,"AutoRefresh":true,"Manual":true,"Overwrite":"skip"}')
check "刮削配置保存" "$SCN" '"OK"'
ST=$(curl -s $B/admin/scrape/state -H "$AH")
check "刮削状态" "$ST" '"State"'
# 未配置 TMDB Key → 应产生失败记录
CT=$(curl -s -X POST $B/admin/scrape/control -H "$AH" -H "Content-Type: application/json" -d '{"Action":"start"}')
check "刮削开始" "$CT" '"Queued"'
sleep 3
FD=$(curl -s "$B/admin/scrape/failed" -H "$AH")
check "失败清单含未配置Key" "$FD" "TMDB"
RT=$(curl -s -X POST $B/admin/scrape/retry -H "$AH" -H "Content-Type: application/json")
check "重试失败" "$RT" '"Queued"'
PA=$(curl -s -X POST $B/admin/scrape/control -H "$AH" -H "Content-Type: application/json" -d '{"Action":"pause"}')
check "刮削暂停" "$PA" '"paused"'
SO=$(curl -s -X POST $B/admin/scrape/control -H "$AH" -H "Content-Type: application/json" -d '{"Action":"stop"}')
check "刮削停止" "$SO" '"idle"'

echo "== 媒体信息提取 =="
PC=$(curl -s $B/admin/probe/config -H "$AH")
check "提取配置读取" "$PC" '"concurrency"'
PCN=$(curl -s -X PUT $B/admin/probe/config -H "$AH" -H "Content-Type: application/json" -d '{"OnBrowse":true,"PreloadNext":true,"Persist":true,"SaveDir":"/tmp/ztest/config/media-info","Concurrency":3}')
check "提取配置保存(并发3)" "$PCN" '"OK"'
PC2=$(curl -s $B/admin/probe/config -H "$AH")
check "并发已生效" "$PC2" '"concurrency":3'
BS=$(curl -s -X POST $B/admin/probe/batch -H "$AH" -H "Content-Type: application/json" -d '{"Action":"start"}')
check "批量提取启动" "$BS" '"Queued"'
sleep 8
PS=$(curl -s $B/admin/probe/status -H "$AH")
check "提取状态-完成>0" "$PS" '"Completed":[1-9]'
# 浏览时提取（详情触发）
IT=$(curl -s "$B/Items?ParentId=$MOVIE_LIB&Recursive=true&IncludeItemTypes=Movie" -H "$AH")
MID=$(echo "$IT" | python3 -c "import json,sys;d=json.load(sys.stdin);print(d['Items'][0]['Id'])")
DET=$(curl -s $B/Items/$MID -H "$AH")
check "条目详情" "$DET" '"Name"'
sleep 4

echo "== 媒体库扩展操作 =="
PA2=$(curl -s -X POST $B/admin/libraries/$MOVIE_LIB -H "$AH" -H "Content-Type: application/json" -d '{"Name":"院线电影"}')
check "重命名" "$PA2" '"OK"'
PH=$(curl -s -X POST $B/admin/libraries/$MOVIE_LIB -H "$AH" -H "Content-Type: application/json" -d '{"Hidden":true}')
check "隐藏" "$PH" '"OK"'
V=$(curl -s $B/Users/me/Views -H "$AH")
if echo "$V" | grep -q "院线电影"; then bad "隐藏库不出现在Views" "$V"; else ok "隐藏库不出现在Views"; fi
PH2=$(curl -s -X POST $B/admin/libraries/$MOVIE_LIB -H "$AH" -H "Content-Type: application/json" -d '{"Hidden":false}')
check "取消隐藏" "$PH2" '"OK"'
PD=$(curl -s -X POST $B/admin/libraries/$MOVIE_LIB -H "$AH" -H "Content-Type: application/json" -d '{"DefaultSort":"ProductionYear|Descending"}')
check "默认排序" "$PD" '"OK"'
V2=$(curl -s $B/Users/me/Views -H "$AH")
check "Views携带DefaultSort" "$V2" 'ProductionYear|Descending'
PA3=$(curl -s -X POST $B/admin/libraries/$MOVIE_LIB -H "$AH" -H "Content-Type: application/json" -d '{"AddFolder":"/tmp/ztest/media/movies"}')
if echo "$PA3" | grep -q "OK"; then bad "重复目录应被拒" "$PA3"; else ok "重复目录拒绝"; fi

echo "== 封面 =="
GCV=$(curl -s -X POST $B/admin/libraries/$MOVIE_LIB/cover/generate -H "$AH" -H "Content-Type: application/json")
check "生成封面" "$GCV" '"OK"'
CODE=$(curl -s -o /tmp/ztest/poster.jpg -w "%{http_code}" "$B/admin/libraries/$MOVIE_LIB/poster?api_key=$TOK")
check "封面可访问" "$CODE" "200"
RCV=$(curl -s -X DELETE $B/admin/libraries/$MOVIE_LIB/cover -H "$AH" -o /dev/null -w "%{http_code}")
check "移除封面" "$RCV" "204"
ICV=$(curl -s -X POST $B/admin/libraries/$MOVIE_LIB/cover -H "$AH" -H "Content-Type: application/json" -d '{"Path":"/tmp/ztest/media/poster.jpg"}')
check "插入封面" "$ICV" '"OK"'

echo "== 增强功能 =="
SG=$(curl -s $B/admin/settings -H "$AH")
check "设置读取" "$SG" '"ServerName"'
SP=$(curl -s -X PUT $B/admin/settings -H "$AH" -H "Content-Type: application/json" -d '{"ServerName":"我的影库"}')
check "设置保存" "$SP" '"OK"'
SG2=$(curl -s $B/admin/settings -H "$AH")
check "名称生效" "$SG2" "我的影库"
PW=$(curl -s -X PUT $B/admin/password -H "$AH" -H "Content-Type: application/json" -d '{"Old":"admin123","New":"admin123"}')
check "改密(同密码回写)" "$PW" '"OK"'
SUB=$(curl -s $B/admin/subtitles -H "$AH")
check "外挂字幕清单" "$SUB" "chs.srt"
AP=$(curl -s $B/admin/api -H "$AH")
check "API文档" "$AP" "AuthenticateByName"

echo "== 其他 =="
SS=$(curl -s -X POST $B/admin/scan-stop -H "$AH")
check "停止扫描" "$SS" '"OK"'
AC=$(curl -s -X DELETE $B/admin/activity -H "$AH" -o /dev/null -w "%{http_code}")
check "清空活跃" "$AC" "204"
LG=$(curl -s "$B/admin/logs?limit=5" -H "$AH")
check "日志" "$LG" "Entries"

echo
echo "======================================"
echo " 通过 $PASS / 失败 $FAIL"
echo "======================================"
kill $SRV 2>/dev/null
pkill -f zemby-test 2>/dev/null
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
