package fun

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ResultStatus 定义结果状态枚举
type ResultStatus int

const (
	Success ResultStatus = iota
	CallError
	Error
	CloseError
	NetworkError
	TimeoutError
)

// Result 定义结果结构体
type Result[T any] struct {
	Id     string       `json:"id"`
	Code   *uint16      `json:"code,omitempty"`
	Msg    string       `json:"msg"`
	Status ResultStatus `json:"status"`
	Data   T            `json:"data"`
}

type Void struct{}

// RequestType 定义请求类型枚举
type RequestType int

const (
	FuncType RequestType = iota
	ProxyType
	CloseType
	InitType
)

type on[T any] struct {
	Message func(message T)
	Close   func()
}

// RequestInfo 定义请求信息结构体
type RequestInfo[T any] struct {
	Id          string            `json:"id"`
	MethodName  string            `json:"methodName"`
	ServiceName string            `json:"serviceName"`
	Dto         *T                `json:"dto,omitempty"`
	State       map[string]string `json:"state,omitempty"`
	Type        RequestType       `json:"type"`
	function    *func(data Result[T])
	on          *on[T]
}

// Client 定义客户端结构体
type Client struct {
	conn        *websocket.Conn
	url         string
	clientId    string
	status      status
	requestList []RequestInfo[any]
	formerCall  func(serviceName, methodName string, state map[string]string)
	afterCall   func(serviceName, methodName string, result Result[any]) Result[any]
	openCall    []func()
	closeCall   []func()
	mutex       sync.Mutex
	writeMutex  sync.Mutex
}

type status int

const (
	sussesStatus status = iota
	closeStatus
)

// NewClient 创建新的客户端实例
func NewClient(url string) *Client {
	client := &Client{
		url:         url,
		clientId:    getClientId(),
		status:      closeStatus,
		requestList: make([]RequestInfo[any], 0),
		openCall:    make([]func(), 0),
		closeCall:   make([]func(), 0),
	}

	// 初始化连接
	go client.initConnection(url)

	return client
}

// getClientId 获取客户端ID
func getClientId() string {
	// 简单实现，实际可以存储到文件或环境变量中
	return uuid.New().String()
}

// initConnection 初始化WebSocket连接
func (c *Client) initConnection(url string) {
	// 构造WebSocket URL
	wsUrl := fmt.Sprintf("%s?id=%s", url, c.clientId)

	// 连接WebSocket服务器
	conn, _, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	for err != nil {
		time.Sleep(5 * time.Second)
		conn, _, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	}

	c.conn = conn
	c.status = sussesStatus

	// 启动读取消息的goroutine
	go c.readMessages()

	// 启动心跳机制
	go c.heartbeat()

	// 触发打开回调
	c.mutex.Lock()
	openCalls := make([]func(), len(c.openCall))
	copy(openCalls, c.openCall)
	c.mutex.Unlock()

	for _, callback := range openCalls {
		callback()
	}

	// 重新发送未完成的请求
	c.mutex.Lock()
	for _, request := range c.requestList {
		c.sendRequest(request)
	}
	c.mutex.Unlock()
}

// readMessages 读取WebSocket消息
func (c *Client) readMessages() {
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			// 连接关闭处理
			c.handleConnectionClose()
			return
		}

		// 处理接收到的消息
		go c.handleMessage(message)
	}
}

// heartbeat 心跳机制
func (c *Client) heartbeat() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.writeMutex.Lock()
		if c.conn != nil && c.status == sussesStatus {
			// 发送ping消息 (binary message with single byte 0)
			err := c.conn.WriteMessage(websocket.BinaryMessage, []byte{0})
			if err != nil {
				c.writeMutex.Unlock()
				c.handleConnectionClose()
				return
			}
		}
		c.writeMutex.Unlock()
	}
}

