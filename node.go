package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// NodeConfig 节点配置
type NodeConfig struct {
	Nick     string
	Port     int
	ShareDir string
	LocalIP  string
	PublicIP string
	UseIPv6  bool
}

// NodeInfo 节点信息
type NodeInfo struct {
	ID       string `json:"id"`
	Nick     string `json:"nick"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	ShareDir string `json:"share_dir"`
	OnlineAt string `json:"online_at"`
}

// FileEntry 文件条目
type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	SizeStr string `json:"size_str"`
	ModTime string `json:"mod_time"`
	Type    string `json:"type"`
}

// Node 节点
type Node struct {
	config      NodeConfig
	Info        NodeInfo
	Port        int
	listener    net.Listener   // 主监听器
	listeners   []net.Listener // 所有监听器
	ListenAddrs []string       // 所有监听地址
	mu          sync.RWMutex
	running     bool
	stopCh      chan struct{}
}

// Request 协议请求
type Request struct {
	Method string                 `json:"method"`
	Path   string                 `json:"path"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// Response 协议响应
type Response struct {
	OK     bool        `json:"ok"`
	Error  string      `json:"error,omitempty"`
	Data   interface{} `json:"data,omitempty"`
	NodeID string      `json:"node_id"`
}

func NewNode(config NodeConfig) *Node {
	id := generateNodeID()
	return &Node{
		config: config,
		Info: NodeInfo{
			ID:       id,
			Nick:     config.Nick,
			IP:       config.LocalIP,
			ShareDir: config.ShareDir,
			OnlineAt: time.Now().Format(time.RFC3339),
		},
		ListenAddrs: make([]string, 0),
		stopCh:      make(chan struct{}),
	}
}

func (n *Node) Start() error {
	// 获取所有可用地址
	ips := n.getAllBindIPs()
	
	// 添加默认端口
	port := n.config.Port
	if port == 0 {
		port = 0 // 自动分配
	}
	
	var firstListener net.Listener
	var allListeners []net.Listener
	var allAddrs []string
	
	for _, ip := range ips {
		var addr string
		if strings.Contains(ip, ":") && !strings.Contains(ip, ".") {
			// IPv6 地址需要用方括号
			addr = fmt.Sprintf("[%s]:%d", ip, port)
		} else {
			addr = fmt.Sprintf("%s:%d", ip, port)
		}
		
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("监听 %s 失败: %v", addr, err)
			continue
		}
		
		if firstListener == nil {
			firstListener = listener
		}
		allListeners = append(allListeners, listener)
		allAddrs = append(allAddrs, listener.Addr().String())
	}
	
	// 如果没有指定IP或都失败了，尝试默认监听
	if len(allListeners) == 0 {
		addr := fmt.Sprintf(":%d", port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("无法在任何地址上监听: %v", err)
		}
		firstListener = listener
		allListeners = append(allListeners, listener)
		allAddrs = append(allAddrs, listener.Addr().String())
	}
	
	n.listener = firstListener
	n.listeners = allListeners
	n.ListenAddrs = allAddrs
	n.Port = firstListener.Addr().(*net.TCPAddr).Port
	n.Info.Port = n.Port
	n.running = true
	
	// 为每个监听器启动接受循环
	for _, listener := range n.listeners {
		go n.acceptLoop(listener)
	}
	
	return nil
}

func (n *Node) Stop() {
	n.running = false
	close(n.stopCh)
	
	if n.listener != nil {
		n.listener.Close()
	}
	
	for _, listener := range n.listeners {
		listener.Close()
	}
}

func (n *Node) getAllBindIPs() []string {
	var ips []string
	
	// 添加指定的IP
	if n.config.LocalIP != "" && n.config.LocalIP != "0.0.0.0" {
		ips = append(ips, n.config.LocalIP)
	}
	
	// 获取所有网卡地址
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			ipStr := ipnet.IP.String()
			
			if n.config.UseIPv6 {
				// IPv6模式：只添加IPv6地址
				if ipnet.IP.To4() == nil && ipnet.IP.IsGlobalUnicast() {
					ips = append(ips, ipStr)
				}
			} else {
				// IPv4模式：优先IPv4，但也接受IPv6
				if ipnet.IP.To4() != nil {
					ips = append(ips, ipStr)
				}
			}
		}
	}
	
	// 去重
	seen := make(map[string]bool)
	var unique []string
	for _, ip := range ips {
		if !seen[ip] {
			seen[ip] = true
			unique = append(unique, ip)
		}
	}
	
	if len(unique) == 0 {
		unique = append(unique, "0.0.0.0")
	}
	
	return unique
}

func (n *Node) WebAddrs(webPort int) []string {
	var addrs []string
	
	addrs = append(addrs, fmt.Sprintf("http://localhost:%d", webPort))
	
	netAddrs, _ := net.InterfaceAddrs()
	for _, addr := range netAddrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				addrs = append(addrs, fmt.Sprintf("http://%s:%d", ipnet.IP.String(), webPort))
			} else if ipnet.IP.IsGlobalUnicast() {
				addrs = append(addrs, fmt.Sprintf("http://[%s]:%d", ipnet.IP.String(), webPort))
			}
		}
	}
	
	return addrs
}

