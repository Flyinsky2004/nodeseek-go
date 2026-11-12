package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGetFeedsLegacyFallback(t *testing.T) {
	var cfg Config
	cfg.RSS.URL = "https://example.com/rss.xml"
	withTestConfig(t, cfg)

	feeds := getFeeds()
	if len(feeds) != 1 || feeds[0].ID != "ns" || feeds[0].URL != cfg.RSS.URL {
		t.Fatalf("unexpected legacy feeds: %#v", feeds)
	}
}

func TestEmailAndTelegramEnabled(t *testing.T) {
	var cfg Config
	withTestConfig(t, cfg)
	if emailEnabled() {
		t.Fatal("email should be disabled without SMTP/recipients")
	}
	if telegramEnabled() {
		t.Fatal("telegram should be disabled without token/chats")
	}

	cfg.SMTP.Host = "smtp.example.com"
	cfg.Email.To = []string{"a@example.com"}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatIDs = []int64{1}
	withTestConfig(t, cfg)
	if !emailEnabled() {
		t.Fatal("email should be enabled")
	}
	if !telegramEnabled() {
		t.Fatal("telegram should be enabled")
	}

	cfg.Email.Disabled = true
	withTestConfig(t, cfg)
	if emailEnabled() {
		t.Fatal("email should respect the disabled switch")
	}
}

func TestCanManageBot(t *testing.T) {
	const chatID = int64(123)
	cfg := menuTestConfig(chatID)
	cfg.Telegram.AdminUserIDs = []int64{555}
	withTestConfig(t, cfg)

	if !canManageBot(chatID, "private", chatID) {
		t.Fatal("private chat owner should manage")
	}
	if canManageBot(chatID, "private", 999) {
		t.Fatal("another user must not manage a private chat")
	}
	if canManageBot(999, "private", 999) {
		t.Fatal("non-whitelisted chat must not manage")
	}

	const groupID = int64(-100123)
	cfg.Telegram.ChatIDs = []int64{groupID}
	withTestConfig(t, cfg)
	if canManageBot(groupID, "group", 777) {
		t.Fatal("non-admin group member must not manage")
	}
	if !canManageBot(groupID, "group", 555) {
		t.Fatal("admin group member should manage")
	}
}

func TestParseKeywordArgs(t *testing.T) {
	feeds := []RSSFeed{{ID: "ns"}, {ID: "ld"}}
	tests := []struct {
		rest       string
		wantSource string
		wantKw     string
	}{
		{rest: "ns 搬瓦工", wantSource: "ns", wantKw: "搬瓦工"},
		{rest: "ld Claude Code", wantSource: "ld", wantKw: "Claude Code"},
		{rest: "all 德鸡", wantSource: "", wantKw: "德鸡"},
		{rest: "搬瓦工", wantSource: "", wantKw: "搬瓦工"},
		{rest: "", wantSource: "", wantKw: ""},
	}
	for _, tt := range tests {
		source, kw := parseKeywordArgs(tt.rest, feeds)
		if source != tt.wantSource || kw != tt.wantKw {
			t.Fatalf("parseKeywordArgs(%q) = (%q, %q), want (%q, %q)", tt.rest, source, kw, tt.wantSource, tt.wantKw)
		}
	}
}

func TestConfigPersistence(t *testing.T) {
	var cfg Config
	cfg.RSS.IntervalSeconds = 30
	cfg.Keywords = KeywordList{{Text: "德鸡"}, {Text: "Claude", Source: "ns"}}
	withTestConfig(t, cfg)

	if err := saveConfig(); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(execDir, "config.json"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(data), "德鸡") {
		t.Fatalf("saved config missing keyword: %s", data)
	}

	configMu.Lock()
	config = Config{}
	configMu.Unlock()
	if err := loadConfig("config.json"); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	configMu.RLock()
	defer configMu.RUnlock()
	if len(config.Keywords) != 2 || config.RSS.IntervalSeconds != 30 {
		t.Fatalf("loaded config mismatch: %#v", config)
	}
}

