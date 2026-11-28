package transport

// =====================================================================
// GO-BACK-N (janela deslizante)
//
// Ideia geral:
//   - O emissor pode ter até N pacotes "no ar" (enviados e sem ACK).
//     N é o tamanho da janela.
//   - O receptor só aceita o pacote que ele está esperando (em ordem).
//     Qualquer outro é descartado.
//   - O ACK é cumulativo: Ack = X quer dizer "já recebi tudo abaixo de X,
//     me manda o X".
//   - Se o pacote mais antigo da janela não for confirmado a tempo
//     (timeout), o emissor "volta" e reenvia a janela inteira.
//     Daí o nome Go-Back-N.
//   - Com janela = 1, isto vira Stop-and-Wait.
//
//   seq:   0   1   2   3   4   5   6   7   8   9
//        [ok][ok][ ][ ][ ][ ][ ][ ][ ][ ]
//                 ^base           ^nextSeq
//                 |<--- no ar --->|<- pode enviar se a janela deixar
// =====================================================================

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"raposa/internal/proto"
)

// gbnSender guarda todo o estado do emissor.
type gbnSender struct {
	link    Link
	options SendOptions
	stats   *Stats
	rto     *rtoEstimator
	source  *packetSource

	inFlight map[uint32]*sentPacket // pacotes no ar, indexados pelo número de sequência
	base     uint32                 // seq do pacote mais antigo ainda sem ACK
	nextSeq  uint32                 // seq do próximo pacote novo

	timerDeadline time.Time // quando o timer do pacote "base" estoura
	timeoutsInRow int       // timeouts seguidos sem nenhum progresso
	duplicateAcks int       // ACKs repetidos seguidos (indício de perda)
}

// =====================================================================
// EMISSOR
// =====================================================================

// SendGBN lê todo o conteúdo de "input", envia com Go-Back-N e termina
// com um pacote FIN. Só retorna quando o FIN for confirmado (ou em erro).
func SendGBN(link Link, input io.Reader, options SendOptions, stats *Stats) error {
	options.Window = normalizeWindow(options.Window)

	sender := &gbnSender{
		link:     link,
		options:  options,
		stats:    stats,
		rto:      newRTOEstimator(options.RTO),
		source:   newPacketSource(input),
		inFlight: make(map[uint32]*sentPacket),
	}
	stats.Window = options.Window

	for {
		if options.OnProgress != nil {
			stats.RTO = sender.rto.Current()
			options.OnProgress()
		}

		// PASSO 1: enquanto houver espaço na janela, envia pacotes novos.
		for !sender.source.finished && sender.windowHasRoom() {
			packet, err := sender.source.Next()
			if err != nil {
				return err
			}
			if err := sender.sendNewPacket(packet); err != nil {
				return err
			}
		}

		// PASSO 2: terminou? (FIN enviado e tudo confirmado)
		if sender.source.finished && sender.base == sender.nextSeq {
			stats.SHA256 = sender.source.Hash()
			return nil
		}

		// PASSO 3: espera um ACK, mas só até o timer estourar.
		ack, err := sender.link.Receive(sender.timerDeadline)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			if err := sender.handleTimeout(); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}

		// PASSO 4: processa o ACK (pode deslizar a janela).
		if err := sender.handleAck(ack); err != nil {
			return err
		}
	}
}

// windowHasRoom diz se ainda cabe mais um pacote no ar.
func (sender *gbnSender) windowHasRoom() bool {
	packetsInFlight := int(sender.nextSeq - sender.base)
	return packetsInFlight < sender.options.Window
}

// sendNewPacket envia um pacote pela primeira vez.
func (sender *gbnSender) sendNewPacket(packet *proto.Packet) error {
	// Se a janela estava vazia, este pacote passa a ser o "base":
	// liga o timer para ele.
	if sender.base == sender.nextSeq {
		sender.restartTimer()
	}

	pending := &sentPacket{packet: packet}
	sender.inFlight[packet.Seq] = pending
	sender.nextSeq++
	return sender.transmit(pending)
}

// transmit coloca o pacote na rede e atualiza as estatísticas.
func (sender *gbnSender) transmit(pending *sentPacket) error {
	pending.sentAt = time.Now()
	sender.stats.Packets++
	return sender.link.Send(pending.packet)
}

// resendWholeWindow é o "go back": reenvia tudo de base até nextSeq-1.
func (sender *gbnSender) resendWholeWindow() error {
	for seq := sender.base; seq < sender.nextSeq; seq++ {
		pending := sender.inFlight[seq]
		pending.packet.Flags |= proto.FlagRetrans // marca no cabeçalho (útil no Wireshark)
		pending.retransmitted = true
		sender.stats.Retrans++
		if err := sender.transmit(pending); err != nil {
			return err
		}
	}
	sender.restartTimer()
	return nil
}

