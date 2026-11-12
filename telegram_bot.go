package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Telegram Bot API 响应结构
type tgUpdatesResp struct {
	OK          bool       `json:"ok"`
	Result      []tgUpdate `json:"result"`
	Description string     `json:"description"`
}

type tgUpdate struct {
	UpdateID      int              `json:"update_id"`
	Message       *tgMessage       `json:"message"`
	CallbackQuery *tgCallbackQuery `json:"callback_query"`
}

type tgMessage struct {
	MessageID int     `json:"message_id"`
	Chat      tgChat  `json:"chat"`
	From      *tgUser `json:"from"`
	Text      string  `json:"text"`
}

type tgChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgAPIResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

type telegramAPIError struct {
	Code        int
	Description string
}

func (e *telegramAPIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("Telegram API 错误 %d: %s", e.Code, e.Description)
	}
	return "Telegram API 错误: " + e.Description
}

var (
	// Overridable in tests so no real Telegram API is contacted.
	telegramAPIBase    = "https://api.telegram.org"
	telegramCallClient = &http.Client{Timeout: 15 * time.Second}
	telegramPollClient = &http.Client{Timeout: 35 * time.Second}
)

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

// escapeHTML escapes dynamic text before embedding it in Telegram HTML.
func escapeHTML(s string) string {
	return htmlEscaper.Replace(s)
}

func telegramAPIURL(method string) string {
	configMu.RLock()
	token := config.Telegram.Token
	configMu.RUnlock()
	return telegramAPIBase + "/bot" + token + "/" + method
}

// callTelegram performs a Bot API call and returns the raw result.
func callTelegram(method string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 Telegram 请求失败: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, telegramAPIURL(method), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建 Telegram 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := telegramCallClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Telegram API 失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 Telegram API 响应失败: %w", err)
	}
	var envelope tgAPIResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("解析 Telegram API 响应失败 (HTTP %d): %w", resp.StatusCode, err)
	}
	if !envelope.OK {
		return nil, &telegramAPIError{Code: envelope.ErrorCode, Description: envelope.Description}
	}
	return envelope.Result, nil
}

// sendTelegramMessage 向指定 chat 发送 Telegram 消息
func sendTelegramMessage(chatID int64, text string) error {
	_, err := callTelegram("sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	})
	return err
}

// sendTelegramMenuMessage sends a message with an inline keyboard and returns
// its message_id so subsequent interactions can edit the same bubble.
func sendTelegramMenuMessage(chatID int64, text string, kb *inlineKeyboardMarkup) (int, error) {
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	raw, err := callTelegram("sendMessage", payload)
	if err != nil {
		return 0, err
	}
	var msg tgMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return 0, fmt.Errorf("解析 sendMessage 响应失败: %w", err)
	}
	if msg.MessageID == 0 {
		return 0, fmt.Errorf("Telegram 未返回 message_id")
	}
	return msg.MessageID, nil
}

// editTelegramMessage edits an existing menu bubble in place.
func editTelegramMessage(chatID int64, messageID int, text string, kb *inlineKeyboardMarkup) error {
	payload := map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	_, err := callTelegram("editMessageText", payload)
	return err
}

// answerCallbackQuery must be called for every callback button press.
func answerCallbackQuery(id, text string, alert bool) error {
	payload := map[string]any{"callback_query_id": id}
	if text != "" {
		payload["text"] = text
	}
	if alert {
		payload["show_alert"] = true
	}
	_, err := callTelegram("answerCallbackQuery", payload)
	return err
}

func isMessageNotModified(err error) bool {
	var apiErr *telegramAPIError
	if errors.As(err, &apiErr) {
		return strings.Contains(strings.ToLower(apiErr.Description), "message is not modified")
	}
	return false
}

func isMessageNotFound(err error) bool {
	var apiErr *telegramAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	d := strings.ToLower(apiErr.Description)
	return strings.Contains(d, "message to edit not found") ||
		strings.Contains(d, "message can't be edited") ||
		strings.Contains(d, "message is not found")
}

