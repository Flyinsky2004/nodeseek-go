package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/robfig/cron/v3"
)

func main() {
	// --version must work before any config file is read.
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version", "-version", "version":
			fmt.Println(versionString())
			return
		}
	}

	// 获取可执行文件所在目录
	if err := initExecDir(); err != nil {
		log.Fatalf("获取可执行文件目录失败: %v", err)
	}

	// 清理上次更新遗留的备份/临时文件（Windows 的 .new 文件保留）。
	cleanupUpdateArtifacts()

	// 加载配置文件
	if err := loadConfig("config.json"); err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	// 加载缓存文件
	loadCache()

	log.Printf("版本: %s", versionString())

	// 发送启动测试邮件
	if emailEnabled() {
		log.Println("发送启动测试邮件...")
		if err := sendTestEmail(); err != nil {
			log.Printf("测试邮件发送失败: %v (程序将继续运行)", err)
		} else {
			log.Println("测试邮件发送成功")
		}
	}

	// 发送启动 Telegram 通知
	if telegramEnabled() {
		log.Println("发送启动 Telegram 通知...")
		if err := sendTelegramTestMessage(); err != nil {
			log.Printf("Telegram 启动通知发送失败: %v (程序将继续运行)", err)
		} else {
			log.Println("Telegram 启动通知发送成功")
		}
	}

	// 启动 Telegram Bot 命令/菜单监听
	if telegramEnabled() {
		go startTelegramPolling()
	}

	// 立即执行一次
	log.Println("立即执行一次 RSS 检查...")
	if err := checkRSS(); err != nil {
		log.Printf("RSS 检查失败: %v", err)
	}

	configMu.RLock()
	interval := config.RSS.IntervalSeconds
	cronExpr := config.RSS.Cron
	configMu.RUnlock()

	// 根据配置选择调度模式
	if interval > 0 {
		log.Printf("RSS 监控服务已启动，每 %d 秒执行一次", interval)
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("开始执行 RSS 监控任务...")
			if err := checkRSS(); err != nil {
				log.Printf("RSS 监控任务执行失败: %v", err)
			}
		}
	} else if cronExpr != "" {
		log.Printf("RSS 监控服务已启动，定时规则: %s", cronExpr)
		c := cron.New()
		_, err := c.AddFunc(cronExpr, func() {
			log.Println("开始执行 RSS 监控任务...")
			if err := checkRSS(); err != nil {
				log.Printf("RSS 监控任务执行失败: %v", err)
			}
		})
		if err != nil {
			log.Fatalf("添加定时任务失败: %v", err)
		}

		c.Start()

		select {}
	} else {
		log.Fatalf("配置错误：必须设置 interval_seconds 或 cron 其中之一")
	}
}
