// Package scanner 增强功能配置（后台「设置-增强功能」的存储与读取）。
package scanner

import (
        "go-emby/internal/db"
        "go-emby/internal/logx"
        "go-emby/internal/models"
)

// EnhanceConfig 增强功能配置（KV 存 Setting 表 key=enhance_config）。
// 默认值对齐参考实现：合并/角标/演员过滤/首字母搜索默认开，其余默认关。
type EnhanceConfig struct {
        Favorites                    bool `json:"favorites"`                    // 开启收藏功能
        TMDB                         bool `json:"tmdb"`                         // 启动TMDB
        SortByReleaseDate            bool `json:"sortByReleaseDate"`            // 按发行日期排序媒体库
        EpisodeMediaReuse            bool `json:"episodeMediaReuse"`            // 剧集媒体信息复用
        PosterEpisodeBadge           bool `json:"posterEpisodeBadge"`           // 海报显示剧集集数角标
        HideActorsNoImage            bool `json:"hideActorsNoImage"`            // 隐藏没有图片的演员信息
        MergeVersionsInLibrary       bool `json:"mergeVersionsInLibrary"`       // 同媒体库内多版本合并
        MergeVersionsAcrossLibraries bool `json:"mergeVersionsAcrossLibraries"` // 跨媒体库合并多版本
        SearchByInitials             bool `json:"searchByInitials"`             // 按照首字母搜索视频
        WatchEnabled                 bool `json:"watchEnabled"`                 // 监听文件变动自动刷新路径
        WatchDelaySeconds            int  `json:"watchDelaySeconds"`            // 媒体变动延时（秒）
        FastPath                     bool `json:"fastPath"`                     // 播放模式-快速路径
        FastPathWaitSec              int  `json:"fastPathWaitSec"`              // 快速路径等待上限（秒）
}

// DefaultEnhanceConfig 默认配置。
func DefaultEnhanceConfig() EnhanceConfig {
        return EnhanceConfig{
                Favorites:                    false,
                TMDB:                         false,
                SortByReleaseDate:            false,
                EpisodeMediaReuse:            false,
                PosterEpisodeBadge:           true,
                HideActorsNoImage:            true,
                MergeVersionsInLibrary:       true,
                MergeVersionsAcrossLibraries: true,
                SearchByInitials:             true,
                WatchEnabled:                 false,
                WatchDelaySeconds:            30,
                FastPath:                     false,
                FastPathWaitSec:              5,
        }
}

// LoadEnhanceConfig 读取增强功能配置。
func LoadEnhanceConfig() EnhanceConfig {
        c := DefaultEnhanceConfig()
        kvGet("enhance_config", &c)
        // 防御越界值
        if c.WatchDelaySeconds < 10 || c.WatchDelaySeconds > 86400 {
                c.WatchDelaySeconds = 30
        }
        if c.FastPathWaitSec < 1 || c.FastPathWaitSec > 60 {
                c.FastPathWaitSec = 5
        }
        return c
}

// SaveEnhanceConfig 保存增强功能配置。
func SaveEnhanceConfig(c EnhanceConfig) { kvPut("enhance_config", c) }

// ApplyEnhanceSideEffects 配置保存后的运行时副作用（文件监听重建）。
func (s *Scanner) ApplyEnhanceSideEffects() {
        cfg := LoadEnhanceConfig()
        s.applyWatchConfig(cfg)
}

// initOnceEnhance 启动时应用：文件监听状态恢复 + 首字母存量回填。
func (s *Scanner) initEnhance() {
        cfg := LoadEnhanceConfig()
        s.applyWatchConfig(cfg)
        go s.backfillInitials()
}

// backfillInitials 为存量条目补算拼音首字母（升级迁移，异步执行）。
func (s *Scanner) backfillInitials() {
        var items []struct {
                ID   string
                Name string
        }
        if err := db.DB.Model(&models.Item{}).Where("initials = '' OR initials IS NULL").
                Limit(50000).Find(&items).Error; err != nil {
                return
        }
        n := 0
        for _, it := range items {
                in := models.InitialsOf(it.Name)
                if in == "" {
                        continue
                }
                if err := db.DB.Model(&models.Item{}).Where("id = ? AND (initials = '' OR initials IS NULL)", it.ID).
                        Update("initials", in).Error; err == nil {
                        n++
                }
        }
        if n > 0 {
                logx.InfoC(logx.CatSystem, "首字母检索回填完成：%d 个条目", n)
        }
}
