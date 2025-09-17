// 接口定义文件 - 提供系统各模块的抽象接口
// 功能：定义数据库、文件处理、日志等模块的接口，遵循SOLID原则
// 作者：文件处理系统开发团队
// 版本：v2.0
// 创建时间：2025年
package main

import "time"

// DatabaseInterface 数据库操作接口
// 定义数据库相关的所有操作方法，遵循接口隔离原则
type DatabaseInterface interface {
	// 文件存在性检查接口
	FileExists(hash string) (bool, string, error)
	
	// 文件记录操作接口
	BatchInsertFileRecord(fileInfo FileInfo) error
	InsertFileRecord(fileInfo FileInfo) error
	GetFileRecord(hash string) (*FileInfo, error)
	DeleteFileRecord(hash string) error
	
	// 查询接口
	GetFilesByExtension(extension string, limit int) ([]*FileInfo, error)
	GetStatistics() (map[string]interface{}, error)
	
	// 连接管理接口
	FlushPendingBatch() error
	Close() error
	Ping() error
}

// FileProcessorInterface 文件处理器接口
// 定义文件处理相关的所有操作方法
type FileProcessorInterface interface {
	// 文件处理接口
	ProcessFile(filePath string) error
	ProcessExistingFiles() error
	
	// 统计信息接口
	GetStats() ProcessorStats
	
	// 生命周期管理接口
	Stop()
}

// FileWatcherInterface 文件监控器接口
// 定义文件监控相关的所有操作方法
type FileWatcherInterface interface {
	// 路径管理接口
	AddPath(path string) error
	RemovePath(path string) error
	GetWatchedPaths() []string
	
	// 生命周期管理接口
	Close() error
}

// LoggerInterface 日志记录器接口
// 定义日志记录相关的所有操作方法
type LoggerInterface interface {
	// 基础日志接口
	Debug(format string, args ...interface{})
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})
	Fatal(format string, args ...interface{})
	
	// 日志级别管理接口
	SetLevel(level LogLevel)
	GetLevel() LogLevel
	
	// 生命周期管理接口
	Close() error
}

// ConfigInterface 配置管理接口
// 定义配置相关的所有操作方法
type ConfigInterface interface {
	// 配置加载接口
	LoadConfig(configPath string) error
	
	// 配置验证接口
	Validate() error
	
	// 文件类型检查接口
	IsSupportedFile(filename string) bool
}

// HashCalculatorInterface 哈希计算器接口
// 定义哈希计算相关的操作方法，支持扩展不同的哈希算法
type HashCalculatorInterface interface {
	// 哈希计算接口
	CalculateHash(filePath string) (string, error)
	
	// 算法信息接口
	GetAlgorithm() string
}

// FileOperatorInterface 文件操作接口
// 定义文件系统操作相关的方法
type FileOperatorInterface interface {
	// 文件移动接口
	MoveFile(sourcePath, targetPath string) error
	
	// 文件信息接口
	GetFileInfo(filePath string) (FileInfo, error)
	
	// 目录操作接口
	EnsureDirectory(dirPath string) error
}

// StatisticsInterface 统计信息接口
// 定义统计信息相关的操作方法
type StatisticsInterface interface {
	// 统计数据获取接口
	GetProcessedCount() int64
	GetDuplicateCount() int64
	GetErrorCount() int64
	GetProcessingSpeed() float64
	
	// 统计数据更新接口
	IncrementProcessed()
	IncrementDuplicate()
	IncrementError()
	
	// 统计数据重置接口
	Reset()
}

// PerformanceMonitorInterface 性能监控接口
// 定义性能监控相关的操作方法
type PerformanceMonitorInterface interface {
	// 性能指标记录接口
	RecordOperation(operation string, duration time.Duration)
	
	// 性能指标获取接口
	GetAverageTime(operation string) time.Duration
	GetOperationCount(operation string) int64
	
	// 性能报告接口
	GenerateReport() map[string]interface{}
}