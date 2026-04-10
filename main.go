package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/gomail.v2"
)

// Config 配置结构
type Config struct {
	RSS struct {
		URL             string `json:"url"`
		IntervalSeconds int    `json:"interval_seconds"`
		Cron            string `json:"cron"`
	} `json:"rss"`
	Keywords []string `json:"keywords"`
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
		Token   string  `json:"token"`
		ChatIDs []int64 `json:"chat_ids"`
	} `json:"telegram"`
	CacheFile string `json:"cache_file"`
}

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
	PubDate     string `xml:"pubDate"`
}

// SentCache 已发送缓存
type SentCache struct {
	Items map[string]time.Time `json:"items"`
}

var (
	config    Config
	configMu  sync.RWMutex // 保护 config.Keywords 的并发读写
	sentCache SentCache
	execDir   string
)

// Telegram Bot API 响应结构
type tgUpdatesResp struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID int        `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgMessage struct {
	Chat tgChat `json:"chat"`
	Text string `json:"text"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

func main() {
	// 获取可执行文件所在目录
	if err := initExecDir(); err != nil {
		log.Fatalf("获取可执行文件目录失败: %v", err)
	}

	// 加载配置文件
	if err := loadConfig("config.json"); err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	// 加载缓存文件
	loadCache()

	// 发送启动测试邮件
	log.Println("发送启动测试邮件...")
	if err := sendTestEmail(); err != nil {
		log.Printf("测试邮件发送失败: %v (程序将继续运行)", err)
	} else {
		log.Println("测试邮件发送成功")
	}

	// 发送启动 Telegram 通知
	if telegramEnabled() {
		log.Println("发送启动 Telegram 通知...")
		if err := sendTelegramTestMessage(); err != nil {
			log.Printf("Telegram 启动通知发送失败: %v (程序将继续运行)", err)
		} else {
			log.Println("Telegram 启动通知发送成功")
		}
	}

	// 启动 Telegram Bot 命令监听
	if telegramEnabled() {
		go startTelegramPolling()
	}

	// 立即执行一次
	log.Println("立即执行一次 RSS 检查...")
	if err := checkRSS(); err != nil {
		log.Printf("RSS 检查失败: %v", err)
	}

	// 根据配置选择调度模式
	if config.RSS.IntervalSeconds > 0 {
		// 使用秒级间隔模式
		log.Printf("RSS 监控服务已启动，每 %d 秒执行一次", config.RSS.IntervalSeconds)
		ticker := time.NewTicker(time.Duration(config.RSS.IntervalSeconds) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("开始执行 RSS 监控任务...")
			if err := checkRSS(); err != nil {
				log.Printf("RSS 监控任务执行失败: %v", err)
			}
		}
	} else if config.RSS.Cron != "" {
		// 使用 Cron 表达式模式
		log.Printf("RSS 监控服务已启动，定时规则: %s", config.RSS.Cron)
		c := cron.New()
		_, err := c.AddFunc(config.RSS.Cron, func() {
			log.Println("开始执行 RSS 监控任务...")
			if err := checkRSS(); err != nil {
				log.Printf("RSS 监控任务执行失败: %v", err)
			}
		})
		if err != nil {
			log.Fatalf("添加定时任务失败: %v", err)
		}

		c.Start()

		// 保持程序运行
		select {}
	} else {
		log.Fatalf("配置错误：必须设置 interval_seconds 或 cron 其中之一")
	}
}

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
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	return json.Unmarshal(data, &config)
}