// handleMessage 处理接收到的消息
func (c *Client) handleMessage(data []byte) {
	// 检查是否是pong响应 (binary message with single byte 1)
	if len(data) == 1 && data[0] == 1 {
		// pong响应，不需要处理
		return
	}

	// 处理文本消息
	var result Result[any]
	if err := json.Unmarshal(data, &result); err != nil {
		return
	}

	c.processResult(result)
}

// processResult 处理结果
func (c *Client) processResult(result Result[any]) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	index := -1
	for i, request := range c.requestList {
		if request.Id == result.Id {
			index = i
			break
		}
	}

	if index == -1 {
		return
	}

	request := c.requestList[index]

	// 根据请求类型处理结果
	if request.Type == FuncType {
		if request.function != nil {
			processedResult := c.after(request.ServiceName, request.MethodName, result)
			(*request.function)(processedResult)
		}
	} else {
		if result.Status == Success {
			if request.on != nil && request.on.Message != nil {
				request.on.Message(result.Data)
			}
		} else {
			if request.on != nil && request.on.Close != nil {
				request.on.Close()
			}
		}
	}

	// 如果是函数调用或者失败结果，则删除请求
	if request.Type == FuncType || result.Status != Success {
		c.requestList = append(c.requestList[:index], c.requestList[index+1:]...)
	}
}

// handleConnectionClose 处理连接关闭
func (c *Client) handleConnectionClose() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.status == closeStatus {
		return
	}

	c.status = closeStatus
	oldConn := c.conn
	c.conn = nil

	if oldConn != nil {
		oldConn.Close()
	}

	// 通知所有未完成的请求网络错误
	for _, request := range c.requestList {
		if request.Type == FuncType {
			if request.function != nil {
				errorResult := c.networkError(request.ServiceName, request.MethodName)
				(*request.function)(errorResult)
			}
		} else {
			if request.on != nil && request.on.Close != nil {
				request.on.Close()
			}
		}
	}

	c.requestList = c.requestList[:0] // 清空请求列表

	// 触发关闭回调
	closeCalls := make([]func(), len(c.closeCall))
	copy(closeCalls, c.closeCall)

	// 在锁外调用回调函数以避免死锁
	c.mutex.Unlock()
	for _, callback := range closeCalls {
		callback()
	}
	c.mutex.Lock()

	// 尝试重新连接
	go func() {
		time.Sleep(5 * time.Second)
		c.initConnection(c.url)
	}()
}

// Proxy 创建代理连接
func Request[T any](c *Client, serviceName, methodName string, dto *T) <-chan Result[T] {
	resultChan := make(chan Result[T], 1)

	go func() {
		id := uuid.New().String()
		state := make(map[string]string)

		// 调用前置回调
		c.former(serviceName, methodName, state)

		request := RequestInfo[T]{
			Id:          id,
			MethodName:  methodName,
			ServiceName: serviceName,
			Dto:         dto,
			State:       state,
			Type:        FuncType,
		}

		// 创建结果处理函数
		resultFunc := func(data Result[T]) {
			processedResult := c.after(serviceName, methodName, data)
			resultChan <- processedResult
			close(resultChan)
		}

		// 类型转换以存储在通用请求列表中
		genericRequest := RequestInfo[any]{
			Id:          request.Id,
			MethodName:  request.MethodName,
			ServiceName: request.ServiceName,
			Dto:         request.Dto,
			State:       request.State,
			Type:        request.Type,
			function:    &resultFunc,
			on:          nil,
		}

		c.mutex.Lock()
		c.requestList = append(c.requestList, genericRequest)
		c.mutex.Unlock()

		// 发送请求
		c.sendRequest(genericRequest)

		// 设置超时处理
		go func() {
			time.Sleep(10 * time.Second) // 10秒超时
			c.mutex.Lock()
			found := false
			index := -1
			for i, req := range c.requestList {
				if req.Id == id {
					found = true
					index = i
					break
				}
			}
			c.mutex.Unlock()

			if found {
				timeoutResult := c.timeoutError(serviceName, methodName)
				resultChan <- timeoutResult
				close(resultChan)

				c.mutex.Lock()
				if index >= 0 && index < len(c.requestList) && c.requestList[index].Id == id {
					c.requestList = append(c.requestList[:index], c.requestList[index+1:]...)
				}
				c.mutex.Unlock()
			}
		}()
	}()

	return resultChan
}

