package transport

import (
	"net"
	"os"
	"time"

	"raposa/internal/proto"
)

// Link é a "conversa" com UM outro ponto da rede.
//
// Isto é uma INTERFACE: ela só lista métodos. Qualquer tipo que tenha
// Send e Receive com essas assinaturas "é" um Link automaticamente.
// Assim o GBN e o SACK funcionam igual no cliente (que lê direto do
// socket) e no servidor (onde cada sessão recebe de uma fila própria).
type Link interface {
	// Send envia um pacote para o outro lado.
	Send(packet *proto.Packet) error
	// Receive espera um pacote válido até "deadline".
	// Se o prazo acabar, devolve os.ErrDeadlineExceeded.
	// deadline zero (time.Time{}) = espera para sempre.
	Receive(deadline time.Time) (*proto.Packet, error)
}

// =====================================================================
// UDPLink: usado pelo CLIENTE (um socket conversando com um servidor)
// =====================================================================

type UDPLink struct {
	conn      net.PacketConn
	peer      net.Addr
	buffer    []byte
	ConnID    uint32 // preenchido após o handshake; vai em todo pacote enviado
	Corrupted int    // pacotes descartados por checksum inválido
}

func NewUDPLink(conn net.PacketConn, peer net.Addr) *UDPLink {
	return &UDPLink{conn: conn, peer: peer, buffer: make([]byte, 65535)}
}

func (link *UDPLink) Send(packet *proto.Packet) error {
	packet.ConnID = link.ConnID
	_, err := link.conn.WriteTo(packet.Marshal(), link.peer)
	return err
}

func (link *UDPLink) Receive(deadline time.Time) (*proto.Packet, error) {
	for {
		link.conn.SetReadDeadline(deadline)
		bytesRead, from, err := link.conn.ReadFrom(link.buffer)
		if err != nil {
			return nil, err
		}
		if from.String() != link.peer.String() {
			continue // pacote de outro endereço
		}

		packet, err := proto.Unmarshal(link.buffer[:bytesRead])
		if err != nil {
			link.Corrupted++
			continue // corrompido: trata como perdido
		}
		if link.ConnID != 0 && packet.ConnID != link.ConnID {
			continue // de outra sessão (ex.: resposta atrasada de um HELLO repetido)
		}
		return packet, nil
	}
}

// =====================================================================
// ChannelLink: usado pelo SERVIDOR (uma por sessão)
//
// No servidor, uma única goroutine lê o socket e distribui os pacotes
// pelo ConnID. Cada sessão recebe os seus por um "channel" (Inbox).
// =====================================================================

type ChannelLink struct {
	conn   net.PacketConn
	peer   net.Addr
	connID uint32
	Inbox  chan *proto.Packet // o distribuidor coloca aqui; a sessão tira
}

func NewChannelLink(conn net.PacketConn, peer net.Addr, connID uint32) *ChannelLink {
	return &ChannelLink{conn: conn, peer: peer, connID: connID, Inbox: make(chan *proto.Packet, 4096)}
}

func (link *ChannelLink) Send(packet *proto.Packet) error {
	packet.ConnID = link.connID
	_, err := link.conn.WriteTo(packet.Marshal(), link.peer)
	return err
}

func (link *ChannelLink) Receive(deadline time.Time) (*proto.Packet, error) {
	if deadline.IsZero() {
		return <-link.Inbox, nil // "<-canal" espera até chegar algo
	}

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	// select espera o que acontecer primeiro: chegar pacote ou o timer tocar.
	select {
	case packet := <-link.Inbox:
		return packet, nil
	case <-timer.C:
		return nil, os.ErrDeadlineExceeded
	}
}
