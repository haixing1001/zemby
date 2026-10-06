// Package api 图片服务。
package api

import (
        "bytes"
        "crypto/hmac"
        "crypto/sha256"
        "encoding/hex"
        "fmt"
        "image"
        "image/color"
        "image/jpeg"
        "image/png"
        _ "image/gif"
        "net/http"
        "os"
        "strings"

        "go-emby/internal/models"
)

// imageSecret 图片签名密钥。
var imageSecret = stableHash("go-emby-image-secret")

// imgSign 计算图片访问 tag。
func imgSign(itemID, kind, rev string) string {
        mac := hmac.New(sha256.New, []byte(imageSecret))
        mac.Write([]byte(itemID + "|" + kind + "|" + rev))
        return hex.EncodeToString(mac.Sum(nil))[:32]
}

// imgTag 图片标签。
func imgTag(it *models.Item, kind string) string {
        return imgSign(it.ID, kind, it.ImageRev)
}

// images 图片端点：/items/{id}/images/{type}[/...]
func (a *App) images(w http.ResponseWriter, r *http.Request, id, imgType string) {
        id = strings.ToLower(id)
        // 收藏虚拟库封面
        if id == favLibID {
                if imgType == "" {
                        // 图片列表
                        if fileExists(a.favCoverPath()) {
                                a.json(w, 200, []M{{"ImageType": "Primary", "ImageIndex": 0, "ImageTag": "fav"}})
                        } else {
                                a.json(w, 200, []M{})
                        }
                        return
                }
                kind := normalizeImageType(imgType)
                p := a.favCoverPath()
                if kind == "primary" && fileExists(p) {
                        a.serveImageFile(w, r, p, "fav")
                        return
                }
                if kind == "primary" || kind == "thumb" {
                        a.servePlaceholder(w, r)
                        return
                }
                a.fail(w, 404, "图片不存在")
                return
        }
        var it models.Item
        if err := a.db.First(&it, "id = ?", id).Error; err != nil {
                a.fail(w, 404, "条目不存在")
                return
        }
        // /Items/{id}/Images → 列表
        if imgType == "" {
                a.json(w, 200, a.imageList(&it))
                return
        }
        kind := normalizeImageType(imgType)
        if kind == "" {
                a.fail(w, 404, "未知图片类型")
                return
        }
        path := a.imagePath(&it, kind)
        if path == "" {
                if kind == "primary" || kind == "thumb" {
                        a.servePlaceholder(w, r)
                        return
                }
                a.fail(w, 404, "图片不存在")
                return
        }
        a.serveImageFile(w, r, path, imgTag(&it, kind))
}

// imageList 图片信息数组。
func (a *App) imageList(it *models.Item) []M {
        out := []M{}
        if it.Poster != "" {
                out = append(out, M{"ImageType": "Primary", "ImageIndex": 0, "ImageTag": imgTag(it, "primary")})
        }
        if it.Backdrop != "" {
                out = append(out, M{"ImageType": "Backdrop", "ImageIndex": 0, "ImageTag": imgTag(it, "backdrop")})
        }
        if it.Thumb != "" {
                out = append(out, M{"ImageType": "Thumb", "ImageIndex": 0, "ImageTag": imgTag(it, "thumb")})
        }
        if it.Logo != "" {
                out = append(out, M{"ImageType": "Logo", "ImageIndex": 0, "ImageTag": imgTag(it, "logo")})
        }
        return out
}

func normalizeImageType(t string) string {
        t = strings.ToLower(t)
        // 处理 primary/0、primary/0/{tag} 等深路径
        if i := strings.IndexByte(t, '/'); i > 0 {
                t = t[:i]
        }
        t = strings.TrimSuffix(t, ".jpg")
        t = strings.TrimSuffix(t, ".png")
        switch t {
        case "primary", "poster", "art":
                return "primary"
        case "backdrop", "fanart", "background":
                return "backdrop"
        case "thumb", "landscape":
                return "thumb"
        case "logo", "clearlogo":
                return "logo"
        case "banner":
                return "banner"
        }
        return ""
}

