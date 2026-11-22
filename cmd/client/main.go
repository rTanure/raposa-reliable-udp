package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"time"

	"raposa/internal/netem"
	"raposa/internal/proto"
)

func main() {
	server := flag.String("server", "127.0.0.1:9000", "endereço do servidor")
	n := flag.Int("n", 1000, "quantidade de pacotes")
	interval := flag.Duration("interval", time.Millisecond, "pausa entre envios")
	delay := flag.Duration("delay", 0, "atraso simulado")
	jitter := flag.Duration("jitter", 0, "variação do atraso")
	loss := flag.Float64("loss", 0, "probabilidade de perda (0 a 1)")
	flag.Parse()

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

	for i := 0; i < *n; i++ {
		p := &proto.Packet{Type: proto.TypeData, Seq: uint32(i), Payload: []byte("x")}
		if _, err := sim.WriteTo(p.Marshal(), dst); err != nil {
			log.Println("erro de envio:", err)
		}
		time.Sleep(*interval)
	}

	// Espera os pacotes atrasados saírem, senão o programa termina antes deles.
	time.Sleep(*delay + *jitter + 500*time.Millisecond)

	fmt.Printf("enviados: %d | descartados pelo netem: %d\n", sim.Sent.Load(), sim.Dropped.Load())
}
