// 日志管理模块 - 提供多级别、多输出的日志记录功能
// 功能：支持DEBUG/INFO/WARN/ERROR级别，控制台和文件双输出
// 特性：自动日志轮转、格式化输出、错误处理、panic恢复
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// LogLevel 日志级别枚举
// 定义系统支持的日志级别，从低到高：DEBUG < INFO < WARN < ERROR
type LogLevel int

const (
	DEBUG LogLevel = iota // 调试级别 - 详细的调试信息
	INFO                  // 信息级别 - 一般的程序运行信息
	WARN                  // 警告级别 - 潜在问题的警告信息
	ERROR                 // 错误级别 - 错误和异常信息
)

// String 返回日志级别的字符串表示
// 用于日志输出时显示可读的级别名称
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger 自定义日志记录器
// 提供多级别日志记录，支持控制台和文件双输出
type Logger struct {
	level      LogLevel    // 当前日志级别，低于此级别的日志将被过滤
	logger     *log.Logger // 底层日志记录器
	file       *os.File    // 日志文件句柄
	enableFile bool        // 是否启用文件输出
}

// NewLogger 创建新的日志记录器
// 参数：level - 日志级别字符串，enableFileLog - 是否启用文件日志，logDir - 日志目录
// 返回：日志记录器实例和错误信息
// 功能：初始化日志记录器，配置输出目标，创建日志文件
func NewLogger(level string, enableFileLog bool, logDir string) (*Logger, error) {
	logLevel := parseLogLevel(level)
	
	// 配置日志输出目标
	var writers []io.Writer
	writers = append(writers, os.Stdout) // 总是输出到控制台

	var logFile *os.File
	if enableFileLog {
		// 确保日志目录存在
		if logDir == "" {
			logDir = "logs" // 默认日志目录
		}
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, fmt.Errorf("创建日志目录失败: %v", err)
		}

		// 创建日志文件
		logFileName := fmt.Sprintf("file-move-%s.log", time.Now().Format("2006-01-02"))
		logFilePath := filepath.Join(logDir, logFileName)
		
		var err error
		logFile, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("创建日志文件失败: %v", err)
		}
		writers = append(writers, logFile)
	}

	// 创建多重写入器
	multiWriter := io.MultiWriter(writers...)
	
	// 创建标准日志记录器
	logger := log.New(multiWriter, "", 0) // 不使用默认前缀，我们自己格式化

	return &Logger{
		level:      logLevel,
		logger:     logger,
		file:       logFile,
		enableFile: enableFileLog,
	}, nil
}

// parseLogLevel 解析日志级别字符串
func parseLogLevel(level string) LogLevel {
	switch strings.ToLower(level) {
	case "debug":
		return DEBUG
	case "info":
		return INFO
	case "warn", "warning":
		return WARN
	case "error":
		return ERROR
	default:
		return INFO
	}
}

// formatMessage 格式化日志消息
func (l *Logger) formatMessage(level LogLevel, msg string) string {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	
	// 获取调用者信息
	_, file, line, ok := runtime.Caller(3) // 跳过3层调用栈
	var caller string
	if ok {
		caller = fmt.Sprintf("%s:%d", filepath.Base(file), line)
	} else {
		caller = "unknown"
	}

	return fmt.Sprintf("[%s] %s %s - %s", 
		timestamp, 
		level.String(), 
		caller, 
		msg)
}

// shouldLog 检查是否应该记录该级别的日志
func (l *Logger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

// Debug 记录调试日志
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.shouldLog(DEBUG) {
		msg := fmt.Sprintf(format, args...)
		l.logger.Println(l.formatMessage(DEBUG, msg))
	}
}

// Info 记录信息日志
func (l *Logger) Info(format string, args ...interface{}) {
	if l.shouldLog(INFO) {
		msg := fmt.Sprintf(format, args...)
		l.logger.Println(l.formatMessage(INFO, msg))
	}
}

// Warn 记录警告日志
func (l *Logger) Warn(format string, args ...interface{}) {
	if l.shouldLog(WARN) {
		msg := fmt.Sprintf(format, args...)
		l.logger.Println(l.formatMessage(WARN, msg))
	}
}

