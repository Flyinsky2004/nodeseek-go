package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetSentCache() {
	cacheMu.Lock()
	sentCache.Items = make(map[string]time.Time)
	cacheMu.Unlock()
}

func TestMatchKeywords(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		keywords    []string
		want        []string
	}{
		{name: "case insensitive", title: "Claude Code", keywords: []string{"claude"}, want: []string{"claude"}},
		{name: "description match", title: "x", description: "搬瓦工 优惠", keywords: []string{"搬瓦工"}, want: []string{"搬瓦工"}},
		{name: "multi word all required", title: "Claude Code 发布", keywords: []string{"claude code"}, want: []string{"claude code"}},
		{name: "multi word partial", title: "Claude", keywords: []string{"claude code"}, want: nil},
		{name: "empty keyword ignored", title: "anything", keywords: []string{""}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchKeywords(tt.title, tt.description, tt.keywords)
			if len(got) != len(tt.want) {
				t.Fatalf("matchKeywords = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("matchKeywords = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestStripHTMLAndTruncate(t *testing.T) {
	if got := stripHTML("<p>hello <b>world</b></p>"); got != "hello world" {
		t.Fatalf("stripHTML = %q", got)
	}
	if got := truncateText("abcdef", 3); got != "abc..." {
		t.Fatalf("truncateText = %q", got)
	}
	if got := truncateText("abc", 3); got != "abc" {
		t.Fatalf("truncateText = %q", got)
	}
	if got := truncateText("你好世界", 2); got != "你好..." {
		t.Fatalf("truncateText rune handling = %q", got)
	}
}

const testRSSXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <item>
      <title>搬瓦工 VPS 优惠</title>
      <description>便宜好用</description>
      <guid>guid-1</guid>
      <link>https://example.com/1</link>
      <pubDate>Mon, 01 Jan 2026 00:00:00 GMT</pubDate>
    </item>
    <item>
      <title>无关帖子</title>
      <description>其他内容</description>
      <guid>guid-2</guid>
      <link>https://example.com/2</link>
    </item>
  </channel>
</rss>`

func TestCheckRSSIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, testRSSXML)
	}))
	defer srv.Close()

	cfg := menuTestConfig(123)
	cfg.RSS.Feeds = []RSSFeed{{ID: "ns", Name: "NodeSeek", URL: srv.URL}}
	cfg.Keywords = KeywordList{{Text: "搬瓦工"}}
	cfg.SMTP.Host = ""
	cfg.Email.To = nil
	cfg.Telegram.Token = ""
	cfg.Telegram.ChatIDs = nil
	withTestConfig(t, cfg)
	resetSentCache()

	oldClient := rssClient
	rssClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { rssClient = oldClient })

	if err := checkRSS(); err != nil {
		t.Fatalf("checkRSS: %v", err)
	}
	if got := cacheCount(); got != 2 {
		t.Fatalf("cache count = %d, want 2", got)
	}

	// A second run must not reprocess cached items.
	if err := checkRSS(); err != nil {
		t.Fatalf("second checkRSS: %v", err)
	}
	if got := cacheCount(); got != 2 {
		t.Fatalf("cache count after second run = %d, want 2", got)
	}
}

func TestCheckRSSHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := menuTestConfig(123)
	cfg.RSS.Feeds = []RSSFeed{{ID: "ns", Name: "NodeSeek", URL: srv.URL}}
	cfg.Telegram.Token = ""
	cfg.Telegram.ChatIDs = nil
	withTestConfig(t, cfg)
	resetSentCache()

	oldClient := rssClient
	rssClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { rssClient = oldClient })

	if err := checkRSS(); err != nil {
		t.Fatalf("checkRSS should tolerate HTTP failures: %v", err)
	}
	if got := cacheCount(); got != 0 {
		t.Fatalf("cache count = %d, want 0", got)
	}
}

func TestFormattingHelpers(t *testing.T) {
	feeds := []RSSFeed{{Name: "A<&>", URL: "https://example.com/?a=1&b=2"}}
	tg := formatFeedsForDisplayTG(feeds)
	if !strings.Contains(tg, "A&lt;&amp;&gt;") || !strings.Contains(tg, "a=1&amp;b=2") {
		t.Fatalf("Telegram feed formatting not escaped: %s", tg)
	}

	kws := []Keyword{{Text: "<b>", Source: "ns"}, {Text: "x"}}
	tgKws := formatKeywordsForDisplayTG(kws)
	if !strings.Contains(tgKws, "&lt;b&gt;") || !strings.Contains(tgKws, "[all] x") {
		t.Fatalf("Telegram keyword formatting not escaped: %s", tgKws)
	}
}
