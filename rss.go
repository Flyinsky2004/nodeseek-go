package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RSS 结构定义
type RSS struct {
	XMLName xml.Name `xml:"rss"`
	Channel Channel  `xml:"channel"`
}

type Channel struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Link        string `xml:"link"`
	Items       []Item `xml:"item"`
}

type Item struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Category    string `xml:"category"`
	Creator     string `xml:"creator"`
	DCCreator   string `xml:"http://purl.org/dc/elements/1.1/ creator"`
	PubDate     string `xml:"pubDate"`
}

// GetCreator 返回作者，兼容 dc:creator 和 creator 两种格式
func (i Item) GetCreator() string {
	if i.DCCreator != "" {
		return i.DCCreator
	}
	return i.Creator
}

// SentCache 已发送缓存
type SentCache struct {
	Items map[string]time.Time `json:"items"`
}

var (
	sentCache SentCache
	cacheMu   sync.RWMutex
	rssClient = &http.Client{Timeout: 30 * time.Second}
)

// loadCache 加载缓存文件
func loadCache() {
	configMu.RLock()
	cacheFile := config.CacheFile
	configMu.RUnlock()
	if cacheFile == "" {
		cacheFile = "sent_cache.json"
		configMu.Lock()
		config.CacheFile = cacheFile
		configMu.Unlock()
	}
	loadCacheFile(filepath.Join(execDir, cacheFile))
}

func loadCacheFile(cachePath string) {
	log.Printf("正在加载缓存文件: %s", cachePath)

	items := make(map[string]time.Time)
	data, err := os.ReadFile(cachePath)
	if err != nil {
		log.Printf("缓存文件不存在，将创建新缓存")
		cacheMu.Lock()
		sentCache.Items = items
		cacheMu.Unlock()
		return
	}

	var loaded SentCache
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("解析缓存文件失败: %v", err)
	} else if loaded.Items != nil {
		items = loaded.Items
	}

	// 清理过期的缓存（超过30天）
	now := time.Now()
	for guid, sentTime := range items {
		if now.Sub(sentTime) > 30*24*time.Hour {
			delete(items, guid)
		}
	}

	cacheMu.Lock()
	sentCache.Items = items
	cacheMu.Unlock()
	log.Printf("缓存加载完成，已记录 %d 条项", len(items))
}

// saveCache 保存缓存文件
func saveCache() error {
	configMu.RLock()
	cacheFile := config.CacheFile
	configMu.RUnlock()
	if cacheFile == "" {
		cacheFile = "sent_cache.json"
	}
	cachePath := filepath.Join(execDir, cacheFile)

	cacheMu.RLock()
	data, err := json.MarshalIndent(sentCache, "", "  ")
	cacheMu.RUnlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(cachePath, data, 0644)
}

func cacheCount() int {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return len(sentCache.Items)
}

// matchKeywords 匹配关键词（大小写不敏感；空格分隔的词组需全部出现）
func matchKeywords(title, description string, keywordTexts []string) []string {
	var matched []string
	titleLower := strings.ToLower(title)
	descLower := strings.ToLower(description)
	for _, keyword := range keywordTexts {
		if keyword == "" {
			continue
		}
		parts := strings.Fields(strings.ToLower(keyword))
		if len(parts) == 0 {
			continue
		}
		allMatch := true
		for _, part := range parts {
			if !strings.Contains(titleLower, part) && !strings.Contains(descLower, part) {
				allMatch = false
				break
			}
		}
		if allMatch {
			matched = append(matched, keyword)
		}
	}
	return matched
}

// checkRSS 检查所有 RSS 源
func checkRSS() error {
	feeds := getFeeds()

	configMu.RLock()
	keywords := make([]Keyword, len(config.Keywords))
	copy(keywords, config.Keywords)
	configMu.RUnlock()

	totalProcessed := 0
	totalSent := 0

	for _, feed := range feeds {
		// 过滤匹配该源的关键词文本（source="" 匹配所有源）
		var feedKeywordTexts []string
		for _, kw := range keywords {
			if kw.Source == "" || kw.Source == feed.ID {
				feedKeywordTexts = append(feedKeywordTexts, kw.Text)
			}
		}

		log.Printf("检查 %s RSS (%s)", feed.Name, feed.URL)

		resp, err := rssClient.Get(feed.URL)
		if err != nil {
			log.Printf("获取 %s RSS 失败: %v", feed.Name, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			log.Printf("%s HTTP 状态码错误: %d", feed.Name, resp.StatusCode)
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			log.Printf("读取 %s RSS 失败: %v", feed.Name, err)
			continue
		}

		var rss RSS
		if err := xml.Unmarshal(body, &rss); err != nil {
			log.Printf("解析 %s RSS 失败: %v", feed.Name, err)
			continue
		}

		log.Printf("%s 解析到 %d 条 RSS 项", feed.Name, len(rss.Channel.Items))

		for _, item := range rss.Channel.Items {
			cacheKey := feed.ID + ":" + item.GUID

			cacheMu.RLock()
			_, exists := sentCache.Items[cacheKey]
			legacyTime, legacyExists := sentCache.Items[item.GUID]
			cacheMu.RUnlock()
			if exists {
				continue
			}
			// 向后兼容旧缓存 key（无前缀）
			if legacyExists {
				cacheMu.Lock()
				sentCache.Items[cacheKey] = legacyTime
				delete(sentCache.Items, item.GUID)
				cacheMu.Unlock()
				continue
			}

			totalProcessed++

			matchedKeywords := matchKeywords(item.Title, item.Description, feedKeywordTexts)

			if len(matchedKeywords) > 0 {
				log.Printf("[%s] 匹配到关键词 [%s]: %s", feed.Name, strings.Join(matchedKeywords, ", "), item.Title)

				notifyFailed := false

				if emailEnabled() {
					if err := sendEmail(item, matchedKeywords, feed); err != nil {
						log.Printf("发送邮件失败: %v", err)
						notifyFailed = true
					}
				}

				if telegramEnabled() {
					if err := sendTelegramNotification(item, matchedKeywords, feed); err != nil {
						log.Printf("发送 Telegram 通知失败: %v", err)
						notifyFailed = true
					}
				}

				if notifyFailed {
					continue
				}

				cacheMu.Lock()
				sentCache.Items[cacheKey] = time.Now()
				cacheMu.Unlock()
				totalSent++

				if err := saveCache(); err != nil {
					log.Printf("保存缓存失败: %v", err)
				}
			} else {
				cacheMu.Lock()
				sentCache.Items[cacheKey] = time.Now()
				cacheMu.Unlock()
			}
		}
	}

	log.Printf("任务完成 - 处理: %d 条新项, 发送: %d 条通知", totalProcessed, totalSent)

	return saveCache()
}

// truncateText 截断文本到指定长度
func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "..."
}

// stripHTML 简单移除 HTML 标签
func stripHTML(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}
	return strings.TrimSpace(result.String())
}

func formatFeedsForDisplay(feeds []RSSFeed) string {
	var lines []string
	for _, f := range feeds {
		lines = append(lines, fmt.Sprintf("%s (%s)", f.Name, f.URL))
	}
	return strings.Join(lines, "<br>")
}

func formatKeywordsForDisplay(keywords []Keyword) string {
	var parts []string
	for _, kw := range keywords {
		src := kw.Source
		if src == "" {
			src = "all"
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", src, kw.Text))
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, ", ")
}
