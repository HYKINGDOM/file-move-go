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
	LogInfo("开始扫描源文件夹: %s", fp.config.SourceFolder)
	LogInfo("配置信息 - 并发工作者: %d, 最大文件大小: %s, 哈希算法: %s",
		fp.config.ConcurrentWorkers,
		formatFileSize(int64(fp.config.MaxFileSize)),
		fp.config.HashAlgorithm)

	// 创建通道
	fileChan := make(chan string, 1000) // 增加缓冲区大小到1000
	errorChan := make(chan error, fp.config.ConcurrentWorkers)

	// 启动工作协程
	var wg sync.WaitGroup
	LogInfo("启动 %d 个工作协程", fp.config.ConcurrentWorkers)
	for i := 0; i < fp.config.ConcurrentWorkers; i++ {
		wg.Add(1)
		go fp.worker(fileChan, errorChan, &wg)
	}

	// 启动进度显示协程
	done := make(chan bool)
	go fp.showProgress(done)

	// 遍历文件夹
	LogInfo("开始遍历文件夹: %s", fp.config.SourceFolder)
	totalFiles := 0
	skippedFiles := 0
	processedFiles := 0

	err := filepath.Walk(fp.config.SourceFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			LogError("访问文件失败 %s: %v", path, err)
			return nil // 继续处理其他文件
		}

		// 跳过目录
		if info.IsDir() {
			return nil
		}

		totalFiles++

		// 检查文件类型
		ext := strings.ToLower(filepath.Ext(path))
		supported := false
		for _, supportedType := range fp.config.SupportedTypes {
			if ext == supportedType {
				supported = true
				break
			}
		}

		if !supported {
			skippedFiles++
			return nil
		}

		// 检查文件大小
		if info.Size() > int64(fp.config.MaxFileSize) {
			LogWarn("文件超过大小限制，跳过: %s (大小: %d 字节)", path, info.Size())
			skippedFiles++
			return nil
		}

		// 发送到处理通道
		select {
		case fileChan <- path:
			processedFiles++
		default:
			LogWarn("处理队列已满，跳过文件: %s", path)
			skippedFiles++
		}

		return nil
	})

	if err != nil {
		LogError("遍历文件夹失败: %v", err)
		close(fileChan)
		done <- true
		return err
	}

	// 关闭文件通道，等待处理完成
	close(fileChan)
	LogInfo("文件夹遍历完成 - 总文件数: %d, 跳过文件数: %d, 待处理文件数: %d",
		totalFiles, skippedFiles, processedFiles)

	// 等待所有工作协程完成
	LogInfo("等待所有工作协程完成...")
	wg.Wait()

	// 停止进度显示
	done <- true

	// 收集错误
	close(errorChan)
	var errors []error
	for err := range errorChan {
		errors = append(errors, err)
	}

	if len(errors) == 0 {
		LogInfo("处理完成，开始打印统计信息...")
		fp.printStats()
	} else {
		LogError("处理完成，但有 %d 个错误", len(errors))
		fp.printStats()
		for _, err := range errors {
			LogError("错误详情: %v", err)
		}
	}

	return nil
}

// worker 工作协程
func (fp *FileProcessor) worker(fileChan <-chan string, errorChan chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	for filePath := range fileChan {
		if err := fp.ProcessFile(filePath); err != nil {
			LogError("处理文件失败 %s: %v", filePath, err)
			errorChan <- err
			fp.incrementErrorCount()
		}
	}
}