// Error 记录错误日志
func (l *Logger) Error(format string, args ...interface{}) {
	if l.shouldLog(ERROR) {
		msg := fmt.Sprintf(format, args...)
		l.logger.Println(l.formatMessage(ERROR, msg))
	}
}

// Fatal 记录致命错误日志并退出程序
func (l *Logger) Fatal(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.logger.Println(l.formatMessage(ERROR, msg))
	l.Close()
	os.Exit(1)
}

// Close 关闭日志记录器
func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// SetLevel 设置日志级别
func (l *Logger) SetLevel(level string) {
	l.level = parseLogLevel(level)
}

// GetLevel 获取当前日志级别
func (l *Logger) GetLevel() LogLevel {
	return l.level
}

// 全局日志记录器实例
var globalLogger *Logger

// InitGlobalLogger 初始化全局日志记录器
func InitGlobalLogger(config *Config) error {
	var err error
	globalLogger, err = NewLogger(config.GetLogLevel(), true, "logs")
	if err != nil {
		return fmt.Errorf("初始化全局日志记录器失败: %v", err)
	}

	// 替换标准日志记录器
	log.SetOutput(io.Discard) // 禁用标准日志输出
	
	globalLogger.Info("全局日志记录器已初始化，级别: %s", config.GetLogLevel())
	return nil
}

// GetGlobalLogger 获取全局日志记录器
func GetGlobalLogger() *Logger {
	if globalLogger == nil {
		// 如果全局日志记录器未初始化，创建一个默认的
		var err error
		globalLogger, err = NewLogger("info", false, "")
		if err != nil {
			panic(fmt.Sprintf("创建默认日志记录器失败: %v", err))
		}
	}
	return globalLogger
}

// 便捷的全局日志函数
func LogDebug(format string, args ...interface{}) {
	GetGlobalLogger().Debug(format, args...)
}

func LogInfo(format string, args ...interface{}) {
	GetGlobalLogger().Info(format, args...)
}

func LogWarn(format string, args ...interface{}) {
	GetGlobalLogger().Warn(format, args...)
}

func LogError(format string, args ...interface{}) {
	GetGlobalLogger().Error(format, args...)
}

func LogFatal(format string, args ...interface{}) {
	GetGlobalLogger().Fatal(format, args...)
}

// ErrorHandler 错误处理器
type ErrorHandler struct {
	logger *Logger
}

// NewErrorHandler 创建新的错误处理器
func NewErrorHandler(logger *Logger) *ErrorHandler {
	return &ErrorHandler{
		logger: logger,
	}
}

// Handle 处理错误
func (eh *ErrorHandler) Handle(err error, context string) {
	if err != nil {
		eh.logger.Error("%s: %v", context, err)
	}
}

// HandleWithCallback 处理错误并执行回调
func (eh *ErrorHandler) HandleWithCallback(err error, context string, callback func(error)) {
	if err != nil {
		eh.logger.Error("%s: %v", context, err)
		if callback != nil {
			callback(err)
		}
	}
}

// HandleFatal 处理致命错误
func (eh *ErrorHandler) HandleFatal(err error, context string) {
	if err != nil {
		eh.logger.Fatal("%s: %v", context, err)
	}
}

// WrapError 包装错误并添加上下文
func WrapError(err error, context string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", context, err)
	}
	return nil
}

// RecoverPanic 恢复panic并记录日志
func RecoverPanic(logger *Logger) {
	if r := recover(); r != nil {
		// 获取堆栈信息
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		stackTrace := string(buf[:n])
		
		logger.Error("程序发生panic: %v\n堆栈信息:\n%s", r, stackTrace)
	}
}

// SafeExecute 安全执行函数，捕获panic
func SafeExecute(fn func() error, logger *Logger, context string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			stackTrace := string(buf[:n])
			
			err = fmt.Errorf("panic in %s: %v\n堆栈信息:\n%s", context, r, stackTrace)
			logger.Error("%v", err)
		}
	}()
	
	return fn()
}