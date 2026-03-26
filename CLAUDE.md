# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Install dependencies
go mod download

# Build for current platform
go build -o rss-monitor

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o rss-monitor-linux
GOOS=darwin GOARCH=amd64 go build -o rss-monitor-mac
GOOS=windows GOARCH=amd64 go build -o rss-monitor.exe

# Run
./rss-monitor

# Run tests (none currently exist)
go test ./...
```

## Architecture

This is a **single-file Go application** (`main.go`, ~700 lines) — a NodeSeek RSS monitor that periodically fetches an RSS feed, matches posts against keywords, and sends HTML email notifications via SMTP.

### Core Flow

1. `main()` loads `config.json` + `sent_cache.json` from the executable's directory
2. Sends a startup test email to verify SMTP config
3. Immediately runs first `checkRSS()` check
4. Schedules recurring checks via either:
   - **Interval mode**: `rss.interval_seconds` using `time.Ticker`
   - **Cron mode**: `rss.cron` (5-field cron expression) using `robfig/cron/v3`

### Key Structs

| Struct | Purpose |
|--------|---------|
| `Config` | Top-level JSON config (RSS URL, SMTP, keywords, email) |
| `RSSFeed / Channel / Item` | XML parsing of RSS feed |
| `SentCache` | Map of GUID → timestamp; entries expire after 30 days |

### Deduplication Logic

Both matching and non-matching items are stored in `sent_cache.json` by GUID to prevent reprocessing on subsequent runs. Cache is persisted to disk after every `checkRSS()` call.

### Configuration

Copy `config.example.json` → `config.json`. Config fields:
- `rss.url` — RSS feed URL
- `rss.interval_seconds` — polling interval (use this OR `cron`, not both)
- `rss.cron` — cron expression (e.g., `*/30 * * * *`)
- `keywords` — list of Chinese/English keywords to match in title or content
- `smtp` — SMTP host, port, credentials, `secure` (TLS toggle)
- `email.subject` — supports `{title}` placeholder
- `telegram.token` — Telegram Bot token (leave empty to disable)
- `telegram.chat_ids` — list of int64 chat IDs to receive notifications

Both email and Telegram can be enabled simultaneously. On startup a test/status message is sent via each enabled channel. If any channel fails to deliver a notification, the item is not marked as sent and will be retried next cycle.

### Dependencies

- `github.com/robfig/cron/v3` — cron scheduling
- `gopkg.in/gomail.v2` — SMTP email sending
