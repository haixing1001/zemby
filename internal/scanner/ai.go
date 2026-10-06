// Package scanner AI 识别辅助：TMDB 识别失败时调用 AI 从文件名/目录名提取关键词。
// 兼容任意 OpenAI Chat Completions 风格端点（OpenAI / DeepSeek / 智谱GLM / Kimi / Ollama 等）。
package scanner

import (
        "bytes"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "net/http"
        "strings"
        "time"

        "go-emby/internal/logx"
)

// AIConfig AI 识别辅助配置（KV 存 Setting 表 key=ai_config）。
type AIConfig struct {
        Enabled bool   `json:"enabled"` // 启用 AI 识别辅助
        BaseURL string `json:"baseUrl"` // OpenAI 兼容 API 根地址（如 https://api.deepseek.com/v1）
        APIKey  string `json:"apiKey"`  // API Key
        Model   string `json:"model"`   // 模型名（如 deepseek-chat / glm-4-flash / gpt-4o-mini）
}

// DefaultAIConfig 默认配置。
func DefaultAIConfig() AIConfig {
        return AIConfig{
                Enabled: false,
                BaseURL: "https://api.openai.com/v1",
                Model:   "gpt-4o-mini",
        }
}

// LoadAIConfig 读取 AI 配置。
func LoadAIConfig() AIConfig {
        c := DefaultAIConfig()
        kvGet("ai_config", &c)
        c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
        c.Model = strings.TrimSpace(c.Model)
        return c
}

// SaveAIConfig 保存 AI 配置。
func SaveAIConfig(c AIConfig) { kvPut("ai_config", c) }

// aiClient AI 请求客户端（识别与测试共用）。
var aiClient = &http.Client{Timeout: 45 * time.Second}

// AIKeywords AI 提取的关键词。
type AIKeywords struct {
        Title         string `json:"title"`          // 中文/常用名称
        OriginalTitle string `json:"original_title"` // 外文原名（可空）
        Year          int    `json:"year"`           // 发行/首播年份（无把握为 0）
}

const aiSystemPrompt = `你是媒体库元数据识别助手。用户会给出一个媒体文件的路径与解析信息，` +
        `请从中提取影视作品的真实名称与年份，用于 TMDB 搜索。` +
        `必须去除发布信息（分辨率、编码、来源、字幕组、音轨标签等）。` +
        `只输出一个 JSON 对象，不要输出任何其他文字，格式：` +
        `{"title":"名称","year":年份,"original_title":"外文原名或空字符串"}`

func aiUserPrompt(itemType, path, name string, year int) string {
        t := "电影"
        if itemType == "Series" {
                t = "剧集"
        }
        return fmt.Sprintf("媒体类型：%s\n文件路径：%s\n当前解析名称：%s\n解析年份：%d\n\n请提取用于 TMDB 搜索的关键词。",
                t, path, name, year)
}

// AIExtractKeywords 调用 AI 从文件路径信息中提取影视名称与年份。
func AIExtractKeywords(ctx context.Context, cfg AIConfig, itemType, path, name string, year int) (*AIKeywords, error) {
        if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "" {
                return nil, fmt.Errorf("AI 配置不完整（地址 / 密钥 / 模型）")
        }
        body := map[string]any{
                "model": cfg.Model,
                "messages": []map[string]string{
                        {"role": "system", "content": aiSystemPrompt},
                        {"role": "user", "content": aiUserPrompt(itemType, path, name, year)},
                },
                "temperature": 0.1,
                "max_tokens":  300,
                "stream":      false,
        }
        b, _ := json.Marshal(body)
        req, err := http.NewRequestWithContext(ctx, "POST", cfg.BaseURL+"/chat/completions", bytes.NewReader(b))
        if err != nil {
                return nil, err
        }
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
        resp, err := aiClient.Do(req)
        if err != nil {
                return nil, err
        }
        defer resp.Body.Close()
        data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
        if resp.StatusCode != 200 {
                msg := strings.TrimSpace(string(data))
                if len(msg) > 300 {
                        msg = msg[:300]
                }
                return nil, fmt.Errorf("AI 接口 %d: %s", resp.StatusCode, msg)
        }
        var cr struct {
                Choices []struct {
                        Message struct {
                                Content string `json:"content"`
                        } `json:"message"`
                } `json:"choices"`
                Error *struct {
                        Message string `json:"message"`
                } `json:"error"`
        }
        if err := json.Unmarshal(data, &cr); err != nil {
                return nil, fmt.Errorf("AI 响应解析失败: %w", err)
        }
        if cr.Error != nil && cr.Error.Message != "" {
                return nil, fmt.Errorf("AI 接口错误: %s", cr.Error.Message)
        }
        if len(cr.Choices) == 0 {
                return nil, fmt.Errorf("AI 未返回结果")
        }
        return parseAIKeywords(cr.Choices[0].Message.Content)
}

// parseAIKeywords 从 AI 回复文本中解析关键词 JSON（容忍 markdown 围栏与多余文字）。
func parseAIKeywords(content string) (*AIKeywords, error) {
        content = strings.TrimSpace(content)
        // 剥离 markdown 代码围栏
        if i := strings.Index(content, "{"); i >= 0 {
                if j := strings.LastIndex(content, "}"); j > i {
                        content = content[i : j+1]
                }
        }
        var k AIKeywords
        if err := json.Unmarshal([]byte(content), &k); err != nil {
                return nil, fmt.Errorf("AI 输出非 JSON: %.120s", content)
        }
        k.Title = strings.TrimSpace(k.Title)
        k.OriginalTitle = strings.TrimSpace(k.OriginalTitle)
        if k.Title == "" {
                k.Title = strings.TrimSpace(k.OriginalTitle)
        }
        if k.Title == "" {
                return nil, fmt.Errorf("AI 未提取到名称")
        }
        if k.Year < 0 || k.Year > 2100 {
                k.Year = 0
        }
        return &k, nil
}

// aiRetrySearch TMDB 搜索 0 结果时的 AI 辅助重试。
// 返回 (关键词, 年份, 是否可用)；未启用/失败均返回 false（调用方沿用原错误）。
func (s *Scanner) aiRetrySearch(ctx context.Context, itemType string, path, name string, year int) (string, int, bool) {
        cfg := LoadAIConfig()
        if !cfg.Enabled {
                return "", 0, false
        }
        logx.InfoC(logx.CatAI, "TMDB 识别失败，调用 AI 辅助提取关键词：《%s》", name)
        k, err := AIExtractKeywords(ctx, cfg, itemType, path, name, year)
        if err != nil {
                logx.WarnC(logx.CatAI, "AI 关键词提取失败: %v", err)
                return "", 0, false
        }
        title := k.Title
        if k.OriginalTitle != "" && k.OriginalTitle != k.Title {
                logx.InfoC(logx.CatAI, "AI 提取关键词：「%s」/「%s」(%d)", k.Title, k.OriginalTitle, k.Year)
        } else {
                logx.InfoC(logx.CatAI, "AI 提取关键词：「%s」(%d)", title, k.Year)
        }
        return title, k.Year, true
}
