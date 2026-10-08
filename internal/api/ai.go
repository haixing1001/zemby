// Package api AI 识别辅助：配置读写与测试识别端点。
package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-emby/internal/logx"
	"go-emby/internal/scanner"
)

// maskAIKey AI 供应商 API Key 掩码（保存时据此判定"未修改"）。
func maskAIKey(k string) string {
	if len(k) > 8 {
		return k[:4] + "****" + k[len(k)-4:]
	}
	return k
}

// adminAI GET/PUT /admin/ai AI 识别辅助配置（多供应商）。
func (a *App) adminAI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := scanner.LoadAIConfig()
		provs := make([]M, 0, len(cfg.Providers))
		for _, p := range cfg.Providers {
			provs = append(provs, M{
				"ID": p.ID, "Name": p.Name, "BaseURL": p.BaseURL,
				"APIKey": maskAIKey(p.APIKey), "HasKey": p.APIKey != "",
				"Model": p.Model, "MaxTokens": p.MaxTokens,
			})
		}
		a.json(w, 200, M{"Enabled": cfg.Enabled, "ActiveID": cfg.ActiveID, "Providers": provs})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		a.fail(w, 405, "方法不支持")
		return
	}
	var b struct {
		Enabled  *bool   `json:"Enabled"`
		ActiveID *string `json:"ActiveID"`
		Providers *[]struct {
			ID        string `json:"ID"`
			Name      string `json:"Name"`
			BaseURL   string `json:"BaseURL"`
			APIKey    string `json:"APIKey"`
			Model     string `json:"Model"`
			MaxTokens int    `json:"MaxTokens"`
		} `json:"Providers"`
		// 兼容旧版单供应商表单
		BaseURL   *string `json:"BaseURL"`
		APIKey    *string `json:"APIKey"`
		Model     *string `json:"Model"`
		MaxTokens *int    `json:"MaxTokens"`
	}
	if err := bodyJSON(r, &b); err != nil {
		a.fail(w, 400, "请求体格式错误")
		return
	}
	cfg := scanner.LoadAIConfig()
	oldByID := make(map[string]scanner.AIProvider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		oldByID[p.ID] = p
	}

	if b.Providers != nil {
		if len(*b.Providers) > scanner.MaxAIProviders {
			a.fail(w, 400, fmt.Sprintf("AI 供应商最多保存 %d 个", scanner.MaxAIProviders))
			return
		}
		list := make([]scanner.AIProvider, 0, len(*b.Providers))
		used := map[string]bool{}
		idMap := map[string]string{} // 提交时的 ID（含前端临时 ID）→ 最终 ID
		for i, in := range *b.Providers {
			display := strings.TrimSpace(in.Name)
			if display == "" {
				display = strings.TrimSpace(in.Model)
			}
			if display == "" {
				display = fmt.Sprintf("第 %d 个供应商", i+1)
			}
			u, err := scanner.NormalizeAIBaseURL(in.BaseURL)
			if err != nil {
				a.fail(w, 400, fmt.Sprintf("供应商「%s」：%v", display, err))
				return
			}
			if in.MaxTokens != 0 && (in.MaxTokens < 64 || in.MaxTokens > 4096) {
				a.fail(w, 400, fmt.Sprintf("供应商「%s」的最大输出 tokens 需在 64 到 4096 之间", display))
				return
			}
			p := scanner.AIProvider{
				Name:      display,
				BaseURL:   u,
				Model:     strings.TrimSpace(in.Model),
				MaxTokens: in.MaxTokens,
			}
			id := strings.TrimSpace(in.ID)
			if id == "" || used[id] || oldByID[id].ID == "" {
				// 新供应商：服务端重新分配 ID
				for id == "" || used[id] {
					id = scanner.NewAIProviderID()
				}
			}
			used[id] = true
			p.ID = id
			idMap[strings.TrimSpace(in.ID)] = id
			// APIKey 为空或与掩码相同视为未修改
			if old, ok := oldByID[id]; ok {
				if key := strings.TrimSpace(in.APIKey); key != "" && key != maskAIKey(old.APIKey) {
					p.APIKey = key
				} else {
					p.APIKey = old.APIKey
				}
				if p.MaxTokens == 0 {
					p.MaxTokens = old.MaxTokens
				}
			} else {
				p.APIKey = strings.TrimSpace(in.APIKey)
			}
			list = append(list, p)
		}
		cfg.Providers = list
		if b.ActiveID != nil {
			want := strings.TrimSpace(*b.ActiveID)
			switch {
			case want == "":
				cfg.ActiveID = ""
			default:
				if mapped, ok := idMap[want]; ok {
					want = mapped
				}
				cfg.ActiveID = want
			}
		}
	} else if b.BaseURL != nil || b.APIKey != nil || b.Model != nil || b.MaxTokens != nil {
		// 兼容旧版单供应商表单：更新当前供应商（无则新建）
		prov, ok := cfg.ActiveProvider()
		if !ok {
			prov = scanner.AIProvider{ID: scanner.NewAIProviderID(), Name: "默认供应商"}
			cfg.Providers = append(cfg.Providers, prov)
		}
		if b.BaseURL != nil {
			u, err := scanner.NormalizeAIBaseURL(*b.BaseURL)
			if err != nil {
				a.fail(w, 400, err.Error())
				return
			}
			prov.BaseURL = u
		}
		if b.APIKey != nil {
			if key := strings.TrimSpace(*b.APIKey); key != "" && key != maskAIKey(prov.APIKey) {
				prov.APIKey = key
			}
		}
		if b.Model != nil {
			prov.Model = strings.TrimSpace(*b.Model)
		}
		if b.MaxTokens != nil {
			if *b.MaxTokens < 64 || *b.MaxTokens > 4096 {
				a.fail(w, 400, "最大输出 tokens 需在 64 到 4096 之间")
				return
			}
			prov.MaxTokens = *b.MaxTokens
		}
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == prov.ID {
				cfg.Providers[i] = prov
			}
		}
		if b.ActiveID != nil {
			cfg.ActiveID = strings.TrimSpace(*b.ActiveID)
		}
	}

	// ActiveID 失效时回退第一个
	activeOK := false
	for _, p := range cfg.Providers {
		if p.ID == cfg.ActiveID {
			activeOK = true
			break
		}
	}
	if !activeOK {
		if len(cfg.Providers) > 0 {
			cfg.ActiveID = cfg.Providers[0].ID
		} else {
			cfg.ActiveID = ""
		}
	}
	if cfg.Enabled {
		prov, ok := cfg.ActiveProvider()
		if !ok || prov.BaseURL == "" || prov.APIKey == "" || prov.Model == "" {
			a.fail(w, 400, "启用前请先添加并选择一个配置完整的 AI 供应商（API 地址、密钥与模型名）")
			return
		}
	}
	if err := scanner.SaveAIConfig(cfg); err != nil {
		a.fail(w, 500, "AI 配置保存失败")
		return
	}
	prov, _ := cfg.ActiveProvider()
	logx.InfoC(logx.CatAI, "AI 识别辅助配置已更新（启用=%v 供应商=%d 个 当前「%s」）", cfg.Enabled, len(cfg.Providers), prov.Name)
	a.json(w, 200, M{"OK": true})
}

