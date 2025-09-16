package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// FileInfo 文件信息结构体
type FileInfo struct {
	ID           int64     `db:"id"`            // 自增主键
	Hash         string    `db:"hash"`          // 文件哈希值(唯一索引)
	OriginalName string    `db:"original_name"` // 原始文件名
	OriginalPath string    `db:"original_path"` // 原始文件路径
	NewPath      string    `db:"new_path"`      // 新文件路径
	FileName     string    `db:"file_name"`     // 文件名
	FileSize     int64     `db:"file_size"`     // 文件大小(字节)
	Extension    string    `db:"extension"`     // 文件扩展名
	CreatedAt    time.Time `db:"created_at"`    // 文件创建时间
	ProcessedAt  time.Time `db:"processed_at"`  // 处理时间
	SourcePath   string    `db:"source_path"`   // 源文件路径
	TargetPath   string    `db:"target_path"`   // 目标文件路径
	HashType     string    `db:"hash_type"`     // 哈希算法类型
}

// Database 数据库连接包装器
type Database struct {
	db *sql.DB
}

// InitDatabase 初始化数据库连接
func InitDatabase(config DatabaseConfig) (*Database, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.Username, config.Password, config.Database, config.SSLMode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %v", err)
	}

	// 测试连接
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库连接测试失败: %v", err)
	}

	// 设置连接池参数
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	database := &Database{db: db}

	// 创建表
	if err := database.createTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("创建数据表失败: %v", err)
	}

	LogInfo("数据库连接成功")
	return database, nil
}

// createTables 创建必要的数据表
func (d *Database) createTables() error {
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS file_records (
		id SERIAL PRIMARY KEY,
		hash VARCHAR(128) UNIQUE NOT NULL,
		original_name VARCHAR(500) NOT NULL,
		file_size BIGINT NOT NULL,
		extension VARCHAR(50) NOT NULL,
		created_at TIMESTAMP NOT NULL,
		processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		source_path TEXT NOT NULL,
		target_path TEXT,
		hash_type VARCHAR(20) NOT NULL DEFAULT 'sha256'
	);

	-- 创建索引以提高查询性能
	CREATE INDEX IF NOT EXISTS idx_file_records_hash ON file_records(hash);
	CREATE INDEX IF NOT EXISTS idx_file_records_extension ON file_records(extension);
	CREATE INDEX IF NOT EXISTS idx_file_records_processed_at ON file_records(processed_at);
	CREATE INDEX IF NOT EXISTS idx_file_records_file_size ON file_records(file_size);
	CREATE INDEX IF NOT EXISTS idx_file_records_hash_type ON file_records(hash_type);
	`

	_, err := d.db.Exec(createTableSQL)
	if err != nil {
		return fmt.Errorf("执行创建表SQL失败: %v", err)
	}

	LogInfo("数据表创建/验证完成")
	return nil
}

// FileExists 检查文件哈希是否已存在
func (db *Database) FileExists(hash string) (bool, string, error) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		LogInfo("⏱️ 数据库查询耗时 (FileExists): %v", duration)
	}()

	var existingPath string
	query := "SELECT target_path FROM file_records WHERE hash = $1 LIMIT 1"
	err := db.db.QueryRow(query, hash).Scan(&existingPath)
	
	if err != nil {
		if err == sql.ErrNoRows {
			return false, "", nil
		}
		return false, "", fmt.Errorf("查询文件记录失败: %v", err)
	}
	
	return true, existingPath, nil
}

// InsertFileRecord 插入文件记录
func (db *Database) InsertFileRecord(fileInfo FileInfo) error {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		LogInfo("⏱️ 数据库插入耗时 (InsertFileRecord): %v", duration)
	}()

	query := `
		INSERT INTO file_records (
			hash, original_name, file_size, extension, created_at, 
			processed_at, source_path, target_path, hash_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	
	_, err := db.db.Exec(query,
		fileInfo.Hash,
		fileInfo.FileName,
		fileInfo.FileSize,
		fileInfo.Extension,
		fileInfo.CreatedAt,
		time.Now(),
		fileInfo.OriginalPath,
		fileInfo.NewPath,
		"sha256",
	)
	
	if err != nil {
		return fmt.Errorf("插入文件记录失败: %v", err)
	}
	
	return nil
}

// GetFileRecord 根据哈希值获取文件记录
func (d *Database) GetFileRecord(hash string) (*FileInfo, error) {
	query := `
	SELECT id, hash, original_name, file_size, extension, created_at, processed_at, source_path, target_path, hash_type
	FROM file_records WHERE hash = $1
	`

	var fileInfo FileInfo
	err := d.db.QueryRow(query, hash).Scan(
		&fileInfo.ID,
		&fileInfo.Hash,
		&fileInfo.OriginalName,
		&fileInfo.FileSize,
		&fileInfo.Extension,
		&fileInfo.CreatedAt,
		&fileInfo.ProcessedAt,
		&fileInfo.SourcePath,
		&fileInfo.TargetPath,
		&fileInfo.HashType,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // 记录不存在
		}
		return nil, fmt.Errorf("查询文件记录失败: %v", err)
	}

	return &fileInfo, nil
}

