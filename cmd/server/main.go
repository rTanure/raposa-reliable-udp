package main

import (
	"flag"
	"log"
	"net"
)

func main() {
	addr := flag.String("addr", ":9000", "endereço UDP para escutar")
	flag.Parse()

	pc, err := net.ListenPacket("udp", *addr)
	if (err) != nil {
		log.Fatal(err)
	}

	defer pc.Close()
	log.Println("Servidor ouvindo a porta", pc.LocalAddr())

	buf := make([]byte, 1500)

	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			log.Println("Erro ao ler o buffer", err)
			continue
		}

		log.Printf("Recebido: %q de %s", buf[:n], from)

		if _, err := pc.WriteTo([]byte("world"), from); err != nil {
			log.Println("erro de escrita", err)
		}

	}
}
