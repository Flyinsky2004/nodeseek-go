package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

type tgCall struct {
	Method string
	Body   map[string]any
}

type tgFake struct {
	mu            sync.Mutex
	calls         []tgCall
	editErr       string
	sendMessageID int
}

func (f *tgFake) record(method string, body map[string]any) {
	f.mu.Lock()
	f.calls = append(f.calls, tgCall{Method: method, Body: body})
	f.mu.Unlock()
}

func (f *tgFake) setEditErr(s string) {
	f.mu.Lock()
	f.editErr = s
	f.mu.Unlock()
}

func (f *tgFake) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func (f *tgFake) last(method string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == method {
			return f.calls[i].Body, true
		}
	}
	return nil, false
}

func newTGFakeServer(t *testing.T) (*tgFake, *httptest.Server) {
	t.Helper()
	fake := &tgFake{sendMessageID: 777}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := path.Base(r.URL.Path)
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		fake.record(method, body)

		fake.mu.Lock()
		editErr := fake.editErr
		msgID := fake.sendMessageID
		fake.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "sendMessage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": msgID,
					"chat":       map[string]any{"id": body["chat_id"]},
					"text":       body["text"],
				},
			})
		case "editMessageText":
			if editErr != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":          false,
					"error_code":  400,
					"description": editErr,
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
			}
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
		}
	}))

	oldBase := telegramAPIBase
	telegramAPIBase = srv.URL
	t.Cleanup(func() {
		srv.Close()
		telegramAPIBase = oldBase
	})
	return fake, srv
}

func withTestConfig(t *testing.T, cfg Config) {
	t.Helper()
	oldCfg := config
	oldDir := execDir

	configMu.Lock()
	config = cfg
	configMu.Unlock()
	execDir = t.TempDir()

	t.Cleanup(func() {
		configMu.Lock()
		config = oldCfg
		configMu.Unlock()
		execDir = oldDir
		resetPendingInputs()
		updateRunning.Store(false)
	})
}

func menuTestConfig(chatID int64) Config {
	var c Config
	c.Telegram.Token = "TEST-TOKEN"
	c.Telegram.ChatIDs = []int64{chatID}
	c.RSS.IntervalSeconds = 30
	c.RSS.Feeds = []RSSFeed{
		{ID: "ns", Name: "NodeSeek", URL: "https://example.com/rss"},
		{ID: "ld", Name: "Linux.DO", URL: "https://example.com/ld"},
	}
	c.Keywords = KeywordList{
		{Text: "德鸡"},
		{Text: "Claude", Source: "ns"},
	}
	c.SMTP.Host = "smtp.example.com"
	c.SMTP.Port = 587
	c.Email.From = "a@example.com"
	c.Email.To = []string{"a@example.com"}
	return c
}

func testCallback(chatID int64, chatType string, userID int64, messageID int, data string) *tgCallbackQuery {
	return &tgCallbackQuery{
		ID:   "cb-1",
		From: tgUser{ID: userID},
		Message: &tgMessage{
			MessageID: messageID,
			Chat:      tgChat{ID: chatID, Type: chatType},
		},
		Data: data,
	}
}

func TestMenuCallbackEditsOriginalMessage(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))

	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:status"))

	if got := fake.count("sendMessage"); got != 0 {
		t.Fatalf("expected no sendMessage, got %d", got)
	}
	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("expected one editMessageText, got %d", got)
	}
	body, ok := fake.last("editMessageText")
	if !ok {
		t.Fatal("missing editMessageText call")
	}
	if int64(body["message_id"].(float64)) != 42 {
		t.Fatalf("edited wrong message: %v", body["message_id"])
	}
	if int64(body["chat_id"].(float64)) != chatID {
		t.Fatalf("edited wrong chat: %v", body["chat_id"])
	}
	if fake.count("answerCallbackQuery") != 1 {
		t.Fatalf("expected one answerCallbackQuery, got %d", fake.count("answerCallbackQuery"))
	}
}

func TestMenuDuplicateClickNotModifiedIsSuccess(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))
	fake.setEditErr("Bad Request: message is not modified")

	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:status"))

	if got := fake.count("sendMessage"); got != 0 {
		t.Fatalf("message is not modified must not create a new bubble, got %d sendMessage", got)
	}
	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("expected one edit attempt, got %d", got)
	}
}