// loadCache 加载缓存文件
func loadCache() {
	if config.CacheFile == "" {
		config.CacheFile = "sent_cache.json"
	}

	cachePath := filepath.Join(execDir, config.CacheFile)
	log.Printf("正在加载缓存文件: %s", cachePath)

	data, err := ioutil.ReadFile(cachePath)
	if err != nil {
		// 如果文件不存在，创建新的缓存
		sentCache.Items = make(map[string]time.Time)
		log.Printf("缓存文件不存在，将创建新缓存")
		return
	}

	if err := json.Unmarshal(data, &sentCache); err != nil {
		log.Printf("解析缓存文件失败: %v", err)
		sentCache.Items = make(map[string]time.Time)
	}

	// 清理过期的缓存（超过30天）
	now := time.Now()
	for guid, sentTime := range sentCache.Items {
		if now.Sub(sentTime) > 30*24*time.Hour {
			delete(sentCache.Items, guid)
		}
	}

	log.Printf("缓存加载完成，已记录 %d 条项", len(sentCache.Items))
}

// saveCache 保存缓存文件
func saveCache() error {
	cachePath := filepath.Join(execDir, config.CacheFile)

	data, err := json.MarshalIndent(sentCache, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(cachePath, data, 0644)
}

// checkRSS 检查 RSS 源
func checkRSS() error {
	// 获取 RSS 内容
	resp, err := http.Get(config.RSS.URL)
	if err != nil {
		return fmt.Errorf("获取 RSS 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP 状态码错误: %d", resp.StatusCode)
	}

	// 读取响应内容
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应内容失败: %w", err)
	}

	// 解析 RSS
	var rss RSS
	if err := xml.Unmarshal(body, &rss); err != nil {
		return fmt.Errorf("解析 RSS 失败: %w", err)
	}

	log.Printf("解析到 %d 条 RSS 项", len(rss.Channel.Items))

	// 快照当前关键词，避免遍历过程中被并发修改
	configMu.RLock()
	keywords := make([]string, len(config.Keywords))
	copy(keywords, config.Keywords)
	configMu.RUnlock()

	processedCount := 0
	sentCount := 0

	// 遍历 RSS 项
	for _, item := range rss.Channel.Items {
		// 检查是否已发送
		if _, exists := sentCache.Items[item.GUID]; exists {
			continue
		}

		processedCount++

		// 检查是否匹配关键词（大小写不敏感；空格分隔的词组需全部出现）
		matchedKeywords := []string{}
		titleLower := strings.ToLower(item.Title)
		descLower := strings.ToLower(item.Description)
		for _, keyword := range keywords {
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
				matchedKeywords = append(matchedKeywords, keyword)
			}
		}

		if len(matchedKeywords) > 0 {
			log.Printf("匹配到关键词 [%s]: %s", strings.Join(matchedKeywords, ", "), item.Title)

			notifyFailed := false

			// 发送邮件
			if emailEnabled() {
				if err := sendEmail(item, matchedKeywords); err != nil {
					log.Printf("发送邮件失败: %v", err)
					notifyFailed = true
				}
			}

			// 发送 Telegram 通知
			if telegramEnabled() {
				if err := sendTelegramNotification(item, matchedKeywords); err != nil {
					log.Printf("发送 Telegram 通知失败: %v", err)
					notifyFailed = true
				}
			}

			if notifyFailed {
				continue
			}

			// 标记为已发送
			sentCache.Items[item.GUID] = time.Now()
			sentCount++

			// 保存缓存
			if err := saveCache(); err != nil {
				log.Printf("保存缓存失败: %v", err)
			}
		} else {
			// 即使不匹配也标记，避免重复检查
			sentCache.Items[item.GUID] = time.Now()
		}
	}

	log.Printf("任务完成 - 处理: %d 条新项, 发送: %d 条通知", processedCount, sentCount)

	// 保存缓存
	return saveCache()
}

