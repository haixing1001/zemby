package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const sessionTTL = 24 * time.Hour

type Manager struct {
	password string
	key      []byte
}

func New(password, dataDir string) (*Manager, error) {
	keyPath := filepath.Join(dataDir, ".session-key")
	key, err := os.ReadFile(keyPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read session key: %w", err)
	}
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate session key: %w", err)
		}
		file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create session key: %w", err)
		}
		if _, err := file.Write(key); err != nil {
			file.Close()
			return nil, fmt.Errorf("write session key: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close session key: %w", err)
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid session key length in %s", keyPath)
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, fmt.Errorf("protect session key: %w", err)
	}
	derived := hmac.New(sha256.New, key)
	_, _ = derived.Write([]byte(password))
	return &Manager{password: password, key: derived.Sum(nil)}, nil
}

func (m *Manager) CheckPassword(candidate string) bool {
	if utf8.RuneCountInString(candidate) != utf8.RuneCountInString(m.password) || len(candidate) != len(m.password) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(m.password)) == 1
}

func (m *Manager) NewToken(now time.Time) string {
	expires := strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	payload := expires + "." + base64.RawURLEncoding.EncodeToString(nonce)
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) ValidToken(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() >= expires {
		return false
	}
	payload := parts[0] + "." + parts[1]
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(payload))
	return hmac.Equal(provided, mac.Sum(nil))
}
