package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	menuPageSize          = 8
	callbackDataMaxLen    = 64
	pendingInputTTL       = 5 * time.Minute
	keywordHashLen        = 10
	maxMenuDisplayKeyword = 200
)

type inlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type inlineKeyboardMarkup struct {
	InlineKeyboard [][]inlineKeyboardButton `json:"inline_keyboard"`
}

func cbButton(text, data string) inlineKeyboardButton {
	if len(data) > callbackDataMaxLen {
		data = data[:callbackDataMaxLen]
	}
	return inlineKeyboardButton{Text: text, CallbackData: data}
}

func backButton() inlineKeyboardButton {
	return cbButton("⬅️ 返回", "m:home")
}

func backKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{{backButton()}}}
}

func emptyKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{}}
}

func sourceLabel(source string) string {
	if source == "" {
		return "all"
	}
	return source
}

func buttonLabel(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func pageCount(n int) int {
	if n <= 0 {
		return 1
	}
	return (n + menuPageSize - 1) / menuPageSize
}

func clampPage(page, total int) int {
	if page < 0 {
		return 0
	}
	if page >= total {
		return total - 1
	}
	return page
}

func parsePageArg(parts []string, idx int) int {
	if len(parts) <= idx {
		return 0
	}
	n, err := strconv.Atoi(parts[idx])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func keywordHash(kw Keyword) string {
	sum := sha1.Sum([]byte(kw.Source + "\x00" + kw.Text))
	return hex.EncodeToString(sum[:])[:keywordHashLen]
}

// ---------------------------------------------------------------------------
// Menu rendering
// ---------------------------------------------------------------------------

func renderHomePage() (string, *inlineKeyboardMarkup) {
	configMu.RLock()
	kwCount := len(config.Keywords)
	emailDisabled := config.Email.Disabled
	configMu.RUnlock()

	feeds := getFeeds()
	names := make([]string, len(feeds))
	for i, f := range feeds {
		names[i] = escapeHTML(f.Name)
	}
	feedLabel := strings.Join(names, ", ")
	if feedLabel == "" {
		feedLabel = "未配置"
	}
	emailStatus := "✅ 开启"
	if emailDisabled {
		emailStatus = "🔕 关闭"
	}

	text := fmt.Sprintf(
		"🤖 <b>NodeSeek RSS Monitor</b>\n"+
			"版本: <code>%s</code>\n\n"+
			"📡 源: %s\n"+
			"🔍 关键词: %d 个\n"+
			"📧 邮件: %s\n\n"+
			"请选择操作：",
		escapeHTML(version), feedLabel, kwCount, emailStatus,
	)

	kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{cbButton("📊 状态", "m:status"), cbButton("🔍 关键词", "m:kw:0")},
		{cbButton("📧 邮件开关", "m:email"), cbButton("ℹ️ 帮助", "m:help")},
		{cbButton("🏷 版本", "m:ver"), cbButton("🔄 检查更新", "m:chk")},
	}}
	return text, kb
}

func renderStatusPage() (string, *inlineKeyboardMarkup) {
	configMu.RLock()
	kwCount := len(config.Keywords)
	emailDisabled := config.Email.Disabled
	tgConfigured := config.Telegram.Token != "" && len(config.Telegram.ChatIDs) > 0
	configMu.RUnlock()

	emailStatus := "✅ 开启"
	if emailDisabled {
		emailStatus = "🔕 关闭"
	}
	tgStatus := "✅ 已启用"
	if !tgConfigured {
		tgStatus = "⚪ 未配置"
	}

	text := fmt.Sprintf(
		"📊 <b>运行状态</b>\n\n"+
			"🏷 <b>版本:</b> %s\n"+
			"📡 <b>RSS 源:</b>\n%s\n\n"+
			"⏱ <b>执行频率:</b> %s\n"+
			"🔍 <b>关键词数量:</b> %d\n"+
			"📧 <b>邮件通知:</b> %s\n"+
			"💬 <b>Telegram:</b> %s\n"+
			"💾 <b>缓存条目:</b> %d\n"+
			"🕐 <b>当前时间:</b> %s",
		escapeHTML(version),
		formatFeedsForDisplayTG(getFeeds()),
		escapeHTML(getScheduleDesc()),
		kwCount,
		emailStatus,
		tgStatus,
		cacheCount(),
		time.Now().Format("2006-01-02 15:04:05"),
	)

	kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{cbButton("🔄 刷新", "m:status"), backButton()},
	}}
	return text, kb
}

