package main

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileProcessor 文件处理器
type FileProcessor struct {
	config   *Config
	database *Database
	mutex    sync.RWMutex
	stats    ProcessorStats
}

// ProcessorStats 处理统计信息
type ProcessorStats struct {
	ProcessedFiles int64
	MovedFiles     int64
	DeletedFiles   int64
	ErrorCount     int64
	TotalSize      int64
	StartTime      time.Time
}

// NewFileProcessor 创建新的文件处理器
func NewFileProcessor(config *Config, database *Database) *FileProcessor {
	return &FileProcessor{
		config:   config,
		database: database,
		stats:    ProcessorStats{StartTime: time.Now()},
	}
}

// ProcessExistingFiles 处理现有文件
func (fp *FileProcessor) ProcessExistingFiles() error {
	log.Printf("开始扫描源文件夹: %s", fp.config.SourceFolder)
	log.Printf("配置信息 - 并发工作者: %d, 最大文件大小: %s, 哈希算法: %s", 
		fp.config.ConcurrentWorkers, 
		formatFileSize(fp.config.MaxFileSize), 
		fp.config.HashAlgorithm)

	// 使用工作池模式处理文件
	fileChan := make(chan string, 100)
	errorChan := make(chan error, fp.config.ConcurrentWorkers)
	var wg sync.WaitGroup

	// 启动工作协程
	log.Printf("启动 %d 个工作协程", fp.config.ConcurrentWorkers)
	for i := 0; i < fp.config.ConcurrentWorkers; i++ {
		wg.Add(1)
		go fp.worker(fileChan, errorChan, &wg)
	}

	// 统计文件数量
	var totalFiles int64
	var skippedFiles int64

	// 遍历文件夹
	log.Printf("开始遍历文件夹: %s", fp.config.SourceFolder)
	go func() {
		defer close(fileChan)
		err := filepath.Walk(fp.config.SourceFolder, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Printf("访问文件失败 %s: %v", path, err)
				return nil // 继续处理其他文件
			}

			// 跳过目录
			if info.IsDir() {
				return nil
			}

			totalFiles++

			// 每处理100个文件显示一次进度
			if totalFiles%100 == 0 {
				fmt.Printf("\r🔍 正在扫描文件... 已发现: %d 个文件", totalFiles)
			}

			// 检查文件类型
			if !fp.config.IsSupportedFile(info.Name()) {
				skippedFiles++
				log.Printf("不支持的文件类型，跳过: %s", path)
				return nil
			}

			// 检查文件大小
			if info.Size() > fp.config.MaxFileSize {
				skippedFiles++
				log.Printf("文件过大，跳过: %s (大小: %s)", path, formatFileSize(info.Size()))
				return nil
			}

			// 发送到工作队列
			select {
			case fileChan <- path:
				log.Printf("文件已加入处理队列: %s (大小: %s)", path, formatFileSize(info.Size()))
			default:
				skippedFiles++
				log.Printf("工作队列已满，跳过文件: %s", path)
			}

			return nil
		})

		if err != nil {
			log.Printf("遍历文件夹失败: %v", err)
		}
		
		// 清除进度显示并打印最终统计
		fmt.Printf("\r")
		fmt.Printf("📊 文件扫描完成 - 总文件数: %d, 跳过文件数: %d, 待处理文件数: %d\n", 
			totalFiles, skippedFiles, totalFiles-skippedFiles)
		log.Printf("文件夹遍历完成 - 总文件数: %d, 跳过文件数: %d, 待处理文件数: %d", 
			totalFiles, skippedFiles, totalFiles-skippedFiles)
	}()

	// 启动进度显示协程
	progressDone := make(chan bool)
	go fp.showProgress(progressDone)

	// 等待所有工作完成
	log.Printf("等待所有工作协程完成...")
	wg.Wait()
	close(errorChan)
	close(progressDone)

	// 收集错误
	var errors []error
	for err := range errorChan {
		if err != nil {
			errors = append(errors, err)
		}
	}

	// 打印统计信息
	log.Printf("处理完成，开始打印统计信息...")
	fp.printStats()

	if len(errors) > 0 {
		log.Printf("处理完成，但有 %d 个错误", len(errors))
		return fmt.Errorf("处理过程中发生 %d 个错误", len(errors))
	}

	log.Println("所有文件处理完成，无错误")
	return nil
}

