package transport

import (
	"io"

	"github.com/hoywu/flexrpc/codec"
)

const (
	MagicNum = 0xf925
	Version  = uint8(1) // protocol version
)

// Transport header(4 byte):
// magicNum(2 bytes) + version(1 byte) + codec(1 byte)
type Header struct {
	MagicNum uint16
	Version  uint8
	Codec    codec.CodecType
}

type HeaderEncoder struct {
	w io.Writer
}

type HeaderDecoder struct {
	r io.Reader
}

func NewHeaderEncoder(w io.Writer) *HeaderEncoder {
	return &HeaderEncoder{w: w}
}

func (e *HeaderEncoder) Encode(codec codec.CodecType) error {
	var buf [4]byte
	buf[0] = byte(MagicNum >> 8)
	buf[1] = byte(MagicNum & 0xff)
	buf[2] = Version
	buf[3] = codec
	_, err := e.w.Write(buf[:])
	return err
}

func NewHeaderDecoder(r io.Reader) *HeaderDecoder {
	return &HeaderDecoder{r: r}
}

func (d *HeaderDecoder) Decode(h *Header) error {
	var buf [4]byte
	_, err := io.ReadFull(d.r, buf[:])
	if err != nil {
		return err
	}
	h.MagicNum = uint16(buf[0])<<8 | uint16(buf[1])
	h.Version = buf[2]
	h.Codec = buf[3]
	return nil
}