// sendTelegramToAll 向所有配置的 chat 发送消息
func sendTelegramToAll(text string) error {
	configMu.RLock()
	chatIDs := append([]int64(nil), config.Telegram.ChatIDs...)
	configMu.RUnlock()

	var lastErr error
	for _, chatID := range chatIDs {
		if err := sendTelegramMessage(chatID, text); err != nil {
			log.Printf("发送 Telegram 消息到 chat %d 失败: %v", chatID, err)
			lastErr = err
		}
	}
	return lastErr
}

// startTelegramPolling 启动 Telegram 长轮询，接收 Bot 命令和按钮回调
func startTelegramPolling() {
	log.Println("Telegram Bot 命令监听已启动")
	offset := 0

	for {
		updates, newOffset, err := getTelegramUpdates(offset)
		if err != nil {
			log.Printf("获取 Telegram 更新失败: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		offset = newOffset
		for _, u := range updates {
			if u.Message != nil {
				handleTelegramMessage(u.Message)
			}
			if u.CallbackQuery != nil {
				handleTelegramCallback(u.CallbackQuery)
			}
		}
	}
}

// getTelegramUpdates 通过长轮询获取新消息
func getTelegramUpdates(offset int) ([]tgUpdate, int, error) {
	q := url.Values{}
	q.Set("offset", strconv.Itoa(offset))
	q.Set("timeout", "30")
	q.Set("allowed_updates", `["message","callback_query"]`)
	apiURL := telegramAPIURL("getUpdates") + "?" + q.Encode()

	resp, err := telegramPollClient.Get(apiURL)
	if err != nil {
		return nil, offset, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, offset, fmt.Errorf("读取响应失败: %w", err)
	}
	var result tgUpdatesResp
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, offset, fmt.Errorf("解析响应失败: %w", err)
	}
	if !result.OK {
		return nil, offset, fmt.Errorf("Telegram API 返回 ok=false: %s", result.Description)
	}

	newOffset := offset
	for _, u := range result.Result {
		if u.UpdateID+1 > newOffset {
			newOffset = u.UpdateID + 1
		}
	}
	return result.Result, newOffset, nil
}

// splitCommand splits "/add@bot ns foo" into ("/add", "ns foo").
func splitCommand(text string) (cmd, rest string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	first := fields[0]
	if i := strings.IndexByte(first, '@'); i >= 0 {
		first = first[:i]
	}
	rest = strings.TrimSpace(text[len(fields[0]):])
	return first, rest
}

// handleTelegramMessage 处理来自 Bot 的普通消息（命令或关键词输入）。
func handleTelegramMessage(msg *tgMessage) {
	if msg == nil {
		return
	}
	if !isAllowedChat(msg.Chat.ID) {
		log.Printf("忽略来自未授权 chat %d 的消息", msg.Chat.ID)
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	if !strings.HasPrefix(text, "/") {
		handlePendingInput(msg)
		return
	}

	cmd, rest := splitCommand(text)
	switch cmd {
	case "/start", "/menu":
		clearPendingInput(msg.Chat.ID)
		text, kb := renderHomePage()
		if _, err := sendTelegramMenuMessage(msg.Chat.ID, text, kb); err != nil {
			log.Printf("发送 Telegram 菜单失败: %v", err)
		}
		return
	case "/cancel":
		if p := peekPendingInput(msg.Chat.ID); p != nil && msg.From != nil && msg.From.ID == p.userID {
			clearPendingInput(msg.Chat.ID)
			editMenuByID(msg.Chat.ID, p.messageID, "已取消操作。", backKeyboard())
			return
		}
	}

	// State-changing legacy commands must pass the same sender check as the
	// menu buttons; read-only commands keep the original chat whitelist.
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	if commandRequiresManage(cmd, rest) && !canManageBot(msg.Chat.ID, msg.Chat.Type, userID) {
		if err := sendTelegramMessage(msg.Chat.ID, "⛔ 无权限执行此操作。群聊中请由管理员在私聊或通过 <code>telegram.admin_user_ids</code> 操作。"); err != nil {
			log.Printf("发送 Telegram 权限提示失败: %v", err)
		}
		return
	}

	clearPendingInput(msg.Chat.ID)
	reply := dispatchCommand(text)
	if reply == "" {
		return
	}
	if err := sendTelegramMessage(msg.Chat.ID, reply); err != nil {
		log.Printf("发送 Telegram 回复失败: %v", err)
	}
}

// commandRequiresManage reports whether a legacy command mutates config.
func commandRequiresManage(cmd, rest string) bool {
	switch cmd {
	case "/add", "/del", "/remove":
		return true
	case "/email":
		switch strings.ToLower(strings.TrimSpace(rest)) {
		case "on", "off":
			return true
		}
	}
	return false
}

// handleTelegramCommand 保留旧入口名称，便于兼容。
func handleTelegramCommand(msg *tgMessage) {
	handleTelegramMessage(msg)
}

// dispatchCommand 解析并执行命令，返回回复文本
func dispatchCommand(text string) string {
	cmd, rest := splitCommand(text)
	feeds := getFeeds()

	switch cmd {
	case "/help", "/start", "/menu":
		return helpText()

	case "/keywords":
		configMu.RLock()
		kws := make([]Keyword, len(config.Keywords))
		copy(kws, config.Keywords)
		configMu.RUnlock()
		if len(kws) == 0 {
			return "📋 当前没有设置任何关键词"
		}
		lines := make([]string, len(kws))
		for i, kw := range kws {
			src := kw.Source
			if src == "" {
				src = "all"
			}
			lines[i] = fmt.Sprintf("%d. [%s] %s", i+1, escapeHTML(src), escapeHTML(kw.Text))
		}
		return "📋 <b>当前关键词列表:</b>\n" + strings.Join(lines, "\n")

	case "/add":
		source, keyword := parseKeywordArgs(rest, feeds)
		if keyword == "" {
			return "❌ 请提供关键词，例如: /add ns 搬瓦工 或 /add 关键词(监听所有)"
		}
		added, err := addKeyword(source, keyword)
		srcLabel := source
		if srcLabel == "" {
			srcLabel = "all"
		}
		if err != nil {
			if added {
				return fmt.Sprintf("✅ 已添加关键词 [%s] <b>%s</b>，但保存配置失败: %s", escapeHTML(srcLabel), escapeHTML(keyword), escapeHTML(err.Error()))
			}
			return fmt.Sprintf("❌ 添加关键词失败: %s", escapeHTML(err.Error()))
		}
		if !added {
			return fmt.Sprintf("⚠️ 关键词 [%s] <b>%s</b> 已存在", escapeHTML(srcLabel), escapeHTML(keyword))
		}
		log.Printf("Telegram 命令: 添加关键词 [%s] %s", srcLabel, keyword)
		return fmt.Sprintf("✅ 已添加关键词: [%s] <b>%s</b>", escapeHTML(srcLabel), escapeHTML(keyword))

	case "/del", "/remove":
		source, keyword := parseKeywordArgs(rest, feeds)
		if keyword == "" {
			return "❌ 请提供关键词，例如: /del ns 搬瓦工 或 /del 关键词"
		}
		removed, err := deleteKeyword(source, keyword)
		if err != nil {
			return fmt.Sprintf("❌ 删除关键词失败: %s", escapeHTML(err.Error()))
		}
		if !removed {
			return fmt.Sprintf("⚠️ 关键词 <b>%s</b> 不存在", escapeHTML(keyword))
		}
		log.Printf("Telegram 命令: 删除关键词 [%s] %s", source, keyword)
		return fmt.Sprintf("✅ 已删除关键词: <b>%s</b>", escapeHTML(keyword))

	case "/email":
		switch strings.ToLower(strings.TrimSpace(rest)) {
		case "on":
			if err := setEmailDisabled(false); err != nil {
				return fmt.Sprintf("✅ 邮件通知已开启，但保存配置失败: %s", escapeHTML(err.Error()))
			}
			log.Println("Telegram 命令: 开启邮件通知")
			return "✅ 邮件通知已开启"
		case "off":
			if err := setEmailDisabled(true); err != nil {
				return fmt.Sprintf("✅ 邮件通知已关闭，但保存配置失败: %s", escapeHTML(err.Error()))
			}
			log.Println("Telegram 命令: 关闭邮件通知")
			return "🔕 邮件通知已关闭"
		case "":
			configMu.RLock()
			disabled := config.Email.Disabled
			configMu.RUnlock()
			if disabled {
				return "🔕 邮件通知当前已关闭\n\n使用 /email on 开启"
			}
			return "✅ 邮件通知当前已开启\n\n使用 /email off 关闭"
		default:
			return "❌ 用法: /email on 或 /email off"
		}

	case "/status":
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
				"🏷 <b>版本:</b> %s\n"+
				"📡 <b>RSS 源:</b>\n%s\n\n"+
				"⏱ <b>执行频率:</b> %s\n"+
				"🔍 <b>关键词数量:</b> %d\n"+
				"📧 <b>邮件通知:</b> %s\n"+
				"💾 <b>缓存条目:</b> %d\n"+
				"🕐 <b>当前时间:</b> %s",
			escapeHTML(version),
			formatFeedsForDisplayTG(feeds),
			escapeHTML(getScheduleDesc()),
			kwCount,
			emailStatus,
			cacheCount(),
			time.Now().Format("2006-01-02 15:04:05"),
		)

	case "/version":
		return versionText()

	case "/update":
		return "🔄 请使用 /menu → <b>检查更新</b>。\n安装新版本前需要在菜单中再次确认。"

	case "/cancel":
		return "没有待取消的操作。"

	default:
		return ""
	}
}

// parseKeywordArgs parses "ns keyword" / "all keyword" / "keyword".
func parseKeywordArgs(rest string, feeds []RSSFeed) (source, keyword string) {
	args := strings.Fields(rest)
	if len(args) == 0 {
		return "", ""
	}
	if len(args) >= 2 {
		for _, f := range feeds {
			if args[0] == f.ID {
				return f.ID, strings.Join(args[1:], " ")
			}
		}
		if args[0] == "all" {
			return "", strings.Join(args[1:], " ")
		}
	}
	return "", strings.Join(args, " ")
}

// addKeyword adds a keyword and persists the config. It returns added=false
// when the keyword already exists.
func addKeyword(source, keyword string) (bool, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return false, fmt.Errorf("关键词为空")
	}
	configMu.Lock()
	for _, existing := range config.Keywords {
		if existing.Text == keyword && existing.Source == source {
			configMu.Unlock()
			return false, nil
		}
	}
	config.Keywords = append(config.Keywords, Keyword{Text: keyword, Source: source})
	configMu.Unlock()
	if err := saveConfig(); err != nil {
		return true, err
	}
	return true, nil
}