// Proxy 创建代理连接
func Proxy[T any](c *Client, serviceName, methodName string, dto *T, onHandler *on[T]) func() {
	id := uuid.New().String()
	state := make(map[string]string)

	// 调用前置回调
	c.former(serviceName, methodName, state)

	request := RequestInfo[T]{
		Id:          id,
		MethodName:  methodName,
		ServiceName: serviceName,
		Dto:         dto,
		State:       state,
		Type:        ProxyType,
	}

	// 类型转换以存储在通用请求列表中
	genericRequest := RequestInfo[any]{
		Id:          request.Id,
		MethodName:  request.MethodName,
		ServiceName: request.ServiceName,
		Dto:         request.Dto,
		State:       request.State,
		Type:        request.Type,
		function:    nil,
		on: &on[any]{
			Message: func(message any) {
				if onHandler.Message != nil {
					onHandler.Message(message.(T))
				}
			},
			Close: onHandler.Close,
		},
	}

	c.mutex.Lock()
	c.requestList = append(c.requestList, genericRequest)
	c.mutex.Unlock()

	// 发送请求
	c.sendRequest(genericRequest)

	// 返回关闭函数
	return func() {
		state := make(map[string]string)
		c.former(serviceName, methodName, state)

		closeRequest := RequestInfo[any]{
			Id:          id,
			MethodName:  methodName,
			ServiceName: serviceName,
			State:       state,
			Type:        CloseType,
		}

		if onHandler.Close != nil {
			onHandler.Close()
		}

		c.sendRequest(closeRequest)

		c.mutex.Lock()
		c.requestList = append(c.requestList, closeRequest)
		c.mutex.Unlock()
	}
}

// sendRequest 发送请求到服务器
func (c *Client) sendRequest(request RequestInfo[any]) {
	c.mutex.Lock()
	if c.status == closeStatus {
		c.mutex.Unlock()
		return
	}
	c.mutex.Unlock()

	// 序列化请求
	data, err := json.Marshal(request)
	if err != nil {
		return
	}

	// 发送数据（加写锁保护）
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	if c.conn != nil {
		c.conn.WriteMessage(websocket.TextMessage, data)
	}
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

// Close 关闭连接
func (c *Client) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.status == closeStatus {
		return
	}

	c.status = closeStatus
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	// 触发关闭回调
	for _, callback := range c.closeCall {
		callback()
	}
}

// former 调用前置回调
func (c *Client) former(serviceName, methodName string, state map[string]string) {
	c.mutex.Lock()
	formerCall := c.formerCall
	c.mutex.Unlock()

	if formerCall != nil {
		formerCall(serviceName, methodName, state)
	}
}

// after 调用后置回调
func (c *Client) after(serviceName, methodName string, result Result[any]) Result[any] {
	c.mutex.Lock()
	afterCall := c.afterCall
	c.mutex.Unlock()

	if afterCall != nil {
		// 类型转换
		genericResult := Result[any]{
			Id:     result.Id,
			Code:   result.Code,
			Msg:    result.Msg,
			Status: result.Status,
			Data:   result.Data,
		}
		processed := afterCall(serviceName, methodName, genericResult)
		// 转换回具体类型
		return Result[any]{
			Id:     processed.Id,
			Code:   processed.Code,
			Msg:    processed.Msg,
			Status: processed.Status,
			Data:   processed.Data.(any),
		}
	}
	return result
}

// networkError 创建网络错误结果
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
