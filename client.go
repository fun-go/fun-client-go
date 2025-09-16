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
type RequestInfo struct {
	Id          string
	MethodName  string
	ServiceName string
	Dto         *any
	State       map[string]string
	Type        RequestType
	function    *func(data Result[any])
	on          *On[any]
}

type Void struct{}

// Client 定义客户端结构体
type Client struct {
	status      status
	requestList []RequestInfo
	formerCall  func(serviceName, methodName string, state map[string]string)
	afterCall   func(serviceName, methodName string, result Result[any]) Result[any]
	openCall    []func()
	closeCall   []func()
	mutex       *sync.Mutex
	client      *websocket.Conn
	timer       *time.Timer
}

type status int

const (
	sussesStatus status = iota
	closeStatus
)

// NewClient 创建新的客户端实例
func NewClient(url string) (*Client, error) {
	client := &Client{
		status:      closeStatus,
		requestList: make([]RequestInfo, 0),
		openCall:    make([]func(), 0),
		closeCall:   make([]func(), 0),
		mutex:       &sync.Mutex{},
	}
	id, err := getId()
	if err != nil {
		return nil, err
	}
	url = fmt.Sprintf("%s?id=%s", url, id)
	// 模拟连接初始化
	go func() {
		for {
			client.initConnection(url)
		}
	}()
	return client, nil
}

// 模拟初始化连接
func (c *Client) initConnection(url string) {
	// 模拟连接过程
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
		c.mutex.Lock()
		c.client.WriteJSON(request)
		c.mutex.Unlock()
	}
	// 消息处理循环
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if messageType == websocket.TextMessage {
			// 处理文本消息
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
	c.mutex.Lock()
	for _, request := range c.requestList {
		if request.Type == funcType {
			if request.function != nil {
				result := networkError[any](c, request.ServiceName, request.MethodName)
				(*request.function)(result)
			}
		} else {
			if request.on != nil && request.on.Close != nil {
				request.on.Close()
			}
		}
	}
	c.requestList = make([]RequestInfo, 0)
	c.mutex.Unlock()
	for _, closeCall := range c.closeCall {
		closeCall()
	}
}

func (c *Client) ping(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.client.WriteMessage(websocket.BinaryMessage, []byte{0})
				if c.timer != nil {
					c.timer.Reset(2 * time.Second)
				} else {
					c.timer = time.AfterFunc(2*time.Second, func() {
						c.client.Close()
					})
				}
			}
		}
	}()
}

// OnFormer 设置请求前回调
func (c *Client) OnFormer(fn func(serviceName, methodName string, state map[string]string)) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.formerCall = fn
}

// OnAfter 设置响应后回调
func (c *Client) OnAfter(fn func(serviceName, methodName string, result Result[any]) Result[any]) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.afterCall = fn
}

// OnClose 添加关闭回调
func (c *Client) OnClose(fn func()) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.closeCall = append(c.closeCall, fn)
}

// OnOpen 添加打开回调
func (c *Client) OnOpen(fn func()) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.openCall = append(c.openCall, fn)
}

// removeRequest 从请求列表中删除指定ID的请求
func (c *Client) removeRequest(id string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	filtered := make([]RequestInfo, 0)
	for _, request := range c.requestList {
		if request.Id != id {
			filtered = append(filtered, request)
		}
	}
	c.requestList = filtered
}

func (c *Client) isRequestId(id string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for _, requestInfo := range c.requestList {
		if requestInfo.Id == id {
			return true
		}
	}
	return false
}

