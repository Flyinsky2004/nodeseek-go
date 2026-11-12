package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"gopkg.in/gomail.v2"
)

// sendTestEmail 发送测试邮件
func sendTestEmail() error {
	configMu.RLock()
	smtpHost := config.SMTP.Host
	smtpPort := config.SMTP.Port
	smtpUser := config.SMTP.User
	smtpPass := config.SMTP.Pass
	from := config.Email.From
	to := append([]string(nil), config.Email.To...)
	keywords := make([]Keyword, len(config.Keywords))
	copy(keywords, config.Keywords)
	configMu.RUnlock()

	subject := "[RSS Monitor] 程序启动测试邮件"

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
      RSS Monitor · %s
    </div>
  </div>
</body>
</html>
	`,
		formatFeedsForDisplay(getFeeds()),
		formatKeywordsForDisplay(keywords),
		getScheduleDesc(),
		strings.Join(to, ", "),
		time.Now().Format("2006-01-02 15:04:05"),
	)

	// 创建邮件
	m := gomail.NewMessage()
	m.SetHeader("From", from)
	m.SetHeader("To", to...)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", htmlBody)

	// 创建 SMTP 拨号器
	d := gomail.NewDialer(smtpHost, smtpPort, smtpUser, smtpPass)

	// 发送邮件
	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("发送测试邮件失败: %w", err)
	}

	return nil
}

// sendEmail 发送邮件
func sendEmail(item Item, keywords []string, feed RSSFeed) error {
	configMu.RLock()
	subjectTemplate := config.Email.Subject
	from := config.Email.From
	to := append([]string(nil), config.Email.To...)
	smtpHost := config.SMTP.Host
	smtpPort := config.SMTP.Port
	smtpUser := config.SMTP.User
	smtpPass := config.SMTP.Pass
	configMu.RUnlock()

	// 替换邮件主题中的占位符
	subject := strings.ReplaceAll(subjectTemplate, "{title}", item.Title)
	subject = strings.ReplaceAll(subject, "{source}", feed.Name)

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
          <span class="meta-label">来源</span>
          <span class="meta-value">%s</span>
        </div>        <div class="meta-item">
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
      RSS Monitor
    </div>
  </div>
</body>
</html>
	`,
		item.Title,
		formatKeywords(keywords),
		feed.Name,
		item.PubDate,
		item.GetCreator(),
		item.Category,
		item.Description,
		item.Link,
	)

	// 创建邮件
	m := gomail.NewMessage()
	m.SetHeader("From", from)
	m.SetHeader("To", to...)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", htmlBody)

	// 创建 SMTP 拨号器
	d := gomail.NewDialer(smtpHost, smtpPort, smtpUser, smtpPass)

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

// formatFeedsForDisplayTG 格式化多源信息用于 Telegram 显示
func formatFeedsForDisplayTG(feeds []RSSFeed) string {
	var lines []string
	for _, f := range feeds {
		lines = append(lines, fmt.Sprintf("%s (%s)", escapeHTML(f.Name), escapeHTML(f.URL)))
	}
	return strings.Join(lines, "\n")
}

// formatKeywordsForDisplayTG 格式化关键词列表（含来源）用于 Telegram 显示
func formatKeywordsForDisplayTG(keywords []Keyword) string {
	var parts []string
	for _, kw := range keywords {
		src := kw.Source
		if src == "" {
			src = "all"
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", escapeHTML(src), escapeHTML(kw.Text)))
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, ", ")
}

// sendTelegramTestMessage 发送 Telegram 启动通知
func sendTelegramTestMessage() error {
	configMu.RLock()
	keywords := make([]Keyword, len(config.Keywords))
	copy(keywords, config.Keywords)
	configMu.RUnlock()

	text := fmt.Sprintf(
		"✅ <b>RSS Monitor 启动成功</b>\n\n"+
			"🏷 <b>版本:</b> %s\n"+
			"📡 <b>RSS 源:</b> %s\n"+
			"🔍 <b>监控关键词:</b> %s\n"+
			"⏱ <b>执行频率:</b> %s\n"+
			"🕐 <b>启动时间:</b> %s",
		escapeHTML(version),
		formatFeedsForDisplayTG(getFeeds()),
		formatKeywordsForDisplayTG(keywords),
		escapeHTML(getScheduleDesc()),
		time.Now().Format("2006-01-02 15:04:05"),
	)
	return sendTelegramToAll(text)
}

// sendTelegramNotification 发送 RSS 匹配通知
func sendTelegramNotification(item Item, keywords []string, feed RSSFeed) error {
	escapedKeywords := make([]string, len(keywords))
	for i, kw := range keywords {
		escapedKeywords[i] = escapeHTML(kw)
	}
	text := fmt.Sprintf(
		"🔔 [%s] <b>%s</b>\n\n"+
			"🏷 <b>匹配关键词:</b> %s\n"+
			"👤 <b>作者:</b> %s\n"+
			"📂 <b>分类:</b> %s\n"+
			"🕐 <b>发布时间:</b> %s\n\n"+
			"📄 <b>内容摘要:</b>\n%s\n\n"+
			"🔗 <a href=\"%s\">查看完整内容</a>",
		escapeHTML(feed.Name),
		escapeHTML(item.Title),
		strings.Join(escapedKeywords, ", "),
		escapeHTML(item.GetCreator()),
		escapeHTML(item.Category),
		escapeHTML(item.PubDate),
		escapeHTML(truncateText(stripHTML(item.Description), 300)),
		escapeHTML(item.Link),
	)
	return sendTelegramToAll(text)
}
