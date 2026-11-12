# NodeSeek RSS 邮件监控

基于 Go 语言的 RSS 监控程序，自动监控 NodeSeek RSS 源，当发现包含特定关键词的新帖时，自动发送邮件通知。

## 功能特性

- 定时拉取 RSS 源内容
- **支持秒级间隔调度**（每 20 秒、30 秒等）
- **支持 Cron 表达式**配置定时任务
- 关键词匹配过滤（标题和描述）
- 自动去重，避免重复发送
- 支持多个收件人
- **启动时发送测试邮件**，验证配置是否正确
- 精美的 HTML 邮件模板（Next.js 黑白风格）
- 本地缓存，记录已发送的帖子
- **Telegram 按钮菜单**：状态、关键词、邮件开关、版本和更新都在同一条消息气泡内编辑，不刷屏
- **自动更新**：从 GitHub Release 检查正式版，校验 SHA256 后原子替换并自动重启（Unix）

## 安装依赖

```bash
go mod download
```

## 编译和运行

### 编译

```bash
# 编译为当前系统的可执行文件
go build -o rss-monitor

# 编译为 Linux 64位
GOOS=linux GOARCH=amd64 go build -o rss-monitor-linux

# 编译为 Windows 64位
GOOS=windows GOARCH=amd64 go build -o rss-monitor.exe

# 编译为 macOS 64位
GOOS=darwin GOARCH=amd64 go build -o rss-monitor-mac
```

### 部署

编译完成后，只需要将以下文件放在同一目录：

```
your-directory/
├── rss-monitor         # 可执行文件
└── config.json         # 配置文件
```

程序运行后会自动在同级目录生成 `sent_cache.json` 缓存文件。

### 运行

直接运行可执行文件即可：

```bash
# Linux/macOS
./rss-monitor

# Windows
rss-monitor.exe
```

**启动流程：**
1. 程序启动后会立即发送一封**测试邮件**，验证 SMTP 配置是否正确
2. 测试邮件包含当前配置信息（RSS 源、关键词、执行频率等）
3. 如果收到测试邮件，说明邮件功能正常
4. 然后执行第一次 RSS 检查
5. 之后按照配置的频率定时执行

## 配置

程序会自动从**可执行文件所在目录**读取 `config.json` 和 `sent_cache.json`。

复制配置文件示例：

```bash
cp config.example.json config.json
```

编辑 `config.json`，配置以下参数：

### RSS 配置

支持两种调度模式：**秒级间隔** 或 **Cron 表达式**，二选一。

#### 模式 1：秒级间隔（推荐用于高频监控）

```json
"rss": {
  "url": "https://www.nodeseek.com/rss.xml",
  "interval_seconds": 30,
  "cron": ""
}
```

- `url`: RSS 源地址
- `interval_seconds`: 执行间隔（秒），例如 30 表示每 30 秒执行一次
- `cron`: 留空即可

**示例：**
- `"interval_seconds": 20` - 每 20 秒执行一次
- `"interval_seconds": 30` - 每 30 秒执行一次
- `"interval_seconds": 60` - 每 60 秒（1 分钟）执行一次

#### 模式 2：Cron 表达式（用于定时任务）

```json
"rss": {
  "url": "https://www.nodeseek.com/rss.xml",
  "interval_seconds": 0,
  "cron": "*/30 * * * *"
}
```

- `url`: RSS 源地址
- `interval_seconds`: 设置为 0
- `cron`: 标准 Cron 表达式（5 个字段）

### 关键词配置

```json
"keywords": [
  "德鸡",
  "绿云",
  "V.PS",
  "搬瓦工"
]
```

当 RSS 项的标题或描述包含任一关键词时，将触发邮件发送。

也支持带来源的对象格式（`source` 为空表示监听所有源）：

```json
"keywords": [
  { "text": "德鸡", "source": "" },
  { "text": "Claude", "source": "ns" },
  { "text": "Codex", "source": "ld" }
]
```

### SMTP 配置

```json
"smtp": {
  "host": "smtp.gmail.com",
  "port": 587,
  "user": "your-email@gmail.com",
  "pass": "your-app-password",
  "secure": false
}
```

- `host`: SMTP 服务器地址
- `port`: SMTP 端口（587 为 TLS，465 为 SSL）
- `user`: SMTP 用户名
- `pass`: SMTP 密码（Gmail 需使用应用专用密码）
- `secure`: 是否使用 SSL/TLS

### 邮件配置

```json
"email": {
  "from": "your-email@gmail.com",
  "to": [
    "recipient1@example.com",
    "recipient2@example.com"
  ],
  "subject": "[NodeSeek RSS] {title}"
}
```

