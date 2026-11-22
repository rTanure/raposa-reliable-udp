package main

import (
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"time"

	"raposa/internal/proto"
)

func main() {
	addr := flag.String("addr", ":9000", "endereço UDP para escutar")
	expected := flag.Int("expected", 1000, "quantos pacotes o cliente vai enviar")
	flag.Parse()

	pc, err := net.ListenPacket("udp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	defer pc.Close()
	log.Println("servidor ouvindo em", pc.LocalAddr())

	buf := make([]byte, 1500)
	var received, outOfOrder, corrupt int
	maxSeq := int64(-1)

	for {
		if received > 0 {
			pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		} else {
			pc.SetReadDeadline(time.Time{})
		}

		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				log.Printf("--- rodada encerrada ---")
				log.Printf("esperados: %d | chegaram: %d | perdidos: %d", *expected, received, *expected-received)
				log.Printf("fora de ordem: %d | corrompidos: %d", outOfOrder, corrupt)
				received, outOfOrder, corrupt, maxSeq = 0, 0, 0, -1
				continue
			}
			log.Println("erro de leitura:", err)
			continue
		}

		p, err := proto.Unmarshal(buf[:n])
		if err != nil {
			corrupt++
			continue
		}
		if p.Type != proto.TypeData {
			continue
		}

		received++
		if int64(p.Seq) < maxSeq {
			outOfOrder++ // chegou depois de um pacote com Seq maior
		} else {
			maxSeq = int64(p.Seq)
		}
	}
}
