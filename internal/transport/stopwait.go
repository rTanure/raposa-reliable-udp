package transport

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"raposa/internal/proto"
	"time"
)

const maxRetries = 20

func readPacket(pc net.PacketConn, peer net.Addr, buf []byte, deadline time.Time) (*proto.Packet, net.Addr, error) {
	for {
		pc.SetReadDeadline(deadline)

		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return nil, nil, err
		}

		if peer != nil && from.String() != peer.String() {
			continue
		}

		p, err := proto.Unmarshal(buf[:n])

		if err != nil {
			continue // trata como se tivesse perdido
		}
		return p, from, nil
	}
}

func sendAndWaitAck(pc net.PacketConn, peer net.Addr, pkt *proto.Packet, rto time.Duration, st *Stats) error {
	data := pkt.Marshal()
	buf := make([]byte, 1500)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if _, err := pc.WriteTo(data, peer); err != nil {
			return err
		}
		st.Packets++
		if attempt > 0 {
			st.Retrans++
		}
		deadline := time.Now().Add(rto)
		for {
			p, _, err := readPacket(pc, peer, buf, deadline)
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break // estourou o tempo: sai e retransmite
			}
			if err != nil {
				return err
			}
			if p.Type == proto.TypeAck && p.Ack == pkt.Seq {
				return nil
			}
			// ACK antigo ou outro tipo: ignora e continua esperando
		}
	}
	return fmt.Errorf("sem ACK para seq %d após %d tentativas", pkt.Seq, maxRetries)
}

// Envia os dados e termina enviando um FIN
func SendStream(pc net.PacketConn, peer net.Addr, connID uint32, r io.Reader, rto time.Duration, st *Stats) error {
	chunk := make([]byte, proto.MaxPayload)
	var seq uint32
	for {
		n, err := io.ReadFull(r, chunk)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		typ := proto.TypeData
		if n == 0 {
			typ = proto.TypeFin // acabou o arquivo
		}
		pkt := &proto.Packet{Type: typ, ConnID: connID, Seq: seq, Payload: chunk[:n]}
		if err := sendAndWaitAck(pc, peer, pkt, rto, st); err != nil {
			return err
		}
		if typ == proto.TypeFin {
			return nil
		}
		seq++
	}
}

// Grava tudo que chegar até receber um FIN
func RecvStream(pc net.PacketConn, w io.Writer, rto time.Duration) error {
	buf := make([]byte, 1500)
	var peer net.Addr
	var expected uint32

	ack := func(seq uint32) {
		pc.WriteTo((&proto.Packet{Type: proto.TypeAck, Ack: seq}).Marshal(), peer)
	}

	for {
		p, from, err := readPacket(pc, peer, buf, time.Now().Add(10*time.Second))
		if err != nil {
			return fmt.Errorf("conexão ociosa: %w", err)
		}
		if peer == nil {
			peer = from
		}

		switch p.Type {
		case proto.TypeData:
			if p.Seq == expected {
				if _, err := w.Write(p.Payload); err != nil {
					return err
				}
				expected++
			}
			if p.Seq < expected { // novo ou duplicado: confirma
				ack(p.Seq)
			}
		case proto.TypeFin:
			if p.Seq != expected {
				continue
			}
			ack(p.Seq)
			// Fica um pouco mais reenviando o ACK do FIN, caso ele se perca.
			deadline := time.Now().Add(3 * rto)
			for {
				q, _, err := readPacket(pc, peer, buf, deadline)
				if err != nil {
					return nil
				}
				if q.Type == proto.TypeFin {
					ack(q.Seq)
				}
			}
		}
	}
}
