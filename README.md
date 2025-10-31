# NodeSeek RSS 邮件监控

基于 Go 语言的 RSS 监控程序，自动监控 NodeSeek RSS 源，当发现包含特定关键词的新帖时，自动发送邮件通知。

## 功能特性

- 定时拉取 RSS 源内容
- **支持秒级间隔调度**（每 20 秒、30 秒等）
- **支持 Cron 表达式**配置定时任务
- 关键词匹配过滤（标题和描述）
- 自动去重，避免重复发送
- 支持多个收件人
- 精美的 HTML 邮件模板
- 本地缓存，记录已发送的帖子

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
├── main.go                  # 主程序
├── go.mod                   # Go 模块文件
├── config.example.json      # 配置文件示例
├── config.json              # 配置文件（需自行创建）
├── sent_cache.json          # 缓存文件（自动生成）
├── .gitignore               # Git 忽略文件
└── README.md                # 说明文档
```

## 邮件模板

程序会发送采用 **Next.js 黑白风格**的精美 HTML 邮件，包含以下信息：

- 帖子标题（黑色标题栏）
- 匹配的关键词（黑色标签高亮显示）
- 发布时间
- 作者
- 分类
- 内容摘要
- 查看完整内容的链接（黑色按钮）

邮件设计特点：
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