// adminAITest POST /admin/ai/test 测试 AI 识别（传入示例路径，返回提取的关键词）。
func (a *App) adminAITest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.fail(w, 405, "方法不支持")
		return
	}
	var b struct {
		Type       string `json:"Type"` // Movie / Series
		Path       string `json:"Path"`
		Name       string `json:"Name"`
		Year       int    `json:"Year"`
		ProviderID string `json:"ProviderID"` // 为空则使用当前选中的供应商
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
	var prov scanner.AIProvider
	if b.ProviderID != "" {
		found := false
		for _, p := range cfg.Providers {
			if p.ID == b.ProviderID {
				prov, found = p, true
				break
			}
		}
		if !found {
			a.fail(w, 400, "未找到指定的 AI 供应商，请先保存")
			return
		}
	} else {
		var ok bool
		prov, ok = cfg.ActiveProvider()
		if !ok {
			a.fail(w, 400, "请先添加 AI 供应商并保存")
			return
		}
	}
	if prov.BaseURL == "" || prov.APIKey == "" || prov.Model == "" {
		a.fail(w, 400, "该供应商尚未配置完整的 API 地址、密钥与模型名")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	k, err := scanner.AIExtractKeywords(ctx, prov, b.Type, b.Path, b.Name, b.Year)
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
		"Type":          k.Type,
		"Provider":      prov.Name,
	})
}
