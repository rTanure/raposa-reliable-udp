package netem

import (
	"math/rand"
	"net"
	"sync/atomic"
	"time"
)

type Config struct {
	Delay  time.Duration
	Loss   float64
	Jitter time.Duration
}

type Conn struct {
	net.PacketConn
	cfg     Config
	Sent    atomic.Uint64
	Dropped atomic.Uint64
}

// Encapsula o socker verdadeiro no simulador
func Wrap(pc net.PacketConn, cfg Config) *Conn {
	return &Conn{PacketConn: pc, cfg: cfg}
}

func (c *Conn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.Sent.Add(1)

	if rand.Float64() < c.cfg.Loss {
		c.Dropped.Add(1)
		return len(b), nil
	}

	if c.cfg.Delay == 0 {
		return c.PacketConn.WriteTo(b, addr)
	}

	buf := append([]byte(nil), b...)

	d := c.cfg.Delay
	if c.cfg.Jitter > 0 {
		d += time.Duration((rand.Float64()*2 - 1) * float64(c.cfg.Jitter))
		if d < 0 {
			d = 0
		}
	}

	time.AfterFunc(c.cfg.Delay, func() {
		c.PacketConn.WriteTo(buf, addr)
	})

	return len(b), nil
}
