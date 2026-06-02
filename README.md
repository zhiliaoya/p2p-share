# P2P 局域网/公网文件共享系统

## 项目介绍

这是一个功能完整的点对点（P2P）文件共享系统，支持局域网自动发现和公网直连，实现设备间的文件浏览、下载、上传和目录挂载等功能。

**主要适用于企业同一网段不同设备之间快速传递文件，彻底告别 U 盘拷贝、微信/QQ 传输等繁琐方式，提升办公效率的同时保障数据内网安全。**

### 主要功能

- **双栈网络支持**：同时支持 IPv4 和 IPv6 协议
- **mDNS 自动发现**：局域网内自动发现其他节点，无需手动配置 IP 地址
- **文件管理**：浏览、下载、上传、预览文件（支持图片、视频、音频、文本、PDF 等）
- **断点续传**：支持 HTTP Range 请求，可实现断点续传
- **文件搜索**：在共享目录中搜索文件
- **隧道挂载**：将远程节点的共享目录以只读隧道方式挂载到本地
- **Web 界面**：友好的浏览器界面，支持移动端访问（PC端和移动端自适应）
- **验证码保护**：支持设置验证码保护管理功能（3次错误自动失效）
- **跨平台**：支持 Windows、Linux、macOS

### 技术架构

| 组件 | 技术选型 |
|------|----------|
| 开发语言 | Go 1.26.3 |
| 网络协议 | 自定义 JSON 协议 over TCP |
| 服务发现 | mDNS (Bonjour/Avahi) |
| Web 框架 | gorilla/mux |
| 前端 | 原生 HTML/CSS/JavaScript（嵌入到二进制文件中） |

## 快速开始

### 环境要求

- Go 1.26.3 或更高版本（如需从源码编译）
- 支持 mDNS 的网络环境（大多数局域网默认支持）

### 安装方式

#### 方式一：直接运行（推荐）

下载对应平台的二进制文件（`我只编译了Windows版本的，其他版本需要自行编译`），windows使用命令行`p2p-share.exe -nick 你的昵称`运行即可。

#### 方式二：从源码编译

```bash
# 克隆或下载源码
cd p2p-share

# 下载依赖
go mod download

# 编译
go build -o p2p-share
```

### 使用说明

#### 基本使用

```bash
# 基本使用（必须指定昵称）
p2p-share -nick 你的昵称

# 指定共享目录和 Web 端口
p2p-share -nick 张三 -share D:\共享文件夹 -web 8080

# 启用 IPv6
p2p-share -nick 李四 -ipv6

# 不启动 Web 界面（纯后台服务）
p2p-share -nick 服务端 -noweb
```

#### 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-nick` | 节点昵称（必填） | 无 |
| `-port` | P2P 通信端口 | 0（自动分配） |
| `-share` | 共享目录路径 | `.\共享文件夹` |
| `-web` | Web 界面端口 | 80 |
| `-noweb` | 不启动 Web 界面 | false |
| `-ipv6` | 优先使用 IPv6 | false |
| `-bind` | 绑定地址 | 自动检测 |
| `-public` | 公网 IP/域名 | 空 |

#### Web 界面访问

程序启动后，在浏览器中打开以下任一地址：

- `http://localhost:80`（或你指定的端口）
- `http://本机IP:80`

程序会自动根据设备类型（PC/手机）渲染合适的界面。

#### 验证码设置（可选）

通过 API 设置验证码保护管理功能：

```bash
# 设置验证码（4位数字，仅本机可调用）
curl -X POST http://localhost/api/verification/set \
  -H "Content-Type: application/json" \
  -d '{"code":"1234"}'

# 验证验证码
curl -X POST http://localhost/api/verification/verify \
  -H "Content-Type: application/json" \
  -d '{"code":"1234"}'
```

## API 接口文档

### 公开接口（所有 IP 可访问）

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/local/info` | GET | 获取本地节点信息 |
| `/api/local/browse` | GET | 浏览共享目录（参数：`path`） |
| `/api/local/download` | GET | 下载文件（参数：`path`） |
| `/api/local/preview` | GET | 预览文件（参数：`path`） |
| `/api/local/search` | GET | 搜索文件（参数：`q`） |
| `/api/local/upload` | POST | 上传文件（multipart/form-data） |
| `/api/peers` | GET | 获取所有发现的节点 |
| `/api/peer/{id}` | GET | 获取指定节点信息 |
| `/api/browse/{id}` | GET | 浏览远程节点目录 |
| `/api/download/{id}` | GET | 从远程节点下载文件 |
| `/api/tunnels` | GET | 列出所有隧道 |
| `/api/tunnel/create` | POST | 创建只读隧道 |
| `/api/tunnel/{id}/close` | POST | 关闭隧道 |
| `/api/tunnel/{id}/files` | GET | 浏览隧道内文件 |
| `/api/tunnel/{id}/download` | GET | 从隧道下载文件 |

### 管理员接口（仅本机 IP 可访问）

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/admin/share` | POST | 修改共享目录 |
| `/api/admin/info` | GET | 获取完整节点信息 |
| `/api/admin/ips` | GET | 获取所有本机 IP |
| `/api/verification/set` | POST | 设置验证码 |
| `/api/verification/clear` | POST | 清除验证码 |
| `/api/verification/status` | GET | 获取验证码状态 |

