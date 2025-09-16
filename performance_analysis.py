#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
文件移动系统性能分析工具
分析日志文件中的性能数据，识别瓶颈并生成报告
"""

import re
import statistics
from collections import defaultdict, Counter
from datetime import datetime
import sys

class PerformanceAnalyzer:
    def __init__(self, log_file_path):
        self.log_file_path = log_file_path
        self.performance_data = defaultdict(list)
        self.file_operations = []
        self.warnings = []
        
    def parse_log_file(self):
        """解析日志文件，提取性能数据"""
        print(f"📊 开始分析日志文件: {self.log_file_path}")
        
        # 性能日志的正则表达式模式
        patterns = {
            'file_copy_detail': r'⏱️ 文件复制详情: 耗时=([0-9.]+)(ms|s), 大小=([0-9.]+) (KB|MB), 速度=([0-9.]+) (MB/s)',
            'file_copy_total': r'⏱️ 文件复制总耗时: ([0-9.]+)(ms|s)',
            'file_copy': r'⏱️ 文件复制耗时: ([0-9.]+)(ms|s)',
            'hash_calculation_detail': r'⏱️ 哈希计算详情: 总耗时=([0-9.]+)(ms|s), 读取耗时=([0-9.]+)(ms|s), 文件大小=([0-9.]+) (KB|MB), 读取速度=([0-9.]+) (MB/s)',
            'hash_calculation_total': r'⏱️ 哈希计算总耗时: ([0-9.]+)(ms|s)',
            'hash_calculation': r'⏱️ 哈希计算耗时: ([0-9.]+)(ms|s)',
            'database_insert': r'⏱️ 数据库插入耗时.*?: ([0-9.]+)(ms|s)',
            'database_query': r'⏱️ 数据库查询耗时.*?: ([0-9.]+)(ms|s)',
            'file_move_total': r'⏱️ 文件移动总耗时: ([0-9.]+)(ms|s) -> (.+)',
            'file_move': r'⏱️ 文件移动耗时: ([0-9.]+)(ms|s) \(文件大小: ([0-9.]+) (KB|MB)\)',
            'directory_creation': r'⏱️ 目录创建耗时: ([0-9.]+)(ms|s)',
            'permission_copy': r'⏱️ 权限复制耗时: ([0-9.]+)(ms|s)',
            'file_delete': r'⏱️ 源文件删除耗时: ([0-9.]+)(ms|s)',
            'file_processing_total': r'⏱️ 文件处理总耗时: (.+) -> ([0-9.]+)(ms|s)',
            'conflict_check': r'⏱️ 文件冲突检查耗时: ([0-9.]+)(ms|s)',
            'file_info': r'⏱️ 获取文件信息耗时: ([0-9.]+)(ms|s)',
            'file_open': r'⏱️ 文件打开耗时: ([0-9.]+)(ms|s)',
            'path_generation': r'⏱️ 路径生成耗时: ([0-9.]+)(ms|s)'
        }
        
        try:
            with open(self.log_file_path, 'r', encoding='utf-8') as f:
                lines = f.readlines()
                
            print(f"📄 日志文件总行数: {len(lines)}")
            
            for line_num, line in enumerate(lines, 1):
                line = line.strip()
                
                # 检查警告信息
                if 'WARN' in line and '处理队列已满' in line:
                    self.warnings.append(line)
                
                # 解析性能数据
                for operation, pattern in patterns.items():
                    match = re.search(pattern, line)
                    if match:
                        self._extract_performance_data(operation, match, line)
                        
        except FileNotFoundError:
            print(f"❌ 错误: 找不到日志文件 {self.log_file_path}")
            sys.exit(1)
        except Exception as e:
            print(f"❌ 解析日志文件时出错: {e}")
            sys.exit(1)
    
    def _extract_performance_data(self, operation, match, line):
        """提取性能数据"""
        try:
            if operation in ['file_copy_detail', 'hash_calculation_detail']:
                # 复杂的性能数据
                time_value = float(match.group(1))
                time_unit = match.group(2)
                size_value = float(match.group(3))
                size_unit = match.group(4)
                speed_value = float(match.group(5))
                
                # 转换为毫秒
                time_ms = time_value if time_unit == 'ms' else time_value * 1000
                # 转换为MB
                size_mb = size_value if size_unit == 'MB' else size_value / 1024
                
                self.performance_data[operation].append({
                    'time_ms': time_ms,
                    'size_mb': size_mb,
                    'speed_mbps': speed_value,
                    'line': line
                })
                
            elif operation in ['file_move', 'file_processing_total']:
                # 包含文件信息的数据
                time_value = float(match.group(1))
                time_unit = match.group(2)
                time_ms = time_value if time_unit == 'ms' else time_value * 1000
                
                if operation == 'file_move':
                    size_value = float(match.group(3))
                    size_unit = match.group(4)
                    size_mb = size_value if size_unit == 'MB' else size_value / 1024
                    
                    self.performance_data[operation].append({
                        'time_ms': time_ms,
                        'size_mb': size_mb,
                        'line': line
                    })
                else:
                    filename = match.group(1)
                    self.performance_data[operation].append({
                        'time_ms': time_ms,
                        'filename': filename,
                        'line': line
                    })
                    
            elif operation == 'file_move_total':
                # 文件移动总耗时
                time_value = float(match.group(1))
                time_unit = match.group(2)
                filename = match.group(3)
                time_ms = time_value if time_unit == 'ms' else time_value * 1000
                
                self.performance_data[operation].append({
                    'time_ms': time_ms,
                    'filename': filename,
                    'line': line
                })
                
            else:
                # 简单的时间数据
                time_value = float(match.group(1))
                time_unit = match.group(2)
                time_ms = time_value if time_unit == 'ms' else time_value * 1000
                
                self.performance_data[operation].append({
                    'time_ms': time_ms,
                    'line': line
                })
                
        except (ValueError, IndexError) as e:
            print(f"⚠️ 解析性能数据时出错: {e}, 行内容: {line}")
    
    def analyze_performance(self):
        """分析性能数据"""
        print("\n" + "="*80)
        print("📈 性能数据统计分析")
        print("="*80)
        
        if not self.performance_data:
            print("❌ 没有找到性能数据")
            return
        
        # 统计各操作的性能数据
        for operation, data_list in self.performance_data.items():
            if not data_list:
                continue
                
            times = [item['time_ms'] for item in data_list]
            count = len(times)
            avg_time = statistics.mean(times)
            median_time = statistics.median(times)
            min_time = min(times)
            max_time = max(times)
            
            print(f"\n🔍 {self._get_operation_name(operation)}")
            print(f"   📊 样本数量: {count}")
            print(f"   ⏱️ 平均耗时: {avg_time:.2f} ms")
            print(f"   📊 中位数耗时: {median_time:.2f} ms")
            print(f"   ⚡ 最短耗时: {min_time:.2f} ms")
            print(f"   🐌 最长耗时: {max_time:.2f} ms")
            
            if count > 1:
                std_dev = statistics.stdev(times)
                print(f"   📈 标准差: {std_dev:.2f} ms")
            
            # 特殊分析
            if operation in ['file_copy_detail', 'hash_calculation_detail']:
                speeds = [item['speed_mbps'] for item in data_list]
                sizes = [item['size_mb'] for item in data_list]
                print(f"   🚀 平均传输速度: {statistics.mean(speeds):.2f} MB/s")
                print(f"   📦 平均文件大小: {statistics.mean(sizes):.2f} MB")
    
    def identify_bottlenecks(self):
        """识别性能瓶颈"""
        print("\n" + "="*80)
        print("🚨 性能瓶颈分析")
        print("="*80)
        
        # 计算各操作的总耗时和平均耗时
        operation_stats = {}
        for operation, data_list in self.performance_data.items():
            if not data_list:
                continue
                
            times = [item['time_ms'] for item in data_list]
            total_time = sum(times)
            avg_time = statistics.mean(times)
            count = len(times)
            
            operation_stats[operation] = {
                'total_time': total_time,
                'avg_time': avg_time,
                'count': count,
                'name': self._get_operation_name(operation)
            }
        
        # 按总耗时排序
        sorted_by_total = sorted(operation_stats.items(), 
                               key=lambda x: x[1]['total_time'], reverse=True)
        
        print("\n🔥 按总耗时排序的操作 (前10名):")
        for i, (operation, stats) in enumerate(sorted_by_total[:10], 1):
            print(f"{i:2d}. {stats['name']}")
            print(f"     总耗时: {stats['total_time']:.2f} ms")
            print(f"     平均耗时: {stats['avg_time']:.2f} ms")
            print(f"     执行次数: {stats['count']}")
            print()
        
        # 按平均耗时排序
        sorted_by_avg = sorted(operation_stats.items(), 
                             key=lambda x: x[1]['avg_time'], reverse=True)
        
        print("\n⏰ 按平均耗时排序的操作 (前10名):")
        for i, (operation, stats) in enumerate(sorted_by_avg[:10], 1):
            print(f"{i:2d}. {stats['name']}")
            print(f"     平均耗时: {stats['avg_time']:.2f} ms")
            print(f"     总耗时: {stats['total_time']:.2f} ms")
            print(f"     执行次数: {stats['count']}")
            print()
        
        return sorted_by_total, sorted_by_avg
    
    def analyze_warnings(self):
        """分析警告信息"""
        print("\n" + "="*80)
        print("⚠️ 警告信息分析")
        print("="*80)
        
        if not self.warnings:
            print("✅ 没有发现警告信息")
            return
        
        print(f"🚨 发现 {len(self.warnings)} 条警告信息")
        print("主要问题: 处理队列已满，导致文件被跳过")
        print("\n建议:")
        print("1. 增加工作协程数量")
        print("2. 优化文件处理速度")
        print("3. 增加队列缓冲区大小")
        
        # 显示部分警告示例
        print(f"\n📝 警告示例 (显示前5条):")
        for i, warning in enumerate(self.warnings[:5], 1):
            filename = warning.split('跳过文件: ')[-1] if '跳过文件: ' in warning else 'Unknown'
            print(f"{i}. {filename}")
    
    def generate_recommendations(self, bottleneck_data):
        """生成优化建议"""
        print("\n" + "="*80)
        print("💡 性能优化建议")
        print("="*80)
        
        sorted_by_total, sorted_by_avg = bottleneck_data
        
        print("\n🎯 主要优化方向:")
        
        # 基于总耗时的建议
        if sorted_by_total:
            top_operation = sorted_by_total[0]
            operation_name = top_operation[1]['name']
            total_time = top_operation[1]['total_time']
            
            print(f"\n1. 优先优化: {operation_name}")
            print(f"   - 当前总耗时: {total_time:.2f} ms")
            
            if 'directory_creation' in top_operation[0]:
                print("   - 建议: 批量创建目录，减少系统调用次数")
                print("   - 建议: 检查目标磁盘性能，考虑使用SSD")
                
            elif 'file_copy' in top_operation[0]:
                print("   - 建议: 使用更大的缓冲区进行文件复制")
                print("   - 建议: 考虑使用并行复制或异步I/O")
                print("   - 建议: 检查网络带宽和磁盘I/O性能")
                
            elif 'permission_copy' in top_operation[0]:
                print("   - 建议: 批量设置文件权限")
                print("   - 建议: 考虑是否必须复制所有权限信息")
                
            elif 'hash_calculation' in top_operation[0]:
                print("   - 建议: 使用更快的哈希算法(如xxHash)")
                print("   - 建议: 增加读取缓冲区大小")
                print("   - 建议: 考虑并行计算哈希值")
        
        print("\n2. 系统级优化:")
        print("   - 增加并发工作协程数量")
        print("   - 优化数据库连接池配置")
        print("   - 使用更快的存储设备(NVMe SSD)")
        print("   - 调整操作系统I/O调度器")
        
        print("\n3. 代码级优化:")
        print("   - 减少不必要的文件系统调用")
        print("   - 使用内存映射文件进行大文件操作")
        print("   - 实现智能重试机制")
        print("   - 添加操作缓存机制")
        
        if self.warnings:
            print("\n4. 队列管理优化:")
            print("   - 增加处理队列大小")
            print("   - 实现动态队列扩容")
            print("   - 添加队列监控和告警")
    
    def _get_operation_name(self, operation):
        """获取操作的中文名称"""
        name_mapping = {
            'file_copy_detail': '文件复制详情',
            'file_copy_total': '文件复制总耗时',
            'file_copy': '文件复制',
            'hash_calculation_detail': '哈希计算详情',
            'hash_calculation_total': '哈希计算总耗时',
            'hash_calculation': '哈希计算',
            'database_insert': '数据库插入',
            'database_query': '数据库查询',
            'file_move_total': '文件移动总耗时',
            'file_move': '文件移动',
            'directory_creation': '目录创建',
            'permission_copy': '权限复制',
            'file_delete': '源文件删除',
            'file_processing_total': '文件处理总耗时',
            'conflict_check': '文件冲突检查',
            'file_info': '获取文件信息',
            'file_open': '文件打开',
            'path_generation': '路径生成'
        }
        return name_mapping.get(operation, operation)
    
    def run_analysis(self):
        """运行完整的性能分析"""
        print("🚀 开始性能分析...")
        
        # 解析日志文件
        self.parse_log_file()
        
        # 分析性能数据
        self.analyze_performance()
        
        # 识别瓶颈
        bottleneck_data = self.identify_bottlenecks()
        
        # 分析警告
        self.analyze_warnings()
        
        # 生成建议
        self.generate_recommendations(bottleneck_data)
        
        print("\n" + "="*80)
        print("✅ 性能分析完成!")
        print("="*80)

def main():
    log_file = "m:/data/file-move-go/logs/file-move-2025-09-16.log"
    
    analyzer = PerformanceAnalyzer(log_file)
    analyzer.run_analysis()

if __name__ == "__main__":
    main()