// deleteKeyword removes an exact keyword/source pair.
func deleteKeyword(source, keyword string) (bool, error) {
	configMu.Lock()
	idx := -1
	for i, existing := range config.Keywords {
		if existing.Text == keyword && existing.Source == source {
			idx = i
			break
		}
	}
	if idx < 0 {
		configMu.Unlock()
		return false, nil
	}
	newKws := make(KeywordList, 0, len(config.Keywords)-1)
	newKws = append(newKws, config.Keywords[:idx]...)
	newKws = append(newKws, config.Keywords[idx+1:]...)
	config.Keywords = newKws
	configMu.Unlock()
	if err := saveConfig(); err != nil {
		return true, err
	}
	return true, nil
}

// setEmailDisabled toggles the email notification switch and persists it.
func setEmailDisabled(disabled bool) error {
	configMu.Lock()
	config.Email.Disabled = disabled
	configMu.Unlock()
	return saveConfig()
}

func helpText() string {
	return "🤖 <b>可用命令:</b>\n\n" +
		"/start 或 /menu — 打开按钮菜单\n" +
		"/keywords — 查看关键词列表\n" +
		"/add [ns|ld|all] &lt;关键词&gt; — 添加关键词\n" +
		"/del [ns|ld|all] &lt;关键词&gt; — 删除关键词\n" +
		"/email on|off — 开启/关闭邮件通知\n" +
		"/status — 查看运行状态\n" +
		"/version — 查看版本\n" +
		"/update — 查看更新入口\n" +
		"/help — 显示帮助\n\n" +
		"💡 菜单中的状态、关键词、邮件开关、版本和更新都可以在同一个气泡内操作。"
}

func versionText() string {
	return fmt.Sprintf(
		"🏷 <b>版本信息</b>\n\n"+
			"版本: <code>%s</code>\n"+
			"提交: <code>%s</code>\n"+
			"构建时间: <code>%s</code>\n"+
			"更新仓库: <code>%s</code>",
		escapeHTML(version),
		escapeHTML(shortCommit()),
		escapeHTML(buildTime),
		escapeHTML(updateRepo),
	)
}
