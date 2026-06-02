package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tunnel 隧道连接
type Tunnel struct {
	ID         string
	RemoteID   string
	RemoteNick string
	RemoteIP   string
	RemotePort int
	RemotePath string // 远程共享的路径
	LocalPath  string // 本地挂载点
	Type       TunnelType
	Status     string // "connecting", "established", "closed", "error"
	CreatedAt  time.Time
	LastActive time.Time

	conn     net.Conn
	mu       sync.RWMutex
	handlers map[string]TunnelHandler

	// 虚拟文件系统
	vfs      *VirtualFS
	stopCh   chan struct{}
	reconnect bool
}

// TunnelHandler 隧道事件处理器
type TunnelHandler func(tunnel *Tunnel, event string, data interface{})

// TunnelManager 隧道管理器
type TunnelManager struct {
	node     *Node
	tunnels  map[string]*Tunnel // tunnelID -> Tunnel
	mu       sync.RWMutex
	listener net.Listener       // 隧道监听器
	port     int
	running  bool
	stopCh   chan struct{}
}

// VirtualFS 虚拟文件系统（用于挂载远程目录）
type VirtualFS struct {
	tunnel     *Tunnel
	rootPath   string
	fileCache  map[string]*VirtualFile
	mu         sync.RWMutex
	lastRefresh time.Time
}

// VirtualFile 虚拟文件
type VirtualFile struct {
	Name    string
	Path    string
	Size    int64
	IsDir   bool
	ModTime time.Time
	Cached  bool
	Data    []byte // 缓存的数据
}

// NewTunnelManager 创建隧道管理器
func NewTunnelManager(node *Node) *TunnelManager {
	return &TunnelManager{
		node:    node,
		tunnels: make(map[string]*Tunnel),
		stopCh:  make(chan struct{}),
	}
}

// Start 启动隧道管理器
func (tm *TunnelManager) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:0", tm.node.Info.IP))
	if err != nil {
		return fmt.Errorf("隧道监听失败: %v", err)
	}

	tm.listener = listener
	tm.port = listener.Addr().(*net.TCPAddr).Port
	tm.running = true

	go tm.acceptLoop()
	log.Printf("隧道管理器已启动，端口: %d", tm.port)
	return nil
}

// Stop 停止隧道管理器
func (tm *TunnelManager) Stop() {
	tm.running = false
	close(tm.stopCh)

	// 关闭所有隧道
	tm.mu.Lock()
	for id, tunnel := range tm.tunnels {
		tunnel.Close()
		delete(tm.tunnels, id)
	}
	tm.mu.Unlock()

	if tm.listener != nil {
		tm.listener.Close()
	}
}

func (tm *TunnelManager) acceptLoop() {
	for tm.running {
		conn, err := tm.listener.Accept()
		if err != nil {
			if tm.running {
				continue
			}
			return
		}
		go tm.handleTunnelConnection(conn)
	}
}

func (tm *TunnelManager) handleTunnelConnection(conn net.Conn) {
	defer conn.Close()

	// 读取隧道建立请求
	msg, err := ReadProtocolMessage(conn)
	if err != nil {
		log.Printf("读取隧道请求失败: %v", err)
		return
	}

	if msg.Type != MsgTypeTunnel {
		// 发送错误
		errMsg := NewProtocolMessage(MsgTypeError, tm.node.Info.ID, tm.node.Info.IP,
			ErrorResponse{Code: ErrCodeBadRequest, Message: "期望隧道建立请求"})
		SendProtocolMessage(conn, errMsg)
		return
	}

	tunnelReq, err := ParsePayload[TunnelRequest](msg)
	if err != nil {
		log.Printf("解析隧道请求失败: %v", err)
		return
	}

	// 验证远程路径
	remotePath := filepath.Join(tm.node.config.ShareDir, tunnelReq.RemotePath)
	absPath, err := filepath.Abs(remotePath)
	if err != nil {
		sendTunnelError(conn, tm.node, "路径无效", ErrCodeBadRequest)
		return
	}

	absShare, _ := filepath.Abs(tm.node.config.ShareDir)
	if !strings.HasPrefix(absPath, absShare) {
		sendTunnelError(conn, tm.node, "禁止访问", ErrCodeForbidden)
		return
	}

	if _, err := os.Stat(absPath); err != nil {
		sendTunnelError(conn, tm.node, "路径不存在", ErrCodeNotFound)
		return
	}

	// 创建隧道
	tunnel := &Tunnel{
		ID:         tunnelReq.TunnelID,
		RemoteID:   msg.SenderID,
		RemoteIP:   msg.SenderIP,
		RemotePath: absPath,
		Type:       tunnelReq.Type,
		Status:     "established",
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
		conn:       conn,
		stopCh:     make(chan struct{}),
	}

	// 创建虚拟文件系统
	tunnel.vfs = NewVirtualFS(tunnel, absPath)

	// 注册隧道
	tm.mu.Lock()
	tm.tunnels[tunnel.ID] = tunnel
	tm.mu.Unlock()

	// 发送成功响应
	resp := NewProtocolMessage(MsgTypeTunnel, tm.node.Info.ID, tm.node.Info.IP,
		TunnelResponse{
			TunnelID:   tunnel.ID,
			Status:     "established",
			RemotePath: absPath,
			Type:       tunnelReq.Type,
			MaxConn:    5,
		})
	SendProtocolMessage(conn, resp)

	log.Printf("隧道已建立: %s -> %s", tunnel.ID, absPath)

	// 处理隧道内的请求
	tunnel.serve()
}