func Request[T any](client *Client, serviceName string, methodName string, dto ...any) Result[T] {
	resultChan := make(chan Result[T], 1)
	id, _ := gonanoid.New()
	state := make(map[string]string)
	if client.formerCall != nil {
		client.formerCall(serviceName, methodName, state)
	}
	requestInfo := RequestInfo{
		Id:          id,
		MethodName:  methodName,
		ServiceName: serviceName,
		State:       state,
		Type:        funcType,
	}
	if len(dto) > 0 {
		requestInfo.Dto = &dto[0]
	}
	var result Result[T]
	function := func(data Result[any]) {
		if client.isRequestId(id) {
			if client.afterCall != nil {
				data = client.afterCall(serviceName, methodName, data)
			}
			result = Result[T]{
				Id:     data.Id,
				Code:   data.Code,
				Data:   data.Data.(T),
				Msg:    data.Msg,
				Status: data.Status,
			}
		}
	}
	requestInfo.function = &function
	if client.status != closeStatus {
		client.mutex.Lock()
		client.client.WriteJSON(requestInfo)
		client.mutex.Unlock()
	}
	client.mutex.Lock()
	client.requestList = append(client.requestList, requestInfo)
	client.mutex.Unlock()

	networkTimeout := time.After(2 * time.Second)
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-networkTimeout:
			// 网络超时处理
			if client.status == closeStatus && client.isRequestId(id) {
				client.removeRequest(id)
				return networkError[T](client, serviceName, methodName)
			}
		case <-timeout:
			// 请求超时处理
			if client.isRequestId(id) {
				client.removeRequest(id)
				return timeoutError[T](client, serviceName, methodName)
			}
		case result = <-resultChan:
			// 正常收到结果，移除请求并返回结果
			if client.isRequestId(id) {
				client.removeRequest(id)
			}
			return result
		}
	}
}

func Proxy[T any](client *Client, serviceName string, methodName string, dto any, on On[T]) func() {
	id, _ := gonanoid.New()
	state := make(map[string]string)
	if client.formerCall != nil {
		client.formerCall(serviceName, methodName, state)
	}
	requestInfo := RequestInfo{
		Id:          id,
		MethodName:  methodName,
		ServiceName: serviceName,
		Dto:         &dto,
		State:       state,
		Type:        proxyType,
	}

	// 创建 On[any] 类型的包装
	onAny := &On[any]{
		Message: func(message any) {
			// 类型断言转换为 T 类型
			if msg, ok := message.(T); ok {
				on.Message(msg)
			}
		},
		Close: on.Close,
	}
	requestInfo.on = onAny

	// 如果客户端已连接，则发送请求
	if client.status != closeStatus {
		client.mutex.Lock()
		client.client.WriteJSON(requestInfo)
		client.mutex.Unlock()
	}

	// 将请求添加到请求列表中
	client.mutex.Lock()
	client.requestList = append(client.requestList, requestInfo)
	client.mutex.Unlock()

	// 返回一个用于关闭连接的函数
	return func() {
		// 创建关闭请求
		closeRequest := RequestInfo{
			Id:          id,
			Type:        closeType,
			ServiceName: serviceName,
			MethodName:  methodName,
		}

		// 发送关闭请求
		if client.status != closeStatus {
			client.mutex.Lock()
			client.client.WriteJSON(closeRequest)
			client.mutex.Unlock()
		}

		// 从请求列表中移除该请求
		client.removeRequest(id)
	}
}

func getId() (string, error) {
	const idFile = "client_id.txt"
	var id string
	if data, err := os.ReadFile(idFile); err == nil && len(data) > 0 {
		id = string(data)
	} else {
		id, _ = gonanoid.New()
		err := os.WriteFile(idFile, []byte(id), 0644)
		if err != nil {
			return "", err
		}
	}
	return id, nil
}

func networkError[T any](c *Client, serviceName, methodName string) Result[T] {
	result := c.after(serviceName, methodName, Result[any]{
		Id:     "",
		Msg:    "fun: Network anomaly",
		Status: NetworkError,
	})

	var data T
	return Result[T]{
		Id:     result.Id,
		Code:   result.Code,
		Data:   data,
		Msg:    result.Msg,
		Status: result.Status,
	}
}

// timeoutError 创建超时错误结果
func timeoutError[T any](c *Client, serviceName, methodName string) Result[T] {
	result := c.after(serviceName, methodName, Result[any]{
		Id:     "",
		Msg:    "fun: Network timeout",
		Status: TimeoutError,
	})

	var data T
	return Result[T]{
		Id:     result.Id,
		Code:   result.Code,
		Data:   data,
		Msg:    result.Msg,
		Status: result.Status,
	}
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