- `from`: 发件人邮箱
- `to`: 收件人邮箱列表（支持多个）
- `subject`: 邮件主题模板（`{title}` 会被替换为帖子标题）

### 缓存配置

```json
"cache_file": "sent_cache.json"
```

- `cache_file`: 缓存文件路径，用于记录已发送的帖子，避免重复发送

### Telegram 配置

```json
"telegram": {
  "token": "123456:ABC...",
  "chat_ids": [123456789],
  "admin_user_ids": []
}
```

- `token`: BotFather 提供的 Bot Token
- `chat_ids`: 允许使用 Bot 的私聊/群聊 ID 白名单
- `admin_user_ids`: 可选。群聊中允许执行变更操作（增删关键词、邮件开关、安装更新）的用户 ID；私聊中默认只有聊天本人可以操作

## Telegram 菜单

在私聊或白名单群聊中发送 `/start` 或 `/menu` 打开菜单：

- 📊 状态、🔍 关键词、📧 邮件开关、ℹ️ 帮助、🏷 版本、🔄 检查更新
- 所有按钮都会调用 `answerCallbackQuery`；页面切换、刷新和执行结果通过 `editMessageText` 更新**原消息**，不会每点一次就产生新气泡
- 关键词支持分页查看、按源添加、按源删除；添加时先选择源，再直接发送关键词文本，机器人会在菜单气泡内提示结果
- 菜单消息被删除时会自动补发一次；重复点击（`message is not modified`）视为成功
- 原有命令仍然可用：`/add`、`/del`、`/email on|off`、`/keywords`、`/status`、`/version`、`/help`
- 群聊中的变更操作需要发送者在 `telegram.admin_user_ids` 中；私聊中只有聊天本人可以变更。查看类命令仍沿用 `chat_ids` 白名单

## 版本与自动更新

版本、提交和构建时间通过 `-ldflags` 注入：

```bash
go build -ldflags "-X main.version=v1.2.3 -X main.commit=$(git rev-parse HEAD) -X main.buildTime=2026-01-01T00:00:00Z" -o rss-monitor
./rss-monitor --version
```

本地直接 `go build` 时版本为 `dev`。`--version` 在读取 `config.json` 之前执行，因此配置文件缺失也能查看版本。

更新流程（Telegram 菜单 → 🏷 版本 → 🔄 检查更新）：

1. 从 `https://api.github.com/repos/Flyinsky2004/nodeseek-go/releases/latest` 读取最新**正式** Release（draft/prerelease 会被拒绝）
2. 按当前 `GOOS/GOARCH` 精确匹配资源名，例如 `nodeseek-rss-monitor-linux-amd64`
3. 下载 `checksums.txt` 并校验目标资源的 SHA256；校验失败不会替换原程序
4. 检查到新版本后只显示「确认安装」按钮；用户再次点击确认后才会安装。确认绑定当前聊天/消息/用户并带 10 分钟有效期，安装前会重新核对 tag、资源名和校验值
5. 下载到可执行文件同目录的临时文件，校验通过后原子替换；Unix 会保留安全备份并在 `exec` 失败时回滚
6. Unix 更新成功后执行 `exec` 重启当前程序；Windows 无法替换正在运行的 `.exe`，只会把新版本保存为 `<程序名>.new` 并提示手动替换，**不会谎报已更新**

不会自动降级：当前版本是合法 SemVer 且不低于最新版时，菜单显示「已是最新版本」。开发构建（`dev`）允许手动安装最新正式版，但会明确提示无法按 SemVer 比较。

手动更新：从 Release 页面下载对应平台的二进制，停止程序后替换可执行文件并重启。`config.json`、`sent_cache.json` 不会被更新流程修改。

## 发布流程（GitHub Actions）

`.github/workflows/release.yml` 在 GitHub Release 发布（`published`）时触发，也支持手动 `workflow_dispatch` 指定已发布的 tag：

> **Release tag 必须是正式 SemVer**，例如 `v1.2.3`。构建版本取自 tag，而不是 Release 的显示名称；tag 不合法时 workflow 会在测试前直接失败，避免发布更新器无法识别的版本。

1. 检出 Release 对应的 tag，运行 `go test ./...` 和 `go vet ./...`
2. 使用 `CGO_ENABLED=0` 构建 linux/darwin/windows 的 amd64/arm64 六个二进制，ldflags 注入 tag、commit、构建时间
3. 所有构建成功后才汇总产物，生成完整的 `checksums.txt`
4. 通过 `gh release upload --clobber` 上传到**同一个已发布 Release**，不会创建重复 Release；draft/prerelease 会被跳过或拒绝

资源命名固定为：