func renderKeywordsPage(page int) (string, *inlineKeyboardMarkup) {
	configMu.RLock()
	kws := make([]Keyword, len(config.Keywords))
	copy(kws, config.Keywords)
	configMu.RUnlock()

	total := pageCount(len(kws))
	page = clampPage(page, total)

	var b strings.Builder
	fmt.Fprintf(&b, "🔍 <b>关键词</b>（第 %d/%d 页，共 %d 个）\n\n", page+1, total, len(kws))
	if len(kws) == 0 {
		b.WriteString("当前没有设置任何关键词。\n点击下方「➕ 添加」创建。")
	} else {
		start := page * menuPageSize
		end := start + menuPageSize
		if end > len(kws) {
			end = len(kws)
		}
		for i := start; i < end; i++ {
			kw := kws[i]
			fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, escapeHTML(sourceLabel(kw.Source)), escapeHTML(truncateText(kw.Text, maxMenuDisplayKeyword)))
		}
	}

	rows := [][]inlineKeyboardButton{
		{cbButton("➕ 添加", "m:kwadd"), cbButton("🗑 删除", "m:kwdel")},
	}
	if total > 1 {
		var nav []inlineKeyboardButton
		if page > 0 {
			nav = append(nav, cbButton("⬅️ 上一页", fmt.Sprintf("m:kw:%d", page-1)))
		}
		if page < total-1 {
			nav = append(nav, cbButton("下一页 ➡️", fmt.Sprintf("m:kw:%d", page+1)))
		}
		if len(nav) > 0 {
			rows = append(rows, nav)
		}
	}
	rows = append(rows, []inlineKeyboardButton{backButton()})
	return b.String(), &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func renderDeletePage(page int) (string, *inlineKeyboardMarkup) {
	configMu.RLock()
	kws := make([]Keyword, len(config.Keywords))
	copy(kws, config.Keywords)
	configMu.RUnlock()

	total := pageCount(len(kws))
	page = clampPage(page, total)

	var b strings.Builder
	fmt.Fprintf(&b, "🗑 <b>删除关键词</b>（第 %d/%d 页）\n\n", page+1, total)
	if len(kws) == 0 {
		b.WriteString("没有可删除的关键词。")
	} else {
		b.WriteString("点击下方按钮删除对应关键词：")
	}

	rows := [][]inlineKeyboardButton{}
	start := page * menuPageSize
	end := start + menuPageSize
	if end > len(kws) {
		end = len(kws)
	}
	for i := start; i < end; i++ {
		kw := kws[i]
		label := fmt.Sprintf("❌ [%s] %s", sourceLabel(kw.Source), kw.Text)
		rows = append(rows, []inlineKeyboardButton{
			cbButton(buttonLabel(label, 48), "m:del:"+keywordHash(kw)),
		})
	}
	if total > 1 {
		var nav []inlineKeyboardButton
		if page > 0 {
			nav = append(nav, cbButton("⬅️ 上一页", fmt.Sprintf("m:delp:%d", page-1)))
		}
		if page < total-1 {
			nav = append(nav, cbButton("下一页 ➡️", fmt.Sprintf("m:delp:%d", page+1)))
		}
		if len(nav) > 0 {
			rows = append(rows, nav)
		}
	}
	rows = append(rows, []inlineKeyboardButton{cbButton("⬅️ 返回关键词", "m:kw:0")})
	return b.String(), &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func renderAddSourcePage() (string, *inlineKeyboardMarkup) {
	feeds := getFeeds()
	rows := [][]inlineKeyboardButton{
		{cbButton("🌐 所有源", "m:addsrc:0")},
	}
	for i, f := range feeds {
		rows = append(rows, []inlineKeyboardButton{
			cbButton("📡 "+buttonLabel(f.Name, 40), fmt.Sprintf("m:addsrc:%d", i+1)),
		})
	}
	rows = append(rows, []inlineKeyboardButton{cbButton("⬅️ 返回关键词", "m:kw:0")})

	text := "➕ <b>添加关键词</b>\n\n请选择要监听的源：\n\n" +
		"选择后直接发送关键词文本即可。"
	return text, &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func renderAddPrompt(sourceName string) (string, *inlineKeyboardMarkup) {
	text := fmt.Sprintf(
		"➕ <b>添加关键词</b>\n\n"+
			"来源: <b>%s</b>\n\n"+
			"请直接发送关键词文本（可包含空格）。\n"+
			"发送 /cancel 或点击下方按钮取消。",
		escapeHTML(sourceName),
	)
	kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{cbButton("❌ 取消", "m:cancel")},
	}}
	return text, kb
}

