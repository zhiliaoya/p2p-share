package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// WebUI Web界面
type WebUI struct {
	node      *Node
	discovery *Discovery
	tm        *TunnelManager
	port      int
	shareDir  string
	router    *mux.Router
	
	// 验证码相关字段
	verificationCode string    // 存储验证码
	codeMutex        sync.RWMutex // 验证码读写锁
	failCount        int       // 当前失败次数
	lastFailTime     time.Time // 上次失败时间
}

// 验证码响应结构
type verificationResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Tip   string `json:"tip,omitempty"`
}

func NewWebUI(node *Node, discovery *Discovery, port int, shareDir string) *WebUI {
	tm := NewTunnelManager(node)
	tm.Start()

	ui := &WebUI{
		node:      node,
		discovery: discovery,
		tm:        tm,
		port:      port,
		shareDir:  shareDir,
		router:    mux.NewRouter(),
	}
	ui.setupRoutes()
	return ui
}

func (ui *WebUI) Start() error {
	addr := fmt.Sprintf("0.0.0.0:%d", ui.port)
	fmt.Printf("Web界面已启动: http://%s:%d\n", ui.node.Info.IP, ui.port)
	fmt.Printf("隧道端口: %d\n", ui.tm.port)
	return http.ListenAndServe(addr, ui.router)
}

func (ui *WebUI) Stop() {
	if ui.tm != nil {
		ui.tm.Stop()
	}
}

// ========================
// 验证码相关方法
// ========================

// 设置验证码（仅本机可调用）
func (ui *WebUI) handleSetVerificationCode(w http.ResponseWriter, r *http.Request) {
	// 验证是否本机访问
	if !ui.isLocalRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "仅本机可设置验证码",
			Tip:   "请在本机上调用此接口",
		})
		return
	}

	// 解析请求
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "无效的请求格式",
		})
		return
	}

	// 验证码必须是4位数字
	if len(req.Code) != 4 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "验证码必须是4位数字",
		})
		return
	}
	
	for _, c := range req.Code {
		if c < '0' || c > '9' {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(verificationResponse{
				OK:    false,
				Error: "验证码只能包含数字",
			})
			return
		}
	}

	// 存储验证码并重置失败计数
	ui.codeMutex.Lock()
	ui.verificationCode = req.Code
	ui.failCount = 0
	ui.lastFailTime = time.Time{}
	ui.codeMutex.Unlock()

	fmt.Printf("[验证码] 已设置新的验证码: %s (仅本机可验证)\n", req.Code)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "验证码已设置成功",
		"code":    req.Code,
		"tip":     "验证码将在3次错误尝试后自动清除",
	})
}

// 验证验证码（所有人可访问，3次错误后自动删除）
func (ui *WebUI) handleVerifyCode(w http.ResponseWriter, r *http.Request) {
	// 解析请求
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "无效的请求格式",
		})
		return
	}

	ui.codeMutex.Lock()
	defer ui.codeMutex.Unlock()

	// 检查是否有验证码
	if ui.verificationCode == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "未设置验证码，请先设置验证码",
		})
		return
	}

	// 检查失败次数（3次错误后自动删除）
	if ui.failCount >= 3 {
		// 清除验证码
		ui.verificationCode = ""
		ui.failCount = 0
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "验证码已失效（连续错误3次）",
			Tip:   "请重新设置验证码",
		})
		return
	}

	// 验证码比对
	if ui.verificationCode == req.Code {
		// 验证成功，重置失败计数但保留验证码（可继续使用）
		ui.failCount = 0
		fmt.Printf("[验证码] 验证成功，验证码继续有效\n")
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"result":  true,
			"message": "验证码正确",
		})
		return
	}

	// 验证失败，增加失败次数
	ui.failCount++
	remaining := 3 - ui.failCount
	ui.lastFailTime = time.Now()
	
	fmt.Printf("[验证码] 验证失败 (错误次数: %d/3)\n", ui.failCount)
	
	errorMsg := fmt.Sprintf("验证码错误，还剩 %d 次尝试机会", remaining)
	if remaining == 0 {
		errorMsg = "验证码已失效（连续错误3次）"
		ui.verificationCode = "" // 清除验证码
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(verificationResponse{
		OK:    false,
		Error: errorMsg,
		Tip:   fmt.Sprintf("剩余尝试次数: %d", remaining),
	})
}

