// Package api 辅助函数。
package api

import (
	"encoding/json"

	"go-emby/internal/db"
	"go-emby/internal/logx"
	"go-emby/internal/models"
	"go-emby/internal/tmdb"
)

type tmdbSettingsType = tmdb.Settings
type logEntryAlias = models.LogEntry

func getSetting(key string) (string, bool) { return db.GetSetting(key) }
func setSetting(key, value string) error   { return db.SetSetting(key, value) }

func jsonUnmarshalSetting(data string, out any) error {
	return json.Unmarshal([]byte(data), out)
}

func jsonMarshalSetting(v any) ([]byte, error) {
	return json.Marshal(v)
}

func logxInfo(format string, args ...any) {
	logx.Info(format, args...)
}

func logxScan(format string, args ...any) {
	logx.Scan(format, args...)
}

func snapshotLogs(n int) []logEntryAlias {
	return logx.Snapshot(n)
}