func (sender *gbnSender) restartTimer() {
	sender.timerDeadline = time.Now().Add(sender.rto.Current())
}

// handleTimeout: o pacote base não foi confirmado a tempo.
func (sender *gbnSender) handleTimeout() error {
	sender.timeoutsInRow++
	if sender.timeoutsInRow > maxRetries {
		return fmt.Errorf("sem resposta do receptor (seq %d, %d timeouts seguidos)", sender.base, maxRetries)
	}
	sender.stats.Timeouts++
	sender.rto.Backoff()
	return sender.resendWholeWindow()
}

// handleAck trata um pacote recebido do receptor.
func (sender *gbnSender) handleAck(ack *proto.Packet) error {
	if ack.Type == proto.TypeErr {
		return proto.ParseError(ack) // o receptor desistiu (ex.: hash divergente)
	}
	if ack.Type != proto.TypeAck {
		return nil // não é ACK: ignora
	}

	// O receptor devolve no campo Seq o número do pacote que gerou este
	// ACK ("eco"). Com isso medimos o RTT exato daquele pacote.
	sender.measureRTT(ack.Seq)

	// ACK fora da janela: é velho (atrasado) ou inválido. Ignora.
	if ack.Ack < sender.base || ack.Ack > sender.nextSeq {
		return nil
	}

	// ACK repetido: o receptor continua pedindo o "base". Isso acontece
	// quando o base se perdeu e os pacotes seguintes estão chegando.
	// Com 3 repetidos seguidos já reenvia, sem esperar o timeout
	// (fast retransmit, igual ao TCP).
	if ack.Ack == sender.base {
		sender.duplicateAcks++
		if sender.duplicateAcks == 3 {
			sender.stats.FastRetrans++
			return sender.resendWholeWindow()
		}
		return nil
	}

	// ACK novo: tudo abaixo de ack.Ack chegou. Desliza a janela.
	sender.slideWindowTo(ack.Ack)
	return nil
}

// measureRTT usa o eco do ACK. Regra de Karn: não mede pacotes
// retransmitidos, porque não dá para saber a qual envio o ACK se refere.
func (sender *gbnSender) measureRTT(echoSeq uint32) {
	pending, found := sender.inFlight[echoSeq]
	if !found || pending.retransmitted {
		return
	}
	sample := time.Since(pending.sentAt)
	sender.rto.AddSample(sample)
	sender.stats.addRTT(sample)
}

// slideWindowTo descarta da memória os pacotes confirmados e move o base.
func (sender *gbnSender) slideWindowTo(newBase uint32) {
	for seq := sender.base; seq < newBase; seq++ {
		sender.stats.BytesAcked += int64(len(sender.inFlight[seq].packet.Payload))
		delete(sender.inFlight, seq)
	}

	sender.base = newBase
	sender.timeoutsInRow = 0
	sender.duplicateAcks = 0
	sender.restartTimer()
}

// =====================================================================
// RECEPTOR
// =====================================================================

// RecvGBN grava em "output", em ordem, tudo o que chegar até o FIN.
// Só aceita o pacote esperado; qualquer outro é descartado.
func RecvGBN(link Link, output io.Writer, options ReceiveOptions) (*Result, error) {
	receiver := newStreamReceiver(link, output, options)

	// sendAck é uma função dentro da função (closure): ela enxerga a
	// variável "receiver" daqui de cima.
	// O ACK diz sempre: "já tenho tudo abaixo de expected".
	sendAck := func(echo uint32) *proto.Packet {
		ack := &proto.Packet{
			Type:   proto.TypeAck,
			Seq:    echo, // eco: o emissor usa para medir o RTT
			Ack:    receiver.expected,
			Window: proto.MaxWindow,
		}
		receiver.link.Send(ack)
		return ack
	}

	for {
		packet, err := receiver.nextPacket()
		if err != nil {
			return receiver.result, err
		}

		// Fora de ordem (ou duplicado): descarta e repete o último ACK.
		// É esse ACK repetido que dispara o fast retransmit no emissor.
		if packet.Seq != receiver.expected {
			receiver.result.Duplicates++
			sendAck(packet.Seq)
			continue
		}

		// É o FIN: o arquivo acabou.
		if packet.Type == proto.TypeFin {
			return receiver.finish(packet, sendAck)
		}

		// Pacote esperado chegou: grava e confirma.
		if err := receiver.write(packet); err != nil {
			return receiver.result, err
		}
		sendAck(packet.Seq)
	}
}
