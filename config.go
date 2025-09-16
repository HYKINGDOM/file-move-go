package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	SSLMode  string `yaml:"ssl_mode"`
}

// Config 应用程序配置
type Config struct {
	Database         DatabaseConfig `yaml:"database"`
	SourceFolder     string         `yaml:"source_folder"`
	TargetFolder     string         `yaml:"target_folder"`
	SupportedTypes   []string       `yaml:"supported_types"`
	HashAlgorithm    string         `yaml:"hash_algorithm"`
	LogLevel         string         `yaml:"log_level"`
	MaxFileSize      int64          `yaml:"max_file_size"` // 最大文件大小(字节)
	ConcurrentWorkers int           `yaml:"concurrent_workers"` // 并发处理数量
}

// LoadConfig 从文件加载配置
func LoadConfig(configPath string) (*Config, error) {
	// 检查配置文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("配置文件不存在: %s", configPath)
	}

	// 读取配置文件
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 解析YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 验证和处理配置
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %v", err)
	}

	// 处理路径
	config.SourceFolder = filepath.Clean(config.SourceFolder)
	config.TargetFolder = filepath.Clean(config.TargetFolder)

	return &config, nil
}

// validate 验证配置的有效性
func (c *Config) validate() error {
	// 验证数据库配置
	if c.Database.Host == "" {
		return fmt.Errorf("数据库主机地址不能为空")
	}
	if c.Database.Port <= 0 || c.Database.Port > 65535 {
		return fmt.Errorf("数据库端口无效: %d", c.Database.Port)
	}
	if c.Database.Username == "" {
		return fmt.Errorf("数据库用户名不能为空")
	}
	if c.Database.Database == "" {
		return fmt.Errorf("数据库名不能为空")
	}

	// 验证文件夹路径
	if c.SourceFolder == "" {
		return fmt.Errorf("源文件夹路径不能为空")
	}
	if c.TargetFolder == "" {
		return fmt.Errorf("目标文件夹路径不能为空")
	}

	// 检查源文件夹是否存在
	if _, err := os.Stat(c.SourceFolder); os.IsNotExist(err) {
		return fmt.Errorf("源文件夹不存在: %s", c.SourceFolder)
	}

	// 验证支持的文件类型
	if len(c.SupportedTypes) == 0 {
		// 设置默认支持的图片类型
		c.SupportedTypes = []string{".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".webp", ".svg"}
	}

	// 标准化文件扩展名（转为小写，确保以.开头）
	for i, ext := range c.SupportedTypes {
		ext = strings.ToLower(ext)
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		c.SupportedTypes[i] = ext
	}

	// 验证哈希算法
	if c.HashAlgorithm == "" {
		c.HashAlgorithm = "sha256" // 默认使用SHA256
	}
	c.HashAlgorithm = strings.ToLower(c.HashAlgorithm)
	if c.HashAlgorithm != "md5" && c.HashAlgorithm != "sha256" {
		return fmt.Errorf("不支持的哈希算法: %s (支持: md5, sha256)", c.HashAlgorithm)
	}

	// 设置默认值
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.MaxFileSize <= 0 {
		c.MaxFileSize = 100 * 1024 * 1024 // 默认100MB
	}
	if c.ConcurrentWorkers <= 0 {
		c.ConcurrentWorkers = 4 // 默认4个并发
	}

	// 设置默认SSL模式
	if c.Database.SSLMode == "" {
		c.Database.SSLMode = "disable"
	}

	return nil
}

// GetDatabaseDSN 获取数据库连接字符串
func (c *Config) GetDatabaseDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Host,
		c.Database.Port,
		c.Database.Username,
		c.Database.Password,
		c.Database.Database,
		c.Database.SSLMode,
	)
}

// IsSupportedFile 检查文件是否为支持的类型
func (c *Config) IsSupportedFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, supportedExt := range c.SupportedTypes {
		if ext == supportedExt {
			return true
		}
	}
	return false
}

// GetLogLevel 获取日志级别
func (c *Config) GetLogLevel() string {
	return strings.ToLower(c.LogLevel)
}