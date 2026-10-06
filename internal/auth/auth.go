// Package auth 认证：登录、令牌校验、设备解析。
package auth

import (
        "context"
        "net/http"
        "regexp"
        "strings"
        "time"

        "golang.org/x/crypto/bcrypt"

        "go-emby/internal/db"
        "go-emby/internal/models"
)

// Identity 请求身份。
type Identity struct {
        User     *models.User
        Token    string // 原始 token
        IsAPIKey bool
        DeviceID string
        Client   string
        Device   string
        Version  string
}

type ctxKey struct{}

// From 获取请求身份。
func From(r *http.Request) *Identity {
        id, _ := r.Context().Value(ctxKey{}).(*Identity)
        return id
}

var authField = regexp.MustCompile(`(?i)(Token|DeviceId|Client|Device|Version)\s*=\s*"([^"]*)"`)

// AuthInfo 解析 X-Emby-Authorization / Authorization 头。
func AuthInfo(r *http.Request) (client, device, deviceID, version, token string) {
        v := r.Header.Get("X-Emby-Authorization")
        if v == "" {
                v = r.Header.Get("Authorization")
        }
        if v == "" {
                v = r.URL.Query().Get("X-Emby-Authorization")
        }
        if v != "" {
                for _, m := range authField.FindAllStringSubmatch(v, -1) {
                        switch strings.ToLower(m[1]) {
                        case "token":
                                token = m[2]
                        case "deviceid":
                                deviceID = m[2]
                        case "client":
                                client = m[2]
                        case "device":
                                device = m[2]
                        case "version":
                                version = m[2]
                        }
                }
        }
        // Bearer 兜底
        if token == "" && strings.HasPrefix(v, "Bearer ") {
                token = strings.TrimSpace(v[7:])
        }
        if token == "" { // query 兜底
                token = r.URL.Query().Get("Token")
        }
        if deviceID == "" {
                deviceID = r.Header.Get("X-Emby-Device-Id")
                if deviceID == "" {
                        deviceID = r.URL.Query().Get("DeviceId")
                }
        }
        if deviceID == "" { // 无设备 ID 时以 IP 代替，保证设备统计可用
                deviceID = ClientIP(r)
        }
        if client == "" {
                client = r.Header.Get("User-Agent")
                if len(client) > 80 {
                        client = client[:80]
                }
        }
        return
}

// ClientIP 提取客户端 IP。
func ClientIP(r *http.Request) string {
        if v := r.Header.Get("X-Real-IP"); v != "" {
                return v
        }
        if v := r.Header.Get("X-Forwarded-For"); v != "" {
                if i := strings.IndexByte(v, ','); i > 0 {
                        return strings.TrimSpace(v[:i])
                }
                return strings.TrimSpace(v)
        }
        if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
                return r.RemoteAddr[:i]
        }
        return r.RemoteAddr
}

// rawToken 按常见顺序提取令牌。
func rawToken(r *http.Request) string {
        for _, h := range []string{"X-Emby-Token", "X-MediaBrowser-Token", "X-Emby-Api-Key"} {
                if v := r.Header.Get(h); v != "" {
                        return v
                }
        }
        q := r.URL.Query()
        for _, k := range []string{"api_key", "X-Emby-Token", "X-MediaBrowser-Token", "api_key_1"} {
                if v := q.Get(k); v != "" {
                        return v
                }
        }
        _, _, _, _, t := AuthInfo(r)
        return t
}

// Middleware 鉴权中间件。public 的路径不做认证。
func Middleware(next http.Handler, isPublic func(*http.Request) bool) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                if isPublic(r) {
                        // 仍然尽力解析身份供下游使用
                        id := tryAuth(r)
                        next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
                        return
                }
                id := tryAuth(r)
                if id == nil {
                        write401(w)
                        return
                }
                // 更新 token 活跃时间
                if !id.IsAPIKey {
                        db.DB.Model(&models.Token{}).Where("hash = ?", models.HashToken(id.Token)).
                                Update("last_seen", time.Now())
                }
                next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
        })
}

func write401(w http.ResponseWriter) {
        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        w.WriteHeader(http.StatusUnauthorized)
        w.Write([]byte(`{"error":"请先登录","Message":"请先登录"}`))
}

// tryAuth 尝试认证，失败返回 nil。
func tryAuth(r *http.Request) *Identity {
        tok := rawToken(r)
        if tok == "" {
                return nil
        }
        id := &Identity{Token: tok}
        id.DeviceID, id.Client, id.Device, id.Version = "", "", "", ""
        c, d, di, ver, _ := AuthInfo(r)
        id.Client, id.Device, id.DeviceID, id.Version = c, d, di, ver

        // API Key？
        var ak models.Setting
        if err := db.DB.Where("`key` = ?", "apikey:"+models.HashToken(tok)).First(&ak).Error; err == nil {
                id.IsAPIKey = true
                return id
        }
        // 后台「API 管理」签发的密钥：等同管理员权限（与 Emby 行为一致）。
        var key models.ApiKey
        if err := db.DB.Where("hash = ?", models.HashToken(tok)).First(&key).Error; err == nil {
                id.IsAPIKey = true
                var u models.User
                if err := db.DB.Where("is_admin = ?", true).Order("created_at").First(&u).Error; err == nil {
                        id.User = &u
                }
                now := time.Now()
                if key.LastSeen == nil || now.Sub(*key.LastSeen) > time.Minute {
                        db.DB.Model(&models.ApiKey{}).Where("id = ?", key.ID).Update("last_seen", now)
                }
                return id
        }
        var t models.Token
        if err := db.DB.Where("hash = ?", models.HashToken(tok)).First(&t).Error; err != nil {
                return nil
        }
        if time.Since(t.CreatedAt) > 30*24*time.Hour {
                db.DB.Delete(&t)
                return nil
        }
        var u models.User
        if err := db.DB.First(&u, "id = ?", t.UserID).Error; err != nil {
                return nil
        }
        id.User = &u
        return id
}

// CheckPassword 校验密码。
func CheckPassword(hash, pw string) bool {
        return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// HashPassword 生成 bcrypt。
func HashPassword(pw string) (string, error) {
        b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
        return string(b), err
}
