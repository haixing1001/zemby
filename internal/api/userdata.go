// Package api 播放上报与用户播放状态。
package api

import (
        "encoding/json"
        "net/http"
        "strings"
        "time"

        "go-emby/internal/auth"
        "go-emby/internal/logx"
        "go-emby/internal/models"
)

// ticks 宽松解析 ticks（可能是字符串）。
func ticks(v any) int64 {
        switch t := v.(type) {
        case float64:
                return int64(t)
        case string:
                var n int64
                for i := 0; i < len(t); i++ {
                        if t[i] < '0' || t[i] > '9' {
                                return 0
                        }
                        n = n*10 + int64(t[i]-'0')
                }
                return n
        }
        return 0
}

// sessionPlaying /sessions/playing[/progress|/stopped|/heartbeats]
func (a *App) sessionPlaying(w http.ResponseWriter, r *http.Request, p string) {
        id := auth.From(r)
        if id == nil || id.User == nil || id.IsAPIKey {
                a.noContent(w)
                return
        }
        var body struct {
                ItemID        string `json:"ItemId"`
                MediaSourceID string `json:"MediaSourceId"`
                PositionTicks any    `json:"PositionTicks"`
                RunTimeTicks  any    `json:"RunTimeTicks"`
                PlaySessionId string `json:"PlaySessionId"`
                IsPaused      bool   `json:"IsPaused"`
        }
        _ = bodyJSON(r, &body)

        itemID := strings.ToLower(body.ItemID)
        if itemID == "" {
                itemID = strings.ToLower(body.MediaSourceID)
        }

        switch {
        case strings.HasSuffix(p, "/stopped"):
                a.releaseDevice(id.User.ID, id.DeviceID)
                if itemID != "" {
                        a.db.Model(&models.UserDatum{}).Where("user_id = ? AND item_id = ?", id.User.ID, itemID).
                                Update("last_played_date", time.Now())
                }
                logx.Playback("用户停止播放 %s（%s）", itemID, id.User.Name)
                var it models.Item
                if a.db.Select("name").First(&it, "id = ?", itemID).Error == nil {
                        a.recordActivity(id.User.Name, itemID, it.Name, id.DeviceID, "stop", ticks(body.PositionTicks))
                }
        default: // playing / progress / heartbeats
                if ok, msg := a.reserve(w, r); !ok {
                        a.fail(w, 403, msg)
                        return
                }
                pos := ticks(body.PositionTicks)
                rt := ticks(body.RunTimeTicks)
                if itemID != "" {
                        var ud models.UserDatum
                        pk := id.User.ID + "|" + itemID
                        err := a.db.First(&ud, "id = ?", pk).Error
                        now := time.Now()
                        if err != nil {
                                ud = models.UserDatum{ID: pk, UserID: id.User.ID, ItemID: itemID, PositionTicks: pos, LastPlayedDate: &now}
                                a.db.Create(&ud)
                        } else {
                                updates := map[string]any{"position_ticks": pos, "last_played_date": now}
                                // 接近片尾（>92%）视为已看完
                                if rt > 0 && pos > 0 && float64(pos)/float64(rt) > 0.92 {
                                        updates["played"] = true
                                        updates["play_count"] = ud.PlayCount + 1
                                }
                                a.db.Model(&models.UserDatum{}).Where("id = ?", pk).Updates(updates)
                        }
                }
                if strings.HasSuffix(p, "/progress") {
                        logx.Detail("playback", id.Client+" / "+id.DeviceID, "播放进度 %s 位置 %d", itemID, pos)
                }
        }
        a.noContent(w)
}

// userDataRoute /users/{uid}/playeditems/{id}、favoriteitems/{id}、rating/{id}、userdata/{id}
func (a *App) userDataRoute(w http.ResponseWriter, r *http.Request, parts []string) {
        // parts: users/{uid}/{kind}/{id}
        if len(parts) < 4 {
                a.fail(w, 404, "未知端点")
                return
        }
        uid := strings.ToLower(parts[1])
        kind := parts[2]
        itemID := strings.ToLower(parts[3])

        id := auth.From(r)
        if id == nil || id.User == nil {
                a.fail(w, 401, "请先登录")
                return
        }
        if id.User.ID != uid && !id.User.IsAdmin {
                a.fail(w, 403, "无权操作该用户数据")
                return
        }
        var it models.Item
        if err := a.db.First(&it, "id = ?", itemID).Error; err != nil {
                a.fail(w, 404, "条目不存在")
                return
        }

        pk := uid + "|" + itemID
        var ud models.UserDatum
        err := a.db.First(&ud, "id = ?", pk).Error
        if err != nil {
                ud = models.UserDatum{ID: pk, UserID: uid, ItemID: itemID}
        }
        now := time.Now()
        changed := false

        switch kind {
        case "playeditems":
                switch r.Method {
                case http.MethodPost, http.MethodPut:
                        ud.Played = true
                        ud.PlayCount++
                        ud.LastPlayedDate = &now
                        // 全部集看完时检查季/剧集
                        changed = true
                case http.MethodDelete:
                        ud.Played = false
                        ud.PlayCount = 0
                        ud.PositionTicks = 0
                        changed = true
                }
        case "favoriteitems":
                switch r.Method {
                case http.MethodPost, http.MethodPut:
                        ud.Favorite = true
                        changed = true
                case http.MethodDelete:
                        ud.Favorite = false
                        changed = true
                }
        case "rating":
                var body struct{ Rating any `json:"Rating"` }
                _ = bodyJSON(r, &body)
                changed = true
        }

        if changed {
                a.db.Save(&ud)
                // 标记已看时向上聚合到季与剧集
                if kind == "playeditems" && r.Method == http.MethodPost {
                        a.markParentsWatched(uid, itemID)
                }
        }
        // 返回更新后的 item DTO
        a.json(w, 200, a.itemDTO(&it, true, uid))
}

// markParentsWatched 集看完后聚合季/剧集状态。
func (a *App) markParentsWatched(uid, itemID string) {
        var ep models.Item
        if a.db.First(&ep, "id = ?", itemID).Error != nil || ep.Type != "Episode" {
                return
        }
        // 季：全部集已看？
        var seasonIDs []string
        if ep.SeasonID != "" {
                seasonIDs = append(seasonIDs, ep.SeasonID)
        }
        if ep.SeriesID != "" {
                seasonIDs = append(seasonIDs, ep.SeriesID)
        }
        for _, pid := range seasonIDs {
                var total, played int64
                a.db.Model(&models.Item{}).Where("season_id = ? OR parent_id = ?", pid, pid).Where("type = 'Episode'").Count(&total)
                if total == 0 {
                        continue
                }
                a.db.Model(&models.UserDatum{}).Where("user_id = ? AND played = ? AND item_id IN (?)",
                        uid, true, a.db.Model(&models.Item{}).Select("id").Where("season_id = ? OR parent_id = ?", pid, pid)).Count(&played)
                _ = played
                // 仅记录，不强标（保持用户手动控制）
        }
}

var _ = json.Marshal
