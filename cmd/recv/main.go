package main

import (
	"flag"
	"log"
	"net"
	"os"
	"time"

	"raposa/internal/netem"
	"raposa/internal/transport"
)

func main() {
	addr := flag.String("addr", ":9000", "endereço UDP")
	out := flag.String("out", "recebido.bin", "arquivo de saída")
	rto := flag.Duration("rto", 300*time.Millisecond, "timeout de retransmissão")
	delay := flag.Duration("delay", 0, "atraso simulado")
	jitter := flag.Duration("jitter", 0, "jitter simulado")
	loss := flag.Float64("loss", 0, "perda simulada (0 a 1)")
	flag.Parse()

	pc, err := net.ListenPacket("udp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	defer pc.Close()
	sim := netem.Wrap(pc, netem.Config{Delay: *delay, Jitter: *jitter, Loss: *loss})

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	log.Println("aguardando em", pc.LocalAddr())
	if err := transport.RecvStream(sim, f, *rto); err != nil {
		log.Fatal("erro:", err)
	}
	log.Println("arquivo salvo em", *out)
}
