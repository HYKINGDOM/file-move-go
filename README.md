# 文件处理系统 (File Move Go)

一个基于 Go 语言开发的智能文件处理系统，专门用于 Windows 环境下的图片文件管理。系统通过计算文件哈希值来检测重复文件，自动整理和去重，支持实时监控和定时扫描两种工作模式。

## 🚀 功能特性

- **智能去重**: 通过 MD5/SHA256 哈希算法检测重复文件
- **双模式运行**: 支持实时监控模式和定时扫描模式
- **数据库存储**: 使用 PostgreSQL 存储文件元数据信息
- **多格式支持**: 支持所有主流图片格式 (JPG, PNG, GIF, BMP, TIFF, WebP 等)
- **并发处理**: 多线程并发处理，提高处理效率
- **日志记录**: 完整的日志记录和错误处理机制
- **配置灵活**: YAML 配置文件，易于管理和修改
- **Windows 优化**: 专为 Windows 文件系统优化

## 📋 系统要求

- **操作系统**: Windows 10/11 或 Windows Server 2016+
- **Go 版本**: Go 1.21 或更高版本
- **数据库**: PostgreSQL 12 或更高版本
- **内存**: 建议 4GB 以上
- **磁盘空间**: 根据处理文件数量而定

## 🛠️ 安装指南

### 1. 克隆项目

```bash
git clone <repository-url>
cd file-move-go
```

### 2. 安装依赖

```bash
go mod download
```

### 3. 编译程序

```bash
go build -o file-move-go.exe
```

### 4. 配置数据库

确保 PostgreSQL 数据库服务正在运行，并创建相应的数据库和用户。

## ⚙️ 配置说明

### 配置文件结构

系统使用 `config.yaml` 文件进行配置，主要包含以下部分：

```yaml
# 数据库配置
database:
  host: "10.0.203.172"          # 数据库主机地址
  port: 5432                    # 数据库端口
  username: "user_tZGjBb"       # 数据库用户名
  password: "password_fajJed"   # 数据库密码
  database: "user_tZGjBb"       # 数据库名
  ssl_mode: "disable"           # SSL模式

# 文件夹配置
source_folder: "C:\\temp\\source"      # 源文件夹路径
target_folder: "C:\\temp\\target"      # 目标文件夹路径

# 支持的文件类型
supported_types:
  - ".jpg"
  - ".jpeg"
  - ".png"
  - ".gif"
  # ... 更多格式

# 其他配置
hash_algorithm: "sha256"        # 哈希算法
log_level: "info"              # 日志级别
max_file_size: 104857600       # 最大文件大小(100MB)
concurrent_workers: 4          # 并发处理数量
```

### 配置项详解

| 配置项 | 说明 | 默认值 | 必填 |
|--------|------|--------|------|
| `database.host` | 数据库主机地址 | - | ✅ |
| `database.port` | 数据库端口 | 5432 | ✅ |
| `database.username` | 数据库用户名 | - | ✅ |
| `database.password` | 数据库密码 | - | ✅ |
| `database.database` | 数据库名 | - | ✅ |
| `database.ssl_mode` | SSL连接模式 | disable | ❌ |
| `source_folder` | 源文件夹路径 | - | ✅ |
| `target_folder` | 目标文件夹路径 | - | ✅ |
| `supported_types` | 支持的文件扩展名 | 图片格式 | ❌ |
| `hash_algorithm` | 哈希算法 | sha256 | ❌ |
| `log_level` | 日志级别 | info | ❌ |
| `max_file_size` | 最大文件大小(字节) | 100MB | ❌ |
| `concurrent_workers` | 并发处理数量 | 4 | ❌ |

## 🚀 使用指南

### 基本用法

```bash
# 使用默认配置文件启动监控模式
./file-move-go.exe

# 指定配置文件
./file-move-go.exe -config=my-config.yaml

# 启动定时扫描模式，每60秒扫描一次
./file-move-go.exe -mode=timer -interval=60s

# 查看帮助信息
./file-move-go.exe -h
```

### 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-config` | 配置文件路径 | config.yaml |
| `-mode` | 运行模式 (watch/timer) | watch |
| `-interval` | 定时模式扫描间隔 | 30s |

### 运行模式

#### 1. 监控模式 (watch)
- 实时监控源文件夹的文件变化
- 新文件添加时自动处理
- 适合需要实时处理的场景