// ProcessFile 处理单个文件
func (fp *FileProcessor) ProcessFile(filePath string) error {
	startTime := time.Now()
	defer func() {
		totalDuration := time.Since(startTime)
		LogInfo("⏱️ 文件处理总耗时: %s -> %v", filepath.Base(filePath), totalDuration)
	}()
	
	// 获取文件信息
	fileInfoStart := time.Now()
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("获取文件信息失败: %v", err)
	}
	LogInfo("⏱️ 获取文件信息耗时: %v", time.Since(fileInfoStart))

	// 计算文件哈希
	hashStart := time.Now()
	hash, err := fp.calculateFileHash(filePath)
	if err != nil {
		return fmt.Errorf("计算文件哈希失败: %v", err)
	}
	hashDuration := time.Since(hashStart)
	LogInfo("⏱️ 哈希计算耗时: %v (文件大小: %s)", hashDuration, formatFileSize(fileInfo.Size()))

	// 检查文件是否已存在
	dbCheckStart := time.Now()
	exists, existingPath, err := fp.database.FileExists(hash)
	if err != nil {
		return fmt.Errorf("数据库查询失败: %v", err)
	}
	LogInfo("⏱️ 数据库查询耗时: %v", time.Since(dbCheckStart))

	if exists {
		// 文件已存在，删除重复文件
		deleteStart := time.Now()
		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("删除重复文件失败: %v", err)
		}
		LogInfo("⏱️ 删除重复文件耗时: %v", time.Since(deleteStart))
		LogInfo("删除重复文件: %s (已存在: %s)", filepath.Base(filePath), existingPath)
		fp.incrementDeletedFiles()
		fp.addTotalSize(fileInfo.Size())
		return nil
	}

	// 移动文件到目标位置
	moveStart := time.Now()
	targetPath, err := fp.moveFileToTarget(filePath, filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("移动文件失败: %v", err)
	}
	moveDuration := time.Since(moveStart)
	LogInfo("⏱️ 文件移动耗时: %v (文件大小: %s)", moveDuration, formatFileSize(fileInfo.Size()))

	// 插入数据库记录
	dbInsertStart := time.Now()
	fileRecord := FileInfo{
		Hash:         hash,
		OriginalPath: filePath,
		NewPath:      targetPath,
		FileName:     filepath.Base(filePath),
		FileSize:     fileInfo.Size(),
		Extension:    strings.ToLower(filepath.Ext(filePath)),
		CreatedAt:    time.Now(),
	}

	if err := fp.database.InsertFileRecord(fileRecord); err != nil {
		// 如果数据库插入失败，尝试恢复文件
		if moveErr := os.Rename(targetPath, filePath); moveErr != nil {
			LogError("恢复文件失败: %v", moveErr)
		}
		return fmt.Errorf("插入数据库记录失败: %v", err)
	}
	LogInfo("⏱️ 数据库插入耗时: %v", time.Since(dbInsertStart))

	LogInfo("文件处理成功: %s -> %s", filepath.Base(filePath), filepath.Base(targetPath))
	fp.incrementProcessedFiles()
	fp.incrementMovedFiles()
	fp.addTotalSize(fileInfo.Size())

	return nil
}