func TestCachePersistence(t *testing.T) {
	var cfg Config
	cfg.CacheFile = "cache.json"
	withTestConfig(t, cfg)

	cacheMu.Lock()
	sentCache.Items = map[string]time.Time{
		"ns:one": time.Now(),
		"ns:two": time.Now(),
	}
	cacheMu.Unlock()

	if err := saveCache(); err != nil {
		t.Fatalf("saveCache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(execDir, "cache.json")); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}

	cacheMu.Lock()
	sentCache.Items = nil
	cacheMu.Unlock()
	loadCache()
	if got := cacheCount(); got != 2 {
		t.Fatalf("cache count after reload = %d, want 2", got)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := writeFileAtomic(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".*.tmp-*"))
	if len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
}

func TestMenuHelperFunctions(t *testing.T) {
	if got := buttonLabel("abcdef", 4); got != "abc…" {
		t.Fatalf("buttonLabel = %q", got)
	}
	if got := buttonLabel("abc", 4); got != "abc" {
		t.Fatalf("buttonLabel = %q", got)
	}
	if got := pageCount(0); got != 1 {
		t.Fatalf("pageCount(0) = %d", got)
	}
	if got := pageCount(17); got != 3 {
		t.Fatalf("pageCount(17) = %d", got)
	}
	if got := clampPage(9, 3); got != 2 {
		t.Fatalf("clampPage = %d", got)
	}
	if got := clampPage(-1, 3); got != 0 {
		t.Fatalf("clampPage = %d", got)
	}
	if got := parsePageArg([]string{"m", "kw", "2"}, 2); got != 2 {
		t.Fatalf("parsePageArg = %d", got)
	}
	if got := parsePageArg([]string{"m", "kw", "bad"}, 2); got != 0 {
		t.Fatalf("parsePageArg invalid = %d", got)
	}
}

func TestRenderPages(t *testing.T) {
	const chatID = int64(123)
	withTestConfig(t, menuTestConfig(chatID))

	pages := []struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{}
	text, kb := renderHomePage()
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"home", text, kb})
	text, kb = renderStatusPage()
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"status", text, kb})
	text, kb = renderKeywordsPage(0)
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"keywords", text, kb})
	text, kb = renderDeletePage(0)
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"delete", text, kb})
	text, kb = renderAddSourcePage()
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"add-source", text, kb})
	text, kb = renderEmailPage("")
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"email", text, kb})
	text, kb = renderHelpPage()
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"help", text, kb})
	text, kb = renderVersionPage()
	pages = append(pages, struct {
		name string
		text string
		kb   *inlineKeyboardMarkup
	}{"version", text, kb})

	for _, page := range pages {
		if strings.TrimSpace(page.text) == "" {
			t.Fatalf("%s page has empty text", page.name)
		}
		if page.kb == nil || len(page.kb.InlineKeyboard) == 0 {
			t.Fatalf("%s page has no keyboard", page.name)
		}
	}

	// Update check page variants.
	noUpdate, _ := renderUpdateCheckPage("v1.2.4", &ReleaseInfo{TagName: "v1.2.4"}, nil, "")
	if !strings.Contains(noUpdate, "已是最新版本") {
		t.Fatalf("unexpected no-update text: %s", noUpdate)
	}
	devUpdate, kb := renderUpdateCheckPage("dev", &ReleaseInfo{TagName: "v1.2.4"}, nil, "nonce")
	if !strings.Contains(devUpdate, "开发构建") {
		t.Fatalf("unexpected dev-update text: %s", devUpdate)
	}
	if len(kb.InlineKeyboard) == 0 || !strings.Contains(kb.InlineKeyboard[0][0].CallbackData, "m:upd:nonce") {
		t.Fatalf("missing install button: %#v", kb)
	}
	errPage, _ := renderUpdateCheckPage("v1.2.3", nil, os.ErrNotExist, "")
	if !strings.Contains(errPage, "检查更新失败") {
		t.Fatalf("unexpected error page: %s", errPage)
	}
}
