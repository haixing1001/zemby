// Package db 数据库初始化与基础操作。
package db

import (
        "log"
        "os"
        "path/filepath"
        "sync"
        "time"

        "github.com/glebarez/sqlite"
        "golang.org/x/crypto/bcrypt"
        "gorm.io/gorm"
        "gorm.io/gorm/logger"

        "go-emby/internal/models"
)

var (
        DB  *gorm.DB
        mu  sync.RWMutex
)

// Open 打开数据库并迁移表结构、写入默认数据。
func Open(path string) error {
        if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
                return err
        }
        lg := logger.New(log.New(os.Stderr, "\r\n", log.LstdFlags), logger.Config{
                SlowThreshold: 2 * time.Second, LogLevel: logger.Warn, Colorful: false,
        })
        d, err := gorm.Open(sqlite.Open(path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"), &gorm.Config{
                Logger: lg,
        })
        if err != nil {
                return err
        }
        sqlDB, err := d.DB()
        if err != nil {
                return err
        }
        sqlDB.SetMaxOpenConns(1) // SQLite 单写者，避免 busy
        sqlDB.SetMaxIdleConns(1)
        if err := d.AutoMigrate(
                &models.User{}, &models.Token{}, &models.Library{}, &models.Item{},
                &models.MediaSource{}, &models.MediaStream{}, &models.UserDatum{},
                &models.PlaySession{}, &models.Setting{}, &models.ScanTask{},
                &models.ApiKey{},
        ); err != nil {
                return err
        }
        DB = d
        return seed()
}

// seed 初始管理员与默认设置。
func seed() error {
        var count int64
        if err := DB.Model(&models.User{}).Count(&count).Error; err != nil {
                return err
        }
        if count == 0 {
                pw := os.Getenv("GEMBY_ADMIN_PASSWORD")
                if pw == "" {
                        pw = "admin123"
                }
                hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
                if err != nil {
                        return err
                }
                admin := models.User{
                        ID: models.NewID(), Name: "admin", PasswordHash: string(hash),
                        IsAdmin: true, Allowed: true, MaxDevices: 5, FirstAdmin: true,
                        CreatedAt: time.Now(),
                }
                if err := DB.Create(&admin).Error; err != nil {
                        return err
                }
                log.Printf("[db] 已创建初始管理员 admin（密码 %s）", pw)
        }
        return nil
}

// GetSetting 读取设置。
func GetSetting(key string) (string, bool) {
        mu.RLock()
        defer mu.RUnlock()
        var s models.Setting
        if DB == nil {
                return "", false
        }
        if err := DB.Where("`key` = ?", key).First(&s).Error; err != nil {
                return "", false
        }
        return s.Value, true
}

// SetSetting 写入设置。
func SetSetting(key, value string) error {
        mu.Lock()
        defer mu.Unlock()
        return DB.Save(&models.Setting{Key: key, Value: value}).Error
}
