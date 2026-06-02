package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// 协议版本
const ProtocolVersion = "1.0"

// 消息类型
const (
	MsgTypePing     = "ping"
	MsgTypePong     = "pong"
	MsgTypeList     = "list"
	MsgTypeDownload = "download"
	MsgTypeTunnel   = "tunnel"
	MsgTypeStream   = "stream"
	MsgTypeError    = "error"
	MsgTypeClose    = "close"
)

// TunnelType 隧道类型
type TunnelType string

const (
	TunnelReadOnly  TunnelType = "readonly"  // 只读隧道
	TunnelReadWrite TunnelType = "readwrite" // 读写隧道
)

// ProtocolMessage 协议消息
type ProtocolMessage struct {
	Version   string                 `json:"version"`
	Type      string                 `json:"type"`
	ID        string                 `json:"id"`        // 消息ID，用于请求-响应匹配
	Timestamp int64                  `json:"timestamp"` // Unix时间戳
	SenderID  string                 `json:"sender_id"`
	SenderIP  string                 `json:"sender_ip"`
	Payload   json.RawMessage        `json:"payload,omitempty"`
	Headers   map[string]string      `json:"headers,omitempty"`
}

// ListRequest 列表请求
type ListRequest struct {
	Path string `json:"path"`
}

// ListResponse 列表响应
type ListResponse struct {
	Path       string      `json:"path"`
	Files      []FileEntry `json:"files"`
	TotalCount int         `json:"total_count"`
}

// DownloadRequest 下载请求
type DownloadRequest struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"` // 断点续传偏移
	Size   int64  `json:"size"`   // 请求大小（0=全部）
}

// DownloadResponse 下载响应
type DownloadResponse struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	TotalSize  int64  `json:"total_size"`
	Data       string `json:"data"` // hex编码的数据
	IsComplete bool   `json:"is_complete"`
}

// TunnelRequest 隧道建立请求
type TunnelRequest struct {
	TunnelID   string     `json:"tunnel_id"`
	RemotePath string     `json:"remote_path"` // 远程目录路径
	LocalPath  string     `json:"local_path"`  // 本地挂载路径
	Type       TunnelType `json:"type"`
	Password   string     `json:"password,omitempty"` // 可选密码保护
}

// TunnelResponse 隧道建立响应
type TunnelResponse struct {
	TunnelID    string     `json:"tunnel_id"`
	Status      string     `json:"status"` // "established", "rejected", "error"
	RemotePath  string     `json:"remote_path"`
	Type        TunnelType `json:"type"`
	MaxConn     int        `json:"max_conn"`
	Message     string     `json:"message,omitempty"`
}

// StreamMessage 流式消息（用于隧道内数据传输）
type StreamMessage struct {
	TunnelID string `json:"tunnel_id"`
	SeqNum   int64  `json:"seq_num"`
	Data     string `json:"data"`    // hex编码的数据块
	IsEOF    bool   `json:"is_eof"`  // 是否结束
	Checksum string `json:"checksum,omitempty"` // MD5校验
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// 错误码
const (
	ErrCodeNotFound      = 404
	ErrCodeForbidden     = 403
	ErrCodeInternal      = 500
	ErrCodeTimeout       = 408
	ErrCodeBadRequest    = 400
	ErrCodeNotSupported  = 501
	ErrCodeAuthFailed    = 401
	ErrCodeQuotaExceeded = 429
)

// NewProtocolMessage 创建协议消息
func NewProtocolMessage(msgType string, senderID string, senderIP string, payload interface{}) *ProtocolMessage {
	var rawPayload json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err == nil {
			rawPayload = data
		}
	}

	return &ProtocolMessage{
		Version:   ProtocolVersion,
		Type:      msgType,
		ID:        generateMessageID(),
		Timestamp: time.Now().UnixMilli(),
		SenderID:  senderID,
		SenderIP:  senderIP,
		Payload:   rawPayload,
		Headers:   make(map[string]string),
	}
}

// SendProtocolMessage 发送协议消息到连接
func SendProtocolMessage(conn net.Conn, msg *ProtocolMessage) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	encoder := json.NewEncoder(conn)
	return encoder.Encode(msg)
}

// ReadProtocolMessage 从连接读取协议消息
func ReadProtocolMessage(conn net.Conn) (*ProtocolMessage, error) {
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var msg ProtocolMessage
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&msg); err != nil {
		return nil, fmt.Errorf("读取协议消息失败: %v", err)
	}
	return &msg, nil
}

