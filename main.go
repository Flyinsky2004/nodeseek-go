package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
	} `json:"email"`
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
	sentCache SentCache
	execDir   string
)

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

	processedCount := 0
	sentCount := 0

	// 遍历 RSS 项
	for _, item := range rss.Channel.Items {
		// 检查是否已发送
		if _, exists := sentCache.Items[item.GUID]; exists {
			continue
		}

		processedCount++

		// 检查是否匹配关键词
		matchedKeywords := []string{}
		for _, keyword := range config.Keywords {
			if strings.Contains(item.Title, keyword) || strings.Contains(item.Description, keyword) {
				matchedKeywords = append(matchedKeywords, keyword)
			}
		}

		if len(matchedKeywords) > 0 {
			log.Printf("匹配到关键词 [%s]: %s", strings.Join(matchedKeywords, ", "), item.Title)

			// 发送邮件
			if err := sendEmail(item, matchedKeywords); err != nil {
				log.Printf("发送邮件失败: %v", err)
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

	log.Printf("任务完成 - 处理: %d 条新项, 发送: %d 封邮件", processedCount, sentCount)

	// 保存缓存
	return saveCache()
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
  <style>
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
      line-height: 1.6;
      color: #333;
      background-color: #f5f5f5;
      margin: 0;
      padding: 20px;
    }
    .container {
      max-width: 600px;
      margin: 0 auto;
      background-color: #ffffff;
      border-radius: 8px;
      overflow: hidden;
      box-shadow: 0 2px 8px rgba(0,0,0,0.1);
    }
    .header {
      background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
      color: white;
      padding: 30px 20px;
    }
    .header h2 {
      margin: 0;
      font-size: 20px;
      font-weight: 600;
    }
    .content {
      padding: 30px 20px;
    }
    .keywords {
      display: inline-block;
      background-color: #e8f5e9;
      color: #2e7d32;
      padding: 4px 12px;
      border-radius: 12px;
      font-size: 14px;
      font-weight: 500;
      margin: 2px 4px 2px 0;
    }
    .meta {
      background-color: #f9f9f9;
      padding: 15px;
      border-radius: 6px;
      margin: 20px 0;
      font-size: 14px;
    }
    .meta-item {
      margin: 8px 0;
      color: #666;
    }
    .meta-item strong {
      color: #333;
      min-width: 80px;
      display: inline-block;
    }
    .description {
      margin: 20px 0;
      padding: 15px;
      background-color: #fafafa;
      border-left: 4px solid #667eea;
      border-radius: 4px;
      font-size: 15px;
      line-height: 1.7;
    }
    .link-button {
      display: inline-block;
      margin-top: 20px;
      padding: 12px 30px;
      background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
      color: white !important;
      text-decoration: none;
      border-radius: 6px;
      font-weight: 500;
      transition: transform 0.2s;
    }
    .link-button:hover {
      transform: translateY(-2px);
    }
    .footer {
      text-align: center;
      padding: 20px;
      color: #999;
      font-size: 12px;
      border-top: 1px solid #eee;
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <h2>%s</h2>
    </div>
    <div class="content">
      <div>
        <strong>匹配关键词：</strong>
        %s
      </div>
      <div class="meta">
        <div class="meta-item">
          <strong>发布时间：</strong>%s
        </div>
        <div class="meta-item">
          <strong>作者：</strong>%s
        </div>
        <div class="meta-item">
          <strong>分类：</strong>%s
        </div>
      </div>
      <div class="description">
        <strong>内容摘要：</strong>
        <p style="margin: 10px 0 0 0;">%s</p>
      </div>
      <div style="text-align: center;">
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