#### 2. 定时模式 (timer)
- 按指定间隔扫描源文件夹
- 批量处理文件
- 适合定期清理的场景

### 工作流程

1. **文件检测**: 系统检测到新文件或扫描到现有文件
2. **格式验证**: 检查文件是否为支持的图片格式
3. **哈希计算**: 计算文件的哈希值 (MD5/SHA256)
4. **重复检查**: 在数据库中查询是否存在相同哈希值
5. **处理决策**:
   - **新文件**: 移动到目标文件夹并记录到数据库
   - **重复文件**: 删除重复文件
6. **日志记录**: 记录处理结果和统计信息

## 📊 数据库表结构

系统会自动创建以下数据表：

### file_records 表

| 字段名 | 类型 | 说明 | 约束 |
|--------|------|------|------|
| hash | VARCHAR(128) | 文件哈希值 | PRIMARY KEY |
| original_name | VARCHAR(500) | 原始文件名 | NOT NULL |
| file_size | BIGINT | 文件大小(字节) | NOT NULL |
| extension | VARCHAR(50) | 文件扩展名 | NOT NULL |
| created_at | TIMESTAMP | 文件创建时间 | NOT NULL |
| processed_at | TIMESTAMP | 处理时间 | DEFAULT CURRENT_TIMESTAMP |
| source_path | TEXT | 源文件路径 | NOT NULL |
| target_path | TEXT | 目标文件路径 | - |
| hash_type | VARCHAR(20) | 哈希算法类型 | DEFAULT 'sha256' |

### 索引

- `idx_file_records_extension`: 按扩展名索引
- `idx_file_records_processed_at`: 按处理时间索引
- `idx_file_records_file_size`: 按文件大小索引
- `idx_file_records_hash_type`: 按哈希类型索引

## 📝 日志系统

### 日志级别

- **DEBUG**: 详细的调试信息
- **INFO**: 一般信息 (默认)
- **WARN**: 警告信息
- **ERROR**: 错误信息

### 日志输出

- **控制台输出**: 实时显示日志信息
- **文件输出**: 保存到 `logs/` 目录下的日志文件
- **日志轮转**: 按日期自动创建新的日志文件

### 日志格式

```
[2024-01-15 14:30:25] INFO main.go:45 - 文件处理系统启动，运行模式: watch
[2024-01-15 14:30:26] INFO fileprocessor.go:123 - 文件已处理: source.jpg -> target/2024/01/15/source.jpg
```

## 🔧 故障排除

### 常见问题

#### 1. 数据库连接失败
```
错误: 初始化数据库失败: connection refused
```
**解决方案**:
- 检查数据库服务是否启动
- 验证数据库连接配置
- 确认网络连接和防火墙设置

#### 2. 文件夹权限问题
```
错误: 创建目标文件夹失败: access denied
```
**解决方案**:
- 确保程序有足够的文件系统权限
- 以管理员身份运行程序
- 检查文件夹路径是否正确

#### 3. 文件处理失败
```
错误: 计算文件哈希失败: file in use
```
**解决方案**:
- 确保文件没有被其他程序占用
- 等待文件写入完成后再处理
- 检查文件是否损坏

### 性能优化建议

1. **调整并发数量**: 根据系统性能调整 `concurrent_workers`
2. **优化数据库**: 定期维护数据库索引和统计信息
3. **磁盘空间**: 确保目标文件夹有足够的磁盘空间
4. **内存使用**: 监控内存使用情况，必要时重启程序

## 🔒 安全注意事项

1. **数据库安全**: 
   - 使用强密码
   - 限制数据库访问权限
   - 定期备份数据库

2. **文件安全**:
   - 定期备份重要文件
   - 设置适当的文件夹权限
   - 监控磁盘使用情况

3. **网络安全**:
   - 使用防火墙保护数据库端口
   - 考虑使用 SSL 连接数据库

## 📈 监控和维护

### 系统监控

- 监控日志文件大小和磁盘使用情况
- 定期检查数据库性能
- 观察处理统计信息

### 定期维护

- 清理旧的日志文件
- 优化数据库表
- 更新系统配置

## 🤝 贡献指南

欢迎提交 Issue 和 Pull Request 来改进这个项目。

## 📄 许可证

本项目采用 MIT 许可证。

## 📞 技术支持

如有问题或建议，请通过以下方式联系：

- 提交 GitHub Issue
- 发送邮件至项目维护者

---

**注意**: 请在生产环境使用前充分测试，并确保有完整的数据备份。