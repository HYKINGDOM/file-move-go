// 图片文件扫描模块 - 扫描指定目录中的图片文件并插入数据库
package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ImageScanner 图片扫描器结构体
// 负责扫描目录中的图片文件并将信息插入数据库
type ImageScanner struct {
	database      *Database // 数据库连接实例
	targetDir     string    // 目标扫描目录
	imageExts     []string  // 支持的图片文件扩展名
	processCount  int64     // 已处理文件计数
	skipCount     int64     // 跳过文件计数
	errorCount    int64     // 错误文件计数
	deleteCount   int64     // 删除重复文件计数
	removeDupes   bool      // 是否删除重复文件
}

// NewImageScanner 创建新的图片扫描器实例
// 参数:
//   - database: 数据库连接实例
//   - targetDir: 要扫描的目标目录路径
//   - removeDupes: 是否删除重复文件
// 返回:
//   - *ImageScanner: 图片扫描器实例
func NewImageScanner(database *Database, targetDir string, removeDupes bool) *ImageScanner {
	return &ImageScanner{
		database:    database,
		targetDir:   targetDir,
		removeDupes: removeDupes,
		// 支持的图片文件扩展名列表
		imageExts: []string{
			".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".tif",
			".webp", ".svg", ".ico", ".raw", ".cr2", ".nef", ".arw",
			".dng", ".orf", ".rw2", ".pef", ".srw", ".x3f",
		},
		processCount: 0,
		skipCount:    0,
		errorCount:   0,
		deleteCount:  0,
	}
}

// isImageFile 检查文件是否为支持的图片格式
// 参数:
//   - filename: 文件名
// 返回:
//   - bool: 是否为图片文件
func (is *ImageScanner) isImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, supportedExt := range is.imageExts {
		if ext == supportedExt {
			return true
		}
	}
	return false
}

// calculateFileHash 计算文件的SHA256哈希值
// 参数:
//   - filePath: 文件路径
// 返回:
//   - string: 文件哈希值
//   - error: 错误信息
func (is *ImageScanner) calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("计算哈希失败: %v", err)
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// processImageFile 处理单个图片文件
// 参数:
//   - filePath: 图片文件路径
// 返回:
//   - error: 错误信息
func (is *ImageScanner) processImageFile(filePath string) error {
	// 获取文件信息
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		LogError("📁 获取文件信息失败: %s, 错误: %v", filePath, err)
		return err
	}

	// 计算文件哈希值
	hash, err := is.calculateFileHash(filePath)
	if err != nil {
		LogError("🔐 计算文件哈希失败: %s, 错误: %v", filePath, err)
		return err
	}

	// 检查文件是否已存在于数据库中
	exists, existingPath, err := is.database.FileExists(hash)
	if err != nil {
		LogError("🔍 检查文件是否存在失败: %s, 错误: %v", filePath, err)
		return err
	}

	if exists {
		if is.removeDupes {
			// 检查重复文件是否仍然存在于磁盘上
			if _, err := os.Stat(existingPath); os.IsNotExist(err) {
				LogWarn("⚠️ 数据库中的重复文件已不存在，将更新记录: %s", existingPath)
				// 文件不存在，删除旧记录并插入新记录
				if deleteErr := is.database.DeleteFileRecord(hash); deleteErr != nil {
					LogError("❌ 删除无效记录失败: %v", deleteErr)
				}
			} else {
				// 删除重复文件（保留第一个，删除当前文件）
				LogWarn("🗑️ 发现重复文件，正在删除: %s (原文件: %s)", filePath, existingPath)
				err = os.Remove(filePath)
				if err != nil {
					LogError("❌ 删除重复文件失败: %s, 错误: %v", filePath, err)
					is.errorCount++
					return err
				}
				LogInfo("✅ 成功删除重复文件: %s", filePath)
				is.deleteCount++
				return nil
			}
		} else {
			LogWarn("⚠️ 文件已存在于数据库中: %s (已存在路径: %s)", filePath, existingPath)
			is.skipCount++
			return nil
		}
	}

	// 创建文件记录
	record := FileInfo{
		Hash:         hash,
		OriginalName: fileInfo.Name(),
		OriginalPath: filePath,
		NewPath:      filePath, // 对于扫描模式，新路径与原路径相同
		FileName:     fileInfo.Name(),
		FileSize:     fileInfo.Size(),
		Extension:    strings.ToLower(filepath.Ext(fileInfo.Name())),
		CreatedAt:    fileInfo.ModTime(),
		ProcessedAt:  time.Now(),
		SourcePath:   filePath,
		TargetPath:   filePath, // 对于扫描模式，目标路径与源路径相同
		HashType:     "sha256",
	}

	// 插入数据库记录
	err = is.database.BatchInsertFileRecord(record)
	if err != nil {
		LogError("💾 插入数据库记录失败: %s, 错误: %v", filePath, err)
		return err
	}

	LogInfo("✅ 成功处理图片文件: %s (大小: %s)", filePath, formatFileSize(fileInfo.Size()))
	is.processCount++
	return nil
}

