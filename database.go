// 数据库管理模块 - 负责PostgreSQL数据库的连接、操作和优化
// 功能：文件记录的增删改查、批量操作、连接池管理、性能优化
// 特性：支持批量插入、连接池优化、事务处理、统计查询
package main

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq" // PostgreSQL驱动
)

// FileInfo 文件信息数据结构体
// 用于存储文件的完整元数据信息，包括哈希值、路径、大小等
type FileInfo struct {
	ID           int64     `db:"id"`            // 数据库自增主键
	Hash         string    `db:"hash"`          // 文件哈希值(用于重复检测，建立唯一索引)
	OriginalName string    `db:"original_name"` // 原始文件名
	OriginalPath string    `db:"original_path"` // 原始文件完整路径
	NewPath      string    `db:"new_path"`      // 移动后的新文件路径
	FileName     string    `db:"file_name"`     // 文件名（不含路径）
	FileSize     int64     `db:"file_size"`     // 文件大小（字节）
	Extension    string    `db:"extension"`     // 文件扩展名（小写）
	CreatedAt    time.Time `db:"created_at"`    // 文件创建时间
	ProcessedAt  time.Time `db:"processed_at"`  // 系统处理时间
	SourcePath   string    `db:"source_path"`   // 源文件路径
	TargetPath   string    `db:"target_path"`   // 目标文件路径
	HashType     string    `db:"hash_type"`     // 哈希算法类型（md5/sha256）
}

// Database 数据库连接管理器
// 提供高性能的数据库操作，支持批量处理和连接池优化
type Database struct {
	db          *sql.DB       // 数据库连接对象
	batchBuffer []FileInfo    // 批量插入缓冲区，提高插入性能
	batchMutex  sync.Mutex    // 批量操作互斥锁，保证线程安全
	batchSize   int           // 批量处理大小，默认1000条
	batchTimer  *time.Timer   // 批量定时器，定期刷新缓冲区
}

// InitDatabase 初始化数据库连接和配置
// 参数：config - 数据库配置信息
// 返回：数据库管理器实例和错误信息
// 功能：建立连接、配置连接池、创建表结构、启动批量处理定时器
func InitDatabase(config DatabaseConfig) (*Database, error) {
	LogInfo("🔗 正在初始化数据库连接...")
	LogDebug("📋 数据库配置: host=%s, port=%d, database=%s, sslmode=%s", 
		config.Host, config.Port, config.Database, config.SSLMode)
	
	// 构建PostgreSQL连接字符串
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.Username, config.Password, config.Database, config.SSLMode)

	// 建立数据库连接
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		LogError("❌ 连接数据库失败: %v", err)
		return nil, fmt.Errorf("连接数据库失败: %v", err)
	}

	// 测试数据库连接可用性
	LogDebug("🔍 正在测试数据库连接...")
	if err := db.Ping(); err != nil {
		db.Close()
		LogError("❌ 数据库连接测试失败: %v", err)
		return nil, fmt.Errorf("数据库连接测试失败: %v", err)
	}

	// 优化连接池参数 - 根据性能需求调整
	LogDebug("⚙️ 配置数据库连接池参数...")
	db.SetMaxOpenConns(50)        // 增加最大连接数以支持更高并发
	db.SetMaxIdleConns(10)        // 增加空闲连接数
	db.SetConnMaxLifetime(10 * time.Minute) // 延长连接生命周期
	db.SetConnMaxIdleTime(5 * time.Minute)  // 设置空闲连接超时

	// 创建数据库管理器实例
	database := &Database{
		db:          db,
		batchBuffer: make([]FileInfo, 0, 100), // 初始化批量缓冲区，预分配100个元素容量
		batchSize:   50,                       // 批量大小设为50，平衡性能和内存使用
	}

	// 创建数据表和索引
	LogDebug("📊 正在创建/验证数据表...")
	if err := database.createTables(); err != nil {
		db.Close()
		LogError("❌ 创建数据表失败: %v", err)
		return nil, fmt.Errorf("创建数据表失败: %v", err)
	}

	// 启动批量处理定时器，定期刷新缓冲区
	LogDebug("⏰ 启动批量处理定时器...")
	database.startBatchTimer()

	LogInfo("✅ 数据库连接成功，连接池配置: MaxOpen=%d, MaxIdle=%d", 50, 10)
	return database, nil
}