// sendTestEmail 发送测试邮件
func sendTestEmail() error {
	subject := "[NodeSeek RSS] 程序启动测试邮件"

	// 构建测试邮件内容
	htmlBody := fmt.Sprintf(`
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    * {
      box-sizing: border-box;
    }
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
      line-height: 1.6;
      color: #000;
      background-color: #fafafa;
      margin: 0;
      padding: 20px;
      font-size: 14px;
    }
    .container {
      max-width: 600px;
      margin: 0 auto;
      background-color: #fff;
      border: 1px solid #e5e5e5;
      border-radius: 8px;
      overflow: hidden;
    }
    .header {
      background-color: #000;
      color: #fff;
      padding: 24px 24px;
      border-bottom: 1px solid #e5e5e5;
    }
    .header h1 {
      margin: 0;
      font-size: 18px;
      font-weight: 600;
      line-height: 1.4;
    }
    .content {
      padding: 24px;
    }
    .section {
      margin-bottom: 24px;
    }
    .section:last-child {
      margin-bottom: 0;
    }
    .label {
      font-size: 12px;
      color: #666;
      text-transform: uppercase;
      letter-spacing: 0.5px;
      margin-bottom: 8px;
      font-weight: 500;
    }
    .status {
      display: inline-block;
      background-color: #000;
      color: #fff;
      padding: 6px 12px;
      border-radius: 4px;
      font-size: 13px;
      font-weight: 500;
    }
    .status.success {
      background-color: #10b981;
    }
    .info-box {
      background-color: #fafafa;
      border: 1px solid #e5e5e5;
      border-radius: 6px;
      padding: 16px;
      margin: 16px 0;
    }
    .info-item {
      display: flex;
      margin: 8px 0;
      font-size: 13px;
    }
    .info-item:first-child {
      margin-top: 0;
    }
    .info-item:last-child {
      margin-bottom: 0;
    }
    .info-label {
      color: #666;
      min-width: 120px;
      font-weight: 500;
    }
    .info-value {
      color: #000;
      flex: 1;
      word-break: break-all;
    }
    .footer {
      text-align: center;
      padding: 16px;
      color: #666;
      font-size: 12px;
      background-color: #fafafa;
      border-top: 1px solid #e5e5e5;
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>✅ 程序启动成功</h1>
    </div>
    <div class="content">
      <div class="section">
        <div class="label">状态</div>
        <div>
          <span class="status success">运行中</span>
        </div>
      </div>
      <div class="section">
        <div class="label">配置信息</div>
        <div class="info-box">
          <div class="info-item">
            <span class="info-label">RSS 源</span>
            <span class="info-value">%s</span>
          </div>
          <div class="info-item">
            <span class="info-label">监控关键词</span>
            <span class="info-value">%s</span>
          </div>
          <div class="info-item">
            <span class="info-label">执行频率</span>
            <span class="info-value">%s</span>
          </div>
          <div class="info-item">
            <span class="info-label">收件人</span>
            <span class="info-value">%s</span>
          </div>
        </div>
      </div>
      <div class="section">
        <div class="label">说明</div>
        <p style="margin: 8px 0 0 0; color: #666; font-size: 13px; line-height: 1.7;">
          这是一封启动测试邮件，用于验证 SMTP 配置是否正确。如果您收到这封邮件，说明邮件发送功能正常。程序将按照配置的频率自动监控 RSS 源，发现匹配关键词的新帖时会自动发送邮件通知。
        </p>
      </div>
    </div>
    <div class="footer">
      NodeSeek RSS Monitor · %s
    </div>
  </div>
</body>
</html>
	`,
		config.RSS.URL,
		strings.Join(config.Keywords, ", "),
		getScheduleDesc(),
		strings.Join(config.Email.To, ", "),
		time.Now().Format("2006-01-02 15:04:05"),
	)

	// 创建邮件
	m := gomail.NewMessage()
	m.SetHeader("From", config.Email.From)
	m.SetHeader("To", config.Email.To...)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", htmlBody)

	// 创建 SMTP 拨号器
	d := gomail.NewDialer(
		config.SMTP.Host,
		config.SMTP.Port,
		config.SMTP.User,
		config.SMTP.Pass,
	)

	// 发送邮件
	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("发送测试邮件失败: %w", err)
	}

	return nil
}