// worker 工作协程
func (fp *FileProcessor) worker(fileChan <-chan string, errorChan chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	for filePath := range fileChan {
		if err := fp.ProcessFile(filePath); err != nil {
			log.Printf("处理文件失败 %s: %v", filePath, err)
			errorChan <- err
			fp.incrementErrorCount()
		}
	}
}

// ProcessFile 处理单个文件
func (fp *FileProcessor) ProcessFile(filePath string) error {
	log.Printf("开始处理文件: %s", filePath)

	// 获取文件信息
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		log.Printf("获取文件信息失败: %s, 错误: %v", filePath, err)
		return fmt.Errorf("获取文件信息失败: %v", err)
	}

	log.Printf("文件信息 - 名称: %s, 大小: %s, 修改时间: %s", 
		fileInfo.Name(), 
		formatFileSize(fileInfo.Size()), 
		fileInfo.ModTime().Format("2006-01-02 15:04:05"))

	// 计算文件哈希
	log.Printf("开始计算文件哈希: %s (算法: %s)", filePath, fp.config.HashAlgorithm)
	hash, err := fp.calculateFileHash(filePath)
	if err != nil {
		log.Printf("计算文件哈希失败: %s, 错误: %v", filePath, err)
		return fmt.Errorf("计算文件哈希失败: %v", err)
	}
	log.Printf("文件哈希计算完成: %s -> %s", filePath, hash[:12]+"...")

	// 检查数据库中是否已存在
	log.Printf("检查数据库中是否存在相同哈希的文件: %s", hash[:12]+"...")
	exists, err := fp.database.FileExists(hash)
	if err != nil {
		log.Printf("检查文件是否存在失败: %s, 错误: %v", filePath, err)
		return fmt.Errorf("检查文件是否存在失败: %v", err)
	}

	if exists {
		// 文件已存在，删除重复文件
		log.Printf("发现重复文件，准备删除: %s (哈希: %s)", filePath, hash[:12]+"...")
		if err := os.Remove(filePath); err != nil {
			log.Printf("删除重复文件失败: %s, 错误: %v", filePath, err)
			return fmt.Errorf("删除重复文件失败: %v", err)
		}
		log.Printf("重复文件已删除: %s", filePath)
		fp.incrementDeletedFiles()
	} else {
		// 文件不存在，移动到目标文件夹并记录到数据库
		log.Printf("文件为新文件，准备移动到目标文件夹: %s", filePath)
		targetPath, err := fp.moveFileToTarget(filePath, fileInfo.Name())
		if err != nil {
			log.Printf("移动文件失败: %s, 错误: %v", filePath, err)
			return fmt.Errorf("移动文件失败: %v", err)
		}
		log.Printf("文件移动成功: %s -> %s", filePath, targetPath)

		// 创建文件记录
		record := &FileInfo{
			Hash:         hash,
			OriginalName: fileInfo.Name(),
			FileSize:     fileInfo.Size(),
			Extension:    strings.ToLower(filepath.Ext(fileInfo.Name())),
			CreatedAt:    fileInfo.ModTime(),
			ProcessedAt:  time.Now(),
			SourcePath:   filePath,
			TargetPath:   targetPath,
			HashType:     fp.config.HashAlgorithm,
		}

		// 插入数据库记录
		log.Printf("准备插入数据库记录: 文件名=%s, 哈希=%s, 大小=%s", 
			record.OriginalName, record.Hash[:12]+"...", formatFileSize(record.FileSize))
		if err := fp.database.InsertFileRecord(record); err != nil {
			// 如果数据库插入失败，尝试恢复文件
			log.Printf("数据库插入失败，尝试恢复文件: %s -> %s", targetPath, filePath)
			if moveErr := os.Rename(targetPath, filePath); moveErr != nil {
				log.Printf("恢复文件失败: %v", moveErr)
			}
			log.Printf("插入数据库记录失败: %s, 错误: %v", filePath, err)
			return fmt.Errorf("插入数据库记录失败: %v", err)
		}
		log.Printf("数据库记录插入成功: ID=%d, 哈希=%s", record.ID, record.Hash[:12]+"...")

		fp.incrementMovedFiles()
		fp.addTotalSize(fileInfo.Size())
		log.Printf("文件处理完成: %s -> %s (大小: %s)", filePath, targetPath, formatFileSize(fileInfo.Size()))
	}

	fp.incrementProcessedFiles()
	log.Printf("文件处理结束: %s", filePath)
	return nil
}

