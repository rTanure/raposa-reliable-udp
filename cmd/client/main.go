package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"time"
)

func main() {
	server := flag.String("server", "127.0.0.1:9000", "endereço do servidor")
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

	start := time.Now()
	if _, err := pc.WriteTo([]byte("hello"), dst); err != nil {
		log.Fatal(err)
	}

	pc.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 1500)
	n, from, err := pc.ReadFrom(buf)

	if err != nil {
		log.Fatal("sem resposta do servidor: ", err)
	}

	fmt.Printf("resposta %q de %s em %v\n", buf[:n], from, time.Since(start))
}