// createTables 创建必要的数据表和索引
// 功能：创建file_records表，建立性能优化索引
// 特性：支持哈希唯一性约束、扩展名索引、时间索引
func (d *Database) createTables() error {
	LogDebug("🏗️ 开始创建数据表和索引...")
	
	// 创建文件记录表的SQL语句
	// 包含完整的文件元数据字段和性能优化索引
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS file_records (
		id SERIAL PRIMARY KEY,                                    -- 自增主键
		hash VARCHAR(128) UNIQUE NOT NULL,                        -- 文件哈希值，唯一约束
		original_name VARCHAR(500) NOT NULL,                      -- 原始文件名
		file_size BIGINT NOT NULL,                               -- 文件大小（字节）
		extension VARCHAR(50) NOT NULL,                          -- 文件扩展名
		created_at TIMESTAMP NOT NULL,                           -- 文件创建时间
		processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,        -- 系统处理时间
		source_path TEXT NOT NULL,                               -- 源文件路径
		target_path TEXT,                                        -- 目标文件路径
		hash_type VARCHAR(20) NOT NULL DEFAULT 'sha256'          -- 哈希算法类型
	);

	-- 创建性能优化索引，提高查询效率
	CREATE UNIQUE INDEX IF NOT EXISTS idx_file_records_hash_unique ON file_records(hash);           -- 哈希值唯一索引
	CREATE INDEX IF NOT EXISTS idx_file_records_extension ON file_records(extension);               -- 扩展名索引
	CREATE INDEX IF NOT EXISTS idx_file_records_processed_at ON file_records(processed_at DESC);    -- 处理时间倒序索引
	CREATE INDEX IF NOT EXISTS idx_file_records_file_size ON file_records(file_size DESC);
	CREATE INDEX IF NOT EXISTS idx_file_records_hash_type ON file_records(hash_type);
	
	-- 复合索引优化常见查询
	CREATE INDEX IF NOT EXISTS idx_file_records_ext_size ON file_records(extension, file_size DESC);
	CREATE INDEX IF NOT EXISTS idx_file_records_ext_processed ON file_records(extension, processed_at DESC);
	
	-- 部分索引优化大文件查询
	CREATE INDEX IF NOT EXISTS idx_file_records_large_files ON file_records(file_size DESC) 
		WHERE file_size > 1048576; -- 大于1MB的文件
	`

	_, err := d.db.Exec(createTableSQL)
	if err != nil {
		LogError("❌ 执行创建表SQL失败: %v", err)
		return fmt.Errorf("执行创建表SQL失败: %v", err)
	}

	LogInfo("✅ 数据表创建/验证完成，索引优化完成")
	return nil
}

// FileExistsWithDetails 检查文件哈希是否已存在，返回详细信息
func (db *Database) FileExistsWithDetails(hash string) (bool, *FileInfo, error) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		if duration > 100*time.Millisecond {
			LogWarn("⚠️ 数据库查询耗时较长 (FileExistsWithDetails): %v", duration)
		} else {
			LogDebug("⏱️ 数据库查询耗时 (FileExistsWithDetails): %v", duration)
		}
	}()

	var fileInfo FileInfo
	query := `SELECT id, hash, original_name, original_path, new_path, file_name, 
			  file_size, extension, created_at, processed_at, source_path, 
			  target_path, hash_type FROM file_records WHERE hash = $1 LIMIT 1`
	
	err := db.db.QueryRow(query, hash).Scan(
		&fileInfo.ID, &fileInfo.Hash, &fileInfo.OriginalName, &fileInfo.OriginalPath,
		&fileInfo.NewPath, &fileInfo.FileName, &fileInfo.FileSize, &fileInfo.Extension,
		&fileInfo.CreatedAt, &fileInfo.ProcessedAt, &fileInfo.SourcePath,
		&fileInfo.TargetPath, &fileInfo.HashType,
	)
	
	if err != nil {
		if err == sql.ErrNoRows {
			LogDebug("🔍 文件哈希不存在: %s", hash[:12]+"...")
			return false, nil, nil
		}
		LogError("❌ 查询文件记录失败: %v", err)
		return false, nil, fmt.Errorf("查询文件记录失败: %v", err)
	}
	
	LogDebug("✅ 发现重复文件: %s -> %s", hash[:12]+"...", fileInfo.TargetPath)
	return true, &fileInfo, nil
}

// FileExists 检查文件哈希是否已存在，优先返回target_path，如果为空则返回original_path
func (db *Database) FileExists(hash string) (bool, string, error) {
	exists, fileInfo, err := db.FileExistsWithDetails(hash)
	if err != nil {
		return false, "", err
	}
	
	if !exists {
		return false, "", nil
	}
	
	// 优先返回target_path，如果为空则返回original_path
	existingPath := fileInfo.TargetPath
	if existingPath == "" {
		existingPath = fileInfo.OriginalPath
	}
	
	return true, existingPath, nil
}

// InsertFileRecord 插入单个文件记录（保持向后兼容）
func (db *Database) InsertFileRecord(fileInfo FileInfo) error {
	return db.BatchInsertFileRecord(fileInfo)
}

// BatchInsertFileRecord 批量插入文件记录，提高插入性能
// 参数：fileInfo - 文件信息记录
// 返回：错误信息
// 功能：将文件记录添加到批量缓冲区，达到批量大小时自动刷新
func (db *Database) BatchInsertFileRecord(fileInfo FileInfo) error {
	db.batchMutex.Lock()
	defer db.batchMutex.Unlock()

	// 添加到批量缓冲区
	db.batchBuffer = append(db.batchBuffer, fileInfo)
	LogDebug("📝 添加文件记录到批量缓冲区: %s (缓冲区大小: %d/%d)", 
		fileInfo.FileName, len(db.batchBuffer), db.batchSize)

	// 检查是否需要刷新批量缓冲区
	if len(db.batchBuffer) >= db.batchSize {
		LogDebug("🚀 批量缓冲区已满，开始刷新...")
		if err := db.flushBatch(); err != nil {
			LogError("❌ 批量刷新失败: %v", err)
			return err
		}
		LogInfo("✅ 批量刷新完成，已处理 %d 条记录", db.batchSize)
	}

	// 重置定时器
	if db.batchTimer != nil {
		db.batchTimer.Reset(2 * time.Second) // 2秒后强制执行批量插入
	}

	return nil
}

// flushBatch 执行批量插入操作
func (db *Database) flushBatch() error {
	if len(db.batchBuffer) == 0 {
		return nil
	}

	startTime := time.Now()
	batchCount := len(db.batchBuffer)
	LogInfo("💾 开始批量插入数据库记录: %d 条", batchCount)
	
	defer func() {
		duration := time.Since(startTime)
		if duration > 1*time.Second {
			LogWarn("⚠️ 批量数据库插入耗时较长: %v (记录数: %d)", duration, batchCount)
		} else {
			LogInfo("✅ 批量数据库插入完成: %v (记录数: %d)", duration, batchCount)
		}
	}()

	// 构建批量插入SQL
	query := `
		INSERT INTO file_records (
			hash, original_name, file_size, extension, created_at, 
			processed_at, source_path, target_path, hash_type
		) VALUES `

	// 构建VALUES子句
	values := make([]interface{}, 0, len(db.batchBuffer)*9)
	placeholders := make([]string, 0, len(db.batchBuffer))
	
	for i, fileInfo := range db.batchBuffer {
		placeholderStart := i*9 + 1
		placeholders = append(placeholders, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			placeholderStart, placeholderStart+1, placeholderStart+2, placeholderStart+3,
			placeholderStart+4, placeholderStart+5, placeholderStart+6, placeholderStart+7, placeholderStart+8))
		
		values = append(values,
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
	}

	// 执行批量插入
	finalQuery := query + fmt.Sprintf("%s", placeholders[0])
	for i := 1; i < len(placeholders); i++ {
		finalQuery += ", " + placeholders[i]
	}

	_, err := db.db.Exec(finalQuery, values...)
	if err != nil {
		LogError("❌ 批量插入文件记录失败: %v", err)
		return fmt.Errorf("批量插入文件记录失败: %v", err)
	}

	// 清空缓冲区
	db.batchBuffer = db.batchBuffer[:0]
	LogDebug("🧹 批量缓冲区已清空")
	
	return nil
}

// startBatchTimer 启动批量处理定时器
func (db *Database) startBatchTimer() {
	db.batchTimer = time.AfterFunc(2*time.Second, func() {
		db.batchMutex.Lock()
		defer db.batchMutex.Unlock()
		
		if len(db.batchBuffer) > 0 {
			LogDebug("⏰ 定时器触发批量插入: %d 条记录", len(db.batchBuffer))
			if err := db.flushBatch(); err != nil {
				LogError("❌ 定时批量插入失败: %v", err)
			}
		}
		
		// 重新启动定时器
		db.startBatchTimer()
	})
}

// FlushPendingBatch 强制执行所有待处理的批量操作
func (db *Database) FlushPendingBatch() error {
	db.batchMutex.Lock()
	defer db.batchMutex.Unlock()
	
	return db.flushBatch()
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
	LogDebug("🗑️ 正在删除文件记录: %s", hash[:12]+"...")
	query := "DELETE FROM file_records WHERE hash = $1"
	result, err := db.db.Exec(query, hash)
	if err != nil {
		LogError("❌ 删除文件记录失败: %v", err)
		return fmt.Errorf("删除文件记录失败: %v", err)
	}
	
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		LogError("❌ 获取删除结果失败: %v", err)
		return fmt.Errorf("获取删除结果失败: %v", err)
	}
	
	if rowsAffected > 0 {
		LogInfo("✅ 文件记录已删除: %s", hash[:12]+"...")
	} else {
		LogWarn("⚠️ 未找到要删除的文件记录: %s", hash[:12]+"...")
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