func renderEmailPage(note string) (string, *inlineKeyboardMarkup) {
	configMu.RLock()
	disabled := config.Email.Disabled
	host := config.SMTP.Host
	port := config.SMTP.Port
	to := append([]string(nil), config.Email.To...)
	configMu.RUnlock()

	status := "✅ 开启"
	toggleText := "🔕 关闭邮件"
	toggleData := "m:email:off"
	if disabled {
		status = "🔕 关闭"
		toggleText = "✅ 开启邮件"
		toggleData = "m:email:on"
	}
	smtpLabel := "未配置"
	if host != "" {
		smtpLabel = fmt.Sprintf("%s:%d", host, port)
	}
	toLabel := "未配置"
	if len(to) > 0 {
		escaped := make([]string, len(to))
		for i, addr := range to {
			escaped[i] = escapeHTML(addr)
		}
		toLabel = strings.Join(escaped, ", ")
	}

	text := ""
	if note != "" {
		text = note + "\n\n"
	}
	text += fmt.Sprintf(
		"📧 <b>邮件通知</b>\n\n"+
			"当前状态: %s\n"+
			"SMTP: %s\n"+
			"收件人: %s",
		status, escapeHTML(smtpLabel), toLabel,
	)
	kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{cbButton(toggleText, toggleData)},
		{backButton()},
	}}
	return text, kb
}

func renderHelpPage() (string, *inlineKeyboardMarkup) {
	text := helpText() + "\n\n" +
		"🔒 群聊中，添加/删除关键词、邮件开关和安装更新仅限私聊或 <code>telegram.admin_user_ids</code> 中的用户。"
	return text, backKeyboard()
}

func renderVersionPage() (string, *inlineKeyboardMarkup) {
	text := versionText()
	if isDevVersion(version) {
		text += "\n\n⚠️ 当前为开发构建，版本比较仅供参考。"
	}
	kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{cbButton("🔄 检查更新", "m:chk"), backButton()},
	}}
	return text, kb
}

func renderUpdateCheckPage(current string, rel *ReleaseInfo, checkErr error, nonce string) (string, *inlineKeyboardMarkup) {
	if checkErr != nil {
		text := "❌ <b>检查更新失败</b>\n\n" + escapeHTML(truncateText(checkErr.Error(), 800))
		kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
			{cbButton("🔄 重试", "m:chk"), backButton()},
		}}
		return text, kb
	}
	if rel == nil {
		text := "❌ <b>检查更新失败</b>\n\n未获取到 Release 信息。"
		kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
			{cbButton("🔄 重试", "m:chk"), backButton()},
		}}
		return text, kb
	}

	available, dev, err := updateAvailability(current, rel.TagName)
	if err != nil {
		text := "❌ <b>版本比较失败</b>\n\n" + escapeHTML(err.Error())
		kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
			{cbButton("🔄 重试", "m:chk"), backButton()},
		}}
		return text, kb
	}

	var b strings.Builder
	if available {
		b.WriteString("🆕 <b>发现新版本</b>\n\n")
	} else {
		b.WriteString("✅ <b>已是最新版本</b>\n\n")
	}
	fmt.Fprintf(&b, "当前: <code>%s</code>\n", escapeHTML(current))
	fmt.Fprintf(&b, "最新: <code>%s</code>\n", escapeHTML(rel.TagName))
	if rel.Name != "" && rel.Name != rel.TagName {
		fmt.Fprintf(&b, "发布: %s\n", escapeHTML(rel.Name))
	}
	if rel.HTMLURL != "" {
		fmt.Fprintf(&b, "链接: %s\n", escapeHTML(rel.HTMLURL))
	}
	if dev {
		b.WriteString("\n⚠️ 当前为开发构建，无法按 SemVer 比较；安装前请确认。")
	}

	rows := [][]inlineKeyboardButton{}
	if available && nonce != "" {
		rows = append(rows, []inlineKeyboardButton{
			cbButton("⬇️ 确认安装 "+buttonLabel(rel.TagName, 24), "m:upd:"+nonce),
		})
	}
	rows = append(rows, []inlineKeyboardButton{
		cbButton("🔄 重新检查", "m:chk"), backButton(),
	})
	return b.String(), &inlineKeyboardMarkup{InlineKeyboard: rows}
}

