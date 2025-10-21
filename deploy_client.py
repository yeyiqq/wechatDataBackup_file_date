#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
部署客户端 - 扫描本地文件夹中的zip文件并发送到部署服务器
"""

import os
import sys
import time
import logging
from pathlib import Path
from typing import List, Optional
import requests
import argparse

# 配置日志
def setup_logging(verbose: bool = False):
    """设置日志配置"""
    level = logging.DEBUG if verbose else logging.INFO
    logging.basicConfig(
        level=level,
        format='%(asctime)s - %(levelname)s - %(message)s',
        handlers=[
            logging.StreamHandler(),
            logging.FileHandler('deploy_client.log', encoding='utf-8')
        ]
    )

logger = logging.getLogger(__name__)

class DeployClient:
    """部署客户端类"""
    
    def __init__(self, server_url: str, watch_dir: str = ".", max_retries: int = 3):
        """
        初始化部署客户端
        
        Args:
            server_url: 服务器URL
            watch_dir: 监控的文件夹路径
            max_retries: 最大重试次数
        """
        self.server_url = server_url.rstrip('/')
        self.watch_dir = Path(watch_dir)
        self.max_retries = max_retries
        self.session = requests.Session()
        
        # 设置超时时间
        self.session.timeout = (30, 300)  # 连接超时30秒，读取超时300秒
        
        # 支持的压缩文件扩展名
        self.supported_extensions = {'.zip', '.tar', '.tar.gz', '.tgz', '.tar.bz2', '.tbz2'}
        
        logger.info(f"部署客户端初始化完成")
        logger.info(f"服务器地址: {self.server_url}")
        logger.info(f"监控目录: {self.watch_dir}")
    
    def check_server_health(self) -> bool:
        """检查服务器健康状态"""
        try:
            response = self.session.get(f"{self.server_url}/health")
            response.raise_for_status()
            health_data = response.json()
            logger.info(f"服务器健康状态: {health_data}")
            return True
        except requests.RequestException as e:
            logger.error(f"服务器健康检查失败: {e}")
            return False
    
    def find_archive_files(self) -> List[Path]:
        """查找目录中的压缩文件"""
        if not self.watch_dir.exists():
            logger.error(f"监控目录不存在: {self.watch_dir}")
            return []
        
        archive_files = []
        for file_path in self.watch_dir.rglob('*'):
            if file_path.is_file() and file_path.suffix.lower() in self.supported_extensions:
                # 跳过隐藏文件和临时文件
                if not file_path.name.startswith('.') and not file_path.name.startswith('~'):
                    archive_files.append(file_path)
        
        logger.info(f"找到 {len(archive_files)} 个压缩文件")
        return archive_files
    
    def deploy_file(self, file_path: Path, project_name: Optional[str] = None) -> bool:
        """
        部署单个文件到服务器
        
        Args:
            file_path: 要部署的文件路径
            project_name: 项目名称，如果不提供则使用文件名
            
        Returns:
            部署是否成功
        """
        if not project_name:
            project_name = file_path.stem
        
        logger.info(f"开始部署文件: {file_path.name} -> {project_name}")
        
        for attempt in range(self.max_retries):
            try:
                with open(file_path, 'rb') as f:
                    files = {'file': (file_path.name, f, 'application/octet-stream')}
                    data = {'project_name': project_name} if project_name else {}
                    
                    logger.debug(f"发送请求到: {self.server_url}/deploy")
                    response = self.session.post(
                        f"{self.server_url}/deploy",
                        files=files,
                        data=data
                    )
                    
                    response.raise_for_status()
                    result = response.json()
                    
                    if result.get('success'):
                        logger.info(f"✅ 部署成功: {project_name}")
                        logger.info(f"   部署路径: {result.get('deploy_path', 'N/A')}")
                        return True
                    else:
                        logger.error(f"❌ 部署失败: {result.get('message', '未知错误')}")
                        return False
                        
            except requests.RequestException as e:
                logger.warning(f"第 {attempt + 1} 次尝试失败: {e}")
                if attempt < self.max_retries - 1:
                    wait_time = 2 ** attempt  # 指数退避
                    logger.info(f"等待 {wait_time} 秒后重试...")
                    time.sleep(wait_time)
                else:
                    logger.error(f"❌ 部署失败，已达到最大重试次数: {file_path.name}")
                    return False
            except Exception as e:
                logger.error(f"❌ 部署过程中发生意外错误: {e}")
                return False
        
        return False
    
    def deploy_all_files(self, project_name_prefix: Optional[str] = None) -> dict:
        """
        部署所有找到的压缩文件
        
        Args:
            project_name_prefix: 项目名称前缀
            
        Returns:
            部署结果统计
        """
        if not self.check_server_health():
            logger.error("服务器健康检查失败，无法继续部署")
            return {"success": 0, "failed": 0, "total": 0}
        
        archive_files = self.find_archive_files()
        if not archive_files:
            logger.info("没有找到需要部署的压缩文件")
            return {"success": 0, "failed": 0, "total": 0}
        
        results = {"success": 0, "failed": 0, "total": len(archive_files)}
        
        logger.info(f"开始部署 {len(archive_files)} 个文件...")
        
        for i, file_path in enumerate(archive_files, 1):
            logger.info(f"进度: {i}/{len(archive_files)}")
            
            # 生成项目名称
            project_name = file_path.stem
            if project_name_prefix:
                project_name = f"{project_name_prefix}_{project_name}"
            
            if self.deploy_file(file_path, project_name):
                results["success"] += 1
            else:
                results["failed"] += 1
        
        logger.info(f"部署完成: 成功 {results['success']} 个，失败 {results['failed']} 个，总计 {results['total']} 个")
        return results
    
    def list_deployed_projects(self) -> List[dict]:
        """获取服务器上已部署的项目列表"""
        try:
            response = self.session.get(f"{self.server_url}/deploy")
            response.raise_for_status()
            data = response.json()
            projects = data.get('projects', [])
            
            logger.info(f"服务器上已部署 {len(projects)} 个项目:")
            for project in projects:
                logger.info(f"  - {project['name']} (路径: {project['path']})")
            
            return projects
        except requests.RequestException as e:
            logger.error(f"获取项目列表失败: {e}")
            return []

def main():
    """主函数"""
    parser = argparse.ArgumentParser(description='部署客户端 - 将本地压缩文件发送到部署服务器')
    parser.add_argument('--server', default='http://192.168.1.138:9421', 
                       help='服务器地址 (默认: http://192.168.1.138:9421)')
    parser.add_argument('--dir', default='.', 
                       help='监控的文件夹路径 (默认: 当前目录)')
    parser.add_argument('--project-prefix', 
                       help='项目名称前缀')
    parser.add_argument('--list', action='store_true', 
                       help='列出服务器上已部署的项目')
    parser.add_argument('--health', action='store_true', 
                       help='检查服务器健康状态')
    parser.add_argument('--verbose', '-v', action='store_true', 
                       help='详细输出')
    
    args = parser.parse_args()
    
    # 设置日志
    setup_logging(args.verbose)
    
    # 创建客户端
    client = DeployClient(args.server, args.dir)
    
    try:
        if args.health:
            # 健康检查
            if client.check_server_health():
                print("✅ 服务器健康状态正常")
                sys.exit(0)
            else:
                print("❌ 服务器健康检查失败")
                sys.exit(1)
        
        elif args.list:
            # 列出已部署的项目
            projects = client.list_deployed_projects()
            if not projects:
                print("服务器上没有已部署的项目")
            sys.exit(0)
        
        else:
            # 执行部署
            results = client.deploy_all_files(args.project_prefix)
            
            if results['failed'] > 0:
                print(f"⚠️  部署完成，但有 {results['failed']} 个文件部署失败")
                sys.exit(1)
            else:
                print(f"✅ 所有文件部署成功！")
                sys.exit(0)
    
    except KeyboardInterrupt:
        logger.info("用户中断操作")
        sys.exit(1)
    except Exception as e:
        logger.error(f"程序执行失败: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()