func (n *Node) acceptLoop(listener net.Listener) {
	for n.running {
		conn, err := listener.Accept()
		if err != nil {
			if n.running {
				continue
			}
			return
		}
		go n.handleConnection(conn)
	}
}

func (n *Node) handleConnection(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	var req Request
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&req); err != nil {
		return
	}

	resp := n.processRequest(&req)

	encoder := json.NewEncoder(conn)
	encoder.Encode(resp)
}

func (n *Node) processRequest(req *Request) *Response {
	resp := &Response{
		OK:     true,
		NodeID: n.Info.ID,
	}

	switch req.Method {
	case "ping":
		resp.Data = map[string]interface{}{
			"nick":      n.Info.Nick,
			"share_dir": n.Info.ShareDir,
			"online_at": n.Info.OnlineAt,
		}

	case "list":
		files, err := n.listDirectory(req.Path)
		if err != nil {
			resp.OK = false
			resp.Error = err.Error()
			return resp
		}
		resp.Data = map[string]interface{}{
			"path":  req.Path,
			"files": files,
		}

	case "download":
		fullPath := filepath.Join(n.config.ShareDir, req.Path)
		info, err := os.Stat(fullPath)
		if err != nil {
			resp.OK = false
			resp.Error = "文件不存在"
			return resp
		}
		if info.IsDir() {
			resp.OK = false
			resp.Error = "不能下载目录"
			return resp
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			resp.OK = false
			resp.Error = "读取文件失败"
			return resp
		}

		resp.Data = map[string]interface{}{
			"name": filepath.Base(fullPath),
			"size": info.Size(),
			"data": hex.EncodeToString(content),
		}

	default:
		resp.OK = false
		resp.Error = "未知方法: " + req.Method
	}

	return resp
}

func (n *Node) listDirectory(subPath string) ([]FileEntry, error) {
	if subPath == "" {
		subPath = "/"
	}

	fullPath := filepath.Join(n.config.ShareDir, subPath)
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, fmt.Errorf("路径无效")
	}
	
	absShare, _ := filepath.Abs(n.config.ShareDir)
	if !strings.HasPrefix(absPath, absShare) {
		return nil, fmt.Errorf("禁止访问")
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("路径不存在")
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("不是目录")
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}

	var files []FileEntry

	if subPath != "/" && subPath != "" {
		parentPath := filepath.Dir(subPath)
		if parentPath == "." {
			parentPath = "/"
		}
		files = append(files, FileEntry{
			Name:  "..",
			Path:  parentPath,
			IsDir: true,
			Type:  "dir",
		})
	}

	for _, entry := range entries {
		// 跳过隐藏文件
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		
		finfo, _ := entry.Info()
		ext := strings.ToLower(filepath.Ext(entry.Name()))

		fe := FileEntry{
			Name:    entry.Name(),
			Path:    filepath.ToSlash(filepath.Join(subPath, entry.Name())),
			IsDir:   entry.IsDir(),
			Size:    finfo.Size(),
			SizeStr: formatFileSize(finfo.Size()),
			ModTime: finfo.ModTime().Format("2006-01-02 15:04"),
			Type:    getFileTypeName(ext, entry.IsDir()),
		}
		files = append(files, fe)
	}

	return files, nil
}

func (n *Node) ConnectToPeer(peerIP string, peerPort int, method string, path string, params map[string]interface{}) (*Response, error) {
	var addr string
	if strings.Contains(peerIP, ":") && !strings.Contains(peerIP, ".") {
		addr = fmt.Sprintf("[%s]:%d", peerIP, peerPort)
	} else {
		addr = fmt.Sprintf("%s:%d", peerIP, peerPort)
	}
	
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(30 * time.Second))

	req := Request{
		Method: method,
		Path:   path,
		Params: params,
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(&req); err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}

	var resp Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	return &resp, nil
}

func (n *Node) GetFileContent(path string) ([]byte, *Response, error) {
	fullPath := filepath.Join(n.config.ShareDir, path)
	absPath, _ := filepath.Abs(fullPath)
	absShare, _ := filepath.Abs(n.config.ShareDir)
	
	if !strings.HasPrefix(absPath, absShare) {
		return nil, nil, fmt.Errorf("禁止访问")
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("文件不存在")
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("不能下载目录")
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("读取文件失败")
	}

	resp := &Response{
		OK:     true,
		NodeID: n.Info.ID,
		Data: map[string]interface{}{
			"name": filepath.Base(absPath),
			"size": info.Size(),
			"data": hex.EncodeToString(content),
		},
	}

	return content, resp, nil
}

func generateNodeID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	} else if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(size)/1024/1024)
	}
	return fmt.Sprintf("%.1fGB", float64(size)/1024/1024/1024)
}

func getFileTypeName(ext string, isDir bool) string {
	if isDir {
		return "dir"
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".svg":
		return "image"
	case ".mp4", ".webm", ".mov", ".avi", ".mkv":
		return "video"
	case ".mp3", ".wav", ".flac", ".m4a":
		return "audio"
	case ".txt", ".log", ".md", ".json", ".xml", ".csv", ".go", ".py", ".js", ".html", ".css":
		return "text"
	case ".pdf":
		return "pdf"
	case ".zip", ".rar", ".7z", ".tar", ".gz":
		return "archive"
	default:
		return "file"
	}
}

var _ = io.EOF