// 获取验证码状态（仅本机可查看，用于调试）
func (ui *WebUI) handleVerificationStatus(w http.ResponseWriter, r *http.Request) {
	if !ui.isLocalRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "仅本机可查看验证码状态",
		})
		return
	}

	ui.codeMutex.RLock()
	defer ui.codeMutex.RUnlock()
	
	status := "未设置"
	if ui.verificationCode != "" {
		status = "已设置"
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":            true,
		"is_set":        ui.verificationCode != "",
		"status":        status,
		"fail_count":    ui.failCount,
		"remaining_attempts": 3 - ui.failCount,
		"last_fail_time": ui.lastFailTime,
		"tip":           "使用 POST /api/verify 接口验证验证码",
	})
}

// 清除验证码（仅本机可调用）
func (ui *WebUI) handleClearVerificationCode(w http.ResponseWriter, r *http.Request) {
	if !ui.isLocalRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(verificationResponse{
			OK:    false,
			Error: "仅本机可清除验证码",
		})
		return
	}

	ui.codeMutex.Lock()
	ui.verificationCode = ""
	ui.failCount = 0
	ui.lastFailTime = time.Time{}
	ui.codeMutex.Unlock()
	
	fmt.Printf("[验证码] 验证码已被手动清除\n")
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "验证码已清除",
	})
}

// ========================
// 权限检查
// ========================
func (ui *WebUI) isLocalRequest(r *http.Request) bool {
	// 检查是否是本机IP访问
	clientIP := getClientIP(r)
	
	// 本地回环地址
	if clientIP == "127.0.0.1" || clientIP == "::1" || clientIP == "localhost" {
		return true
	}
	
	// 检查是否匹配本机的任何IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.String() == clientIP {
				return true
			}
		}
	}
	
	return false
}

func getClientIP(r *http.Request) string {
	// 优先从 X-Forwarded-For 获取
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}
	
	// 从 X-Real-IP 获取
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	
	// 从连接地址获取
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ========================
// 路由设置
// ========================
func (ui *WebUI) setupRoutes() {
	// 静态文件
	ui.router.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.Dir("static"))),
	)

	// 首页
	ui.router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/index.html")
	})

	api := ui.router.PathPrefix("/api").Subrouter()

	// ======== 验证码相关接口 ========
	// 设置验证码（仅本机可访问）
	api.HandleFunc("/verification/set", ui.handleSetVerificationCode).Methods("POST")
	// 验证验证码（所有人可访问，3次错误后自动删除）
	api.HandleFunc("/verification/verify", ui.handleVerifyCode).Methods("POST")
	// 获取验证码状态（仅本机）
	api.HandleFunc("/verification/status", ui.handleVerificationStatus).Methods("GET")
	// 清除验证码（仅本机）
	api.HandleFunc("/verification/clear", ui.handleClearVerificationCode).Methods("POST")

	// ======== 公开API（所有IP可访问）========
	
	// 本地信息（公开版本，隐藏敏感信息）
	api.HandleFunc("/local/info", ui.handleLocalInfo).Methods("GET")
	// 浏览文件（只读）
	api.HandleFunc("/local/browse", ui.handleLocalBrowse).Methods("GET")
	// 下载文件
	api.HandleFunc("/local/download", ui.handleLocalDownload).Methods("GET")
	// 预览文件
	api.HandleFunc("/local/preview", ui.handleLocalPreview).Methods("GET")
	// 文件搜索
	api.HandleFunc("/local/search", ui.handleLocalSearch).Methods("GET")
	// 上传文件
	api.HandleFunc("/local/upload", ui.handleLocalUpload).Methods("POST")

	// 节点发现
	api.HandleFunc("/peers", ui.handlePeers).Methods("GET")
	api.HandleFunc("/peer/{id}", ui.handlePeerInfo).Methods("GET")
	api.HandleFunc("/browse/{id}", ui.handleBrowse).Methods("GET")
	api.HandleFunc("/download/{id}", ui.handleDownload).Methods("GET")

	// 隧道（只读）
	api.HandleFunc("/tunnels", ui.handleListTunnels).Methods("GET")
	api.HandleFunc("/tunnel/create", ui.handleCreateTunnel).Methods("POST")
	api.HandleFunc("/tunnel/{id}/close", ui.handleCloseTunnel).Methods("POST")
	api.HandleFunc("/tunnel/{id}/files", ui.handleTunnelFiles).Methods("GET")
	api.HandleFunc("/tunnel/{id}/download", ui.handleTunnelDownload).Methods("GET")

	// ======== 管理员API（仅本机可访问）========
	
	admin := api.PathPrefix("/admin").Subrouter()
	admin.Use(ui.adminMiddleware)
	
	// 设置共享目录
	admin.HandleFunc("/share", ui.handleAdminShare).Methods("POST")
	// 获取共享目录信息
	admin.HandleFunc("/info", ui.handleAdminInfo).Methods("GET")
	// 获取所有本地IP
	admin.HandleFunc("/ips", ui.handleAdminIPs).Methods("GET")
}

