package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RSSFeed 单个 RSS 源配置
type RSSFeed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Keyword 关键词配置
type Keyword struct {
	Text   string `json:"text"`
	Source string `json:"source"` // "ns" / "ld" / "" = all feeds
}

// KeywordList 支持旧版 string 数组向后兼容
type KeywordList []Keyword

func (kl *KeywordList) UnmarshalJSON(data []byte) error {
	var kws []Keyword
	if err := json.Unmarshal(data, &kws); err == nil {
		*kl = KeywordList(kws)
		return nil
	}
	var strs []string
	if err := json.Unmarshal(data, &strs); err != nil {
		return fmt.Errorf("keywords: must be string array or object array with text/source")
	}
	for _, s := range strs {
		*kl = append(*kl, Keyword{Text: s})
	}
	return nil
}

// Config 配置结构
type Config struct {
	RSS struct {
		URL             string    `json:"url"`
		IntervalSeconds int       `json:"interval_seconds"`
		Cron            string    `json:"cron"`
		Feeds           []RSSFeed `json:"feeds"`
	} `json:"rss"`
	Keywords KeywordList `json:"keywords"`
	SMTP     struct {
		Host   string `json:"host"`
		Port   int    `json:"port"`
		User   string `json:"user"`
		Pass   string `json:"pass"`
		Secure bool   `json:"secure"`
	} `json:"smtp"`
	Email struct {
		From     string   `json:"from"`
		To       []string `json:"to"`
		Subject  string   `json:"subject"`
		Disabled bool     `json:"disabled"`
	} `json:"email"`
	Telegram struct {
		Token        string  `json:"token"`
		ChatIDs      []int64 `json:"chat_ids"`
		AdminUserIDs []int64 `json:"admin_user_ids"`
	} `json:"telegram"`
	CacheFile string `json:"cache_file"`
}

var (
	config   Config
	configMu sync.RWMutex
	execDir  string
)

// initExecDir 初始化可执行文件目录
func initExecDir() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行文件路径失败: %w", err)
	}

	execDir = filepath.Dir(execPath)
	log.Printf("可执行文件目录: %s", execDir)
	return nil
}

// loadConfig 加载配置文件
func loadConfig(filename string) error {
	configPath := filepath.Join(execDir, filename)
	log.Printf("正在加载配置文件: %s", configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}
	return nil
}

// saveConfig 将当前配置原子地持久化到 config.json。
func saveConfig() error {
	configPath := filepath.Join(execDir, "config.json")
	configMu.RLock()
	data, err := json.MarshalIndent(config, "", "  ")
	configMu.RUnlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(configPath, data, 0644)
}

// writeFileAtomic writes data to a temporary file in the same directory and
// renames it over the target, so a crash cannot leave a truncated file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return fmt.Errorf("设置文件权限失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换文件失败: %w", err)
	}
	tmp = ""
	return nil
}

// getFeeds 获取所有 RSS 源，向后兼容旧版单 URL 配置
func getFeeds() []RSSFeed {
	configMu.RLock()
	defer configMu.RUnlock()

	if len(config.RSS.Feeds) > 0 {
		feeds := make([]RSSFeed, len(config.RSS.Feeds))
		copy(feeds, config.RSS.Feeds)
		return feeds
	}
	if config.RSS.URL != "" {
		return []RSSFeed{
			{ID: "ns", Name: "NodeSeek", URL: config.RSS.URL},
		}
	}
	return nil
}

// emailEnabled 判断邮件是否已配置且未被禁用
func emailEnabled() bool {
	configMu.RLock()
	defer configMu.RUnlock()
	return config.SMTP.Host != "" && len(config.Email.To) > 0 && !config.Email.Disabled
}

// telegramEnabled 判断 Telegram 是否已配置
func telegramEnabled() bool {
	configMu.RLock()
	defer configMu.RUnlock()
	return config.Telegram.Token != "" && len(config.Telegram.ChatIDs) > 0
}

// isAllowedChat 检查 chatID 是否在白名单中
func isAllowedChat(chatID int64) bool {
	configMu.RLock()
	defer configMu.RUnlock()
	for _, id := range config.Telegram.ChatIDs {
		if id == chatID {
			return true
		}
	}
	return false
}

// canManageBot reports whether userID may perform state-changing actions in
// this chat. Private chats are restricted to the chat owner; group chats
// require an explicit telegram.admin_user_ids entry.
func canManageBot(chatID int64, chatType string, userID int64) bool {
	if !isAllowedChat(chatID) {
		return false
	}
	if strings.EqualFold(chatType, "private") {
		return userID != 0 && userID == chatID
	}
	configMu.RLock()
	defer configMu.RUnlock()
	for _, id := range config.Telegram.AdminUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// getScheduleDesc 获取调度描述
func getScheduleDesc() string {
	configMu.RLock()
	defer configMu.RUnlock()
	if config.RSS.IntervalSeconds > 0 {
		return fmt.Sprintf("每 %d 秒执行一次", config.RSS.IntervalSeconds)
	}
	if config.RSS.Cron != "" {
		return fmt.Sprintf("Cron: %s", config.RSS.Cron)
	}
	return "未配置"
}