// getScheduleDesc 获取调度描述
func getScheduleDesc() string {
	if config.RSS.IntervalSeconds > 0 {
		return fmt.Sprintf("每 %d 秒执行一次", config.RSS.IntervalSeconds)
	} else if config.RSS.Cron != "" {
		return fmt.Sprintf("Cron: %s", config.RSS.Cron)
	}
	return "未配置"
}

// sendEmail 发送邮件
func sendEmail(item Item, keywords []string) error {
	// 替换邮件主题中的占位符
	subject := strings.ReplaceAll(config.Email.Subject, "{title}", item.Title)

	// 构建 HTML 邮件内容
	htmlBody := fmt.Sprintf(`
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <style>
    * {
      box-sizing: border-box;
    }
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
      line-height: 1.6;
      color: #000;
      background-color: #fafafa;
      margin: 0;
      padding: 20px;
      font-size: 14px;
    }
    .container {
      max-width: 600px;
      margin: 0 auto;
      background-color: #fff;
      border: 1px solid #e5e5e5;
      border-radius: 8px;
      overflow: hidden;
    }
    .header {
      background-color: #000;
      color: #fff;
      padding: 24px 24px;
      border-bottom: 1px solid #e5e5e5;
    }
    .header h1 {
      margin: 0;
      font-size: 18px;
      font-weight: 600;
      line-height: 1.4;
    }
    .content {
      padding: 24px;
    }
    .section {
      margin-bottom: 24px;
    }
    .section:last-child {
      margin-bottom: 0;
    }
    .label {
      font-size: 12px;
      color: #666;
      text-transform: uppercase;
      letter-spacing: 0.5px;
      margin-bottom: 8px;
      font-weight: 500;
    }
    .keywords {
      display: inline-block;
      background-color: #000;
      color: #fff;
      padding: 4px 10px;
      border-radius: 4px;
      font-size: 12px;
      font-weight: 500;
      margin: 4px 4px 4px 0;
    }
    .meta {
      background-color: #fafafa;
      border: 1px solid #e5e5e5;
      border-radius: 6px;
      padding: 16px;
      margin: 16px 0;
    }
    .meta-item {
      display: flex;
      margin: 8px 0;
      font-size: 13px;
    }
    .meta-item:first-child {
      margin-top: 0;
    }
    .meta-item:last-child {
      margin-bottom: 0;
    }
    .meta-label {
      color: #666;
      min-width: 80px;
      font-weight: 500;
    }
    .meta-value {
      color: #000;
      flex: 1;
    }
    .description {
      background-color: #fafafa;
      border-left: 3px solid #000;
      padding: 16px;
      margin: 16px 0;
      font-size: 14px;
      line-height: 1.7;
      color: #333;
    }
    .description p {
      margin: 0;
    }
    .button-wrapper {
      margin-top: 24px;
      padding-top: 24px;
      border-top: 1px solid #e5e5e5;
    }
    .link-button {
      display: inline-block;
      padding: 10px 20px;
      background-color: #000;
      color: #fff !important;
      text-decoration: none;
      border-radius: 6px;
      font-weight: 500;
      font-size: 13px;
      border: 1px solid #000;
      transition: all 0.2s ease;
    }
    .link-button:hover {
      background-color: #333;
      border-color: #333;
    }
    .footer {
      text-align: center;
      padding: 16px;
      color: #666;
      font-size: 12px;
      background-color: #fafafa;
      border-top: 1px solid #e5e5e5;
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h1>%s</h1>
    </div>
    <div class="content">
      <div class="section">
        <div class="label">匹配关键词</div>
        <div>
          %s
        </div>
      </div>
      <div class="meta">
        <div class="meta-item">
          <span class="meta-label">发布时间</span>
          <span class="meta-value">%s</span>
        </div>
        <div class="meta-item">
          <span class="meta-label">作者</span>
          <span class="meta-value">%s</span>
        </div>
        <div class="meta-item">
          <span class="meta-label">分类</span>
          <span class="meta-value">%s</span>
        </div>
      </div>
      <div class="section">
        <div class="label">内容摘要</div>
        <div class="description">
          <p>%s</p>
        </div>
      </div>
      <div class="button-wrapper">
        <a href="%s" class="link-button" target="_blank">查看完整内容 →</a>
      </div>
    </div>
    <div class="footer">
      NodeSeek RSS Monitor
    </div>
  </div>
</body>
</html>
	`,
		item.Title,
		formatKeywords(keywords),
		item.PubDate,
		item.Creator,
		item.Category,
		item.Description,
		item.Link,
	)

	// 创建邮件
	m := gomail.NewMessage()
	m.SetHeader("From", config.Email.From)
	m.SetHeader("To", config.Email.To...)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", htmlBody)

	// 创建 SMTP 拨号器
	d := gomail.NewDialer(
		config.SMTP.Host,
		config.SMTP.Port,
		config.SMTP.User,
		config.SMTP.Pass,
	)

	// 发送邮件
	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}

	log.Printf("邮件发送成功: %s", item.Title)
	return nil
}