// calculateFileHash 计算文件哈希值
func (fp *FileProcessor) calculateFileHash(filePath string) (string, error) {
	startTime := time.Now()
	defer func() {
		totalDuration := time.Since(startTime)
		LogInfo("⏱️ 哈希计算总耗时: %v", totalDuration)
	}()

	// 打开文件
	openStart := time.Now()
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	LogInfo("⏱️ 文件打开耗时: %v", time.Since(openStart))

	// 获取文件信息
	fileInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	fileSize := fileInfo.Size()

	// 创建哈希计算器
	var hasher hash.Hash
	switch fp.config.HashAlgorithm {
	case "md5":
		hasher = md5.New()
	case "sha256":
		hasher = sha256.New()
	default:
		hasher = sha256.New()
	}

	// 使用1MB缓冲区进行文件读取和哈希计算
	readStart := time.Now()
	buffer := make([]byte, 1024*1024) // 1MB缓冲区
	_, err = io.CopyBuffer(hasher, file, buffer)
	if err != nil {
		return "", err
	}
	readDuration := time.Since(readStart)

	// 计算读取速度
	speed := float64(fileSize) / readDuration.Seconds() / (1024 * 1024) // MB/s

	LogInfo("⏱️ 哈希计算详情: 总耗时=%v, 读取耗时=%v, 文件大小=%s, 读取速度=%.2f MB/s",
		time.Since(startTime), readDuration, formatFileSize(fileSize), speed)

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// moveFileToTarget 移动文件到目标位置
func (fp *FileProcessor) moveFileToTarget(sourcePath, fileName string) (string, error) {
	startTime := time.Now()
	defer func() {
		totalDuration := time.Since(startTime)
		LogInfo("⏱️ 文件移动总耗时: %v -> %s", totalDuration, filepath.Base(sourcePath))
	}()

	// 生成目标路径
	pathGenStart := time.Now()
	ext := strings.ToLower(filepath.Ext(fileName))
	targetDir := filepath.Join(fp.config.TargetFolder, ext[1:]) // 去掉点号
	targetPath := filepath.Join(targetDir, fileName)
	LogInfo("⏱️ 路径生成耗时: %v", time.Since(pathGenStart))

	// 确保目标目录存在
	mkdirStart := time.Now()
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("创建目标目录失败: %v", err)
	}
	LogInfo("⏱️ 目录创建耗时: %v", time.Since(mkdirStart))

	// 处理文件名冲突
	counter := 1
	conflictCheckStart := time.Now()
	for {
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			break
		}
		// 文件已存在，生成新的文件名
		name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		targetPath = filepath.Join(targetDir, fmt.Sprintf("%s_%d%s", name, counter, filepath.Ext(fileName)))
		counter++
	}
	LogInfo("⏱️ 文件冲突检查耗时: %v", time.Since(conflictCheckStart))

	// 尝试移动文件
	moveStart := time.Now()
	err := os.Rename(sourcePath, targetPath)
	if err != nil {
		// 如果重命名失败（可能跨分区），则使用复制+删除
		LogWarn("⏱️ 重命名失败，尝试复制: %v", err)
		copyStart := time.Now()
		if err := fp.copyFile(sourcePath, targetPath); err != nil {
			return "", fmt.Errorf("复制文件失败: %v", err)
		}
		LogInfo("⏱️ 文件复制耗时: %v", time.Since(copyStart))

		deleteStart := time.Now()
		if err := os.Remove(sourcePath); err != nil {
			LogError("删除源文件失败: %v", err)
		}
		LogInfo("⏱️ 源文件删除耗时: %v", time.Since(deleteStart))
	} else {
		LogInfo("⏱️ 文件重命名耗时: %v", time.Since(moveStart))
	}

	return targetPath, nil
}

// copyFile 复制文件
func (fp *FileProcessor) copyFile(src, dst string) error {
	startTime := time.Now()
	defer func() {
		totalDuration := time.Since(startTime)
		LogInfo("⏱️ 文件复制总耗时: %v", totalDuration)
	}()

	// 打开源文件
	openSrcStart := time.Now()
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	LogInfo("⏱️ 打开源文件耗时: %v", time.Since(openSrcStart))

	// 创建目标文件
	createDstStart := time.Now()
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	LogInfo("⏱️ 创建目标文件耗时: %v", time.Since(createDstStart))

	// 获取源文件信息
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}
	fileSize := srcInfo.Size()

	// 复制文件内容，使用1MB缓冲区提升性能
	copyStart := time.Now()
	buffer := make([]byte, 1024*1024) // 1MB缓冲区
	_, err = io.CopyBuffer(dstFile, srcFile, buffer)
	if err != nil {
		return err
	}
	copyDuration := time.Since(copyStart)

	// 计算复制速度
	speed := float64(fileSize) / copyDuration.Seconds() / (1024 * 1024) // MB/s

	LogInfo("⏱️ 文件复制详情: 耗时=%v, 大小=%s, 速度=%.2f MB/s",
		copyDuration, formatFileSize(fileSize), speed)

	// 复制文件权限
	chmodStart := time.Now()
	if err := os.Chmod(dst, srcInfo.Mode()); err != nil {
		return err
	}
	LogInfo("⏱️ 权限复制耗时: %v", time.Since(chmodStart))

	return nil
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