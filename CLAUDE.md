# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Install dependencies
go mod download

# Build for current platform
go build -o rss-monitor

# Cross-compile (release asset names must match the updater)
GOOS=linux  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o nodeseek-rss-monitor-linux-amd64
GOOS=linux  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o nodeseek-rss-monitor-linux-arm64
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o nodeseek-rss-monitor-darwin-amd64
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o nodeseek-rss-monitor-darwin-arm64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o nodeseek-rss-monitor-windows-amd64.exe
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o nodeseek-rss-monitor-windows-arm64.exe

# Run
./rss-monitor

# Version (works before config.json is read)
./rss-monitor --version

# Tests
go test ./...
go test -race ./...
go vet ./...
```

## Architecture

This is a **multi-file Go application** — a NodeSeek RSS monitor that periodically fetches RSS feeds, matches posts against keywords, and sends HTML email / Telegram notifications.

| File | Responsibility |
|------|----------------|
| `main.go` | Entry point, `--version`, startup, interval/cron scheduling |
| `config.go` | Config structs, load/atomic save, feeds, whitelist/admin helpers |
| `rss.go` | RSS parsing, keyword matching, `sent_cache.json` dedup |
| `notify.go` | SMTP email templates, Telegram notification formatting |
| `telegram_bot.go` | Long polling, message/command handling, Bot API helpers |
| `telegram_menu.go` | Inline-keyboard menu, callbacks, pending keyword input, update UI |
| `update.go` | GitHub Release check, SemVer compare, SHA256 verify, install flow |
| `update_platform_unix.go` | Atomic replace, backup/rollback, `exec` restart |
| `update_platform_windows.go` | `.new` file + manual-replace limitation |
| `version.go` | ldflags version vars, SemVer parsing/comparison |

### Core Flow

1. `main()` prints `--version` and exits if requested, then loads `config.json` + `sent_cache.json` from the executable's directory.
2. Sends startup test/status messages via each enabled channel.
3. Starts Telegram long polling (`getUpdates`, messages + callback queries).
4. Runs `checkRSS()` immediately, then schedules via `rss.interval_seconds` or `rss.cron`.

### Telegram Menu

- `/start` and `/menu` open an inline keyboard. All navigation/refresh/results use `editMessageText` on the original `message_id`; buttons always call `answerCallbackQuery`.
- `message is not modified` is treated as success. If the original message was deleted, one recovery message is sent.
- Keyword add flow: choose source → user sends plain text → bot edits the same menu bubble. Pending input has a TTL, is bound to the initiating user, and is cleared/rebound on navigation or menu recovery.
- Legacy commands (`/add`, `/del`, `/email on|off`, `/keywords`, `/status`, `/version`, `/help`) remain supported.
- Authorization: `telegram.chat_ids` is the chat whitelist. State-changing actions require the private-chat owner or a user in `telegram.admin_user_ids`; read-only commands/menu pages only need the chat whitelist.

### Update Flow

- Fixed repo: `Flyinsky2004/nodeseek-go`; only stable releases from `releases/latest`.
- Version/commit/build time are injected with `-ldflags "-X main.version=... -X main.commit=... -X main.buildTime=..."`.
- Asset names: `nodeseek-rss-monitor-<os>-<arch>[.exe]`; `checksums.txt` is required and SHA256 is verified.
- Checking an update stores a short nonce bound to chat/message/user (10 min TTL). Installing re-fetches the exact tag and re-verifies tag, asset name and checksum before download.
- Downloads go to a temp file in the executable directory, then atomically replace the binary. Unix keeps a safe backup and rolls back if `exec` fails; Windows saves `<exe>.new` and requires manual replacement.
- No downgrade: a valid current SemVer >= latest means "already up to date". Dev builds may install the latest stable release with an explicit warning.

### Release Workflow

`.github/workflows/release.yml` triggers on `release: [published]` (and manual `workflow_dispatch` with a tag). It validates the tag is formal SemVer (`v1.2.3`), runs tests/vet, builds six `CGO_ENABLED=0` platforms with ldflags, generates `checksums.txt` only after all builds succeed, and uploads to the existing release with `gh release upload --clobber` (never creates a release).

### Key Structs

| Struct | Purpose |
|--------|---------|
| `Config` | Top-level JSON config (RSS, keywords, SMTP, email, Telegram) |
| `RSSFeed / Channel / Item` | XML parsing of RSS feeds |
| `SentCache` | Map of cache key → timestamp; entries expire after 30 days |
| `ReleaseInfo / ReleaseAsset` | GitHub Release API subset |
| `pendingUpdate` | Nonce-bound confirmation of exact tag/asset/checksum |
| `pendingInput` | In-progress keyword text input bound to chat/user/message |

### Deduplication

Both matching and non-matching items are stored in `sent_cache.json` by `feedID:guid` to prevent reprocessing. The cache is persisted after every `checkRSS()` call; legacy unprefixed GUID keys are migrated on read.

### Configuration

Copy `config.example.json` → `config.json`. Config fields:
- `rss.url` / `rss.feeds` — single URL (legacy) or multiple feeds with `id`/`name`/`url`
- `rss.interval_seconds` — polling interval (use this OR `cron`)
- `rss.cron` — 5-field cron expression
- `keywords` — string array or `{text, source}` objects (`source` empty = all feeds)
- `smtp` — host, port, user, pass, `secure`
- `email` — from, to, subject (`{title}`/`{source}`), `disabled`
- `telegram.token`, `telegram.chat_ids`, `telegram.admin_user_ids`
- `cache_file` — defaults to `sent_cache.json`

### Dependencies

- `github.com/robfig/cron/v3` — cron scheduling
- `gopkg.in/gomail.v2` — SMTP email sending
