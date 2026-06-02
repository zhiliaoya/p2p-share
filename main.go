package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"time"
	"syscall"
)

//go:embed static/*
var embeddedStatic embed.FS

func main() {
	var (
		nick     = flag.String("nick", "", "你的昵称 (必填)")
		port     = flag.Int("port", 0, "P2P通信端口 (0=自动)")
		shareDir = flag.String("share", ".\\共享文件夹", "共享目录路径")
		webPort  = flag.Int("web", 80, "Web界面端口")
		noWeb    = flag.Bool("noweb", false, "不启动Web界面")
		useIPv6  = flag.Bool("ipv6", false, "优先使用IPv6")
		bindAddr = flag.String("bind", "", "绑定地址 (默认自动检测)")
		publicIP = flag.String("public", "", "公网IP/域名 (用于NAT穿透场景)")
	)
	flag.Parse()

	// 检查昵称是否提供
	if *nick == "" {
		fmt.Println("错误: 必须指定昵称")
		fmt.Println("使用方法: -nick 你的昵称")
		fmt.Println("示例: ./p2p-share -nick 张三")
		flag.Usage()
		os.Exit(1)
	}

	// 首次运行时解压静态文件（如果不存在）
	if err := extractStaticFiles(); err != nil {
		log.Printf("警告: 解压静态文件失败: %v", err)
	}

	absShareDir, err := filepath.Abs(*shareDir)
	if err != nil {
		log.Fatalf("无效的共享目录: %v", err)
	}

	if info, err := os.Stat(absShareDir); err != nil || !info.IsDir() {
		log.Fatalf("目录不存在或不是目录: %s", absShareDir)
	}

	// 获取本机IP
	localIP := *bindAddr
	if localIP == "" {
		localIP = getBestIP(*useIPv6)
	}

	// 公网IP
	pubIP := *publicIP
	if pubIP == "" {
		pubIP = getPublicIP()
	}

	node := NewNode(NodeConfig{
		Nick:     *nick,
		Port:     *port,
		ShareDir: absShareDir,
		LocalIP:  localIP,
		PublicIP: pubIP,
		UseIPv6:  *useIPv6,
	})

	if err := node.Start(); err != nil {
		log.Fatalf("启动节点失败: %v", err)
	}

	discovery := NewDiscovery(node)
	if err := discovery.Start(); err != nil {
		log.Fatalf("启动服务发现失败: %v", err)
	}

	var web *WebUI
	if !*noWeb {
		web = NewWebUI(node, discovery, *webPort, absShareDir)
		go func() {
			if err := web.Start(); err != nil {
				log.Printf("Web界面启动失败: %v", err)
			}
		}()
	}

	printBanner(node, web, *noWeb, *webPort, absShareDir, pubIP)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n正在关闭...")
	if web != nil {
		web.Stop()
	}
	discovery.Stop()
	node.Stop()
}

// extractStaticFiles 解压嵌入的静态文件到 static 目录（仅当文件不存在时）
func extractStaticFiles() error {
	staticDir := "static"
	
	// 检查 index.html 是否已存在
	indexPath := filepath.Join(staticDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		// 文件已存在，无需解压
		return nil
	}

	// 创建 static 目录
	if err := os.MkdirAll(staticDir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %v", err)
	}

	// 遍历嵌入的文件并写入磁盘
	return fs.WalkDir(embeddedStatic, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		
		// 读取文件内容
		content, err := embeddedStatic.ReadFile(path)
		if err != nil {
			return err
		}
		
		// 计算输出路径（去掉 "static/" 前缀）
		relPath := path[len("static/"):]
		outPath := filepath.Join(staticDir, relPath)
		
		// 确保目录存在
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}
		
		// 写入文件
		if err := os.WriteFile(outPath, content, 0644); err != nil {
			return err
		}
		
		log.Printf("已解压: %s", outPath)
		return nil
	})
}

// getBestIP 获取最佳IP地址
func getBestIP(preferIPv6 bool) string {
	if preferIPv6 {
		if ip := getGlobalIPv6(); ip != "" {
			return ip
		}
	}
	
	// 尝试获取IPv4公网地址
	if ip := getPublicIPv4(); ip != "" {
		return ip
	}
	
	// 回退到本地地址
	return getLocalIPv4()
}

// getGlobalIPv6 获取全局IPv6地址
func getGlobalIPv6() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.To4() == nil && !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() {
				// 排除临时地址和本地地址
				if ipnet.IP.IsGlobalUnicast() {
					return ipnet.IP.String()
				}
			}
		}
	}
	return ""
}

// getPublicIPv4 获取公网IPv4
func getPublicIPv4() string {
	// 尝试通过外部服务获取
	addrs := []string{
		"api.ipify.org:80",
		"icanhazip.com:80",
		"ifconfig.me:80",
	}

	for _, addr := range addrs {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			continue
		}
		defer conn.Close()
		
		localAddr := conn.LocalAddr().(*net.TCPAddr)
		if localAddr.IP.IsGlobalUnicast() && localAddr.IP.To4() != nil {
			return localAddr.IP.String()
		}
	}
	
	return ""
}

// getLocalIPv4 获取本地IPv4
func getLocalIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() && ipnet.IP.IsGlobalUnicast() {
				return ipnet.IP.String()
			}
		}
	}
	
	// 回退
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// getPublicIP 获取公网IP（通过HTTP）
func getPublicIP() string {
	// 简单实现，实际可以用HTTP请求
	return ""
}

func printBanner(node *Node, web *WebUI, noWeb bool, webPort int, shareDir string, pubIP string) {
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════╗")
	fmt.Println("║     P2P 局域网/公网文件共享系统               ║")
	fmt.Println("╠════════════════════════════════════════════════╣")
	fmt.Printf("║  昵称: %-40s║\n", node.Info.Nick)
	fmt.Printf("║  本地: %-40s║\n", node.Info.IP+":"+fmt.Sprintf("%d", node.Port))
	
	// 显示所有监听地址
	for _, addr := range node.ListenAddrs {
		fmt.Printf("║  监听: %-40s║\n", addr)
	}
	
	fmt.Printf("║  共享: %-40s║\n", truncatePath(shareDir))
	
	if !noWeb {
		// 显示所有可访问的Web地址
		for _, addr := range node.WebAddrs(webPort) {
			fmt.Printf("║  Web:  %-40s║\n", addr)
		}
	}
	
	if pubIP != "" {
		fmt.Printf("║  公网: %-40s║\n", pubIP)
	}
	
	fmt.Println("╠════════════════════════════════════════════════╣")
	fmt.Println("║  支持 IPv4 / IPv6 双栈                        ║")
	fmt.Println("║  局域网自动发现 + 公网直连                    ║")
	fmt.Println("║  在浏览器中打开Web界面即可使用                ║")
	fmt.Println("╚════════════════════════════════════════════════╝")
	fmt.Println()
}

func truncatePath(path string) string {
	if len(path) > 35 {
		return "..." + path[len(path)-32:]
	}
	return path
}