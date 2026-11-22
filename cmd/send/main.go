package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"raposa/internal/netem"
	"raposa/internal/transport"
)

// progressReader embrulha um io.Reader e desenha uma barra a cada leitura.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	start time.Time
	last  time.Time
	st    *transport.Stats
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if time.Since(p.last) > 100*time.Millisecond || err == io.EOF {
		p.draw()
		p.last = time.Now()
	}
	return n, err
}

func (p *progressReader) draw() {
	const width = 30
	frac := 1.0
	if p.total > 0 {
		frac = float64(p.read) / float64(p.total)
	}
	filled := int(frac * width)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)

	el := time.Since(p.start).Seconds()
	speed := 0.0
	if el > 0 {
		speed = float64(p.read) / 1024 / el
	}
	fmt.Printf("\r[%s] %5.1f%% | %.2f/%.2f MB | %.1f KB/s | retrans: %d   ",
		bar, frac*100, float64(p.read)/1e6, float64(p.total)/1e6, speed, p.st.Retrans)
}

func main() {
	server := flag.String("server", "127.0.0.1:9000", "endereço do receptor")
	file := flag.String("file", "", "arquivo a enviar")
	rto := flag.Duration("rto", 300*time.Millisecond, "timeout de retransmissão")
	delay := flag.Duration("delay", 0, "atraso simulado")
	jitter := flag.Duration("jitter", 0, "jitter simulado")
	loss := flag.Float64("loss", 0, "perda simulada (0 a 1)")
	flag.Parse()

	f, err := os.Open(*file)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()

	dst, err := net.ResolveUDPAddr("udp", *server)
	if err != nil {
		log.Fatal(err)
	}
	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		log.Fatal(err)
	}
	defer pc.Close()
	sim := netem.Wrap(pc, netem.Config{Delay: *delay, Jitter: *jitter, Loss: *loss})

	st := &transport.Stats{}
	start := time.Now()
	pr := &progressReader{r: f, total: info.Size(), start: start, st: st}

	if err := transport.SendStream(sim, dst, 1, pr, *rto, st); err != nil {
		fmt.Println()
		log.Fatal("erro: ", err)
	}
	el := time.Since(start)

	pr.draw()
	fmt.Printf("\n%d bytes em %v | pacotes: %d | retransmissões: %d\n",
		info.Size(), el.Round(time.Millisecond), st.Packets, st.Retrans)
}
