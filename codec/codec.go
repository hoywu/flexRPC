package codec

import (
	"io"
)

type CodecType = byte

const (
	// 在这里定义编解码器类型
	GobCodecType  = 0x01
	JsonCodecType = 0x02
)

type Codec interface {
	io.Closer
	ReadNext(any) error // Read the next value from the connection, and store result in the argument
	Write(any) error    // Write to the connection
}

type NewCodecFunc func(io.ReadWriteCloser) Codec

var CodecImpl map[CodecType]NewCodecFunc = map[CodecType]NewCodecFunc{
	// 在这里注册编解码器实现
	GobCodecType: NewGobCodec,
}

type ReqHeader struct {
	Seq         uint64 // sequence number
	CallTarget  string // target to call, "Service.Method"
	StreamCall  bool   // TODO
	StreamReply bool   // TODO
}

type RespHeader struct {
	Seq uint64 // sequence number
	Err string // error message
}
