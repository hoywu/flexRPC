package flexrpc

import (
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/hoywu/flexrpc/codec"
	"github.com/hoywu/flexrpc/service"
	"github.com/hoywu/flexrpc/transport"
)

type ServerOpt struct {
	ReadHeaderTimeout time.Duration
	ConnIdleTimeout   time.Duration
	ReadWriteTimeout  time.Duration
}

type SOptFunc func(*ServerOpt)

func WithReadHeaderTimeout(timeout time.Duration) SOptFunc {
	return func(opt *ServerOpt) {
		opt.ReadHeaderTimeout = timeout
	}
}

func WithConnIdleTimeout(timeout time.Duration) SOptFunc {
	return func(opt *ServerOpt) {
		opt.ConnIdleTimeout = timeout
	}
}

func WithReadWriteTimeout(timeout time.Duration) SOptFunc {
	return func(opt *ServerOpt) {
		opt.ReadWriteTimeout = timeout
	}
}

type Server struct {
	serviceMap sync.Map
	opt        *ServerOpt
}

func (s *Server) Register(rcvr interface{}) error {
	return s.RegisterName(rcvr, "")
}

func (s *Server) RegisterName(rcvr interface{}, name string) error {
	svc := service.NewService(rcvr, name)
	_, loaded := s.serviceMap.LoadOrStore(svc.Name, svc)
	if loaded {
		return fmt.Errorf("service %v already registered", svc.Name)
	}
	return nil
}

func (s *Server) parseCallTarget(callTarget string) (*service.Service, *service.Method, error) {
	i := strings.LastIndex(callTarget, ".")
	if i < 0 {
		return nil, nil, fmt.Errorf("invalid target: %v", callTarget)
	}

	svcName := callTarget[:i]
	methodName := callTarget[i+1:]

	value, ok := s.serviceMap.Load(svcName)
	if !ok {
		return nil, nil, fmt.Errorf("service %v not found", svcName)
	}
	svc := value.(*service.Service)
	m := svc.Methods[methodName]
	if m == nil {
		return nil, nil, fmt.Errorf("method %v not found", methodName)
	}

	return svc, m, nil
}

func NewServer() *Server {
	return &Server{
		opt: &ServerOpt{},
	}
}

func (s *Server) Accept(lis net.Listener, opts ...SOptFunc) {
	for _, o := range opts {
		o(s.opt)
	}

	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			return
		}
		go s.Serve(conn)
	}
}

// 处理一个传入连接
func (s *Server) Serve(conn io.ReadWriteCloser) {
	defer conn.Close()

	var header transport.Header
	ch := make(chan error)
	go func() {
		ch <- transport.NewHeaderDecoder(conn).Decode(&header)
		close(ch)
	}()

	select {
	case err := <-ch:
		if err != nil {
			// 报文头解码错误
			log.Printf("Decode header error: %v", err)
			return
		}
	case <-timeoutSignal(s.opt.ReadHeaderTimeout):
		return
	}

	if header.MagicNum != transport.MagicNum {
		// 魔数不匹配
		log.Printf("Invalid magic number: %x", header.MagicNum)
		return
	}
	if header.Version != transport.Version {
		// 版本不匹配
		log.Printf("Invalid version: %v", header.Version)
		return
	}

	c := codec.CodecImpl[header.Codec]
	if c == nil {
		// 无效的编码
		log.Printf("Invalid codec: %v", header.Codec)
		return
	}

	// RPC 连接建立成功，开始处理请求
	rc := &rpcConn{server: s, codec: c(conn)}
	rc.handle()
}

type rpcConn struct {
	server *Server
	codec  codec.Codec
	respMu sync.Mutex
}

type rpcReq struct {
	header codec.ReqHeader
	svc    *service.Service
	method *service.Method
	arg    reflect.Value
}

type rpcResp struct {
	err string
	val reflect.Value
}

// 处理一个合法的 RPC 连接
func (c *rpcConn) handle() {
	wg := sync.WaitGroup{}
	for {
		req := &rpcReq{}
		if err := c.readReq(req); err != nil {
			if err == io.EOF {
				// 连接处理完毕
				break
			}
			if req.header.Seq != 0 {
				// 有请求序号，需要发送错误响应
				resp := &rpcResp{err: err.Error()}
				c.makeResp(req, resp)
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.handleReq(req)
		}()
	}
	wg.Wait()
	c.codec.Close()
}

// 将一个 RPC 请求读入 req 中
func (c *rpcConn) readReq(req *rpcReq) (err error) {
	ch := make(chan error)
	defer close(ch)

	// 读取 RPC 请求头
	go func() {
		ch <- c.codec.ReadNext(&req.header)
	}()
	select {
	case err = <-ch:
		if err != nil {
			return
		}
	case <-timeoutSignal(c.server.opt.ConnIdleTimeout):
		return io.EOF
	}

	// 解析请求头
	svc, m, err := c.server.parseCallTarget(req.header.CallTarget)
	if err != nil {
		return
	}
	req.svc = svc
	req.method = m

	// 读取 RPC 请求参数
	if m.Arg.Kind() == reflect.Pointer {
		req.arg = reflect.New(m.Arg.Elem())
	} else {
		req.arg = reflect.New(m.Arg)
	}
	go func() {
		ch <- c.codec.ReadNext(req.arg.Interface())
	}()

	select {
	case err = <-ch:
		if err != nil {
			return
		}
	case <-timeoutSignal(c.server.opt.ReadWriteTimeout):
		return io.EOF
	}

	return
}

// 在单独的协程中处理一个 RPC 请求
func (c *rpcConn) handleReq(req *rpcReq) {
	defer func() {
		// 防止 panic 导致没有生成响应
		if err := recover(); err != nil {
			log.Printf("Handle request panic: %v", err)
			resp := &rpcResp{err: fmt.Sprintf("panic: %v", err)}
			c.makeResp(req, resp)
		}
	}()

	// 调用 RPC 服务，获取结果
	reply := reflect.New(req.method.Reply.Elem())
	err := req.svc.Call(req.method, req.arg, reply)

	// 封装 RPC 响应
	resp := &rpcResp{val: reply}
	if err != nil {
		resp.err = err.Error()
	}

	// 发送 RPC 响应
	c.makeResp(req, resp)
}

func (c *rpcConn) makeResp(req *rpcReq, resp *rpcResp) (err error) {
	c.respMu.Lock()
	defer c.respMu.Unlock()

	// 发送 RPC 响应
	respHeader := codec.RespHeader{Seq: req.header.Seq, Err: resp.err}
	if err = c.codec.Write(&respHeader); err != nil {
		return
	}
	if !resp.val.IsValid() {
		resp.val = reflect.ValueOf(struct{}{})
	}

	ch := make(chan error)
	go func() {
		ch <- c.codec.Write(resp.val.Interface())
	}()

	select {
	case err = <-ch:
		if err != nil {
			return
		}
	case <-timeoutSignal(c.server.opt.ReadWriteTimeout):
		return
	}
	return
}
