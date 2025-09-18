package client

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"context"

	"github.com/gorilla/websocket"
	gonanoid "github.com/matoous/go-nanoid/v2"
)

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
	Dto         any
	State       map[string]string
	Type        RequestType
	result      chan Result[any]
	on          On[any]
}

type Void struct{}

// Client 定义客户端结构体
type Client struct {
	status      status
	requestList sync.Map // 使用线程安全的map
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
		requestList: sync.Map{},
		openCall:    make([]func(), 0),
		closeCall:   make([]func(), 0),
		mutex:       &sync.Mutex{},
	}
	id, err := getId()
	if err != nil {
		return nil, err
	}
	url = fmt.Sprintf("%s?id=%s", url, id)
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
	c.mutex.Lock()
	c.client = conn
	c.status = sussesStatus
	c.mutex.Unlock()
	// 启动 ping 机制
	ctx, cancelFunc := context.WithCancel(context.Background())
	c.ping(ctx)
	// 触发打开回调
	c.mutex.Lock()
	for _, callback := range c.openCall {
		callback()
	}
	c.mutex.Unlock()
	c.requestList.Range(func(key, value interface{}) bool {
		request := value.(RequestInfo)
		if c.status == sussesStatus || conn != nil {
			c.mutex.Lock()
			c.client.WriteJSON(request)
			c.mutex.Unlock()
		}
		return true
	})
	// 消息处理循环
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if messageType == websocket.TextMessage {
			var result Result[any]
			if err := json.Unmarshal(message, &result); err == nil {
				c.handleMessage(result)
			}
		}

		if messageType == websocket.BinaryMessage {
			if len(message) == 1 && message[0] == 1 {
				c.mutex.Lock()
				if c.timer != nil {
					c.timer.Stop()
				}
				c.mutex.Unlock()
			}
		}
	}
	c.mutex.Lock()
	c.status = closeStatus
	cancelFunc()
	c.requestList.Range(func(key, value interface{}) bool {
		request := value.(RequestInfo)
		if request.Type == funcType {
			result := networkError[any](c, request.ServiceName, request.MethodName)
			request.result <- result
		} else {
			if request.on.Close != nil {
				request.on.Close()
			}
		}
		return true
	})
	c.requestList = sync.Map{} // 重置sync.Map
	for _, closeCall := range c.closeCall {
		closeCall()
	}
	c.mutex.Unlock()
}

func (c *Client) handleMessage(result Result[any]) {
	// 使用sync.Map优化查找性能
	if value, exists := c.requestList.Load(result.Id); exists {
		request := value.(RequestInfo)
		if request.Type == proxyType {
			if result.Status == SuccessCode {
				if request.on.Message != nil {
					request.on.Message(result.Data)
				}
			} else {
				if request.on.Close != nil {
					request.on.Close()
				}
				c.requestList.Delete(result.Id)
			}
		} else {
			result := c.after(request.ServiceName, request.MethodName, result)
			request.result <- result
		}
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
				c.mutex.Lock()
				if c.status == sussesStatus || c.client != nil {
					c.client.WriteMessage(websocket.BinaryMessage, []byte{0})
				}
				if c.timer != nil {
					c.timer.Reset(2 * time.Second)
				} else {
					c.timer = time.AfterFunc(2*time.Second, func() {
						if c.status == sussesStatus || c.client != nil {
							c.client.Close()
						}
					})
				}
				c.mutex.Unlock()
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
	c.requestList.Delete(id)
}

func (c *Client) isRequestId(id string) bool {
	_, exists := c.requestList.Load(id)
	return exists
}

func Request[T any](client *Client, serviceName string, methodName string, dto ...any) Result[T] {
	resultChan := make(chan Result[any], 1)
	id, _ := gonanoid.New()
	state := make(map[string]string)
	client.mutex.Lock()
	if client.formerCall != nil {
		client.formerCall(serviceName, methodName, state)
	}
	client.mutex.Unlock()
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
	var result Result[any]
	requestInfo.result = resultChan
	client.mutex.Lock()
	if client.status != closeStatus {
		if client.status == sussesStatus || client.client != nil {
			client.client.WriteJSON(requestInfo)
		}
	}
	client.mutex.Unlock()
	// 使用sync.Map的Store操作
	client.requestList.Store(requestInfo.Id, requestInfo)

	networkTimeout := time.After(2 * time.Second)
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-networkTimeout:
			// 网络超时处理
			client.mutex.Lock()
			if client.status == closeStatus && client.isRequestId(id) {
				client.removeRequest(id)
				return networkError[T](client, serviceName, methodName)
			}
			client.mutex.Unlock()
		case <-timeout:
			// 请求超时处理
			if client.isRequestId(id) {
				client.removeRequest(id)
				return timeoutError[T](client, serviceName, methodName)
			}
		case result = <-resultChan:
			if client.isRequestId(id) {
				client.removeRequest(id)
				client.mutex.Lock()
				if client.afterCall != nil {
					result = client.afterCall(serviceName, methodName, result)
				}
				client.mutex.Unlock()
				convertedData := safeConvert[T](result.Data)
				return Result[T]{
					Id:     result.Id,
					Code:   result.Code,
					Data:   convertedData,
					Msg:    result.Msg,
					Status: result.Status,
				}
			}
		}
	}
}