func sendTunnelError(conn net.Conn, node *Node, message string, code int) {
	errMsg := NewProtocolMessage(MsgTypeError, node.Info.ID, node.Info.IP,
		ErrorResponse{Code: code, Message: message})
	SendProtocolMessage(conn, errMsg)
}

// EstablishTunnel 建立到远程节点的隧道
func (tm *TunnelManager) EstablishTunnel(peerIP string, peerPort int, remotePath string, localPath string, tunnelType TunnelType) (*Tunnel, error) {
	// 连接到远程节点
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", peerIP, peerPort), 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("连接远程节点失败: %v", err)
	}

	tunnelID := generateTunnelID()

	// 发送隧道建立请求
	req := NewProtocolMessage(MsgTypeTunnel, tm.node.Info.ID, tm.node.Info.IP,
		TunnelRequest{
			TunnelID:   tunnelID,
			RemotePath: remotePath,
			LocalPath:  localPath,
			Type:       tunnelType,
		})

	if err := SendProtocolMessage(conn, req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送隧道请求失败: %v", err)
	}

	// 等待响应
	resp, err := ReadProtocolMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取隧道响应失败: %v", err)
	}

	if resp.Type == MsgTypeError {
		errResp, _ := ParsePayload[ErrorResponse](resp)
		conn.Close()
		return nil, fmt.Errorf("隧道建立被拒绝: %s", errResp.Message)
	}

	tunnelResp, err := ParsePayload[TunnelResponse](resp)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("解析隧道响应失败: %v", err)
	}

	if tunnelResp.Status != "established" {
		conn.Close()
		return nil, fmt.Errorf("隧道建立失败: %s", tunnelResp.Message)
	}

	// 创建隧道对象
	tunnel := &Tunnel{
		ID:         tunnelID,
		RemoteID:   resp.SenderID,
		RemoteIP:   peerIP,
		RemotePort: peerPort,
		RemotePath: tunnelResp.RemotePath,
		LocalPath:  localPath,
		Type:       tunnelType,
		Status:     "established",
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
		conn:       conn,
		stopCh:     make(chan struct{}),
		handlers:   make(map[string]TunnelHandler),
		reconnect:  true,
	}

	// 创建虚拟文件系统
	tunnel.vfs = NewVirtualFS(tunnel, tunnelResp.RemotePath)

	// 注册隧道
	tm.mu.Lock()
	tm.tunnels[tunnelID] = tunnel
	tm.mu.Unlock()

	log.Printf("隧道已建立: %s -> %s:%d%s", tunnelID, peerIP, peerPort, remotePath)

	return tunnel, nil
}

// GetTunnel 获取隧道
func (tm *TunnelManager) GetTunnel(id string) *Tunnel {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.tunnels[id]
}

// ListTunnels 列出所有隧道
func (tm *TunnelManager) ListTunnels() []*Tunnel {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tunnels := make([]*Tunnel, 0, len(tm.tunnels))
	for _, t := range tm.tunnels {
		tunnels = append(tunnels, t)
	}
	return tunnels
}

// CloseTunnel 关闭隧道
func (tm *TunnelManager) CloseTunnel(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tunnel, exists := tm.tunnels[id]
	if !exists {
		return fmt.Errorf("隧道不存在: %s", id)
	}

	tunnel.Close()
	delete(tm.tunnels, id)
	return nil
}

func (t *Tunnel) serve() {
	defer t.Close()

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		msg, err := ReadProtocolMessage(t.conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("隧道读取错误 [%s]: %v", t.ID, err)
			}
			return
		}

		t.LastActive = time.Now()

		switch msg.Type {
		case MsgTypeList:
			go t.handleList(msg)
		case MsgTypeDownload:
			go t.handleDownload(msg)
		case MsgTypeClose:
			return
		default:
			errMsg := NewProtocolMessage(MsgTypeError, "", "",
				ErrorResponse{Code: ErrCodeNotSupported, Message: "不支持的操作"})
			SendProtocolMessage(t.conn, errMsg)
		}
	}
}