// GetFilesByExtension 根据扩展名获取文件列表
func (d *Database) GetFilesByExtension(extension string, limit int) ([]*FileInfo, error) {
	query := `
	SELECT id, hash, original_name, file_size, extension, created_at, processed_at, source_path, target_path, hash_type
	FROM file_records 
	WHERE extension = $1 
	ORDER BY processed_at DESC 
	LIMIT $2
	`

	rows, err := d.db.Query(query, extension, limit)
	if err != nil {
		return nil, fmt.Errorf("查询文件列表失败: %v", err)
	}
	defer rows.Close()

	var files []*FileInfo
	for rows.Next() {
		var fileInfo FileInfo
		err := rows.Scan(
			&fileInfo.ID,
			&fileInfo.Hash,
			&fileInfo.OriginalName,
			&fileInfo.FileSize,
			&fileInfo.Extension,
			&fileInfo.CreatedAt,
			&fileInfo.ProcessedAt,
			&fileInfo.SourcePath,
			&fileInfo.TargetPath,
			&fileInfo.HashType,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描文件记录失败: %v", err)
		}
		files = append(files, &fileInfo)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历查询结果失败: %v", err)
	}

	return files, nil
}

// GetStatistics 获取文件统计信息
func (d *Database) GetStatistics() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// 总文件数
	var totalFiles int
	err := d.db.QueryRow("SELECT COUNT(*) FROM file_records").Scan(&totalFiles)
	if err != nil {
		return nil, fmt.Errorf("查询总文件数失败: %v", err)
	}
	stats["total_files"] = totalFiles

	// 总文件大小
	var totalSize sql.NullInt64
	err = d.db.QueryRow("SELECT SUM(file_size) FROM file_records").Scan(&totalSize)
	if err != nil {
		return nil, fmt.Errorf("查询总文件大小失败: %v", err)
	}
	if totalSize.Valid {
		stats["total_size"] = totalSize.Int64
	} else {
		stats["total_size"] = 0
	}

	// 按扩展名统计
	extQuery := `
	SELECT extension, COUNT(*), SUM(file_size) 
	FROM file_records 
	GROUP BY extension 
	ORDER BY COUNT(*) DESC
	`
	rows, err := d.db.Query(extQuery)
	if err != nil {
		return nil, fmt.Errorf("查询扩展名统计失败: %v", err)
	}
	defer rows.Close()

	extStats := make(map[string]map[string]interface{})
	for rows.Next() {
		var ext string
		var count int
		var size sql.NullInt64

		err := rows.Scan(&ext, &count, &size)
		if err != nil {
			return nil, fmt.Errorf("扫描扩展名统计失败: %v", err)
		}

		extInfo := map[string]interface{}{
			"count": count,
		}
		if size.Valid {
			extInfo["size"] = size.Int64
		} else {
			extInfo["size"] = 0
		}
		extStats[ext] = extInfo
	}
	stats["by_extension"] = extStats

	return stats, nil
}

// DeleteFileRecord 删除文件记录
func (db *Database) DeleteFileRecord(hash string) error {
	query := "DELETE FROM file_records WHERE hash = $1"
	result, err := db.db.Exec(query, hash)
	if err != nil {
		return fmt.Errorf("删除文件记录失败: %v", err)
	}
	
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取删除结果失败: %v", err)
	}
	
	if rowsAffected > 0 {
		LogInfo("文件记录已删除: %s", hash[:12]+"...")
	}
	
	return nil
}

// Close 关闭数据库连接
func (d *Database) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// Ping 测试数据库连接
func (d *Database) Ping() error {
	return d.db.Ping()
}