// updateAvailability reports whether latest is newer than current. Dev or
// unparsable current builds are treated as installable, but never downgraded
// when the current version is a valid SemVer.
func updateAvailability(current, latest string) (available bool, dev bool, err error) {
	if _, err := parseSemver(latest); err != nil {
		return false, false, fmt.Errorf("最新版本无效: %w", err)
	}
	if isDevVersion(current) {
		return true, true, nil
	}
	cmp, err := compareVersions(current, latest)
	if err != nil {
		return false, false, err
	}
	return cmp < 0, false, nil
}

// ---------------------------------------------------------------------------
// Callback handling
// ---------------------------------------------------------------------------

func isSensitiveMenuAction(action string, args []string) bool {
	switch action {
	case "addsrc", "del", "upd":
		return true
	case "email":
		if len(args) > 0 {
			switch args[0] {
			case "on", "off":
				return true
			}
		}
	}
	return false
}

func handleTelegramCallback(cb *tgCallbackQuery) {
	if cb == nil {
		return
	}
	if cb.Message == nil {
		_ = answerCallbackQuery(cb.ID, "菜单已失效，请重新打开 /menu", true)
		return
	}
	chatID := cb.Message.Chat.ID
	if !isAllowedChat(chatID) {
		_ = answerCallbackQuery(cb.ID, "无权限", true)
		return
	}

	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 || parts[0] != "m" {
		_ = answerCallbackQuery(cb.ID, "未知操作", true)
		return
	}
	action := parts[1]
	var args []string
	if len(parts) > 2 {
		args = parts[2:]
	}
	if isSensitiveMenuAction(action, args) && !canManageBot(chatID, cb.Message.Chat.Type, cb.From.ID) {
		_ = answerCallbackQuery(cb.ID, "仅管理员可在私聊中执行此操作", true)
		return
	}

	// Any navigation away from the keyword input prompt cancels that input.
	// "addsrc" replaces it with a new prompt.
	if action != "addsrc" {
		clearPendingInput(chatID)
	}

	// Every callback must be answered, even when the follow-up work is async.
	_ = answerCallbackQuery(cb.ID, "", false)

	switch action {
	case "home":
		text, kb := renderHomePage()
		editMenu(cb, text, kb)
	case "status":
		text, kb := renderStatusPage()
		editMenu(cb, text, kb)
	case "kw":
		page := parsePageArg(parts, 2)
		text, kb := renderKeywordsPage(page)
		editMenu(cb, text, kb)
	case "kwadd":
		text, kb := renderAddSourcePage()
		editMenu(cb, text, kb)
	case "addsrc":
		startAddKeyword(cb, parsePageArg(parts, 2))
	case "kwdel":
		text, kb := renderDeletePage(0)
		editMenu(cb, text, kb)
	case "delp":
		text, kb := renderDeletePage(parsePageArg(parts, 2))
		editMenu(cb, text, kb)
	case "del":
		hash := ""
		if len(parts) >= 3 {
			hash = parts[2]
		}
		handleDeleteKeyword(cb, hash)
	case "email":
		handleEmailMenu(cb, args)
	case "help":
		text, kb := renderHelpPage()
		editMenu(cb, text, kb)
	case "ver":
		text, kb := renderVersionPage()
		editMenu(cb, text, kb)
	case "chk":
		startUpdateCheck(cb)
	case "upd":
		nonce := ""
		if len(parts) >= 3 {
			nonce = parts[2]
		}
		startUpdateInstall(cb, nonce)
	case "cancel":
		clearPendingInput(chatID)
		text, kb := renderHomePage()
		editMenu(cb, text, kb)
	default:
		editMenu(cb, "❓ 未知操作。", backKeyboard())
	}
}

