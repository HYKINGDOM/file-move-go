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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// FileProcessor 文件处理器，支持智能负载均衡和优化的并发处理
type FileProcessor struct {
	config   *Config
	database *Database
	mutex    sync.RWMutex
	stats    ProcessorStats

	// 智能负载均衡相关
	workerLoad []int64      // 每个工作协程的负载计数
	loadMutex  sync.RWMutex // 负载统计互斥锁

	// 目录缓存相关
	dirCache      map[string]bool // 目录存在性缓存
	dirCacheMutex sync.RWMutex    // 目录缓存互斥锁

	// 队列管理
	queueSize    int64 // 当前队列大小
	maxQueueSize int64 // 最大队列大小

	// 性能统计
	totalProcessTime int64 // 总处理时间（纳秒）
	processedCount   int64 // 已处理文件数
}

// ProcessorStats 处理器统计信息
type ProcessorStats struct {
	ProcessedFiles int64
	MovedFiles     int64
	DeletedFiles   int64
	ErrorCount     int64
	TotalSize      int64
	StartTime      time.Time
}

// NewFileProcessor 创建新的文件处理器，支持智能负载均衡
func NewFileProcessor(config *Config, database *Database) *FileProcessor {
	// 根据系统资源动态调整并发数
	if config.ConcurrentWorkers <= 0 {
		config.ConcurrentWorkers = runtime.NumCPU() * 2 // 默认为CPU核心数的2倍
	}

	return &FileProcessor{
		config:       config,
		database:     database,
		stats:        ProcessorStats{StartTime: time.Now()},
		workerLoad:   make([]int64, config.ConcurrentWorkers),
		dirCache:     make(map[string]bool),
		maxQueueSize: int64(config.ConcurrentWorkers * 100), // 队列大小为工作协程数的100倍
	}
}