// 管理员中间件
func (ui *WebUI) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ui.isLocalRequest(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":    false,
				"error": "仅本机可访问管理接口",
				"tip":   "请在本机上打开浏览器访问",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ========================
// 公开API实现
// ========================

func (ui *WebUI) handleLocalInfo(w http.ResponseWriter, r *http.Request) {
	isLocal := ui.isLocalRequest(r)
	
	info := map[string]interface{}{
		"nick":       ui.node.Info.Nick,
		"ip":         ui.node.Info.IP,
		"port":       ui.node.Port,
		"share_dir":  ui.shareDir,
		"is_local":   isLocal,
		"online_at":  ui.node.Info.OnlineAt,
	}
	
	// 只有本机才显示完整信息
	if isLocal {
		info["tunnel_port"] = ui.tm.port
		info["web_port"] = ui.port
		info["listen_addrs"] = ui.node.ListenAddrs
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (ui *WebUI) handleLocalBrowse(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	// 安全检查：限制在共享目录内
	fullPath := filepath.Join(ui.shareDir, path)
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "路径无效"})
		return
	}

	absShare, _ := filepath.Abs(ui.shareDir)
	if !strings.HasPrefix(absPath, absShare) {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "仅能访问共享目录内的文件"})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "路径不存在"})
		return
	}

	// 如果是文件，返回文件信息
	if !info.IsDir() {
		ext := strings.ToLower(filepath.Ext(absPath))
		fe := FileEntry{
			Name:    filepath.Base(absPath),
			Path:    path,
			IsDir:   false,
			Size:    info.Size(),
			SizeStr: formatFileSize(info.Size()),
			ModTime: info.ModTime().Format("2006-01-02 15:04"),
			Type:    getFileTypeName(ext, false),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    true,
			"path":  path,
			"files": []FileEntry{fe},
		})
		return
	}

	// 列出目录内容
	entries, err := os.ReadDir(absPath)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "读取目录失败"})
		return
	}

	var files []FileEntry

	// 添加父目录
	if path != "/" && path != "" {
		parentPath := filepath.Dir(path)
		if parentPath == "." {
			parentPath = "/"
		}
		files = append(files, FileEntry{
			Name:  "..",
			Path:  filepath.ToSlash(parentPath),
			IsDir: true,
			Type:  "dir",
		})
	}

	// 隐藏文件过滤
	showHidden := r.URL.Query().Get("hidden") == "1"

	for _, entry := range entries {
		// 跳过隐藏文件（以.开头的文件）
		if !showHidden && strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		
		finfo, err := entry.Info()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		entryPath := filepath.ToSlash(filepath.Join(path, entry.Name()))

		fe := FileEntry{
			Name:    entry.Name(),
			Path:    entryPath,
			IsDir:   entry.IsDir(),
			Size:    finfo.Size(),
			SizeStr: formatFileSize(finfo.Size()),
			ModTime: finfo.ModTime().Format("2006-01-02 15:04"),
			Type:    getFileTypeName(ext, entry.IsDir()),
		}
		files = append(files, fe)
	}

	// 面包屑
	breadcrumbs := buildBreadcrumbs(path)
	
	// 是否本机访问
	isLocal := ui.isLocalRequest(r)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":          true,
		"path":        path,
		"files":       files,
		"breadcrumbs": breadcrumbs,
		"share_dir":   ui.shareDir,
		"is_local":    isLocal,
		"total":       len(files),
	})
}