## 项目文件结构
```bash
p2p-share/
├── main.go          # 程序入口、静态文件嵌入
├── node.go          # 节点核心、文件操作、P2P通信
├── discovery.go     # mDNS服务发现
├── protocol.go      # 协议定义、流式传输
├── tunnel.go        # 隧道挂载、虚拟文件系统
├── webui.go         # Web界面、API接口
├── static/          # 静态前端文件（嵌入）
│   ├── index.html   # PC端Web界面
│   └── mobile.html  # 移动端Web界面（手机/平板自适应）
└── go.mod           # 依赖管理
```
### 静态文件说明

| 文件 | 说明 |
|------|------|
| `static/index.html` | PC端主界面，适用于桌面浏览器访问 |
| `static/mobile.html` | 移动端界面，针对手机/平板触摸操作优化，响应式设计 |

程序启动时会自动将嵌入的静态文件解压到 `static/` 目录（如果不存在），并根据请求的 User-Agent 自动选择合适的界面返回。

## 核心模块说明

### 1. 节点模块（node.go）

负责 P2P 网络通信的核心逻辑：
- TCP 服务端/客户端实现
- 请求响应协议处理
- 文件列表、下载、上传操作
- 多网卡绑定支持（IPv4/IPv6）

### 2. 服务发现模块（discovery.go）

基于 mDNS 的局域网节点发现：
- 自动广播本节点信息
- 定期扫描其他节点
- 节点上线/离线状态检测
- 自动清理过期节点

### 3. 协议模块（protocol.go）

定义统一的通信协议：
- JSON 格式消息封装
- 流式数据传输（支持大文件）
- 消息类型定义（ping、list、download、tunnel 等）
- 错误码规范

### 4. 隧道模块（tunnel.go）

实现远程目录挂载：
- 只读隧道建立与管理
- 虚拟文件系统
- 远程文件访问代理

### 5. Web 界面模块（webui.go）

提供 HTTP API 和前端界面：
- RESTful API 接口
- 静态文件服务（支持 PC 和移动端双界面）
- 权限控制（本机/非本机）
- 验证码保护机制

## 常见问题

### Q: 为什么看不到其他节点？

A: 请检查以下事项：
1. 确保所有设备在同一局域网同一网段
2. 检查防火墙是否允许程序使用的端口（P2P端口和Web端口）
3. 确认 mDNS 服务未被禁用（Windows 需启用"功能发现发布"服务）
4. 尝试使用 `-bind` 参数指定正确的网卡IP

### Q: 如何在外网访问？

A: 
1. 使用 `-public` 参数指定公网 IP 或域名
2. 在路由器上配置端口转发（P2P端口和Web端口）
3. 确保防火墙允许相应端口入站

### Q: 上传文件失败？

A: 
1. 检查共享目录是否有写入权限
2. 确认文件大小未超过限制（当前限制 100MB）
3. 检查磁盘空间是否充足

### Q: 隧道挂载后看不到文件？

A: 
1. 确认目标节点在线且共享目录存在
2. 隧道为只读模式，无法写入
3. 检查隧道连接状态（通过 `/api/tunnels` 接口）

### Q: 如何修改 Web 端口？

A: 使用 `-web` 参数，例如 `-web 8080`。

### Q: 忘记验证码怎么办？

A: 由于验证码仅本机可设置和清除，可以：
1. 在本机上调用 `/api/verification/clear` 接口清除
2. 或者重启程序重新设置

### Q: PC 和手机访问界面不同？

A: 程序会自动检测访问设备的 User-Agent，PC 浏览器返回 `index.html`，手机/平板浏览器返回 `mobile.html`，提供最佳的浏览体验。

## 版本历史

| 版本 | 更新内容 |
|------|----------|
| v1.0，初始版本：基础 P2P 文件共享、mDNS 发现、Web 界面，添加 IPv6 支持、多网卡绑定，添加隧道挂载功能、虚拟文件系统，添加文件上传、搜索、预览功能，添加验证码保护、管理员 API，优化大文件传输、断点续传支持，添加移动端界面（mobile.html），支持手机/平板访问 |

## 致谢

**本项目的所有代码均由 DeepSeek 编写。作者仅负责部署、测试和向 DeepSeek 反馈 Bug。**

## 许可证

本项目仅供学习交流使用，请勿用于非法用途。