func Proxy[T any](client *Client, serviceName string, methodName string, dto any, on On[T]) func() {
	id, _ := gonanoid.New()
	state := make(map[string]string)
	client.mutex.Lock()
	if client.formerCall != nil {
		client.formerCall(serviceName, methodName, state)
	}
	client.mutex.Unlock()
	requestInfo := RequestInfo{
		Id:          id,
		MethodName:  methodName,
		ServiceName: serviceName,
		State:       state,
		Type:        proxyType,
	}

	if dto != nil {
		requestInfo.Dto = dto
	}

	// 创建 On[any] 类型的包装
	onAny := On[any]{
		Message: func(message any) {
			// 类型断言转换为 T 类型
			convertedData := safeConvert[T](message)
			on.Message(convertedData)
		},
		Close: on.Close,
	}
	requestInfo.on = onAny

	// 如果客户端已连接，则发送请求
	client.mutex.Lock()
	if client.status != closeStatus {
		if client.status == sussesStatus || client.client != nil {
			client.client.WriteJSON(requestInfo)
		}

	}
	client.mutex.Unlock()
	// 将请求添加到请求列表中
	// 使用sync.Map的Store操作
	client.requestList.Store(requestInfo.Id, requestInfo)

	// 返回一个用于关闭连接的函数
	return func() {
		// 创建关闭请求
		closeRequest := RequestInfo{
			Id:          id,
			Type:        closeType,
			ServiceName: serviceName,
			MethodName:  methodName,
		}
		state = make(map[string]string)
		client.mutex.Lock()
		if client.formerCall != nil {
			client.formerCall(serviceName, methodName, state)
		}
		// 发送关闭请求
		if client.status != closeStatus {
			if client.status == sussesStatus || client.client != nil {
				client.client.WriteJSON(closeRequest)
			}
		}
		client.mutex.Unlock()
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
	return Result[T]{
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

	return Result[T]{
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

func safeConvert[T any](data any) (result T) {
	defer func() {
		if r := recover(); r != nil {
			result = *new(T)
		}
	}()

	// 直接类型断言
	if typedData, typeOk := data.(T); typeOk {
		return typedData
	}

	if data == nil {
		return *new(T)
	}

	// 尝试通过JSON进行转换
	jsonData, err := json.Marshal(data)
	if err != nil {
		return *new(T)
	}

	var converted T
	err = json.Unmarshal(jsonData, &converted)
	if err != nil {
		return *new(T)
	}

	return converted
}