func (ui *WebUI) handleLocalDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "缺少路径"})
		return
	}

	fullPath := filepath.Join(ui.shareDir, path)
	absPath, _ := filepath.Abs(fullPath)
	absShare, _ := filepath.Abs(ui.shareDir)

	if !strings.HasPrefix(absPath, absShare) {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "禁止访问"})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "文件不存在"})
		return
	}

	if info.IsDir() {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "不能直接下载目录",
			"tip":   "可使用隧道方式浏览目录内容",
		})
		return
	}

	// 支持Range请求（断点续传）
	rangeHeader := r.Header.Get("Range")
	
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(absPath)))
	
	if rangeHeader != "" {
		var start, end int64
		fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
		if end == 0 {
			end = info.Size() - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size()))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		
		file, _ := os.Open(absPath)
		defer file.Close()
		file.Seek(start, 0)
		io.CopyN(w, file, end-start+1)
		return
	}

	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, absPath)
}

// 修复后的 handleLocalUpload 函数 - 替换 webui.go 中的对应函数

// 处理文件上传
func (ui *WebUI) handleLocalUpload(w http.ResponseWriter, r *http.Request) {
	// 限制上传文件大小（100MB）
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100MB
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   fmt.Sprintf("解析表单失败: %v", err),
		})
		return
	}

	// 获取文件
	file, handler, err := r.FormFile("file")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   fmt.Sprintf("获取文件失败: %v", err),
		})
		return
	}
	defer file.Close()

	// 获取目标路径
	targetPath := r.FormValue("path")
	if targetPath == "" {
		targetPath = "/" + handler.Filename
	}

	// 安全检查：限制在共享目录内
	// 清理路径，防止路径遍历攻击
	cleanPath := filepath.Clean(targetPath)
	// 确保路径不以 .. 开头
	if strings.Contains(cleanPath, "..") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   "非法的路径",
		})
		return
	}

	fullPath := filepath.Join(ui.shareDir, cleanPath)
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   "路径无效",
		})
		return
	}

	absShare, err := filepath.Abs(ui.shareDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   "服务器内部错误",
		})
		return
	}

	if !strings.HasPrefix(absPath, absShare) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   "只能上传到共享目录内",
		})
		return
	}

	// 创建目标目录（如果不存在）
	targetDir := filepath.Dir(absPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   fmt.Sprintf("创建目录失败: %v", err),
		})
		return
	}

	// 创建目标文件
	dst, err := os.Create(absPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   fmt.Sprintf("创建文件失败: %v", err),
		})
		return
	}
	defer dst.Close()

	// 复制文件内容
	written, err := io.Copy(dst, file)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"err":   fmt.Sprintf("写入文件失败: %v", err),
		})
		return
	}

	fmt.Printf("[上传] 文件已保存: %s (%.2f KB)\n", absPath, float64(written)/1024)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"message":  "上传成功",
		"path":     targetPath,
		"size":     written,
		"size_str": formatFileSize(written),
	})
}

