package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// 设置panic恢复
	defer RecoverPanic(GetGlobalLogger())

	// 命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	mode := flag.String("mode", "watch", "运行模式: watch(监控模式) 或 timer(定时模式)")
	interval := flag.Duration("interval", 30*time.Second, "定时模式的扫描间隔")
	flag.Parse()

	// 加载配置
	config, err := LoadConfig(*configPath)
	if err != nil {
		LogFatal("加载配置文件失败: %v", err)
	}

	// 初始化全局日志记录器
	if err := InitGlobalLogger(config); err != nil {
		LogFatal("初始化日志系统失败: %v", err)
	}
	defer GetGlobalLogger().Close()

	// 初始化数据库
	db, err := InitDatabase(config.Database)
	if err != nil {
		LogFatal("初始化数据库失败: %v", err)
	}
	defer db.Close()

	// 创建文件处理器
	processor := NewFileProcessor(config, db)

	// 确保目标文件夹存在
	if err := os.MkdirAll(config.TargetFolder, 0755); err != nil {
		LogFatal("创建目标文件夹失败: %v", err)
	}

	// 打印启动信息
	fmt.Println("========================================")
	fmt.Println("       文件整理系统 v1.0")
	fmt.Println("========================================")
	fmt.Printf("🚀 系统启动时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("📂 源文件夹: %s\n", config.SourceFolder)
	fmt.Printf("📁 目标文件夹: %s\n", config.TargetFolder)
	fmt.Printf("⚙️  运行模式: %s\n", *mode)
	if *mode == "timer" {
		fmt.Printf("⏰ 扫描间隔: %v\n", *interval)
	}
	fmt.Printf("🔧 工作线程数: %d\n", config.ConcurrentWorkers)
	fmt.Println("========================================")

	LogInfo("文件处理系统启动，运行模式: %s", *mode)
	LogInfo("源文件夹: %s", config.SourceFolder)
	LogInfo("目标文件夹: %s", config.TargetFolder)

	// 设置信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	switch *mode {
	case "watch":
		// 监控模式
		watcher, err := NewFileWatcher(config.SourceFolder, processor)
		if err != nil {
			LogFatal("创建文件监控器失败: %v", err)
		}
		defer watcher.Close()

		LogInfo("文件监控器已启动，等待文件变化...")
		
		// 首次扫描现有文件
		if err := processor.ProcessExistingFiles(); err != nil {
			LogError("处理现有文件时出错: %v", err)
		}

		// 等待信号
		<-sigChan
		LogInfo("收到退出信号，正在关闭...")

	case "timer":
		// 定时模式
		ticker := time.NewTicker(*interval)
		defer ticker.Stop()

		LogInfo("定时扫描模式已启动，扫描间隔: %v", *interval)

		// 立即执行一次
		if err := processor.ProcessExistingFiles(); err != nil {
			LogError("处理文件时出错: %v", err)
		}

		for {
			select {
			case <-ticker.C:
				if err := processor.ProcessExistingFiles(); err != nil {
					LogError("处理文件时出错: %v", err)
				}
			case <-sigChan:
				LogInfo("收到退出信号，正在关闭...")
				return
			}
		}

	default:
		fmt.Printf("不支持的运行模式: %s\n", *mode)
		fmt.Println("支持的模式: watch, timer")
		os.Exit(1)
	}
}