func TestMenuExpiredMessageRecovers(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))
	fake.setEditErr("Bad Request: message to edit not found")

	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:status"))

	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("expected one edit attempt, got %d", got)
	}
	if got := fake.count("sendMessage"); got != 1 {
		t.Fatalf("expected one recovery sendMessage, got %d", got)
	}
}

func TestMenuUnauthorizedChat(t *testing.T) {
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(123))

	handleTelegramCallback(testCallback(999, "private", 999, 42, "m:status"))

	if got := fake.count("editMessageText"); got != 0 {
		t.Fatalf("unauthorized chat must not edit, got %d", got)
	}
	if got := fake.count("sendMessage"); got != 0 {
		t.Fatalf("unauthorized chat must not send, got %d", got)
	}
	body, ok := fake.last("answerCallbackQuery")
	if !ok {
		t.Fatal("callback must be answered")
	}
	if body["show_alert"] != true {
		t.Fatalf("expected alert for unauthorized chat, got %v", body)
	}
}

func TestMenuGroupSensitiveDeniedUnlessAdmin(t *testing.T) {
	const chatID = int64(-100123)
	cfg := menuTestConfig(chatID)
	cfg.Telegram.AdminUserIDs = []int64{555}
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, cfg)

	handleTelegramCallback(testCallback(chatID, "group", 777, 42, "m:email"))
	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("viewing the email page should be allowed, got %d edits", got)
	}
	configMu.RLock()
	disabled := config.Email.Disabled
	configMu.RUnlock()
	if disabled {
		t.Fatal("opening the email page must not change state")
	}

	handleTelegramCallback(testCallback(chatID, "group", 777, 42, "m:email:off"))
	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("non-admin group member must not change email, got %d edits", got)
	}
	configMu.RLock()
	disabled = config.Email.Disabled
	configMu.RUnlock()
	if disabled {
		t.Fatal("non-admin group member must not toggle email")
	}

	handleTelegramCallback(testCallback(chatID, "group", 555, 42, "m:email:off"))
	if got := fake.count("editMessageText"); got != 2 {
		t.Fatalf("admin should be allowed to edit, got %d", got)
	}
	configMu.RLock()
	disabled = config.Email.Disabled
	configMu.RUnlock()
	if !disabled {
		t.Fatal("admin toggle should have disabled email")
	}
}

func TestMenuPrivateSensitiveAllowed(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))

	// Opening the page is read-only.
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:email"))
	configMu.RLock()
	disabled := config.Email.Disabled
	configMu.RUnlock()
	if disabled {
		t.Fatal("opening the email page must not change state")
	}

	// :on is idempotent when already enabled.
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:email:on"))
	configMu.RLock()
	disabled = config.Email.Disabled
	configMu.RUnlock()
	if disabled {
		t.Fatal("email should still be enabled")
	}

	// :off disables, and repeating it stays disabled.
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:email:off"))
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:email:off"))
	configMu.RLock()
	disabled = config.Email.Disabled
	configMu.RUnlock()
	if !disabled {
		t.Fatal("email should be disabled after :off")
	}
	if got := fake.count("editMessageText"); got != 4 {
		t.Fatalf("expected four in-place edits, got %d", got)
	}
}

func TestMenuAddKeywordInputFlow(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))

	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:addsrc:0"))
	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("expected prompt edit, got %d", got)
	}
	if peekPendingInput(chatID) == nil {
		t.Fatal("expected pending keyword input")
	}

	handleTelegramMessage(&tgMessage{
		MessageID: 9,
		Chat:      tgChat{ID: chatID, Type: "private"},
		From:      &tgUser{ID: chatID},
		Text:      "测试关键词",
	})

	if got := fake.count("sendMessage"); got != 0 {
		t.Fatalf("keyword input must not create a new bubble, got %d sendMessage", got)
	}
	if got := fake.count("deleteMessage"); got != 0 {
		t.Fatalf("user's own message must not be deleted, got %d deleteMessage", got)
	}
	if got := fake.count("editMessageText"); got != 2 {
		t.Fatalf("expected prompt + result edits, got %d", got)
	}
	body, _ := fake.last("editMessageText")
	if int64(body["message_id"].(float64)) != 42 {
		t.Fatalf("result should edit the menu bubble, got message_id %v", body["message_id"])
	}
	if !strings.Contains(body["text"].(string), "测试关键词") {
		t.Fatalf("result text missing keyword: %v", body["text"])
	}

	configMu.RLock()
	found := false
	for _, kw := range config.Keywords {
		if kw.Text == "测试关键词" && kw.Source == "" {
			found = true
		}
	}
	configMu.RUnlock()
	if !found {
		t.Fatal("keyword was not added to config")
	}
}