func (ui *WebUI) handleLocalPreview(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		w.WriteHeader(400)
		fmt.Fprintf(w, "缺少路径参数")
		return
	}

	fullPath := filepath.Join(ui.shareDir, path)
	absPath, _ := filepath.Abs(fullPath)
	absShare, _ := filepath.Abs(ui.shareDir)

	if !strings.HasPrefix(absPath, absShare) {
		w.WriteHeader(403)
		fmt.Fprintf(w, "禁止访问")
		return
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		w.WriteHeader(404)
		fmt.Fprintf(w, "文件不存在")
		return
	}

	// 大文件限制（预览最大50MB）
	if info.Size() > 50*1024*1024 {
		w.WriteHeader(400)
		fmt.Fprintf(w, "文件太大无法预览 (最大50MB)，请下载后查看")
		return
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "读取文件失败")
		return
	}

	mimeTypes := map[string]string{
		".txt":  "text/plain; charset=utf-8",
		".md":   "text/markdown; charset=utf-8",
		".json": "application/json; charset=utf-8",
		".xml":  "application/xml; charset=utf-8",
		".html": "text/html; charset=utf-8",
		".css":  "text/css; charset=utf-8",
		".js":   "application/javascript; charset=utf-8",
		".go":   "text/plain; charset=utf-8",
		".py":   "text/plain; charset=utf-8",
		".java": "text/plain; charset=utf-8",
		".cpp":  "text/plain; charset=utf-8",
		".c":    "text/plain; charset=utf-8",
		".h":    "text/plain; charset=utf-8",
		".csv":  "text/csv; charset=utf-8",
		".log":  "text/plain; charset=utf-8",
		".yaml": "text/yaml; charset=utf-8",
		".yml":  "text/yaml; charset=utf-8",
		".toml": "text/plain; charset=utf-8",
		".ini":  "text/plain; charset=utf-8",
		".cfg":  "text/plain; charset=utf-8",
		".conf": "text/plain; charset=utf-8",
		".sh":   "text/plain; charset=utf-8",
		".bat":  "text/plain; charset=utf-8",
		".svg":  "image/svg+xml",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".bmp":  "image/bmp",
		".ico":  "image/x-icon",
		".pdf":  "application/pdf",
		".mp4":  "video/mp4",
		".webm": "video/webm",
		".ogg":  "video/ogg",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".flac": "audio/flac",
		".m4a":  "audio/mp4",
	}

	if mime, ok := mimeTypes[ext]; ok {
		w.Header().Set("Content-Type", mime)
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(content)
}

// 文件搜索
func (ui *WebUI) handleLocalSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(r.URL.Query().Get("q"))
	if query == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "缺少搜索关键词"})
		return
	}

	var results []FileEntry
	maxResults := 100

	filepath.Walk(ui.shareDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || len(results) >= maxResults {
			return nil
		}

		// 跳过隐藏文件
		if strings.HasPrefix(info.Name(), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// 搜索文件名
		if strings.Contains(strings.ToLower(info.Name()), query) {
			relPath, _ := filepath.Rel(ui.shareDir, path)
			relPath = filepath.ToSlash(relPath)
			if !strings.HasPrefix(relPath, "/") {
				relPath = "/" + relPath
			}

			ext := strings.ToLower(filepath.Ext(info.Name()))
			results = append(results, FileEntry{
				Name:    info.Name(),
				Path:    relPath,
				IsDir:   info.IsDir(),
				Size:    info.Size(),
				SizeStr: formatFileSize(info.Size()),
				ModTime: info.ModTime().Format("2006-01-02 15:04"),
				Type:    getFileTypeName(ext, info.IsDir()),
			})
		}

		return nil
	})

	if results == nil {
		results = []FileEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"query":   query,
		"results": results,
		"total":   len(results),
	})
}