// formatKeywords 格式化关键词为 HTML
func formatKeywords(keywords []string) string {
	var result []string
	for _, kw := range keywords {
		result = append(result, fmt.Sprintf(`<span class="keywords">%s</span>`, kw))
	}
	return strings.Join(result, "")
}

// saveConfig 将当前配置持久化到 config.json
func saveConfig() error {
	configPath := filepath.Join(execDir, "config.json")
	configMu.RLock()
	data, err := json.MarshalIndent(config, "", "  ")
	configMu.RUnlock()
	if err != nil {
		return err
	}
	return ioutil.WriteFile(configPath, data, 0644)
}

// isAllowedChat 检查 chatID 是否在白名单中
func isAllowedChat(chatID int64) bool {
	for _, id := range config.Telegram.ChatIDs {
		if id == chatID {
			return true
		}
	}
	return false
}

// startTelegramPolling 启动 Telegram 长轮询，接收 Bot 命令
func startTelegramPolling() {
	log.Println("Telegram Bot 命令监听已启动")
	client := &http.Client{Timeout: 35 * time.Second}
	offset := 0

	for {
		updates, newOffset, err := getTelegramUpdates(client, offset)
		if err != nil {
			log.Printf("获取 Telegram 更新失败: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		offset = newOffset
		for _, u := range updates {
			if u.Message != nil {
				handleTelegramCommand(u.Message)
			}
		}
	}
}

// getTelegramUpdates 通过长轮询获取新消息
func getTelegramUpdates(client *http.Client, offset int) ([]tgUpdate, int, error) {
	apiURL := fmt.Sprintf(
		"https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30",
		config.Telegram.Token, offset,
	)
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, offset, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	var result tgUpdatesResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, offset, fmt.Errorf("解析响应失败: %w", err)
	}
	if !result.OK {
		return nil, offset, fmt.Errorf("Telegram API 返回 ok=false")
	}

	newOffset := offset
	for _, u := range result.Result {
		if u.UpdateID+1 > newOffset {
			newOffset = u.UpdateID + 1
		}
	}
	return result.Result, newOffset, nil
}

// handleTelegramCommand 处理来自 Bot 的命令消息
func handleTelegramCommand(msg *tgMessage) {
	if !isAllowedChat(msg.Chat.ID) {
		log.Printf("忽略来自未授权 chat %d 的消息", msg.Chat.ID)
		return
	}
	text := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}
	reply := dispatchCommand(text)
	if reply == "" {
		return
	}
	if err := sendTelegramMessage(msg.Chat.ID, reply); err != nil {
		log.Printf("发送 Telegram 回复失败: %v", err)
	}
}

