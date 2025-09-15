package client

import (
	"sync"
	"time"
)

type Api struct {
	User *user
	*Client
}

type user struct {
}

func (user *user) genDefaultServiceTemplate() string {

}

func CreateApi(url string) Api {
	return Api{
		User:   new(user),
		Client: NewClient(url),
	}
}


package fun

import (
"sync"
"time"
)

// Result 定义结果结构体

// RequestType 定义请求类型枚举
type RequestType int

const (
	FuncType RequestType = iota
	ProxyType
	CloseType
	InitType
)

// RequestInfo 定义请求信息结构体

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
	go client.initConnection(url)

	return client
}

// 模拟初始化连接
func (c *Client) initConnection(url string) {
	// 模拟连接过程
	time.Sleep(100 * time.Millisecond)
	c.status = susses

	// 触发打开回调
	for _, callback := range c.openCall {
		callback()
	}
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

// Close 模拟关闭连接
func (c *Client) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.status = closeStatus
	for _, callback := range c.closeCall {
		callback()
	}
}