// ProcessExistingFiles 处理现有文件，使用优化的并发和队列管理
func (fp *FileProcessor) ProcessExistingFiles() error {
	LogInfo("开始扫描源文件夹: %s", fp.config.SourceFolder)
	LogInfo("配置信息 - 并发工作者: %d, 最大文件大小: %s, 哈希算法: %s",
		fp.config.ConcurrentWorkers,
		formatFileSize(int64(fp.config.MaxFileSize)),
		fp.config.HashAlgorithm)

	// 创建动态调整的通道
	channelSize := fp.config.ConcurrentWorkers * 50 // 动态调整缓冲区大小
	fileChan := make(chan string, channelSize)
	errorChan := make(chan error, fp.config.ConcurrentWorkers)

	// 启动智能工作协程
	var wg sync.WaitGroup
	LogInfo("启动 %d 个智能工作协程 (队列缓冲: %d)", fp.config.ConcurrentWorkers, channelSize)
	for i := 0; i < fp.config.ConcurrentWorkers; i++ {
		wg.Add(1)
		go fp.smartWorker(i, fileChan, errorChan, &wg)
	}

	// 启动负载监控协程
	loadMonitorDone := make(chan bool)
	go fp.monitorWorkerLoad(loadMonitorDone)

	// 启动进度显示协程
	progressDone := make(chan bool)
	go fp.showProgress(progressDone)

	// 遍历文件夹
	LogInfo("开始遍历文件夹: %s", fp.config.SourceFolder)
	totalFiles := 0
	skippedFiles := 0
	processedFiles := 0
	queueFullCount := 0

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

		// 智能队列管理 - 检查队列负载
		currentQueueSize := atomic.LoadInt64(&fp.queueSize)
		if currentQueueSize >= fp.maxQueueSize {
			queueFullCount++
			if queueFullCount%100 == 0 { // 每100次记录一次
				LogWarn("队列负载过高，等待处理: 当前队列=%d, 最大队列=%d", currentQueueSize, fp.maxQueueSize)
			}

			// 等待队列有空间
			for atomic.LoadInt64(&fp.queueSize) >= fp.maxQueueSize {
				time.Sleep(10 * time.Millisecond)
			}
		}

		// 发送到处理通道
		select {
		case fileChan <- path:
			atomic.AddInt64(&fp.queueSize, 1)
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
		progressDone <- true
		loadMonitorDone <- true
		return err
	}

	// 关闭文件通道，等待处理完成
	close(fileChan)
	LogInfo("文件夹遍历完成 - 总文件数: %d, 跳过文件数: %d, 待处理文件数: %d",
		totalFiles, skippedFiles, processedFiles)

	if queueFullCount > 0 {
		LogInfo("队列满载次数: %d (已优化处理)", queueFullCount)
	}

	// 等待所有工作协程完成
	LogInfo("等待所有工作协程完成...")
	wg.Wait()

	// 停止监控协程
	progressDone <- true
	loadMonitorDone <- true

	// 强制执行数据库批量操作
	if err := fp.database.FlushPendingBatch(); err != nil {
		LogError("强制执行批量数据库操作失败: %v", err)
	}

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

// smartWorker 智能工作协程，支持负载均衡
func (fp *FileProcessor) smartWorker(workerID int, fileChan <-chan string, errorChan chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	LogInfo("智能工作协程 #%d 启动", workerID)
	processedCount := 0

	for filePath := range fileChan {
		startTime := time.Now()

		// 减少队列计数
		atomic.AddInt64(&fp.queueSize, -1)

		// 处理文件
		if err := fp.ProcessFile(filePath); err != nil {
			select {
			case errorChan <- fmt.Errorf("工作协程 #%d 处理文件失败 %s: %v", workerID, filePath, err):
			default:
				LogError("工作协程 #%d 处理文件失败 %s: %v", workerID, filePath, err)
			}
			fp.incrementErrorCount()
		}

		// 更新负载统计
		processingTime := time.Since(startTime)
		atomic.AddInt64(&fp.workerLoad[workerID], processingTime.Nanoseconds())
		atomic.AddInt64(&fp.totalProcessTime, processingTime.Nanoseconds())
		atomic.AddInt64(&fp.processedCount, 1)

		processedCount++
	}

	LogInfo("智能工作协程 #%d 完成，处理文件数: %d", workerID, processedCount)
}

// monitorWorkerLoad 监控工作协程负载
func (fp *FileProcessor) monitorWorkerLoad(done <-chan bool) {
	ticker := time.NewTicker(30 * time.Second) // 每30秒监控一次
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fp.logWorkerLoadStats()
		case <-done:
			fp.logWorkerLoadStats() // 最后一次统计
			return
		}
	}
}

// logWorkerLoadStats 记录工作协程负载统计
func (fp *FileProcessor) logWorkerLoadStats() {
	fp.loadMutex.RLock()
	defer fp.loadMutex.RUnlock()

	totalLoad := int64(0)
	maxLoad := int64(0)
	minLoad := int64(^uint64(0) >> 1) // 最大int64值

	for _, load := range fp.workerLoad {
		totalLoad += load
		if load > maxLoad {
			maxLoad = load
		}
		if load < minLoad {
			minLoad = load
		}
	}

	if len(fp.workerLoad) > 0 {
		avgLoad := totalLoad / int64(len(fp.workerLoad))
		processedCount := atomic.LoadInt64(&fp.processedCount)

		if processedCount > 0 {
			avgProcessTime := time.Duration(atomic.LoadInt64(&fp.totalProcessTime) / processedCount)
			LogDebug("📊 负载均衡统计 - 平均负载: %v, 最大负载: %v, 最小负载: %v, 平均处理时间: %v",
				time.Duration(avgLoad), time.Duration(maxLoad), time.Duration(minLoad), avgProcessTime)
		}
	}

	currentQueueSize := atomic.LoadInt64(&fp.queueSize)
	LogDebug("📊 队列状态 - 当前队列: %d, 最大队列: %d, 利用率: %.1f%%",
		currentQueueSize, fp.maxQueueSize, float64(currentQueueSize)/float64(fp.maxQueueSize)*100)
}

