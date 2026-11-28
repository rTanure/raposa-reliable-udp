package transport

// Testes do transporte sem rede de verdade: dois "links" ligados por
// channels, com perda e corrupção aleatórias.
// Rodar com:  go test ./internal/transport/ -v

import (
	"bytes"
	"crypto/rand"
	mathrand "math/rand"
	"os"
	"testing"
	"time"

	"raposa/internal/proto"
)

// memoryLink é um Link que entrega os pacotes direto na fila do outro lado.
type memoryLink struct {
	inbox   chan []byte
	peer    *memoryLink
	loss    float64
	corrupt float64
	drop    func(packet *proto.Packet) bool // opcional: perde pacotes escolhidos
}

// newMemoryPair cria duas pontas ligadas entre si.
func newMemoryPair(loss, corrupt float64) (*memoryLink, *memoryLink) {
	left := &memoryLink{inbox: make(chan []byte, 8192), loss: loss, corrupt: corrupt}
	right := &memoryLink{inbox: make(chan []byte, 8192), loss: loss, corrupt: corrupt}
	left.peer, right.peer = right, left
	return left, right
}

func (link *memoryLink) Send(packet *proto.Packet) error {
	if mathrand.Float64() < link.loss || (link.drop != nil && link.drop(packet)) {
		return nil // perdido
	}
	data := packet.Marshal()
	if mathrand.Float64() < link.corrupt {
		data[mathrand.Intn(len(data))] ^= 0xFF
	}
	select {
	case link.peer.inbox <- data:
	default: // fila cheia: perdido
	}
	return nil
}

func (link *memoryLink) Receive(deadline time.Time) (*proto.Packet, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case data := <-link.inbox:
			packet, err := proto.Unmarshal(data)
			if err != nil {
				continue // corrompido
			}
			return packet, nil
		case <-timer.C:
			return nil, os.ErrDeadlineExceeded
		}
	}
}