// calculateFileHash 计算文件哈希值
func (fp *FileProcessor) calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	var hasher hash.Hash
	switch fp.config.HashAlgorithm {
	case "md5":
		hasher = md5.New()
	case "sha256":
		hasher = sha256.New()
	default:
		return "", fmt.Errorf("不支持的哈希算法: %s", fp.config.HashAlgorithm)
	}

	// 使用缓冲区读取文件以提高性能
	buffer := make([]byte, 64*1024) // 64KB 缓冲区
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			hasher.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("读取文件失败: %v", err)
		}
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// moveFileToTarget 移动文件到目标文件夹
func (fp *FileProcessor) moveFileToTarget(sourcePath, fileName string) (string, error) {
	// 生成目标路径，按日期组织文件夹
	now := time.Now()
	dateFolder := now.Format("2006/01/02")
	targetDir := filepath.Join(fp.config.TargetFolder, dateFolder)

	// 确保目标目录存在
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("创建目标目录失败: %v", err)
	}

	// 生成目标文件路径
	targetPath := filepath.Join(targetDir, fileName)

	// 如果目标文件已存在，添加时间戳后缀
	if _, err := os.Stat(targetPath); err == nil {
		ext := filepath.Ext(fileName)
		nameWithoutExt := strings.TrimSuffix(fileName, ext)
		timestamp := now.Format("_20060102_150405")
		targetPath = filepath.Join(targetDir, nameWithoutExt+timestamp+ext)
	}

	// 移动文件
	if err := os.Rename(sourcePath, targetPath); err != nil {
		// 如果重命名失败（可能跨分区），尝试复制后删除
		if err := fp.copyFile(sourcePath, targetPath); err != nil {
			return "", fmt.Errorf("复制文件失败: %v", err)
		}
		if err := os.Remove(sourcePath); err != nil {
			log.Printf("删除源文件失败: %v", err)
			// 不返回错误，因为文件已经复制成功
		}
	}

	return targetPath, nil
}

// copyFile 复制文件
func (fp *FileProcessor) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	// 复制文件内容
	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		os.Remove(dst) // 清理失败的目标文件
		return err
	}

	// 复制文件权限
	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	return os.Chmod(dst, sourceInfo.Mode())
}

// 统计信息相关方法
func (fp *FileProcessor) incrementProcessedFiles() {
	fp.mutex.Lock()
	defer fp.mutex.Unlock()
	fp.stats.ProcessedFiles++
}

func (fp *FileProcessor) incrementMovedFiles() {
	fp.mutex.Lock()
	defer fp.mutex.Unlock()
	fp.stats.MovedFiles++
}

func (fp *FileProcessor) incrementDeletedFiles() {
	fp.mutex.Lock()
	defer fp.mutex.Unlock()
	fp.stats.DeletedFiles++
}

func (fp *FileProcessor) incrementErrorCount() {
	fp.mutex.Lock()
	defer fp.mutex.Unlock()
	fp.stats.ErrorCount++
}

func (fp *FileProcessor) addTotalSize(size int64) {
	fp.mutex.Lock()
	defer fp.mutex.Unlock()
	fp.stats.TotalSize += size
}

// GetStats 获取处理统计信息
func (fp *FileProcessor) GetStats() ProcessorStats {
	fp.mutex.RLock()
	defer fp.mutex.RUnlock()
	return fp.stats
}

