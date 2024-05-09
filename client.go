package flexrpc

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hoywu/flexrpc/codec"
	"github.com/hoywu/flexrpc/transport"
)

type ClientOpt struct {
	CodecType codec.CodecType
	Timeout   time.Duration
}

type Option func(*ClientOpt)

func WithCodecType(codecType codec.CodecType) Option {
	return func(opt *ClientOpt) {
		opt.CodecType = codecType
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(opt *ClientOpt) {
		opt.Timeout = timeout
	}
}

type Call struct {
	Seq        uint64
	CallTarget string

	streamCall  bool // TODO
	streamReply bool // TODO

	Args  any
	Reply any
	Err   error
	Done  chan *Call
}

var (
	ErrCodecType = errors.New("invalid codec type")
	ErrClosed    = errors.New("connection already closed")
)

type Client struct {
	codec codec.Codec
	reqMu sync.Mutex // protects request sending

	nextSeq uint64           // next request sequence number, starting with 1
	reqs    map[uint64]*Call // pending requests
	mu      sync.Mutex       // protects nextSeq and reqs

	closing  bool // user has called Close
	shutdown bool // server has told us to stop
	closeMu  sync.RWMutex
}

func Dial(network, address string, opts ...Option) (*Client, error) {
	// 默认选项
	opt := &ClientOpt{
		CodecType: codec.GobCodecType,
		Timeout:   0,
	}
	for _, o := range opts {
		o(opt)
	}

	conn, err := net.DialTimeout(network, address, opt.Timeout)
	if err != nil {
		return nil, err
	}
	return NewClient(conn, opt.CodecType, opt.Timeout)
}

func NewClient(conn io.ReadWriteCloser, codecType codec.CodecType, timeout time.Duration) (*Client, error) {
	codec := codec.CodecImpl[codecType]
	if codec == nil {
		return nil, ErrCodecType
	}

	c := &Client{
		codec:   codec(conn),
		nextSeq: 1,
		reqs:    make(map[uint64]*Call),
	}

	// 创建客户端时，发送协议头建立连接
	ch := make(chan error)
	go func() {
		ch <- transport.NewHeaderEncoder(conn).Encode(codecType)
		close(ch)
	}()

	// 等待协议头发送完成
	select {
	case err := <-ch:
		if err != nil {
			return nil, err
		}
	case <-time.After(timeout):
		return nil, errors.New("sending header timeout")
	}

	// 启动接收协程
	go c.recv()
	return c, nil
}

func (c *Client) Call(callTarget string, args, reply any) error {
	call := <-c.Go(callTarget, args, reply, make(chan *Call, 1)).Done
	return call.Err
}

func (c *Client) Go(callTarget string, args, reply any, done chan *Call) *Call {
	if cap(done) == 0 {
		panic("done channel is unbuffered")
	}
	call := &Call{
		CallTarget:  callTarget,
		streamCall:  false, // TODO
		streamReply: false, // TODO
		Args:        args,
		Reply:       reply,
		Done:        done,
	}
	go c.send(call)
	return call
}

// 在独立的 goroutine 中接收服务端响应
func (c *Client) recv() {
	var err error
	for err == nil {
		// 读取响应头
		var header codec.RespHeader
		if err = c.codec.ReadNext(&header); err != nil {
			break
		}

		call := c.popCall(header.Seq)
		if call == nil {
			// 不存在对应的请求，忽略响应体
			err = c.codec.ReadNext(nil)
			continue
		}
		if header.Err != "" {
			// 响应头中包含错误，忽略响应体，完成请求
			call.Err = errors.New(header.Err)
			err = c.codec.ReadNext(nil)
			call.done()
			continue
		}

		// 读取响应体，完成请求
		if err = c.codec.ReadNext(call.Reply); err != nil {
			call.Err = errors.New("reading body: " + err.Error())
		}
		call.done()
	}
	c.terminate(err)
}

// 发送请求
func (c *Client) send(call *Call) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	// 创建调用请求
	seq, err := c.newCall(call)
	if err != nil {
		call.Err = err
		call.done()
		return
	}

	// 发送请求头
	h := codec.ReqHeader{
		Seq:         seq,
		CallTarget:  call.CallTarget,
		StreamCall:  false, // TODO
		StreamReply: false, // TODO
	}
	if err = c.codec.Write(&h); err != nil {
		c.errCall(seq, err)
		return
	}

	// 发送请求参数
	if err = c.codec.Write(call.Args); err != nil {
		c.errCall(seq, err)
		return
	}
}

// 创建一个新的调用请求
func (c *Client) newCall(call *Call) (seq uint64, err error) {
	c.mu.Lock()
	c.closeMu.RLock()
	defer c.mu.Unlock()
	defer c.closeMu.RUnlock()
	if c.closing || c.shutdown {
		return 0, ErrClosed
	}
	call.Seq = c.nextSeq
	c.nextSeq++
	c.reqs[call.Seq] = call
	return call.Seq, nil
}

// 弹出一个调用请求
func (c *Client) popCall(seq uint64) *Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	call := c.reqs[seq]
	delete(c.reqs, seq)
	return call
}

// 以 err 错误完成调用请求
func (c *Client) errCall(seq uint64, err error) {
	call := c.popCall(seq)
	if call != nil {
		call.Err = err
		call.done()
	}
}

// 终止所有请求
func (c *Client) terminate(err error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	c.shutdown = true
	for _, call := range c.reqs {
		call.Err = err
		call.done()
	}
}

func (c *Client) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	c.closing = true
	return c.codec.Close()
}

func (c *Client) IsAvailable() bool {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()

	return !c.closing && !c.shutdown
}

func (c *Call) done() {
	c.Done <- c
}