// imagePath 取图片路径。
func (a *App) imagePath(it *models.Item, kind string) string {
        switch kind {
        case "primary":
                if it.Poster != "" {
                        return it.Poster
                }
                // 剧集回退：用季/集的图
                if it.Type == "Series" || it.Type == "Season" {
                        var child models.Item
                        if a.db.Where("series_id = ? AND poster <> ''", it.ID).Order("parent_index_number").First(&child).Error == nil {
                                return child.Poster
                        }
                }
                if it.Type == "Episode" && it.Thumb != "" {
                        return it.Thumb
                }
        case "backdrop":
                if it.Backdrop != "" {
                        return it.Backdrop
                }
        case "thumb":
                if it.Thumb != "" {
                        return it.Thumb
                }
                if it.Type == "Episode" && it.SeasonID != "" {
                        var s models.Item
                        if a.db.Where("id = ?", it.SeasonID).First(&s).Error == nil && s.Poster != "" {
                                return s.Poster
                        }
                }
        case "logo":
                if it.Logo != "" {
                        return it.Logo
                }
        case "banner":
                if it.Backdrop != "" {
                        return it.Backdrop
                }
        }
        return ""
}

// serveImageFile 图片文件服务（缩放 + 缓存）。
func (a *App) serveImageFile(w http.ResponseWriter, r *http.Request, path, tag string) {
        data, err := os.ReadFile(path)
        if err != nil {
                a.fail(w, 404, "图片读取失败")
                return
        }
        w.Header().Set("ETag", `"`+tag+`"`)
        w.Header().Set("Cache-Control", "private, max-age=86400")
        if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, tag) {
                w.WriteHeader(http.StatusNotModified)
                return
        }
        maxW := qInt(r, "MaxWidth", 0)
        maxH := qInt(r, "MaxHeight", 0)
        if maxW == 0 {
                maxW = qInt(r, "Width", 0)
        }
        if maxH == 0 {
                maxH = qInt(r, "Height", 0)
        }
        if maxW > 0 || maxH > 0 {
                if scaled, ok := scaleImage(data, maxW, maxH); ok {
                        data = scaled
                }
        }
        ct := "image/jpeg"
        if !isJPEG(data) {
                ct = "image/png"
        }
        w.Header().Set("Content-Type", ct)
        w.Header().Set("Content-Length", fmt.Sprint(len(data)))
        w.Write(data)
}

func isJPEG(b []byte) bool {
        return len(b) > 2 && b[0] == 0xFF && b[1] == 0xD8
}

// scaleImage 等比缩小（最近邻，零依赖）。
func scaleImage(data []byte, maxW, maxH int) ([]byte, bool) {
        if maxW <= 0 && maxH <= 0 {
                return data, false
        }
        src, _, err := image.Decode(bytes.NewReader(data))
        if err != nil {
                return data, false
        }
        b := src.Bounds()
        w, h := b.Dx(), b.Dy()
        if w == 0 || h == 0 {
                return data, false
        }
        scale := 1.0
        if maxW > 0 {
                scale = float64(maxW) / float64(w)
        }
        if maxH > 0 {
                s2 := float64(maxH) / float64(h)
                if s2 < scale {
                        scale = s2
                }
        }
        if scale >= 1 {
                return data, false
        }
        nw, nh := int(float64(w)*scale), int(float64(h)*scale)
        if nw < 1 {
                nw = 1
        }
        if nh < 1 {
                nh = 1
        }
        dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
        for y := 0; y < nh; y++ {
                sy := y * h / nh
                for x := 0; x < nw; x++ {
                        sx := x * w / nw
                        dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
                }
        }
        var buf bytes.Buffer
        if isJPEG(data) {
                _ = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
        } else {
                _ = png.Encode(&buf, dst)
        }
        return buf.Bytes(), true
}

// servePlaceholder 程序生成占位海报。
func (a *App) servePlaceholder(w http.ResponseWriter, r *http.Request) {
        const W, H = 360, 540
        img := image.NewRGBA(image.Rect(0, 0, W, H))
        for y := 0; y < H; y++ {
                for x := 0; x < W; x++ {
                        t := float64(x+y) / float64(W+H)
                        img.Set(x, y, color.RGBA{
                                uint8(40 + 60*t), uint8(44 + 50*t), uint8(70 + 80*t), 255,
                        })
                }
        }
        var buf bytes.Buffer
        _ = png.Encode(&buf, img)
        w.Header().Set("Content-Type", "image/png")
        w.Header().Set("Cache-Control", "public, max-age=86400")
        w.Write(buf.Bytes())
}
