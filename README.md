P2P 局域网/公网文件共享系统
项目介绍
这是一个功能完整的点对点（P2P）文件共享系统，支持局域网自动发现和公网直连，实现设备间的文件浏览、下载、上传和目录挂载等功能。

主要功能
双栈网络支持：同时支持 IPv4 和 IPv6 协议

mDNS 自动发现：局域网内自动发现其他节点，无需手动配置

文件管理：浏览、下载、上传、预览文件（支持图片、视频、音频、文本、PDF 等）

断点续传：支持 HTTP Range 请求，可实现断点续传

文件搜索：在共享目录中搜索文件

隧道挂载：将远程节点的共享目录以只读隧道方式挂载到本地

Web 界面：友好的浏览器界面，支持移动端访问

验证码保护：支持设置验证码保护管理功能（3次错误自动失效）

跨平台：支持 Windows、Linux、macOS

技术架构
开发语言：Go 1.26.3

网络协议：自定义 JSON 协议 over TCP

服务发现：mDNS (Bonjour/Avahi)

Web 框架：gorilla/mux

前端：原生 HTML/CSS/JavaScript（嵌入到二进制文件中）

使用方式
bash
# 基本使用
p2p-share.exe -nick 你的昵称

# 指定共享目录和端口
p2p-share.exe -nick 张三 -share D:\共享文件夹 -web 8080

# 启用 IPv6
p2p-share.exe -nick 李四 -ipv6

# 不启动 Web 界面（纯后台服务）
p2p-share.exe -nick 服务端 -noweb
命令行参数
参数	说明	默认值
-nick	节点昵称（必填）	无
-port	P2P 通信端口	0（自动分配）
-share	共享目录路径	.\共享文件夹
-web	Web 界面端口	80
-noweb	不启动 Web 界面	false
-ipv6	优先使用 IPv6	false
-bind	绑定地址	自动检测
-public	公网 IP/域名	空
项目文件结构
text
p2p-share/
├── main.go          # 程序入口、静态文件嵌入
├── node.go          # 节点核心、文件操作、P2P通信
├── discovery.go     # mDNS服务发现
├── protocol.go      # 协议定义、流式传输
├── tunnel.go        # 隧道挂载、虚拟文件系统
├── webui.go         # Web界面、API接口
├── static/          # 静态前端文件（嵌入）
│   └── index.html   # Web界面
├── go.mod           # 依赖管理
└── launcher.vbs     # Windows 启动脚本
Project Introduction
This is a fully functional peer-to-peer (P2P) file sharing system that supports both LAN auto-discovery and public network direct connections, enabling file browsing, downloading, uploading, and directory mounting between devices.

Main Features
Dual-stack Network Support: Supports both IPv4 and IPv6 protocols

mDNS Auto-discovery: Automatically discovers other nodes on LAN without manual configuration

File Management: Browse, download, upload, and preview files (supports images, videos, audio, text, PDF, etc.)

Resumable Downloads: Supports HTTP Range requests for resumable downloads

File Search: Search for files within the shared directory

Tunnel Mounting: Mount remote node's shared directory locally as a read-only tunnel

Web Interface: User-friendly browser interface with mobile support

Verification Code Protection: Set verification codes to protect admin functions (auto-expires after 3 failures)

Cross-platform: Supports Windows, Linux, and macOS

Technical Architecture
Language: Go 1.26.3

Network Protocol: Custom JSON protocol over TCP

Service Discovery: mDNS (Bonjour/Avahi)

Web Framework: gorilla/mux

Frontend: Native HTML/CSS/JavaScript (embedded into binary)

Usage
bash
# Basic usage
p2p-share.exe -nick YourNickname

# Specify share directory and port
p2p-share.exe -nick John -share D:\SharedFolder -web 8080

# Enable IPv6
p2p-share.exe -nick Jane -ipv6

# Run without Web UI (background service only)
p2p-share.exe -nick Server -noweb
Command Line Arguments
Argument	Description	Default
-nick	Node nickname (required)	None
-port	P2P communication port	0 (auto)
-share	Shared directory path	.\SharedFolder
-web	Web interface port	80
-noweb	Disable Web UI	false
-ipv6	Prefer IPv6	false
-bind	Bind address	auto-detect
-public	Public IP/Domain	empty
Project Structure
text
p2p-share/
├── main.go          # Entry point, static file embedding
├── node.go          # Core node, file operations, P2P communication
├── discovery.go     # mDNS service discovery
├── protocol.go      # Protocol definition, streaming
├── tunnel.go        # Tunnel mounting, virtual filesystem
├── webui.go         # Web interface, API endpoints
├── static/          # Static frontend files (embedded)
│   └── index.html   # Web UI
├── go.mod           # Dependency management
└── launcher.vbs     # Windows launcher script
致谢 / Acknowledgments
本项目的所有代码均由 DeepSeek 编写。作者仅负责部署、测试和向 DeepSeek 反馈 Bug。

All code in this project is written by DeepSeek. The author is only responsible for deployment, testing, and reporting bugs to DeepSeek.
