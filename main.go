// 高性能文件处理系统 - 主程序入口
// 功能：大规模图片文件的智能管理、重复检测和自动整理
// 作者：文件处理系统开发团队
// 版本：v2.0
// 创建时间：2025年
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// formatFileSize 格式化文件大小显示
// 将字节数转换为人类可读的格式（B, KB, MB, GB等）
// 参数：size - 文件大小（字节）
// 返回：格式化后的文件大小字符串
func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// main 主函数 - 程序入口点
// 负责初始化系统组件、解析命令行参数、启动文件处理服务
func main() {
	// 设置panic恢复机制，确保程序异常时能够优雅处理
	defer RecoverPanic(GetGlobalLogger())

	LogInfo("🚀 文件移动系统正在启动...")
	
	// 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	mode := flag.String("mode", "watch", "运行模式: watch(监控模式), timer(定时模式) 或 scan(图片扫描模式)")
	interval := flag.Duration("interval", 30*time.Second, "定时模式的扫描间隔")
	scanDir := flag.String("scandir", "", "图片扫描模式的目标目录路径")
	removeDupes := flag.Bool("removedupes", false, "图片扫描模式下是否删除重复文件(保留第一个)")
	flag.Parse()

	LogDebug("📋 命令行参数: config=%s, mode=%s, interval=%v, scandir=%s, removedupes=%v", *configPath, *mode, *interval, *scanDir, *removeDupes)

	// 加载系统配置文件
	LogInfo("📄 正在加载配置文件: %s", *configPath)
	config, err := LoadConfig(*configPath)
	if err != nil {
		LogFatal("❌ 加载配置文件失败: %v", err)
	}
	LogInfo("✅ 配置文件加载成功")

	// 初始化全局日志记录器
	LogDebug("📝 正在初始化日志系统...")
	if err := InitGlobalLogger(config); err != nil {
		LogFatal("❌ 初始化日志系统失败: %v", err)
	}
	defer GetGlobalLogger().Close()
	LogInfo("✅ 日志系统初始化完成")

	// 初始化数据库连接
	LogInfo("🗄️ 正在初始化数据库连接...")
	database, err := InitDatabase(config.Database)
	if err != nil {
		LogFatal("❌ 初始化数据库失败: %v", err)
	}
	defer database.Close()
	LogInfo("✅ 数据库初始化完成")

	// 创建文件处理器
	LogDebug("⚙️ 正在创建文件处理器...")
	processor := NewFileProcessor(config, database)
	LogInfo("✅ 文件处理器创建完成")

	// 创建目标文件夹（如果不存在）
	LogDebug("📁 正在检查/创建目标文件夹: %s", config.TargetFolder)
	if err := os.MkdirAll(config.TargetFolder, 0755); err != nil {
		LogError("❌ 创建目标文件夹失败: %v", err)
		return
	}
	LogDebug("✅ 目标文件夹准备完成")

	// 打印启动信息
	LogInfo("🚀 文件移动系统启动")
	LogInfo("📂 源文件夹: %s", config.SourceFolder)
	LogInfo("📁 目标文件夹: %s", config.TargetFolder)
	LogInfo("⚡ 智能工作线程数: %d (CPU核心数: %d)", config.ConcurrentWorkers, runtime.NumCPU())
	LogInfo("🔍 哈希算法: %s", config.HashAlgorithm)
	LogInfo("📊 支持的文件类型: %v", config.SupportedTypes)
	LogInfo("💾 最大文件大小: %s", formatFileSize(int64(config.MaxFileSize)))

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
	LogInfo("📂 源文件夹: %s", config.SourceFolder)
	LogInfo("📁 目标文件夹: %s", config.TargetFolder)
	LogDebug("🔧 系统配置详情: 工作线程=%d, 哈希算法=%s, 最大文件大小=%s", 
		config.ConcurrentWorkers, config.HashAlgorithm, formatFileSize(int64(config.MaxFileSize)))

	// 设置信号处理
	LogDebug("📡 正在设置系统信号处理...")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	LogDebug("✅ 信号处理设置完成")

	switch *mode {
	case "watch":
		// 监控模式
		LogInfo("🔍 启动文件监控模式...")
		watcher, err := NewFileWatcher(config.SourceFolder, processor)
		if err != nil {
			LogFatal("❌ 创建文件监控器失败: %v", err)
		}
		defer watcher.Close()
		LogInfo("✅ 文件监控器已启动")

		LogInfo("👀 文件监控器已启动，等待文件变化...")
		
		// 首次扫描现有文件
		LogInfo("🔄 开始首次扫描现有文件...")
		if err := processor.ProcessExistingFiles(); err != nil {
			LogError("❌ 处理现有文件时出错: %v", err)
		} else {
			LogInfo("✅ 首次扫描完成")
		}

		// 等待信号
		LogInfo("⏳ 系统运行中，按 Ctrl+C 退出...")
		<-sigChan
		LogInfo("📡 收到退出信号，正在关闭...")

	case "timer":
		// 定时模式
		LogInfo("⏰ 启动定时扫描模式，扫描间隔: %v", *interval)
		ticker := time.NewTicker(*interval)
		defer ticker.Stop()

		LogInfo("✅ 定时扫描模式已启动，扫描间隔: %v", *interval)

		// 立即执行一次
		LogInfo("🔄 执行首次文件扫描...")
		if err := processor.ProcessExistingFiles(); err != nil {
			LogError("❌ 处理文件时出错: %v", err)
		} else {
			LogInfo("✅ 首次扫描完成")
		}

		for {
			select {
			case <-ticker.C:
				LogInfo("⏰ 定时器触发，开始扫描文件...")
				if err := processor.ProcessExistingFiles(); err != nil {
					LogError("❌ 处理文件时出错: %v", err)
				} else {
					LogDebug("✅ 定时扫描完成")
				}
			case <-sigChan:
				LogInfo("📡 收到退出信号，正在关闭...")
				return
			}
		}

	case "scan":
		// 图片扫描模式
		if *scanDir == "" {
			LogError("❌ 图片扫描模式需要指定扫描目录，请使用 -scandir 参数")
			fmt.Println("❌ 图片扫描模式需要指定扫描目录")
			fmt.Println("使用方法: ./file-move-go.exe -mode=scan -scandir=\"C:\\Pictures\"")
			os.Exit(1)
		}

		LogInfo("🖼️ 启动图片文件扫描模式...")
		LogInfo("📂 扫描目录: %s", *scanDir)
		if *removeDupes {
			LogInfo("🗑️ 重复文件删除: 启用 (保留第一个文件)")
		} else {
			LogInfo("⚠️ 重复文件删除: 禁用 (仅跳过重复文件)")
		}
		
		// 创建图片扫描器
		scanner := NewImageScanner(database, *scanDir, *removeDupes)
		
		// 执行扫描
		LogInfo("🔍 开始扫描图片文件...")
		if err := scanner.ScanDirectory(); err != nil {
			LogError("❌ 图片扫描失败: %v", err)
			fmt.Printf("❌ 图片扫描失败: %v\n", err)
			os.Exit(1)
		}
		
		// 显示统计信息
		stats := scanner.GetStatistics()
		fmt.Println("========================================")
		fmt.Println("       图片扫描完成")
		fmt.Println("========================================")
		fmt.Printf("📂 扫描目录: %s\n", *scanDir)
		fmt.Printf("📈 总文件数: %d\n", stats["total"])
		fmt.Printf("✅ 成功处理: %d\n", stats["processed"])
		fmt.Printf("⚠️ 跳过文件: %d\n", stats["skipped"])
		if *removeDupes && stats["deleted"] > 0 {
			fmt.Printf("🗑️ 删除重复: %d\n", stats["deleted"])
		}
		fmt.Printf("❌ 错误文件: %d\n", stats["errors"])
		fmt.Println("========================================")
		
		LogInfo("🎉 图片扫描模式执行完成")
		return

	default:
		LogError("❌ 不支持的运行模式: %s", *mode)
		fmt.Printf("不支持的运行模式: %s\n", *mode)
		fmt.Println("支持的模式: watch, timer, scan")
		os.Exit(1)
	}
}