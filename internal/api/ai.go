// Package api AI 识别辅助：配置读写与测试识别端点。
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go-emby/internal/logx"
	"go-emby/internal/scanner"
)

// adminAI GET/PUT /admin/ai AI 识别辅助配置。
func (a *App) adminAI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := scanner.LoadAIConfig()
		masked := cfg.APIKey
		if len(masked) > 8 {
			masked = masked[:4] + "****" + masked[len(masked)-4:]
		}
		a.json(w, 200, M{
			"Enabled": cfg.Enabled,
			"BaseURL": cfg.BaseURL,
			"APIKey":  masked,
			"HasKey":  cfg.APIKey != "",
			"Model":   cfg.Model,
		})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		a.fail(w, 405, "方法不支持")
		return
	}
	var b struct {
		Enabled *bool   `json:"Enabled"`
		BaseURL *string `json:"BaseURL"`
		APIKey  *string `json:"APIKey"`
		Model   *string `json:"Model"`
	}
	if err := bodyJSON(r, &b); err != nil {
		a.fail(w, 400, "请求体格式错误")
		return
	}
	cfg := scanner.LoadAIConfig()
	if b.Enabled != nil {
		cfg.Enabled = *b.Enabled
	}
	if b.BaseURL != nil {
		u, err := scanner.NormalizeAIBaseURL(*b.BaseURL)
		if err != nil {
			a.fail(w, 400, err.Error())
			return
		}
		cfg.BaseURL = u
	}
	// APIKey 含掩码（未修改）时不覆盖
	if b.APIKey != nil {
		incomingKey := strings.TrimSpace(*b.APIKey)
		maskedCurrent := cfg.APIKey
		if len(maskedCurrent) > 8 {
			maskedCurrent = maskedCurrent[:4] + "****" + maskedCurrent[len(maskedCurrent)-4:]
		}
		if incomingKey != "" && incomingKey != maskedCurrent {
			cfg.APIKey = incomingKey
		}
	}
	if b.Model != nil {
		cfg.Model = strings.TrimSpace(*b.Model)
	}
	normalizedBaseURL, err := scanner.NormalizeAIBaseURL(cfg.BaseURL)
	if err != nil {
		a.fail(w, 400, err.Error())
		return
	}
	cfg.BaseURL = normalizedBaseURL
	if cfg.Enabled && (cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "") {
		a.fail(w, 400, "启用前请完整填写 API 地址、密钥与模型名")
		return
	}
	if err := scanner.SaveAIConfig(cfg); err != nil {
		a.fail(w, 500, "AI 配置保存失败")
		return
	}
	logx.InfoC(logx.CatAI, "AI 识别辅助配置已更新（启用=%v 模型=%s）", cfg.Enabled, cfg.Model)
	a.json(w, 200, M{"OK": true})
}

// adminAITest POST /admin/ai/test 测试 AI 识别（传入示例路径，返回提取的关键词）。
func (a *App) adminAITest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.fail(w, 405, "方法不支持")
		return
	}
	var b struct {
		Type string `json:"Type"` // Movie / Series
		Path string `json:"Path"`
		Name string `json:"Name"`
		Year int    `json:"Year"`
	}
	if err := bodyJSON(r, &b); err != nil {
		a.fail(w, 400, "请求体格式错误")
		return
	}
	if b.Type != "Series" {
		b.Type = "Movie"
	}
	if strings.TrimSpace(b.Path) == "" {
		// 默认示例：一个带发布信息的文件路径
		b.Path = "/media/downloads/Spider.Man.Across.the.Spider.Verse.2023.1080p.BluRay.x264-GROUP/蜘蛛侠 纵横宇宙 2023 1080p.mkv"
		b.Name = "蜘蛛侠 纵横宇宙"
		b.Year = 0
	}
	cfg := scanner.LoadAIConfig()
	if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "" {
		a.fail(w, 400, "请先完整填写 API 地址、密钥与模型名并保存")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	k, err := scanner.AIExtractKeywords(ctx, cfg, b.Type, b.Path, b.Name, b.Year)
	if err != nil {
		logx.WarnC(logx.CatAI, "AI 识别测试失败: %v", err)
		a.fail(w, 502, "AI 识别失败: "+err.Error())
		return
	}
	logx.InfoC(logx.CatAI, "AI 识别测试成功：「%s」(%d)", k.Title, k.Year)
	a.json(w, 200, M{
		"OK":            true,
		"Title":         k.Title,
		"OriginalTitle": k.OriginalTitle,
		"Year":          k.Year,
	})
}