```
nodeseek-rss-monitor-linux-amd64
nodeseek-rss-monitor-linux-arm64
nodeseek-rss-monitor-darwin-amd64
nodeseek-rss-monitor-darwin-arm64
nodeseek-rss-monitor-windows-amd64.exe
nodeseek-rss-monitor-windows-arm64.exe
checksums.txt
```

## Cron 表达式说明

支持标准 Cron 格式（5 个字段）：

```
* * * * *
│ │ │ │ │
│ │ │ │ └─ 星期几 (0-6, 0 = 周日)
│ │ │ └─── 月份 (1-12)
│ │ └───── 日期 (1-31)
│ └─────── 小时 (0-23)
└───────── 分钟 (0-59)
```

### 常用示例

- `*/30 * * * *` - 每 30 分钟执行一次
- `0 * * * *` - 每小时整点执行
- `0 */2 * * *` - 每 2 小时执行一次
- `0 9 * * *` - 每天上午 9 点执行
- `0 9,18 * * *` - 每天上午 9 点和下午 6 点执行
- `0 9 * * 1-5` - 工作日上午 9 点执行

## 使用 Gmail SMTP

如果使用 Gmail 发送邮件，需要：

1. 开启两步验证
2. 生成应用专用密码：
   - 访问 [Google 账户安全设置](https://myaccount.google.com/security)
   - 找到"应用专用密码"
   - 生成新密码并复制到配置文件的 `smtp.pass` 字段

配置示例：

```json
"smtp": {
  "host": "smtp.gmail.com",
  "port": 587,
  "user": "your-email@gmail.com",
  "pass": "your-16-char-app-password",
  "secure": false
}
```

## 项目结构

```
nodeseek-rss-monitor/
├── main.go                  # 主程序入口
├── config.go                # 配置加载/原子保存
├── rss.go                   # RSS 拉取、匹配与缓存
├── notify.go                # 邮件/Telegram 通知
├── telegram_bot.go          # Telegram 轮询与命令
├── telegram_menu.go         # 内联键盘菜单与回调
├── update.go                # GitHub Release 检查/下载/校验
├── update_platform_unix.go  # Unix 原子替换与 exec 重启
├── update_platform_windows.go # Windows 手动替换限制
├── version.go               # 版本信息与 SemVer 比较
├── go.mod                   # Go 模块文件
├── config.example.json      # 配置文件示例
├── config.json              # 配置文件（需自行创建）
├── sent_cache.json          # 缓存文件（自动生成）
├── .github/workflows/release.yml # Release 构建与上传
├── .gitignore               # Git 忽略文件
└── README.md                # 说明文档
```

## 邮件模板

程序会发送采用 **Next.js 黑白风格**的精美 HTML 邮件。

### 测试邮件

程序启动时会发送一封测试邮件，包含：
- ✅ 程序启动成功提示
- 绿色运行状态标签
- 当前配置信息（RSS 源、关键词、执行频率、收件人）
- 功能说明
- 启动时间

### RSS 通知邮件

当发现匹配关键词的帖子时，会发送通知邮件，包含：
- 帖子标题（黑色标题栏）
- 匹配的关键词（黑色标签高亮显示）
- 发布时间
- 作者
- 分类
- 内容摘要
- 查看完整内容的链接（黑色按钮）

### 邮件设计特点

- 简洁的黑白配色方案
- 清晰的视觉层次
- 现代化的排版
- 响应式设计，适配各种邮件客户端

## 注意事项

1. **文件路径**：
   - 程序会从**可执行文件所在目录**读取 `config.json` 配置文件
   - 缓存文件 `sent_cache.json` 也会保存在可执行文件所在目录
   - 无论在哪里运行程序，都会使用可执行文件目录下的配置文件
   - 这使得程序可以部署到任意目录，配置文件跟随可执行文件即可
2. **调度模式**：
   - 优先使用 `interval_seconds`（秒级间隔）
   - 如果 `interval_seconds` 为 0 或未设置，则使用 `cron` 表达式
   - 两个参数至少设置一个
3. **缓存文件**：`sent_cache.json` 会自动记录已发送的帖子，缓存有效期为 30 天
4. **去重机制**：即使不匹配关键词的帖子也会被记录，避免重复检查
5. **立即执行**：程序启动时会立即执行一次检查，然后按设定的规则定时执行
6. **错误处理**：如果某封邮件发送失败，不会影响后续邮件的发送
7. **关键词匹配**：大小写敏感，支持中文关键词
8. **高频监控**：使用秒级间隔时建议不要设置过小的值（建议 ≥ 10 秒），避免对 RSS 源造成压力

## License

MIT