// ensureDirectoryExists 确保目录存在，使用缓存优化
func (fp *FileProcessor) ensureDirectoryExists(dirPath string) error {
	// 检查缓存
	fp.dirCacheMutex.RLock()
	if exists, found := fp.dirCache[dirPath]; found && exists {
		fp.dirCacheMutex.RUnlock()
		return nil
	}
	fp.dirCacheMutex.RUnlock()

	// 创建目录
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return err
	}

	// 更新缓存
	fp.dirCacheMutex.Lock()
	fp.dirCache[dirPath] = true
	fp.dirCacheMutex.Unlock()

	return nil
}

// ProcessFile 处理单个文件
func (fp *FileProcessor) ProcessFile(filePath string) error {
	startTime := time.Now()
	defer func() {
		totalDuration := time.Since(startTime)
		LogDebug("⏱️ 文件处理总耗时: %s -> %v", filepath.Base(filePath), totalDuration)
	}()

	// 获取文件信息
	fileInfoStart := time.Now()
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("获取文件信息失败: %v", err)
	}
	LogDebug("⏱️ 获取文件信息耗时: %v", time.Since(fileInfoStart))

	// 计算文件哈希
	hashStart := time.Now()
	hash, err := fp.calculateFileHash(filePath)
	if err != nil {
		return fmt.Errorf("计算文件哈希失败: %v", err)
	}
	hashDuration := time.Since(hashStart)
	LogDebug("⏱️ 哈希计算耗时: %v (文件大小: %s)", hashDuration, formatFileSize(fileInfo.Size()))

	// 检查文件是否已存在
	dbCheckStart := time.Now()
	exists, existingPath, err := fp.database.FileExists(hash)
	if err != nil {
		return fmt.Errorf("数据库查询失败: %v", err)
	}
	LogDebug("⏱️ 数据库查询耗时: %v", time.Since(dbCheckStart))

	if exists {
		// 文件已存在，删除重复文件
		deleteStart := time.Now()
		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("删除重复文件失败: %v", err)
		}
		LogDebug("⏱️ 删除重复文件耗时: %v", time.Since(deleteStart))
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
	LogDebug("⏱️ 文件移动总耗时: %v -> %s", totalDuration, filepath.Base(sourcePath))

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
	LogDebug("⏱️ 数据库插入耗时: %v", time.Since(dbInsertStart))

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
		LogDebug("⏱️ 哈希计算总耗时: %v", totalDuration)
	}()

	// 打开文件
	openStart := time.Now()
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	LogDebug("⏱️ 文件打开耗时: %v", time.Since(openStart))

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

	LogDebug("⏱️ 哈希计算详情: 总耗时=%v, 读取耗时=%v, 文件大小=%s, 读取速度=%.2f MB/s",
		time.Since(startTime), readDuration, formatFileSize(fileSize), speed)

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// moveFileToTarget 将文件移动到目标位置，优先使用原子移动操作
func (fp *FileProcessor) moveFileToTarget(sourcePath, fileName string) (string, error) {
	startTime := time.Now()
	defer func() {
		totalDuration := time.Since(startTime)
		LogDebug("⏱️ 文件移动总耗时: %v -> %s", totalDuration, filepath.Base(sourcePath))
	}()

	// 生成目标路径
	pathGenStart := time.Now()
	ext := strings.ToLower(filepath.Ext(fileName))
	targetDir := filepath.Join(fp.config.TargetFolder, ext[1:]) // 去掉点号
	targetPath := filepath.Join(targetDir, fileName)
	LogDebug("⏱️ 路径生成耗时: %v", time.Since(pathGenStart))

	// 使用缓存的目录创建
	mkdirStart := time.Now()
	if err := fp.ensureDirectoryExists(targetDir); err != nil {
		return "", fmt.Errorf("创建目标目录失败: %v", err)
	}
	LogDebug("⏱️ 目录创建耗时: %v", time.Since(mkdirStart))

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
	if counter > 1 {
		LogDebug("⏱️ 文件冲突检查耗时: %v (重命名次数: %d)", time.Since(conflictCheckStart), counter-1)
	} else {
		LogDebug("⏱️ 文件冲突检查耗时: %v", time.Since(conflictCheckStart))
	}

	// 获取源文件信息用于性能统计
	srcInfo, err := os.Stat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("获取源文件信息失败: %v", err)
	}
	fileSize := srcInfo.Size()

	// 尝试原子移动操作（同分区内的快速移动）
	moveStart := time.Now()
	err = os.Rename(sourcePath, targetPath)
	moveDuration := time.Since(moveStart)

	if err != nil {
		// 原子移动失败，可能是跨分区，使用复制+删除方式
		LogWarn("⚠️ 原子移动失败，使用复制+删除方式: %v", err)

		// 执行文件复制
		copyStart := time.Now()
		if err := fp.copyFile(sourcePath, targetPath); err != nil {
			return "", fmt.Errorf("复制文件失败: %v", err)
		}
		copyDuration := time.Since(copyStart)

		// 计算复制速度
		copySpeed := float64(fileSize) / copyDuration.Seconds() / (1024 * 1024) // MB/s
		LogDebug("⏱️ 文件复制完成: 耗时=%v, 大小=%s, 速度=%.2f MB/s",
			copyDuration, formatFileSize(fileSize), copySpeed)

		// 删除源文件
		deleteStart := time.Now()
		if err := os.Remove(sourcePath); err != nil {
			// 复制成功但删除失败，记录错误但不返回失败
			LogError("⚠️ 删除源文件失败: %v (目标文件已创建: %s)", err, targetPath)
		} else {
			LogDebug("⏱️ 源文件删除耗时: %v", time.Since(deleteStart))
		}

		LogInfo("✅ 跨分区文件移动完成: %s -> %s", filepath.Base(sourcePath), filepath.Base(targetPath))
	} else {
		// 原子移动成功
		moveSpeed := float64(fileSize) / moveDuration.Seconds() / (1024 * 1024) // MB/s
		LogInfo("⚡ 原子移动成功: 耗时=%v, 大小=%s, 速度=%.2f MB/s",
			moveDuration, formatFileSize(fileSize), moveSpeed)
		LogInfo("✅ 同分区文件移动完成: %s -> %s", filepath.Base(sourcePath), filepath.Base(targetPath))
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

// PreCreateDirectories 批量预创建目录结构
func (fp *FileProcessor) PreCreateDirectories() error {
	LogInfo("🚀 开始批量预创建目录结构...")
	startTime := time.Now()

	// 获取所有支持的文件类型
	supportedTypes := fp.config.SupportedTypes
	createdDirs := make([]string, 0, len(supportedTypes))

	// 为每种文件类型创建目录
	for _, ext := range supportedTypes {
		// 去掉扩展名的点号
		dirName := ext[1:]
		targetDir := filepath.Join(fp.config.TargetFolder, dirName)

		// 创建目录
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			LogError("创建目录失败 %s: %v", targetDir, err)
			continue
		}

		// 添加到缓存
		fp.dirCacheMutex.Lock()
		fp.dirCache[targetDir] = true
		fp.dirCacheMutex.Unlock()

		createdDirs = append(createdDirs, targetDir)
		LogInfo("✅ 预创建目录: %s", targetDir)
	}

	duration := time.Since(startTime)
	LogInfo("🎉 批量预创建目录完成: 创建了 %d 个目录，耗时 %v", len(createdDirs), duration)

	return nil
}

// AsyncCreateDirectory 异步创建目录
func (fp *FileProcessor) AsyncCreateDirectory(dirPath string) <-chan error {
	resultChan := make(chan error, 1)

	go func() {
		defer close(resultChan)

		// 检查缓存
		fp.dirCacheMutex.RLock()
		if exists, found := fp.dirCache[dirPath]; found && exists {
			fp.dirCacheMutex.RUnlock()
			resultChan <- nil
			return
		}
		fp.dirCacheMutex.RUnlock()

		// 异步创建目录
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			resultChan <- err
			return
		}

		// 更新缓存
		fp.dirCacheMutex.Lock()
		fp.dirCache[dirPath] = true
		fp.dirCacheMutex.Unlock()

		resultChan <- nil
	}()

	return resultChan
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

	// 同时输出到日志 - 使用DEBUG级别记录详细性能统计
	LogDebug("处理统计: 总计=%d, 移动=%d, 删除=%d, 错误=%d, 总大小=%s, 耗时=%v",
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