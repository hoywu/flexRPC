package flexrpc

import (
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"sync"

	"github.com/hoywu/flexrpc/codec"
	"github.com/hoywu/flexrpc/transport"
)

type Server struct{}

func NewServer() *Server {
	return &Server{}
}

func (s *Server) Accept(lis net.Listener) {
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
	if err := transport.NewHeaderDecoder(conn).Decode(&header); err != nil {
		// 报文头解码错误
		log.Printf("Decode header error: %v", err)
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
	rc := &rpcConn{codec: c(conn)}
	rc.handle()
}

type rpcConn struct {
	codec  codec.Codec
	respMu sync.Mutex
}

type rpcReq struct {
	header codec.ReqHeader
	arg    reflect.Value // TODO
}

type rpcResp struct {
	err string
	val reflect.Value
}

// 处理一个合法的 RPC 连接
func (c *rpcConn) handle() {
	wg := sync.WaitGroup{}
	for {
		// 读入 RPC 请求
		req := &rpcReq{}
		if err := c.readReq(req); err != nil {
			if err == io.EOF {
				// 连接处理完毕
				break
			}
			// RPC 请求读取错误
			log.Printf("Read request error: %v", err)
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 处理 RPC 请求
			c.handleReq(req)
		}()
	}
	wg.Wait()
	c.codec.Close()
}

func (c *rpcConn) readReq(req *rpcReq) (err error) {
	// 读取 RPC 请求头
	if err = c.codec.ReadNext(&req.header); err != nil {
		return
	}

	// TODO: 读取 RPC 请求参数
	req.arg = reflect.New(reflect.TypeOf(""))
	if err = c.codec.ReadNext(req.arg.Interface()); err != nil {
		return
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

	// TODO: 调用 RPC 服务，将结果写入 resp
	resp := &rpcResp{}
	resp.val = reflect.ValueOf(fmt.Sprintf("Hello, %v", req.arg.Elem().Interface()))

	// 结果准备完毕，发送 RPC 响应
	c.makeResp(req, resp)
}

func (c *rpcConn) makeResp(req *rpcReq, resp *rpcResp) (err error) {
	c.respMu.Lock()
	defer c.respMu.Unlock()

	// TODO: 发送 RPC 响应
	respHeader := codec.RespHeader{Seq: req.header.Seq, Err: resp.err}
	if err = c.codec.Write(&respHeader); err != nil {
		return
	}
	if err = c.codec.Write(resp.val.Interface()); err != nil {
		return
	}
	return
}