// ScanDirectory 扫描目录中的所有图片文件
// 递归遍历目录，处理所有找到的图片文件
// 返回:
//   - error: 错误信息
func (is *ImageScanner) ScanDirectory() error {
	LogInfo("🔍 开始扫描图片文件目录: %s", is.targetDir)
	
	// 检查目标目录是否存在
	if _, err := os.Stat(is.targetDir); os.IsNotExist(err) {
		return fmt.Errorf("目标目录不存在: %s", is.targetDir)
	}

	startTime := time.Now()

	// 递归遍历目录
	err := filepath.Walk(is.targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			LogError("🚫 访问路径失败: %s, 错误: %v", path, err)
			is.errorCount++
			return nil // 继续处理其他文件
		}

		// 跳过目录
		if info.IsDir() {
			return nil
		}

		// 检查是否为图片文件
		if !is.isImageFile(info.Name()) {
			return nil
		}

		// 处理图片文件
		if err := is.processImageFile(path); err != nil {
			is.errorCount++
			// 记录错误但继续处理其他文件
			return nil
		}

		return nil
	})

	if err != nil {
		LogError("🚫 扫描目录失败: %v", err)
		return err
	}

	// 刷新批量插入缓冲区
	if err := is.database.FlushPendingBatch(); err != nil {
		LogError("💾 刷新数据库批量缓冲区失败: %v", err)
		return err
	}

	// 计算处理时间和统计信息
	duration := time.Since(startTime)
	totalFiles := is.processCount + is.skipCount + is.errorCount + is.deleteCount

	LogInfo("🎉 图片文件扫描完成!")
	LogInfo("📊 扫描统计:")
	LogInfo("   📁 扫描目录: %s", is.targetDir)
	LogInfo("   📈 总文件数: %d", totalFiles)
	LogInfo("   ✅ 成功处理: %d", is.processCount)
	LogInfo("   ⚠️ 跳过文件: %d", is.skipCount)
	if is.removeDupes && is.deleteCount > 0 {
		LogInfo("   🗑️ 删除重复: %d", is.deleteCount)
	}
	LogInfo("   ❌ 错误文件: %d", is.errorCount)
	LogInfo("   ⏱️ 处理耗时: %v", duration)
	
	if totalFiles > 0 {
		LogInfo("   🚀 处理速度: %.2f 文件/秒", float64(totalFiles)/duration.Seconds())
	}

	return nil
}

// GetStatistics 获取扫描统计信息
// 返回:
//   - map[string]int64: 统计信息映射
func (is *ImageScanner) GetStatistics() map[string]int64 {
	return map[string]int64{
		"processed": is.processCount,
		"skipped":   is.skipCount,
		"errors":    is.errorCount,
		"deleted":   is.deleteCount,
		"total":     is.processCount + is.skipCount + is.errorCount + is.deleteCount,
	}
}