// ParsePayload 解析消息载荷
func ParsePayload[T any](msg *ProtocolMessage) (*T, error) {
	if msg.Payload == nil {
		return nil, fmt.Errorf("消息载荷为空")
	}
	var result T
	if err := json.Unmarshal(msg.Payload, &result); err != nil {
		return nil, fmt.Errorf("解析载荷失败: %v", err)
	}
	return &result, nil
}

// 生成消息ID
func generateMessageID() string {
	b := make([]byte, 8)
	readRandom(b)
	return hexEncodeToString(b)
}

// 辅助函数（避免循环依赖）
func readRandom(b []byte) {
	// 使用简单的伪随机作为fallback
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i * 8 % 64) & 0xFF)
	}
}

func hexEncodeToString(b []byte) string {
	return fmt.Sprintf("%x", b)
}

// StreamReader 流式读取器
type StreamReader struct {
	conn     net.Conn
	tunnelID string
	seqNum   int64
}

func NewStreamReader(conn net.Conn, tunnelID string) *StreamReader {
	return &StreamReader{
		conn:     conn,
		tunnelID: tunnelID,
	}
}

func (sr *StreamReader) Read(p []byte) (int, error) {
	msg, err := ReadProtocolMessage(sr.conn)
	if err != nil {
		return 0, err
	}

	if msg.Type == MsgTypeClose {
		return 0, io.EOF
	}

	if msg.Type == MsgTypeError {
		errResp, _ := ParsePayload[ErrorResponse](msg)
		return 0, fmt.Errorf("流错误: %s", errResp.Message)
	}

	if msg.Type != MsgTypeStream {
		return 0, fmt.Errorf("意外的消息类型: %s", msg.Type)
	}

	streamMsg, err := ParsePayload[StreamMessage](msg)
	if err != nil {
		return 0, err
	}

	if streamMsg.TunnelID != sr.tunnelID {
		return 0, fmt.Errorf("隧道ID不匹配")
	}

	data, err := hexDecodeString(streamMsg.Data)
	if err != nil {
		return 0, fmt.Errorf("解码数据失败: %v", err)
	}

	n := copy(p, data)
	sr.seqNum = streamMsg.SeqNum

	if streamMsg.IsEOF {
		return n, io.EOF
	}

	return n, nil
}

// StreamWriter 流式写入器
type StreamWriter struct {
	conn     net.Conn
	tunnelID string
	senderID string
	senderIP string
	seqNum   int64
}

func NewStreamWriter(conn net.Conn, tunnelID, senderID, senderIP string) *StreamWriter {
	return &StreamWriter{
		conn:     conn,
		tunnelID: tunnelID,
		senderID: senderID,
		senderIP: senderIP,
	}
}

func (sw *StreamWriter) Write(p []byte) (int, error) {
	sw.seqNum++
	chunkSize := 32 * 1024 // 32KB chunks
	
	totalWritten := 0
	for offset := 0; offset < len(p); offset += chunkSize {
		end := offset + chunkSize
		if end > len(p) {
			end = len(p)
		}
		
		chunk := p[offset:end]
		isEOF := end == len(p)
		
		streamMsg := &StreamMessage{
			TunnelID: sw.tunnelID,
			SeqNum:   sw.seqNum,
			Data:     hexEncodeToString(chunk),
			IsEOF:    isEOF,
		}
		
		msg := NewProtocolMessage(MsgTypeStream, sw.senderID, sw.senderIP, streamMsg)
		msg.ID = generateMessageID()
		
		if err := SendProtocolMessage(sw.conn, msg); err != nil {
			return totalWritten, fmt.Errorf("发送流数据失败: %v", err)
		}
		
		totalWritten += len(chunk)
	}
	
	return totalWritten, nil
}

// Close 关闭流
func (sw *StreamWriter) Close() error {
	closeMsg := NewProtocolMessage(MsgTypeClose, sw.senderID, sw.senderIP, map[string]string{
		"tunnel_id": sw.tunnelID,
	})
	return SendProtocolMessage(sw.conn, closeMsg)
}

func hexDecodeString(s string) ([]byte, error) {
	var result []byte
	for i := 0; i < len(s); i += 2 {
		var b byte
		_, err := fmt.Sscanf(s[i:i+2], "%02x", &b)
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}