// ========================
// 节点相关
// ========================
func (ui *WebUI) handlePeers(w http.ResponseWriter, r *http.Request) {
	nodes := ui.discovery.GetNodes()
	if nodes == nil {
		nodes = []*DiscoveredNode{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodes)
}

func (ui *WebUI) handlePeerInfo(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	node := ui.discovery.GetNode(id)
	if node == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "节点不存在"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

func (ui *WebUI) handleBrowse(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	node := ui.discovery.GetNode(id)
	if node == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "节点不存在"})
		return
	}

	if !node.IsOnline {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "节点已离线"})
		return
	}

	resp, err := ui.node.ConnectToPeer(node.Info.IP, node.Info.Port, "list", path, nil)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (ui *WebUI) handleDownload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	path := r.URL.Query().Get("path")

	if path == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "缺少路径"})
		return
	}

	if id == "local" {
		// 重定向到本地下载
		http.Redirect(w, r, "/api/local/download?path="+path, http.StatusTemporaryRedirect)
		return
	}

	node := ui.discovery.GetNode(id)
	if node == nil || !node.IsOnline {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "节点不存在或已离线"})
		return
	}

	resp, err := ui.node.ConnectToPeer(node.Info.IP, node.Info.Port, "download", path, nil)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	// 如果响应中包含文件数据，直接返回文件
	if data, ok := resp.Data.(map[string]interface{}); ok {
		if fileData, ok := data["data"].(string); ok {
			decoded, err := hexDecodeString(fileData)
			if err == nil {
				fileName := "download"
				if name, ok := data["name"].(string); ok {
					fileName = name
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(decoded)))
				w.Write(decoded)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ========================
// 隧道相关（只读）
// ========================
func (ui *WebUI) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	tunnels := ui.tm.ListTunnels()

	result := make([]map[string]interface{}, 0)
	for _, t := range tunnels {
		result = append(result, map[string]interface{}{
			"id":          t.ID,
			"remote_id":   t.RemoteID,
			"remote_nick": t.RemoteNick,
			"remote_ip":   t.RemoteIP,
			"remote_path": t.RemotePath,
			"local_path":  t.LocalPath,
			"type":        t.Type,
			"status":      t.Status,
			"created_at":  t.CreatedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"tunnels": result,
	})
}

func (ui *WebUI) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID     string `json:"peer_id"`
		RemotePath string `json:"remote_path"`
		LocalPath  string `json:"local_path"`
		Type       string `json:"type"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.PeerID == "" || req.RemotePath == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "缺少参数"})
		return
	}

	node := ui.discovery.GetNode(req.PeerID)
	if node == nil || !node.IsOnline {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "节点不存在或已离线"})
		return
	}

	// 强制只读隧道
	tunnelType := TunnelReadOnly

	if req.LocalPath == "" {
		req.LocalPath = filepath.Join(ui.shareDir, "tunnels", node.Info.Nick)
	}

	tunnel, err := ui.tm.EstablishTunnel(node.Info.IP, node.Info.Port, req.RemotePath, req.LocalPath, tunnelType)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true,
		"tunnel": map[string]interface{}{
			"id":          tunnel.ID,
			"remote_path": tunnel.RemotePath,
			"local_path":  tunnel.LocalPath,
			"type":        tunnel.Type,
			"status":      tunnel.Status,
		},
	})
}

func (ui *WebUI) handleCloseTunnel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	if err := ui.tm.CloseTunnel(id); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (ui *WebUI) handleTunnelFiles(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	tunnel := ui.tm.GetTunnel(id)
	if tunnel == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "隧道不存在"})
		return
	}

	files, err := tunnel.ListDir(path)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"path":  path,
		"files": files,
	})
}

func (ui *WebUI) handleTunnelDownload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	path := r.URL.Query().Get("path")

	tunnel := ui.tm.GetTunnel(id)
	if tunnel == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "隧道不存在"})
		return
	}

	data, err := tunnel.ReadFile(path)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(path)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

// ========================
// 管理员API（仅本机）
// ========================
func (ui *WebUI) handleAdminShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Path == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "缺少路径"})
		return
	}

	absPath, err := filepath.Abs(req.Path)
	if err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "路径无效"})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "目录不存在或不是目录"})
		return
	}

	ui.shareDir = absPath
	ui.node.config.ShareDir = absPath
	ui.node.Info.ShareDir = absPath

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"share_dir": absPath,
	})
}

func (ui *WebUI) handleAdminInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"nick":         ui.node.Info.Nick,
		"ip":           ui.node.Info.IP,
		"port":         ui.node.Port,
		"share_dir":    ui.shareDir,
		"tunnel_port":  ui.tm.port,
		"web_port":     ui.port,
		"listen_addrs": ui.node.ListenAddrs,
		"online_at":    ui.node.Info.OnlineAt,
	})
}

func (ui *WebUI) handleAdminIPs(w http.ResponseWriter, r *http.Request) {
	ips := ui.node.getAllBindIPs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":  true,
		"ips": ips,
	})
}

// ========================
// 辅助函数
// ========================
func buildBreadcrumbs(path string) []map[string]interface{} {
	var crumbs []map[string]interface{}

	crumbs = append(crumbs, map[string]interface{}{
		"name": "🏠",
		"path": "/",
	})

	if path == "/" || path == "" {
		return crumbs
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	currentPath := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		currentPath = "/" + filepath.Join(currentPath, part)
		currentPath = filepath.ToSlash(currentPath)
		crumbs = append(crumbs, map[string]interface{}{
			"name": part,
			"path": currentPath,
		})
	}

	return crumbs
}