func editMenu(cb *tgCallbackQuery, text string, kb *inlineKeyboardMarkup) {
	editMenuByID(cb.Message.Chat.ID, cb.Message.MessageID, text, kb)
}

// editMenuByID edits the original menu bubble. If the bubble was deleted, it
// recovers by sending a new menu message (the only case where a new bubble is
// acceptable). "message is not modified" is treated as success.
func editMenuByID(chatID int64, messageID int, text string, kb *inlineKeyboardMarkup) {
	if messageID == 0 {
		if _, err := sendTelegramMenuMessage(chatID, text, kb); err != nil {
			log.Printf("发送 Telegram 菜单失败: %v", err)
		}
		return
	}
	err := editTelegramMessage(chatID, messageID, text, kb)
	if err == nil || isMessageNotModified(err) {
		return
	}
	if isMessageNotFound(err) {
		newID, sendErr := sendTelegramMenuMessage(chatID, text, kb)
		if sendErr != nil {
			log.Printf("菜单消息已失效，重新发送失败: %v", sendErr)
		} else {
			// Keep an in-progress keyword input attached to the new bubble.
			rebindPendingInput(chatID, messageID, newID)
		}
		return
	}
	log.Printf("编辑 Telegram 菜单失败: %v", err)
}

func startAddKeyword(cb *tgCallbackQuery, idx int) {
	feeds := getFeeds()
	source := ""
	sourceName := "所有源"
	if idx > 0 {
		if idx > len(feeds) {
			clearPendingInput(cb.Message.Chat.ID)
			editMenu(cb, "❌ 源已失效，请重新选择。", backKeyboard())
			return
		}
		source = feeds[idx-1].ID
		sourceName = feeds[idx-1].Name
	}
	setPendingInput(cb.Message.Chat.ID, &pendingInput{
		userID:    cb.From.ID,
		source:    source,
		messageID: cb.Message.MessageID,
		expires:   time.Now().Add(pendingInputTTL),
	})
	text, kb := renderAddPrompt(sourceName)
	editMenu(cb, text, kb)
}

func handleDeleteKeyword(cb *tgCallbackQuery, hash string) {
	kw, removed, err := deleteKeywordByHash(hash)
	switch {
	case err != nil:
		text, kb := renderDeletePage(0)
		editMenu(cb, "❌ 删除失败: "+escapeHTML(err.Error())+"\n\n"+text, kb)
	case !removed:
		text, kb := renderDeletePage(0)
		editMenu(cb, "⚠️ 关键词不存在或已删除。\n\n"+text, kb)
	default:
		log.Printf("Telegram 菜单: 删除关键词 [%s] %s", kw.Source, kw.Text)
		text, kb := renderDeletePage(0)
		editMenu(cb, "✅ 已删除关键词: ["+escapeHTML(sourceLabel(kw.Source))+"] <b>"+escapeHTML(kw.Text)+"</b>\n\n"+text, kb)
	}
}

func deleteKeywordByHash(hash string) (Keyword, bool, error) {
	if hash == "" {
		return Keyword{}, false, nil
	}
	configMu.Lock()
	idx := -1
	var found Keyword
	for i, kw := range config.Keywords {
		if keywordHash(kw) == hash {
			idx = i
			found = kw
			break
		}
	}
	if idx < 0 {
		configMu.Unlock()
		return Keyword{}, false, nil
	}
	newKws := make(KeywordList, 0, len(config.Keywords)-1)
	newKws = append(newKws, config.Keywords[:idx]...)
	newKws = append(newKws, config.Keywords[idx+1:]...)
	config.Keywords = newKws
	configMu.Unlock()
	if err := saveConfig(); err != nil {
		return found, true, err
	}
	return found, true, nil
}

// handleEmailMenu opens the email page without changing state. Only the
// explicit :on/:off actions mutate config, and those are idempotent.
func handleEmailMenu(cb *tgCallbackQuery, args []string) {
	if len(args) == 0 {
		text, kb := renderEmailPage("")
		editMenu(cb, text, kb)
		return
	}
	switch args[0] {
	case "on":
		handleEmailSet(cb, true)
	case "off":
		handleEmailSet(cb, false)
	default:
		editMenu(cb, "❓ 未知的邮件操作。", backKeyboard())
	}
}