// dispatchCommand 解析并执行命令，返回回复文本
func dispatchCommand(text string) string {
	switch {
	case text == "/help":
		return "🤖 <b>可用命令:</b>\n\n" +
			"/keywords — 查看关键词列表\n" +
			"/add &lt;关键词&gt; — 添加关键词\n" +
			"/del &lt;关键词&gt; — 删除关键词\n" +
			"/email on — 开启邮件通知\n" +
			"/email off — 关闭邮件通知\n" +
			"/status — 查看运行状态\n" +
			"/help — 显示帮助"

	case text == "/keywords":
		configMu.RLock()
		kws := make([]string, len(config.Keywords))
		copy(kws, config.Keywords)
		configMu.RUnlock()
		if len(kws) == 0 {
			return "📋 当前没有设置任何关键词"
		}
		lines := make([]string, len(kws))
		for i, kw := range kws {
			lines[i] = fmt.Sprintf("%d. %s", i+1, kw)
		}
		return "📋 <b>当前关键词列表:</b>\n" + strings.Join(lines, "\n")

	case text == "/add":
		return "❌ 请提供关键词，例如: /add 搬瓦工"

	case strings.HasPrefix(text, "/add "):
		kw := strings.TrimSpace(text[5:])
		if kw == "" {
			return "❌ 请提供关键词，例如: /add 搬瓦工"
		}
		configMu.Lock()
		for _, existing := range config.Keywords {
			if existing == kw {
				configMu.Unlock()
				return fmt.Sprintf("⚠️ 关键词 <b>%s</b> 已存在", kw)
			}
		}
		config.Keywords = append(config.Keywords, kw)
		configMu.Unlock()
		if err := saveConfig(); err != nil {
			log.Printf("保存配置失败: %v", err)
			return fmt.Sprintf("✅ 已添加关键词 <b>%s</b>，但保存配置失败: %v", kw, err)
		}
		log.Printf("Telegram 命令: 添加关键词 [%s]", kw)
		return fmt.Sprintf("✅ 已添加关键词: <b>%s</b>", kw)

	case text == "/del" || text == "/remove":
		return "❌ 请提供关键词，例如: /del 搬瓦工"

	case strings.HasPrefix(text, "/del ") || strings.HasPrefix(text, "/remove "):
		var kw string
		if strings.HasPrefix(text, "/del ") {
			kw = strings.TrimSpace(text[5:])
		} else {
			kw = strings.TrimSpace(text[8:])
		}
		if kw == "" {
			return "❌ 请提供关键词，例如: /del 搬瓦工"
		}
		configMu.Lock()
		found := false
		newKws := config.Keywords[:0:0]
		for _, existing := range config.Keywords {
			if existing == kw {
				found = true
			} else {
				newKws = append(newKws, existing)
			}
		}
		if found {
			config.Keywords = newKws
		}
		configMu.Unlock()
		if !found {
			return fmt.Sprintf("⚠️ 关键词 <b>%s</b> 不存在", kw)
		}
		if err := saveConfig(); err != nil {
			log.Printf("保存配置失败: %v", err)
			return fmt.Sprintf("✅ 已删除关键词 <b>%s</b>，但保存配置失败: %v", kw, err)
		}
		log.Printf("Telegram 命令: 删除关键词 [%s]", kw)
		return fmt.Sprintf("✅ 已删除关键词: <b>%s</b>", kw)

	case text == "/email on":
		configMu.Lock()
		config.Email.Disabled = false
		configMu.Unlock()
		if err := saveConfig(); err != nil {
			log.Printf("保存配置失败: %v", err)
			return fmt.Sprintf("✅ 邮件通知已开启，但保存配置失败: %v", err)
		}
		log.Println("Telegram 命令: 开启邮件通知")
		return "✅ 邮件通知已开启"

	case text == "/email off":
		configMu.Lock()
		config.Email.Disabled = true
		configMu.Unlock()
		if err := saveConfig(); err != nil {
			log.Printf("保存配置失败: %v", err)
			return fmt.Sprintf("✅ 邮件通知已关闭，但保存配置失败: %v", err)
		}
		log.Println("Telegram 命令: 关闭邮件通知")
		return "🔕 邮件通知已关闭"

	case text == "/email":
		configMu.RLock()
		disabled := config.Email.Disabled
		configMu.RUnlock()
		if disabled {
			return "🔕 邮件通知当前已关闭\n\n使用 /email on 开启"
		}
		return "✅ 邮件通知当前已开启\n\n使用 /email off 关闭"

	case text == "/status":
		configMu.RLock()
		kwCount := len(config.Keywords)
		emailDisabled := config.Email.Disabled
		configMu.RUnlock()
		emailStatus := "✅ 开启"
		if emailDisabled {
			emailStatus = "🔕 关闭"
		}
		return fmt.Sprintf(
			"📊 <b>运行状态</b>\n\n"+
				"📡 <b>RSS 源:</b> %s\n"+
				"⏱ <b>执行频率:</b> %s\n"+
				"🔍 <b>关键词数量:</b> %d\n"+
				"📧 <b>邮件通知:</b> %s\n"+
				"💾 <b>缓存条目:</b> %d\n"+
				"🕐 <b>当前时间:</b> %s",
			config.RSS.URL,
			getScheduleDesc(),
			kwCount,
			emailStatus,
			len(sentCache.Items),
			time.Now().Format("2006-01-02 15:04:05"),
		)
	}
	return ""
}