func TestPendingInputClearedOnNavigation(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))

	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:addsrc:0"))
	if peekPendingInput(chatID) == nil {
		t.Fatal("expected pending input")
	}

	// Navigating to another page must cancel the input flow.
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:status"))
	if peekPendingInput(chatID) != nil {
		t.Fatal("navigation should clear pending input")
	}

	before := fake.count("editMessageText")
	handleTelegramMessage(&tgMessage{
		MessageID: 10,
		Chat:      tgChat{ID: chatID, Type: "private"},
		From:      &tgUser{ID: chatID},
		Text:      "不应被添加",
	})
	if got := fake.count("editMessageText"); got != before {
		t.Fatalf("plain text after navigation must not edit the menu, got %d edits", got)
	}
	configMu.RLock()
	for _, kw := range config.Keywords {
		if kw.Text == "不应被添加" {
			configMu.RUnlock()
			t.Fatal("plain text after navigation must not add a keyword")
		}
	}
	configMu.RUnlock()
}

func TestPendingInputRebindsAfterMenuRecovery(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))

	fake.setEditErr("Bad Request: message to edit not found")
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:addsrc:0"))
	p := peekPendingInput(chatID)
	if p == nil {
		t.Fatal("expected pending input")
	}
	if p.messageID != fake.sendMessageID {
		t.Fatalf("pending input should be rebound to the recovery message, got %d want %d", p.messageID, fake.sendMessageID)
	}

	fake.setEditErr("")
	handleTelegramMessage(&tgMessage{
		MessageID: 11,
		Chat:      tgChat{ID: chatID, Type: "private"},
		From:      &tgUser{ID: chatID},
		Text:      "恢复后的关键词",
	})
	body, _ := fake.last("editMessageText")
	if int64(body["message_id"].(float64)) != int64(fake.sendMessageID) {
		t.Fatalf("result should edit the recovery menu message, got %v", body["message_id"])
	}
	if got := fake.count("sendMessage"); got != 1 {
		t.Fatalf("only the recovery message should be sent, got %d", got)
	}
}

func TestLegacyMutatingCommandsRequireAdmin(t *testing.T) {
	const chatID = int64(-100123)
	cfg := menuTestConfig(chatID)
	cfg.Telegram.AdminUserIDs = []int64{555}
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, cfg)

	groupMsg := func(userID int64, text string) *tgMessage {
		return &tgMessage{
			Chat: tgChat{ID: chatID, Type: "group"},
			From: &tgUser{ID: userID},
			Text: text,
		}
	}

	handleTelegramMessage(groupMsg(777, "/add 绕过"))
	configMu.RLock()
	for _, kw := range config.Keywords {
		if kw.Text == "绕过" {
			configMu.RUnlock()
			t.Fatal("non-admin must not add keywords via legacy command")
		}
	}
	configMu.RUnlock()
	body, _ := fake.last("sendMessage")
	if !strings.Contains(body["text"].(string), "无权限") {
		t.Fatalf("expected permission denial, got %v", body["text"])
	}

	// Read-only commands remain available to whitelisted group members.
	handleTelegramMessage(groupMsg(777, "/status"))
	body, _ = fake.last("sendMessage")
	if !strings.Contains(body["text"].(string), "运行状态") {
		t.Fatalf("read-only command should still work, got %v", body["text"])
	}

	handleTelegramMessage(groupMsg(777, "/email off"))
	configMu.RLock()
	disabled := config.Email.Disabled
	configMu.RUnlock()
	if disabled {
		t.Fatal("non-admin must not toggle email via legacy command")
	}

	handleTelegramMessage(groupMsg(555, "/email off"))
	configMu.RLock()
	disabled = config.Email.Disabled
	configMu.RUnlock()
	if !disabled {
		t.Fatal("admin should be able to toggle email via legacy command")
	}
}

func TestPendingInputConcurrentAccess(t *testing.T) {
	const chatID = int64(1)
	setPendingInput(chatID, &pendingInput{
		userID:    1,
		messageID: 1,
		expires:   time.Now().Add(time.Minute),
	})
	t.Cleanup(func() { clearPendingInput(chatID) })

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = peekPendingInput(chatID)
			}
		}()
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rebindPendingInput(chatID, n, n+1)
			}
		}(i)
	}
	wg.Wait()
}