func handleEmailSet(cb *tgCallbackQuery, enabled bool) {
	configMu.Lock()
	config.Email.Disabled = !enabled
	disabled := config.Email.Disabled
	configMu.Unlock()

	note := "✅ 邮件通知已开启。"
	if disabled {
		note = "🔕 邮件通知已关闭。"
	}
	if err := saveConfig(); err != nil {
		note = "⚠️ 状态已设置，但保存配置失败: " + escapeHTML(err.Error())
	} else {
		log.Printf("Telegram 菜单: 邮件通知 disabled=%v", disabled)
	}
	text, kb := renderEmailPage(note)
	editMenu(cb, text, kb)
}

// ---------------------------------------------------------------------------
// Pending keyword input
// ---------------------------------------------------------------------------

type pendingInput struct {
	userID    int64
	source    string
	messageID int
	expires   time.Time
}

var pendingInputs = struct {
	sync.Mutex
	m map[int64]*pendingInput
}{m: make(map[int64]*pendingInput)}

func setPendingInput(chatID int64, p *pendingInput) {
	pendingInputs.Lock()
	pendingInputs.m[chatID] = p
	pendingInputs.Unlock()
}

// peekPendingInput returns a copy so callers cannot mutate shared state
// outside the lock (rebindPendingInput may run concurrently).
func peekPendingInput(chatID int64) *pendingInput {
	pendingInputs.Lock()
	defer pendingInputs.Unlock()
	p := pendingInputs.m[chatID]
	if p == nil {
		return nil
	}
	if time.Now().After(p.expires) {
		delete(pendingInputs.m, chatID)
		return nil
	}
	cp := *p
	return &cp
}

func clearPendingInput(chatID int64) {
	pendingInputs.Lock()
	delete(pendingInputs.m, chatID)
	pendingInputs.Unlock()
}

// rebindPendingInput keeps the pending input attached to the menu bubble when
// editMessageText reports that the original message no longer exists.
func rebindPendingInput(chatID int64, oldMessageID, newMessageID int) {
	pendingInputs.Lock()
	defer pendingInputs.Unlock()
	if p := pendingInputs.m[chatID]; p != nil && p.messageID == oldMessageID {
		p.messageID = newMessageID
	}
}

func resetPendingInputs() {
	pendingInputs.Lock()
	pendingInputs.m = make(map[int64]*pendingInput)
	pendingInputs.Unlock()
}

// handlePendingInput consumes a plain text message as the keyword for an
// in-progress "add keyword" flow. It edits the menu bubble, never sends a new
// message, and leaves the user's own message untouched.
func handlePendingInput(msg *tgMessage) bool {
	p := peekPendingInput(msg.Chat.ID)
	if p == nil {
		return false
	}
	if msg.From == nil || msg.From.ID != p.userID {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return true
	}
	if strings.HasPrefix(text, "/") {
		clearPendingInput(msg.Chat.ID)
		return false
	}
	clearPendingInput(msg.Chat.ID)

	if !canManageBot(msg.Chat.ID, msg.Chat.Type, msg.From.ID) {
		editMenuByID(msg.Chat.ID, p.messageID, "⛔ 无权限执行此操作。", backKeyboard())
		return true
	}

	added, err := addKeyword(p.source, text)
	srcLabel := sourceLabel(p.source)
	display := truncateText(text, maxMenuDisplayKeyword)
	var result string
	switch {
	case err != nil && added:
		result = fmt.Sprintf("✅ 已添加关键词 [%s] <b>%s</b>，但保存配置失败: %s",
			escapeHTML(srcLabel), escapeHTML(display), escapeHTML(err.Error()))
	case err != nil:
		result = "❌ 添加关键词失败: " + escapeHTML(err.Error())
	case !added:
		result = fmt.Sprintf("⚠️ 关键词 [%s] <b>%s</b> 已存在", escapeHTML(srcLabel), escapeHTML(display))
	default:
		result = fmt.Sprintf("✅ 已添加关键词: [%s] <b>%s</b>", escapeHTML(srcLabel), escapeHTML(display))
		log.Printf("Telegram 菜单: 添加关键词 [%s] %s", srcLabel, text)
	}

	kb := &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{cbButton("🔍 查看关键词", "m:kw:0"), backButton()},
	}}
	editMenuByID(msg.Chat.ID, p.messageID, result, kb)
	return true
}

