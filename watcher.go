package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher 文件监控器
type FileWatcher struct {
	watcher   *fsnotify.Watcher
	processor *FileProcessor
	config    *Config
	done      chan bool
}

// NewFileWatcher 创建新的文件监控器
func NewFileWatcher(watchPath string, processor *FileProcessor) (*FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建文件监控器失败: %v", err)
	}

	fw := &FileWatcher{
		watcher:   watcher,
		processor: processor,
		config:    processor.config,
		done:      make(chan bool),
	}

	// 添加监控路径
	err = watcher.Add(watchPath)
	if err != nil {
		watcher.Close()
		return nil, fmt.Errorf("添加监控路径失败: %v", err)
	}

	// 递归添加子目录监控
	err = filepath.Walk(watchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("访问路径失败 %s: %v", path, err)
			return nil // 继续处理其他路径
		}

		if info.IsDir() && path != watchPath {
			if err := watcher.Add(path); err != nil {
				log.Printf("添加子目录监控失败 %s: %v", path, err)
			} else {
				log.Printf("已添加目录监控: %s", path)
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("遍历子目录失败: %v", err)
	}

	// 启动监控协程
	go fw.watchLoop()

	log.Printf("文件监控器已启动，监控路径: %s", watchPath)
	return fw, nil
}

// watchLoop 监控循环
func (fw *FileWatcher) watchLoop() {
	// 用于防抖的映射，避免重复处理同一文件的多个事件
	debounceMap := make(map[string]*time.Timer)
	debounceDuration := 2 * time.Second

	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				log.Println("文件监控器事件通道已关闭")
				return
			}

			fw.handleEvent(event, debounceMap, debounceDuration)

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				log.Println("文件监控器错误通道已关闭")
				return
			}
			log.Printf("文件监控器错误: %v", err)

		case <-fw.done:
			log.Println("文件监控器收到停止信号")
			return
		}
	}
}

// handleEvent 处理文件系统事件
func (fw *FileWatcher) handleEvent(event fsnotify.Event, debounceMap map[string]*time.Timer, debounceDuration time.Duration) {
	// 记录事件（调试用）
	log.Printf("文件事件: %s %s", event.Op.String(), event.Name)

	// 检查是否为支持的文件类型
	if !fw.config.IsSupportedFile(filepath.Base(event.Name)) {
		return
	}

	// 只处理创建和写入事件
	if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
		// 检查是否为文件（而不是目录）
		if info, err := os.Stat(event.Name); err == nil && !info.IsDir() {
			fw.debounceFileProcessing(event.Name, debounceMap, debounceDuration)
		}
	}

	// 处理目录创建事件，添加新目录到监控
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			if err := fw.watcher.Add(event.Name); err != nil {
				log.Printf("添加新目录监控失败 %s: %v", event.Name, err)
			} else {
				log.Printf("已添加新目录监控: %s", event.Name)
			}
		}
	}

	// 处理目录删除事件，从监控中移除
	if event.Op&fsnotify.Remove == fsnotify.Remove {
		// fsnotify会自动处理已删除路径的清理，但我们可以记录一下
		log.Printf("路径已删除: %s", event.Name)
	}
}

// debounceFileProcessing 防抖处理文件
func (fw *FileWatcher) debounceFileProcessing(filePath string, debounceMap map[string]*time.Timer, debounceDuration time.Duration) {
	// 取消之前的定时器（如果存在）
	if timer, exists := debounceMap[filePath]; exists {
		timer.Stop()
	}

	// 创建新的定时器
	debounceMap[filePath] = time.AfterFunc(debounceDuration, func() {
		// 从防抖映射中删除
		delete(debounceMap, filePath)

		// 处理文件
		fw.processFileWithRetry(filePath)
	})
}

// processFileWithRetry 带重试的文件处理
func (fw *FileWatcher) processFileWithRetry(filePath string) {
	maxRetries := 3
	retryDelay := 1 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// 检查文件是否仍然存在
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			log.Printf("文件已不存在，跳过处理: %s", filePath)
			return
		}

		// 检查文件是否可以打开（可能正在被写入）
		if !fw.isFileReady(filePath) {
			if attempt < maxRetries {
				log.Printf("文件可能正在被写入，等待重试 (%d/%d): %s", attempt, maxRetries, filePath)
				time.Sleep(retryDelay)
				continue
			} else {
				log.Printf("文件在多次重试后仍不可用，跳过: %s", filePath)
				return
			}
		}

		// 处理文件
		if err := fw.processor.ProcessFile(filePath); err != nil {
			if attempt < maxRetries {
				log.Printf("处理文件失败，将重试 (%d/%d): %s, 错误: %v", attempt, maxRetries, filePath, err)
				time.Sleep(retryDelay)
				continue
			} else {
				log.Printf("处理文件最终失败: %s, 错误: %v", filePath, err)
			}
		} else {
			log.Printf("文件处理成功: %s", filePath)
			return
		}
	}
}

// isFileReady 检查文件是否准备好被处理
func (fw *FileWatcher) isFileReady(filePath string) bool {
	// 尝试以独占模式打开文件
	file, err := os.OpenFile(filePath, os.O_RDONLY, 0)
	if err != nil {
		// 如果无法打开，可能文件正在被写入
		return false
	}
	defer file.Close()

	// 获取文件信息
	info, err := file.Stat()
	if err != nil {
		return false
	}

	// 检查文件大小是否合理
	if info.Size() == 0 {
		return false // 空文件可能还在写入
	}

	// 检查文件是否超过最大大小限制
	if info.Size() > fw.config.MaxFileSize {
		log.Printf("文件超过大小限制，跳过: %s (大小: %d 字节)", filePath, info.Size())
		return false
	}

	return true
}

// Close 关闭文件监控器
func (fw *FileWatcher) Close() error {
	log.Println("正在关闭文件监控器...")
	
	// 发送停止信号
	select {
	case fw.done <- true:
	default:
	}

	// 关闭监控器
	if fw.watcher != nil {
		return fw.watcher.Close()
	}

	log.Println("文件监控器已关闭")
	return nil
}

// GetWatchedPaths 获取当前监控的路径列表
func (fw *FileWatcher) GetWatchedPaths() []string {
	if fw.watcher == nil {
		return nil
	}

	return fw.watcher.WatchList()
}

// AddPath 添加新的监控路径
func (fw *FileWatcher) AddPath(path string) error {
	if fw.watcher == nil {
		return fmt.Errorf("监控器未初始化")
	}

	err := fw.watcher.Add(path)
	if err != nil {
		return fmt.Errorf("添加监控路径失败: %v", err)
	}

	log.Printf("已添加监控路径: %s", path)
	return nil
}

// RemovePath 移除监控路径
func (fw *FileWatcher) RemovePath(path string) error {
	if fw.watcher == nil {
		return fmt.Errorf("监控器未初始化")
	}

	err := fw.watcher.Remove(path)
	if err != nil {
		return fmt.Errorf("移除监控路径失败: %v", err)
	}

	log.Printf("已移除监控路径: %s", path)
	return nil
}

// isHiddenFile 检查是否为隐藏文件
func isHiddenFile(filename string) bool {
	return strings.HasPrefix(filepath.Base(filename), ".")
}

// isTempFile 检查是否为临时文件
func isTempFile(filename string) bool {
	base := filepath.Base(filename)
	return strings.HasPrefix(base, "~") || 
		   strings.HasSuffix(base, ".tmp") || 
		   strings.HasSuffix(base, ".temp") ||
		   strings.Contains(base, ".part")
}