func TestUpdateConfirmationWithoutNonceRejected(t *testing.T) {
	const chatID = int64(123)
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, menuTestConfig(chatID))

	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:upd:deadbeef"))

	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("expected one edit, got %d", got)
	}
	body, _ := fake.last("editMessageText")
	if !strings.Contains(body["text"].(string), "过期或无效") {
		t.Fatalf("expected expired confirmation message, got %v", body["text"])
	}
	if updateRunning.Load() {
		t.Fatal("update guard must be released after a rejected confirmation")
	}
}

func TestUpdateConfirmationBoundToUser(t *testing.T) {
	const chatID = int64(-100123)
	cfg := menuTestConfig(chatID)
	cfg.Telegram.AdminUserIDs = []int64{555, 666}
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, cfg)

	storePendingUpdate(&pendingUpdate{
		Nonce:     "boundnonce",
		ChatID:    chatID,
		MessageID: 42,
		UserID:    555,
		Tag:       "v1.2.4",
		AssetName: "nodeseek-rss-monitor-linux-amd64",
		Checksum:  strings.Repeat("a", 64),
		Expires:   time.Now().Add(time.Minute),
	})

	// A different admin in the same chat must not consume the confirmation.
	handleTelegramCallback(testCallback(chatID, "group", 666, 42, "m:upd:boundnonce"))
	body, _ := fake.last("editMessageText")
	if !strings.Contains(body["text"].(string), "过期或无效") {
		t.Fatalf("confirmation must be bound to the original user, got %v", body["text"])
	}
	if updateRunning.Load() {
		t.Fatal("update guard must be released")
	}
}
func TestMenuDeleteDuplicateAndUnknown(t *testing.T) {
	const chatID = int64(123)
	cfg := menuTestConfig(chatID)
	hash := keywordHash(cfg.Keywords[0])
	fake, _ := newTGFakeServer(t)
	withTestConfig(t, cfg)

	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:del:"+hash))
	if got := fake.count("editMessageText"); got != 1 {
		t.Fatalf("expected delete edit, got %d", got)
	}
	body, _ := fake.last("editMessageText")
	if !strings.Contains(body["text"].(string), "已删除") {
		t.Fatalf("expected delete confirmation, got %v", body["text"])
	}

	// Duplicate click: the keyword is already gone.
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:del:"+hash))
	body, _ = fake.last("editMessageText")
	if !strings.Contains(body["text"].(string), "不存在或已删除") {
		t.Fatalf("duplicate delete should be reported safely, got %v", body["text"])
	}

	// Unknown hash must not panic.
	handleTelegramCallback(testCallback(chatID, "private", chatID, 42, "m:del:deadbeef00"))
	body, _ = fake.last("editMessageText")
	if !strings.Contains(body["text"].(string), "不存在或已删除") {
		t.Fatalf("unknown delete should be reported safely, got %v", body["text"])
	}
}

func TestMenuEscapesHTML(t *testing.T) {
	const chatID = int64(123)
	cfg := menuTestConfig(chatID)
	cfg.Keywords = KeywordList{{Text: `<b>&x</b>`}}
	withTestConfig(t, cfg)

	text, _ := renderKeywordsPage(0)
	if !strings.Contains(text, "&lt;b&gt;&amp;x&lt;/b&gt;") {
		t.Fatalf("keyword was not HTML-escaped: %s", text)
	}
	if strings.Contains(text, "<b>&x</b>") {
		t.Fatalf("raw HTML leaked into menu text: %s", text)
	}
}

func TestMenuKeywordsPagination(t *testing.T) {
	const chatID = int64(123)
	cfg := menuTestConfig(chatID)
	cfg.Keywords = make(KeywordList, 0, 20)
	for i := 0; i < 20; i++ {
		cfg.Keywords = append(cfg.Keywords, Keyword{Text: "kw" + string(rune('a'+i))})
	}
	withTestConfig(t, cfg)

	text, _ := renderKeywordsPage(0)
	if !strings.Contains(text, "第 1/3 页") {
		t.Fatalf("unexpected page header: %s", text)
	}
	text, _ = renderKeywordsPage(1)
	if !strings.Contains(text, "第 2/3 页") {
		t.Fatalf("unexpected page header: %s", text)
	}
	text, _ = renderKeywordsPage(99)
	if !strings.Contains(text, "第 3/3 页") {
		t.Fatalf("page should clamp to last page: %s", text)
	}
}