// emailEnabled 判断邮件是否已配置且未被禁用
func emailEnabled() bool {
	return config.SMTP.Host != "" && len(config.Email.To) > 0 && !config.Email.Disabled
}

// telegramEnabled 判断 Telegram 是否已配置
func telegramEnabled() bool {
	return config.Telegram.Token != "" && len(config.Telegram.ChatIDs) > 0
}

// sendTelegramMessage 向指定 chat 发送 Telegram 消息
func sendTelegramMessage(chatID int64, text string) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", config.Telegram.Token)

	payload := map[string]interface{}{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %w", err)
	}

	resp, err := http.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("请求 Telegram API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("Telegram API 返回错误 %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// sendTelegramToAll 向所有配置的 chat 发送消息
func sendTelegramToAll(text string) error {
	var lastErr error
	for _, chatID := range config.Telegram.ChatIDs {
		if err := sendTelegramMessage(chatID, text); err != nil {
			log.Printf("发送 Telegram 消息到 chat %d 失败: %v", chatID, err)
			lastErr = err
		}
	}
	return lastErr
}

// sendTelegramTestMessage 发送 Telegram 启动通知
func sendTelegramTestMessage() error {
	text := fmt.Sprintf(
		"✅ <b>NodeSeek RSS Monitor 启动成功</b>\n\n"+
			"📡 <b>RSS 源:</b> %s\n"+
			"🔍 <b>监控关键词:</b> %s\n"+
			"⏱ <b>执行频率:</b> %s\n"+
			"🕐 <b>启动时间:</b> %s",
		config.RSS.URL,
		strings.Join(config.Keywords, ", "),
		getScheduleDesc(),
		time.Now().Format("2006-01-02 15:04:05"),
	)
	return sendTelegramToAll(text)
}

// sendTelegramNotification 发送 RSS 匹配通知
func sendTelegramNotification(item Item, keywords []string) error {
	text := fmt.Sprintf(
		"🔔 <b>%s</b>\n\n"+
			"🏷 <b>匹配关键词:</b> %s\n"+
			"👤 <b>作者:</b> %s\n"+
			"📂 <b>分类:</b> %s\n"+
			"🕐 <b>发布时间:</b> %s\n\n"+
			"📄 <b>内容摘要:</b>\n%s\n\n"+
			"🔗 <a href=\"%s\">查看完整内容</a>",
		item.Title,
		strings.Join(keywords, ", "),
		item.Creator,
		item.Category,
		item.PubDate,
		truncateText(stripHTML(item.Description), 300),
		item.Link,
	)
	return sendTelegramToAll(text)
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