func (t *Tunnel) handleList(msg *ProtocolMessage) {
	listReq, err := ParsePayload[ListRequest](msg)
	if err != nil {
		return
	}

	path := filepath.Join(t.RemotePath, listReq.Path)
	files, err := listFiles(path)
	
	resp := NewProtocolMessage(MsgTypeList, "", "", ListResponse{
		Path:  listReq.Path,
		Files: files,
	})
	
	if err != nil {
		resp = NewProtocolMessage(MsgTypeError, "", "",
			ErrorResponse{Code: ErrCodeInternal, Message: err.Error()})
	}
	
	SendProtocolMessage(t.conn, resp)
}

func (t *Tunnel) handleDownload(msg *ProtocolMessage) {
	dlReq, err := ParsePayload[DownloadRequest](msg)
	if err != nil {
		return
	}

	path := filepath.Join(t.RemotePath, dlReq.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		errResp := NewProtocolMessage(MsgTypeError, "", "",
			ErrorResponse{Code: ErrCodeNotFound, Message: "文件不存在"})
		SendProtocolMessage(t.conn, errResp)
		return
	}

	// 支持偏移
	if dlReq.Offset > 0 && dlReq.Offset < int64(len(data)) {
		data = data[dlReq.Offset:]
	}
	if dlReq.Size > 0 && dlReq.Size < int64(len(data)) {
		data = data[:dlReq.Size]
	}

	resp := NewProtocolMessage(MsgTypeDownload, "", "", DownloadResponse{
		Name:       filepath.Base(path),
		Size:       int64(len(data)),
		TotalSize:  int64(len(data)),
		Data:       hexEncodeToString(data),
		IsComplete: true,
	})

	SendProtocolMessage(t.conn, resp)
}

// Close 关闭隧道
func (t *Tunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.Status == "closed" {
		return
	}

	t.Status = "closed"
	close(t.stopCh)

	// 发送关闭消息
	closeMsg := NewProtocolMessage(MsgTypeClose, "", "", nil)
	SendProtocolMessage(t.conn, closeMsg)

	if t.conn != nil {
		t.conn.Close()
	}

	// 清除虚拟文件系统
	if t.vfs != nil {
		t.vfs.Clear()
	}
}

// On 注册事件处理器
func (t *Tunnel) On(event string, handler TunnelHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handlers[event] = handler
}

// emit 触发事件
func (t *Tunnel) emit(event string, data interface{}) {
	t.mu.RLock()
	handler, exists := t.handlers[event]
	t.mu.RUnlock()

	if exists {
		go handler(t, event, data)
	}
}

// ReadFile 从隧道读取文件
func (t *Tunnel) ReadFile(path string) ([]byte, error) {
	if t.vfs != nil {
		return t.vfs.ReadFile(path)
	}
	return nil, fmt.Errorf("虚拟文件系统未初始化")
}

// ListDir 列出隧道目录
func (t *Tunnel) ListDir(path string) ([]FileEntry, error) {
	if t.vfs != nil {
		return t.vfs.ListDir(path)
	}
	return nil, fmt.Errorf("虚拟文件系统未初始化")
}

// NewVirtualFS 创建虚拟文件系统
func NewVirtualFS(tunnel *Tunnel, rootPath string) *VirtualFS {
	os.MkdirAll(rootPath, 0755)
	return &VirtualFS{
		tunnel:   tunnel,
		rootPath: rootPath,
		fileCache: make(map[string]*VirtualFile),
		lastRefresh: time.Now(),
	}
}

// ListDir 列出虚拟目录
func (vfs *VirtualFS) ListDir(path string) ([]FileEntry, error) {
	fullPath := filepath.Join(vfs.rootPath, path)
	
	// 安全检查
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, fmt.Errorf("路径无效")
	}
	if !strings.HasPrefix(absPath, vfs.rootPath) {
		return nil, fmt.Errorf("禁止访问")
	}

	return listFiles(absPath)
}

// ReadFile 读取虚拟文件
func (vfs *VirtualFS) ReadFile(path string) ([]byte, error) {
	fullPath := filepath.Join(vfs.rootPath, path)
	
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, fmt.Errorf("路径无效")
	}
	if !strings.HasPrefix(absPath, vfs.rootPath) {
		return nil, fmt.Errorf("禁止访问")
	}

	return os.ReadFile(absPath)
}

// Clear 清除缓存
func (vfs *VirtualFS) Clear() {
	vfs.mu.Lock()
	defer vfs.mu.Unlock()
	vfs.fileCache = make(map[string]*VirtualFile)
}

// listFiles 列出目录文件
func listFiles(dirPath string) ([]FileEntry, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var files []FileEntry
	for _, entry := range entries {
		info, _ := entry.Info()
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		
		fe := FileEntry{
			Name:    entry.Name(),
			Path:    entry.Name(),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			SizeStr: formatFileSize(info.Size()),
			ModTime: info.ModTime().Format("2006-01-02 15:04"),
			Type:    getFileTypeName(ext, entry.IsDir()),
		}
		files = append(files, fe)
	}
	return files, nil
}

// generateTunnelID 生成隧道ID
func generateTunnelID() string {
	b := make([]byte, 8)
	readRandom(b)
	return "tun_" + hexEncodeToString(b)
}