// transfer envia "original" de um lado para o outro e devolve o que chegou.
func transfer(t *testing.T, algorithm string, original []byte, window int, loss, corrupt float64) ([]byte, *Stats, *Result) {
	t.Helper()
	senderSide, receiverSide := newMemoryPair(loss, corrupt)

	stats := &Stats{}
	sendErr := make(chan error, 1)
	go func() {
		options := SendOptions{Window: window, RTO: 50 * time.Millisecond}
		sendErr <- Send(algorithm, senderSide, bytes.NewReader(original), options, stats)
	}()

	var received bytes.Buffer
	result, err := Receive(algorithm, receiverSide, &received, ReceiveOptions{RTO: 50 * time.Millisecond, Linger: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("receptor: %v", err)
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("emissor: %v", err)
	}
	return received.Bytes(), stats, result
}

func randomBytes(size int) []byte {
	data := make([]byte, size)
	rand.Read(data)
	return data
}

func TestTransferWithoutLoss(t *testing.T) {
	for _, algorithm := range []string{AlgorithmGBN, AlgorithmSACK} {
		t.Run(algorithm, func(t *testing.T) {
			original := randomBytes(300_000)
			received, stats, result := transfer(t, algorithm, original, 64, 0, 0)
			if !bytes.Equal(original, received) {
				t.Fatal("conteúdo diferente")
			}
			if stats.Retrans != 0 {
				t.Errorf("sem perda não deveria retransmitir, retransmitiu %d", stats.Retrans)
			}
			if result.SenderSHA256 != result.ReceivedSHA256 {
				t.Error("hashes diferentes")
			}
		})
	}
}

func TestTransferWithLossAndCorruption(t *testing.T) {
	for _, algorithm := range []string{AlgorithmGBN, AlgorithmSACK} {
		t.Run(algorithm, func(t *testing.T) {
			original := randomBytes(500_000)
			received, stats, _ := transfer(t, algorithm, original, 32, 0.10, 0.02)
			if !bytes.Equal(original, received) {
				t.Fatal("conteúdo diferente")
			}
			t.Logf("%s: %d pacotes, %d retransmissões", algorithm, stats.Packets, stats.Retrans)
		})
	}
}

// Com perda, o SACK deve retransmitir bem menos que o GBN.
func TestSackRetransmitsLessThanGBN(t *testing.T) {
	original := randomBytes(500_000)
	_, gbnStats, _ := transfer(t, AlgorithmGBN, original, 64, 0.05, 0)
	_, sackStats, _ := transfer(t, AlgorithmSACK, original, 64, 0.05, 0)
	t.Logf("retransmissões: GBN=%d SACK=%d", gbnStats.Retrans, sackStats.Retrans)
	if sackStats.Retrans >= gbnStats.Retrans {
		t.Errorf("SACK (%d) deveria retransmitir menos que GBN (%d)", sackStats.Retrans, gbnStats.Retrans)
	}
}

func TestEmptyFile(t *testing.T) {
	for _, algorithm := range []string{AlgorithmGBN, AlgorithmSACK} {
		received, _, _ := transfer(t, algorithm, nil, 8, 0.1, 0)
		if len(received) != 0 {
			t.Fatalf("%s: esperava vazio", algorithm)
		}
	}
}

func TestPacketRoundTrip(t *testing.T) {
	original := &proto.Packet{Type: proto.TypeAck, Flags: 1, Window: 7, ConnID: 9, Seq: 3, Ack: 4, Sack: 0xF0F0, Payload: []byte("raposa")}
	decoded, err := proto.Unmarshal(original.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Sack != original.Sack || decoded.Ack != original.Ack || string(decoded.Payload) != "raposa" {
		t.Fatalf("diferente: %+v", decoded)
	}

	corrupted := original.Marshal()
	corrupted[proto.HeaderSize] ^= 0xFF
	if _, err := proto.Unmarshal(corrupted); err != proto.ErrBadChecksum {
		t.Fatalf("esperava ErrBadChecksum, veio %v", err)
	}
}

// Caso que travava o upload: o FIN chega ANTES de um pacote perdido
// (o emissor fica sabendo que o FIN chegou) e depois o ACK final se perde.
// O emissor reenvia só o DATA que acha que falta; o receptor precisa
// responder a esse DATA com a confirmação final.
func TestFinalAckLostAfterFinArrivedEarly(t *testing.T) {
	for _, algorithm := range []string{AlgorithmGBN, AlgorithmSACK} {
		t.Run(algorithm, func(t *testing.T) {
			original := randomBytes(5 * 1200) // pacotes 0..4 + FIN (seq 5)
			senderSide, receiverSide := newMemoryPair(0, 0)

			droppedData, droppedFinalAck := false, false
			senderSide.drop = func(packet *proto.Packet) bool { // perde o DATA 2 uma vez
				if packet.Type == proto.TypeData && packet.Seq == 2 && !droppedData {
					droppedData = true
					return true
				}
				return false
			}
			receiverSide.drop = func(packet *proto.Packet) bool { // perde o ACK final uma vez
				if packet.Type == proto.TypeAck && packet.Ack == 6 && !droppedFinalAck {
					droppedFinalAck = true
					return true
				}
				return false
			}

			stats := &Stats{}
			sendErr := make(chan error, 1)
			go func() {
				options := SendOptions{Window: 16, RTO: 50 * time.Millisecond}
				sendErr <- Send(algorithm, senderSide, bytes.NewReader(original), options, stats)
			}()

			var received bytes.Buffer
			options := ReceiveOptions{RTO: 50 * time.Millisecond, Linger: 2 * time.Second}
			if _, err := Receive(algorithm, receiverSide, &received, options); err != nil {
				t.Fatalf("receptor: %v", err)
			}
			select {
			case err := <-sendErr:
				if err != nil {
					t.Fatalf("emissor: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("emissor travou esperando a confirmação final")
			}
			if !bytes.Equal(original, received.Bytes()) || !droppedFinalAck {
				t.Fatal("conteúdo diferente ou cenário não aconteceu")
			}
		})
	}
}
