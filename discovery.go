package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

// DiscoveredNode 发现的节点
type DiscoveredNode struct {
	Info     NodeInfo  `json:"info"`
	LastSeen time.Time `json:"last_seen"`
	IsOnline bool      `json:"is_online"`
}

// Discovery mDNS服务发现
type Discovery struct {
	node       *Node
	server     *mdns.Server
	discovered map[string]*DiscoveredNode
	mu         sync.RWMutex
	stopCh     chan struct{}
	onUpdate   func()
}

func NewDiscovery(node *Node) *Discovery {
	return &Discovery{
		node:       node,
		discovered: make(map[string]*DiscoveredNode),
		stopCh:     make(chan struct{}),
	}
}

func (d *Discovery) Start() error {
	// 将节点信息序列化
	infoData, err := json.Marshal(d.node.Info)
	if err != nil {
		return fmt.Errorf("序列化节点信息失败: %v", err)
	}

	// 创建mDNS服务
	service, err := mdns.NewMDNSService(
		d.node.Info.Nick,
		"_p2pshare._tcp",
		"",
		"",
		d.node.Port,
		nil,
		[]string{string(infoData)},
	)
	if err != nil {
		return fmt.Errorf("创建mDNS服务失败: %v", err)
	}

	// 启动mDNS服务器
	d.server, err = mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return fmt.Errorf("启动mDNS服务器失败: %v", err)
	}

	// 启动发现循环
	go d.discoverLoop()
	// 启动清理循环
	go d.cleanupLoop()

	log.Printf("mDNS服务发现已启动: %s (%s:%d)", d.node.Info.Nick, d.node.Info.IP, d.node.Port)
	return nil
}

func (d *Discovery) Stop() {
	close(d.stopCh)
	if d.server != nil {
		d.server.Shutdown()
	}
	log.Println("mDNS服务发现已停止")
}

func (d *Discovery) discoverLoop() {
	// 首次立即扫描
	d.scan()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.scan()
		case <-d.stopCh:
			return
		}
	}
}

func (d *Discovery) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			d.mu.Lock()
			for id, node := range d.discovered {
				// 移除超过2分钟未响应的离线节点
				if !node.IsOnline && now.Sub(node.LastSeen) > 120*time.Second {
					delete(d.discovered, id)
					log.Printf("移除离线节点: %s", node.Info.Nick)
				}
			}
			d.mu.Unlock()
		case <-d.stopCh:
			return
		}
	}
}

func (d *Discovery) scan() {
	entriesCh := make(chan *mdns.ServiceEntry, 32)
	
	go func() {
		params := &mdns.QueryParam{
			Service: "_p2pshare._tcp",
			Domain:  "local",
			Timeout: 3 * time.Second,
			Entries: entriesCh,
		}
		if err := mdns.Query(params); err != nil {
			log.Printf("mDNS查询失败: %v", err)
		}
		close(entriesCh)
	}()

	now := time.Now()
	found := make(map[string]bool)

	for entry := range entriesCh {
		if len(entry.InfoFields) > 0 {
			var info NodeInfo
			if err := json.Unmarshal([]byte(entry.InfoFields[0]), &info); err != nil {
				log.Printf("解析节点信息失败: %v", err)
				continue
			}

			// 跳过自己
			if info.ID == d.node.Info.ID {
				continue
			}

			// 验证IP和端口
			if info.IP == "" || info.Port == 0 {
				continue
			}

			found[info.ID] = true
			
			d.mu.Lock()
			if existing, ok := d.discovered[info.ID]; ok {
				// 更新已有节点
				existing.Info = info
				existing.LastSeen = now
				if !existing.IsOnline {
					log.Printf("节点重新上线: %s (%s:%d)", info.Nick, info.IP, info.Port)
				}
				existing.IsOnline = true
			} else {
				// 发现新节点
				d.discovered[info.ID] = &DiscoveredNode{
					Info:     info,
					LastSeen: now,
					IsOnline: true,
				}
				log.Printf("发现新节点: %s (%s:%d)", info.Nick, info.IP, info.Port)
			}
			d.mu.Unlock()
		}
	}

	// 将未出现在扫描结果中的节点标记为离线
	d.mu.Lock()
	for id, node := range d.discovered {
		if !found[id] && now.Sub(node.LastSeen) > 15*time.Second {
			if node.IsOnline {
				log.Printf("节点离线: %s", node.Info.Nick)
			}
			node.IsOnline = false
		}
	}
	d.mu.Unlock()

	// 触发更新回调
	if d.onUpdate != nil {
		d.onUpdate()
	}
}

// GetNodes 获取所有发现的节点
func (d *Discovery) GetNodes() []*DiscoveredNode {
	d.mu.RLock()
	defer d.mu.RUnlock()

	nodes := make([]*DiscoveredNode, 0, len(d.discovered))
	for _, n := range d.discovered {
		nodes = append(nodes, n)
	}
	return nodes
}

// GetNode 根据ID获取节点
func (d *Discovery) GetNode(id string) *DiscoveredNode {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.discovered[id]
}

// GetOnlineNodes 获取所有在线节点
func (d *Discovery) GetOnlineNodes() []*DiscoveredNode {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var nodes []*DiscoveredNode
	for _, n := range d.discovered {
		if n.IsOnline {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// SetUpdateCallback 设置更新回调
func (d *Discovery) SetUpdateCallback(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onUpdate = fn
}

// ForceScan 强制立即扫描
func (d *Discovery) ForceScan() {
	go d.scan()
}

// GetNodeCount 获取节点数量
func (d *Discovery) GetNodeCount() (total int, online int) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	total = len(d.discovered)
	for _, n := range d.discovered {
		if n.IsOnline {
			online++
		}
	}
	return
}