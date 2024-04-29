package codec

import (
	"bufio"
	"encoding/gob"
	"io"
)

type GobCodec struct {
	conn io.ReadWriteCloser
	buf  *bufio.Writer
	dec  *gob.Decoder
	enc  *gob.Encoder
}

// ReadNext implements codec.Codec.
func (c *GobCodec) ReadNext(b any) error {
	return c.dec.Decode(b)
}

// Write implements codec.Codec.
func (c *GobCodec) Write(b any) (err error) {
	defer func() {
		c.buf.Flush()
		if err != nil {
			// close the connection if error occurs
			c.Close()
		}
	}()

	if err = c.enc.Encode(b); err != nil {
		return
	}
	return
}

// Close implements codec.Codec.
func (c *GobCodec) Close() error {
	return c.conn.Close()
}

func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriterSize(conn, 4096)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn),
		enc:  gob.NewEncoder(buf),
	}
}