func TestMenuCallbackDataLength(t *testing.T) {
	const chatID = int64(123)
	cfg := menuTestConfig(chatID)
	cfg.RSS.Feeds = []RSSFeed{
		{ID: "ns", Name: "NodeSeek", URL: "https://example.com/rss"},
		{ID: "ld", Name: "Linux.DO", URL: "https://example.com/ld"},
		{ID: "very-long-feed-id", Name: "Long", URL: "https://example.com/long"},
	}
	cfg.Keywords = make(KeywordList, 0, 20)
	for i := 0; i < 20; i++ {
		cfg.Keywords = append(cfg.Keywords, Keyword{Text: "keyword-" + string(rune('a'+i))})
	}
	withTestConfig(t, cfg)

	var keyboards []*inlineKeyboardMarkup
	text, kb := renderHomePage()
	_ = text
	keyboards = append(keyboards, kb)
	text, kb = renderStatusPage()
	_ = text
	keyboards = append(keyboards, kb)
	for page := 0; page < 3; page++ {
		_, kb = renderKeywordsPage(page)
		keyboards = append(keyboards, kb)
		_, kb = renderDeletePage(page)
		keyboards = append(keyboards, kb)
	}
	_, kb = renderAddSourcePage()
	keyboards = append(keyboards, kb)
	_, kb = renderAddPrompt("所有源")
	keyboards = append(keyboards, kb)
	_, kb = renderEmailPage("")
	keyboards = append(keyboards, kb)
	_, kb = renderHelpPage()
	keyboards = append(keyboards, kb)
	_, kb = renderVersionPage()
	keyboards = append(keyboards, kb)
	_, kb = renderUpdateCheckPage("v1.2.3", &ReleaseInfo{TagName: "v1.2.4"}, nil, "0123456789abcdef")
	keyboards = append(keyboards, kb)

	for _, keyboard := range keyboards {
		for _, row := range keyboard.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData == "" {
					continue
				}
				if len(btn.CallbackData) > callbackDataMaxLen {
					t.Fatalf("callback_data too long (%d): %q", len(btn.CallbackData), btn.CallbackData)
				}
			}
		}
	}
}

func TestCommandDispatch(t *testing.T) {
	const chatID = int64(123)
	cfg := menuTestConfig(chatID)
	withTestConfig(t, cfg)

	if got := dispatchCommand("/help"); !strings.Contains(got, "/menu") {
		t.Fatalf("help missing menu hint: %s", got)
	}
	if got := dispatchCommand("/add ns 搬瓦工"); !strings.Contains(got, "已添加") {
		t.Fatalf("add failed: %s", got)
	}
	configMu.RLock()
	found := false
	for _, kw := range config.Keywords {
		if kw.Text == "搬瓦工" && kw.Source == "ns" {
			found = true
		}
	}
	configMu.RUnlock()
	if !found {
		t.Fatal("command add did not persist keyword")
	}

	if got := dispatchCommand("/del ns 搬瓦工"); !strings.Contains(got, "已删除") {
		t.Fatalf("delete failed: %s", got)
	}
	if got := dispatchCommand("/email off"); !strings.Contains(got, "已关闭") {
		t.Fatalf("email off failed: %s", got)
	}
	if got := dispatchCommand("/status"); !strings.Contains(got, version) {
		t.Fatalf("status missing version: %s", got)
	}
	if got := dispatchCommand("/version"); !strings.Contains(got, version) {
		t.Fatalf("version command missing version: %s", got)
	}
}

func TestUpdateAvailability(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		latest    string
		available bool
		dev       bool
		wantErr   bool
	}{
		{name: "dev build", current: "dev", latest: "v1.2.4", available: true, dev: true},
		{name: "dev build invalid latest", current: "dev", latest: "not-a-version", wantErr: true},
		{name: "newer", current: "v1.2.3", latest: "v1.2.4", available: true},
		{name: "same", current: "v1.2.4", latest: "v1.2.4", available: false},
		{name: "older", current: "v1.2.5", latest: "v1.2.4", available: false},
		{name: "invalid latest", current: "v1.2.3", latest: "not-a-version", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			available, dev, err := updateAvailability(tt.current, tt.latest)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if available != tt.available || dev != tt.dev {
				t.Fatalf("got available=%v dev=%v, want available=%v dev=%v", available, dev, tt.available, tt.dev)
			}
		})
	}
}
