package client

import (
	"fmt"
	"os"
	"sync"
	"time"

	"context"

	"github.com/gorilla/websocket"
	gonanoid "github.com/matoous/go-nanoid/v2"
)

// Result 定义结果结构体
// RequestType 定义请求类型枚举
type RequestType uint8

const (
	funcType RequestType = iota
	proxyType
	closeType
)

type Result[T any] struct {
	Id     string
	Code   *uint16
	Data   T
	Msg    string
	Status ResultCode
}

type ResultCode uint8

const (
	SuccessCode ResultCode = iota
	CellErrorCode
	ErrorCode
	closeErrorCode
	NetworkError
	TimeoutError
)

type On[T any] struct {
	Message func(message T)
	Close   func()
}

// RequestInfo 定义请求信息结构体
type RequestInfo[T any] struct {
	Id          string
	MethodName  string
	ServiceName string
	Dto         *any
	State       map[string]string
	Type        RequestType
	function    *func(data Result[T])
	on          *On[T]
}

type Void struct{}

// Client 定义客户端结构体
type Client struct {
	// 注意：Go中没有直接对应Web Worker的结构，这里简化处理
	status      status
	requestList []RequestInfo[any]
	formerCall  func(serviceName, methodName string, state map[string]string)
	afterCall   func(serviceName, methodName string, result Result[any]) Result[any]
	openCall    []func()
	closeCall   []func()
	mutex       sync.Mutex
	client      *websocket.Conn
	timer       *time.Timer
	ticker      *time.Ticker
}

type status int

const (
	sussesStatus status = iota
	closeStatus
)

// NewClient 创建新的客户端实例
func NewClient(url string) *Client {
	client := &Client{
		status:      closeStatus,
		requestList: make([]RequestInfo[any], 0),
		openCall:    make([]func(), 0),
		closeCall:   make([]func(), 0),
	}
	// 模拟连接初始化
	go func() {
		for {
			client.initConnection(url)
		}
	}()
	return client
}

// 模拟初始化连接
func (c *Client) initConnection(url string) {
	// 模拟连接过程
	url = fmt.Sprintf("%s?id=%s", url, getId())
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	for err != nil {
		time.Sleep(5 * time.Second)
		conn, _, err = websocket.DefaultDialer.Dial(url, nil)
	}
	c.client = conn
	c.status = sussesStatus
	// 启动 ping 机制
	ctx, cancelFunc := context.WithCancel(context.Background())
	c.ping(ctx)
	// 触发打开回调
	for _, callback := range c.openCall {
		callback()
	}
	for _, request := range c.requestList {
		c.client.WriteJSON(request)
		//发送信息
	}
	// 消息处理循环
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if messageType == websocket.TextMessage {
			// 处理文u哦
			// testMessageQueue <- message (需要定义 testMessageQueue)
		}

		if messageType == websocket.BinaryMessage {
			if len(message) == 1 && message[0] == 1 {
				if c.timer != nil {
					c.timer.Stop()
				}
			}
		}
	}
	c.status = closeStatus
	cancelFunc()
	c.ticker.Stop()
	c.client.Close()
	for _, request := range c.requestList {
		if request.Type == funcType {
			if request.function != nil {
				result := c.networkError(request.ServiceName, request.MethodName)
				(*request.function)(result)
			}
		} else {
			if request.on != nil && request.on.Close != nil {
				request.on.Close()
			}
		}
	}
	c.requestList = make([]RequestInfo[any], 0)
	for _, closeCall := range c.closeCall {
		closeCall()
	}
}

func (c *Client) ping(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		c.ticker = ticker
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.client.WriteMessage(websocket.BinaryMessage, []byte{0})
				c.timer = time.AfterFunc(2*time.Second, func() {
					c.client.Close()
				})
			}
		}
	}()
}

// OnFormer 设置请求前回调
func (c *Client) OnFormer(fn func(serviceName, methodName string, state map[string]string)) {
	c.formerCall = fn
}

// OnAfter 设置响应后回调
func (c *Client) OnAfter(fn func(serviceName, methodName string, result Result[any]) Result[any]) {
	c.afterCall = fn
}

// OnClose 添加关闭回调
func (c *Client) OnClose(fn func()) {
	c.closeCall = append(c.closeCall, fn)
}

// OnOpen 添加打开回调
func (c *Client) OnOpen(fn func()) {
	c.openCall = append(c.openCall, fn)
}

// removeRequest 从请求列表中删除指定ID的请求
func (c *Client) removeRequest(id string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	filtered := make([]RequestInfo[any], 0)
	for _, request := range c.requestList {
		if request.Id != id {
			filtered = append(filtered, request)
		}
	}
	c.requestList = filtered
}

func request[T any](serviceName string, methodName string, dto any) Result[T] {
	return Result[T]{}
}

func Proxy[T any](serviceName string, methodName string, dto any, on *On[T]) func() {
	return func() {}
}

func getId() string {
	const idFile = "client_id.txt"
	var id string
	if data, err := os.ReadFile(idFile); err == nil && len(data) > 0 {
		id = string(data)
	} else {
		id, _ = gonanoid.New()
		os.WriteFile(idFile, []byte(id), 0644)
	}
	return id
}

func (c *Client) networkError(serviceName, methodName string) Result[any] {
	return c.after(serviceName, methodName, Result[any]{
		Id:     "",
		Msg:    "fun: Network anomaly",
		Status: NetworkError,
		Data:   nil,
	})
}

// timeoutError 创建超时错误结果
func (c *Client) timeoutError(serviceName, methodName string) Result[any] {
	return c.after(serviceName, methodName, Result[any]{
		Id:     "",
		Msg:    "fun: Network timeout",
		Status: TimeoutError,
		Data:   nil,
	})
}

func (c *Client) after(serviceName, methodName string, result Result[any]) Result[any] {
	// 如果设置了 afterCall 回调，则调用它并返回处理后的结果
	if c.afterCall != nil {
		processedResult := c.afterCall(serviceName, methodName, result)
		// 转换回具体类型
		return processedResult
	}

	// 否则直接返回原始结果
	return result
}