// ---------------------------------------------------------------------------
// Update flows (run asynchronously so polling is not blocked)
// ---------------------------------------------------------------------------

var updateRunning atomic.Bool

func beginUpdate() bool {
	return updateRunning.CompareAndSwap(false, true)
}

func endUpdate() {
	updateRunning.Store(false)
}

func retryUpdateKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{cbButton("🔄 重新检查", "m:chk"), backButton()},
	}}
}

func startUpdateCheck(cb *tgCallbackQuery) {
	chatID := cb.Message.Chat.ID
	messageID := cb.Message.MessageID
	userID := cb.From.ID
	// A fresh check invalidates any older confirmation for this bubble.
	clearPendingUpdateFor(chatID, messageID, userID)

	go func() {
		editMenuByID(chatID, messageID, "🔄 正在检查更新…", emptyKeyboard())
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		rel, err := fetchLatestRelease(ctx)
		if err != nil {
			text, kb := renderUpdateCheckPage(version, nil, err, "")
			editMenuByID(chatID, messageID, text, kb)
			return
		}
		available, _, availErr := updateAvailability(version, rel.TagName)
		if availErr != nil {
			text, kb := renderUpdateCheckPage(version, nil, availErr, "")
			editMenuByID(chatID, messageID, text, kb)
			return
		}

		nonce := ""
		if available {
			p, prepErr := prepareUpdateForChat(ctx, rel, chatID, messageID, userID)
			if prepErr != nil {
				text, kb := renderUpdateCheckPage(version, nil, prepErr, "")
				editMenuByID(chatID, messageID, text, kb)
				return
			}
			nonce = p.Nonce
		}
		text, kb := renderUpdateCheckPage(version, rel, nil, nonce)
		editMenuByID(chatID, messageID, text, kb)
	}()
}

func startUpdateInstall(cb *tgCallbackQuery, nonce string) {
	chatID := cb.Message.Chat.ID
	messageID := cb.Message.MessageID

	if !beginUpdate() {
		editMenu(cb, "⏳ 更新正在进行中，请稍候。", backKeyboard())
		return
	}
	p := takePendingUpdate(nonce, chatID, messageID, cb.From.ID)
	if p == nil {
		endUpdate()
		editMenu(cb, "⚠️ <b>确认已过期或无效</b>\n\n请重新点击「检查更新」后再确认安装。", retryUpdateKeyboard())
		return
	}

	go func() {
		defer endUpdate()
		editMenuByID(chatID, messageID, "⬇️ 正在下载并校验更新…", emptyKeyboard())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		result, err := installPreparedUpdate(ctx, p)
		if err != nil {
			editMenuByID(chatID, messageID, "❌ <b>更新失败</b>\n\n"+escapeHTML(truncateText(err.Error(), 800)), backKeyboard())
			return
		}
		if result.ManualReplace {
			editMenuByID(chatID, messageID, fmt.Sprintf(
				"✅ <b>已下载并校验 %s</b>\n\n"+
					"Windows 无法替换正在运行的程序。新版本已保存到:\n<code>%s</code>\n\n"+
					"请停止程序后手动替换并重启。",
				escapeHTML(result.Version), escapeHTML(result.NewPath)), backKeyboard())
			return
		}

		editMenuByID(chatID, messageID, fmt.Sprintf("✅ <b>已更新到 %s</b>\n\n正在重启程序…", escapeHTML(result.Version)), emptyKeyboard())
		rolledBack, restartErr := restartProcess(result.ExecPath, result.BackupPath)
		if restartErr != nil {
			var msg string
			if rolledBack {
				msg = "⚠️ <b>自动重启失败，已回滚</b>\n\n程序仍运行旧版本。\n\n" + escapeHTML(restartErr.Error())
			} else {
				msg = "⚠️ <b>自动重启失败</b>\n\n" + escapeHTML(restartErr.Error())
			}
			editMenuByID(chatID, messageID, msg, backKeyboard())
		}
	}()
}