// printStats 打印统计信息
func (fp *FileProcessor) printStats() {
	stats := fp.GetStats()
	
	// 计算处理时间
	duration := time.Since(stats.StartTime)
	
	// 打印分隔线
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("                    文件处理统计报告")
	fmt.Println(strings.Repeat("=", 60))
	
	// 基本统计信息
	fmt.Printf("📊 处理总数: %d 个文件\n", stats.ProcessedFiles)
	fmt.Printf("📁 移动文件: %d 个\n", stats.MovedFiles)
	fmt.Printf("🗑️  删除重复: %d 个\n", stats.DeletedFiles)
	fmt.Printf("❌ 处理错误: %d 个\n", stats.ErrorCount)
	fmt.Printf("💾 总处理大小: %s\n", formatFileSize(stats.TotalSize))
	fmt.Printf("⏱️  处理时间: %v\n", duration.Round(time.Second))
	
	// 计算处理速度
	if duration.Seconds() > 0 {
		filesPerSecond := float64(stats.ProcessedFiles) / duration.Seconds()
		bytesPerSecond := float64(stats.TotalSize) / duration.Seconds()
		fmt.Printf("🚀 处理速度: %.1f 文件/秒, %s/秒\n", filesPerSecond, formatFileSize(int64(bytesPerSecond)))
	}
	
	// 计算百分比
	if stats.ProcessedFiles > 0 {
		movePercent := float64(stats.MovedFiles) / float64(stats.ProcessedFiles) * 100
		deletePercent := float64(stats.DeletedFiles) / float64(stats.ProcessedFiles) * 100
		errorPercent := float64(stats.ErrorCount) / float64(stats.ProcessedFiles) * 100
		
		fmt.Println(strings.Repeat("-", 60))
		fmt.Printf("📈 移动文件比例: %.1f%%\n", movePercent)
		fmt.Printf("📈 重复文件比例: %.1f%%\n", deletePercent)
		fmt.Printf("📈 错误率: %.1f%%\n", errorPercent)
	}
	
	// 平均文件大小
	if stats.MovedFiles > 0 {
		avgSize := stats.TotalSize / stats.MovedFiles
		fmt.Printf("📏 平均文件大小: %s\n", formatFileSize(avgSize))
	}
	
	fmt.Println(strings.Repeat("=", 60))
	
	// 同时输出到日志
	log.Printf("处理统计: 总计=%d, 移动=%d, 删除=%d, 错误=%d, 总大小=%s, 耗时=%v",
		stats.ProcessedFiles,
		stats.MovedFiles,
		stats.DeletedFiles,
		stats.ErrorCount,
		formatFileSize(stats.TotalSize),
		duration.Round(time.Second),
	)
}

// showProgress 显示实时处理进度
func (fp *FileProcessor) showProgress(done <-chan bool) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			// 清除进度显示
			fmt.Printf("\r%s\r", strings.Repeat(" ", 80))
			return
		case <-ticker.C:
			stats := fp.GetStats()
			duration := time.Since(stats.StartTime)
			
			// 计算处理速度
			var speed string
			if duration.Seconds() > 0 {
				filesPerSecond := float64(stats.ProcessedFiles) / duration.Seconds()
				speed = fmt.Sprintf("%.1f 文件/秒", filesPerSecond)
			} else {
				speed = "计算中..."
			}
			
			// 显示进度信息
			progressMsg := fmt.Sprintf("\r⚡ 处理中: %d 个文件 | 移动: %d | 删除: %d | 错误: %d | 速度: %s | 耗时: %v",
				stats.ProcessedFiles,
				stats.MovedFiles,
				stats.DeletedFiles,
				stats.ErrorCount,
				speed,
				duration.Round(time.Second),
			)
			
			// 确保不超过终端宽度
			if len(progressMsg) > 120 {
				progressMsg = progressMsg[:117] + "..."
			}
			
			fmt.Print(progressMsg)
		}
	}
}

// formatFileSize 格式化文件大小
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