package transport

// =====================================================================
// SELECTIVE REPEAT COM SACK (janela deslizante)
//
// Diferença para o Go-Back-N:
//   - O receptor GUARDA os pacotes que chegam fora de ordem, em vez de
//     descartar.
//   - O emissor reenvia SÓ os pacotes que faltam.
//
// Como o emissor sabe o que já chegou? Cada ACK traz três informações:
//   Ack  = cumulativo: "tenho tudo abaixo de Ack" (igual ao GBN);
//   Seq  = eco: "este ACK foi gerado pelo pacote Seq" (então o Seq chegou);
//   Sack = bitmap de 64 bits sobre os pacotes ANTERIORES ao eco:
//          bit i ligado = "também tenho o pacote Seq-1-i".
//          É redundância: se o ACK do pacote 500 se perder, o ACK do 501
//          avisa (bit 0) que o 500 chegou.
//
// Exemplo: o pacote 5 se perdeu, chegaram 6, 7 e 9.
//
//   recebidos: 0 1 2 3 4 _ 6 7 _ 9
//   ACK do 9:  Ack = 5, Seq = 9, Sack: bit 0 (pacote 8) = 0, bit 1 (7) = 1,
//              bit 2 (6) = 1, bit 3 (5) = 0, bits 4 a 8 (4..0) = 1
//
// Como o emissor decide que um pacote se perdeu? (ideia do RACK do TCP,
// RFC 8985): se já chegou um pacote que foi enviado DEPOIS dele, ele
// deveria ter chegado também. Damos uma pequena folga (RTT/4) para o caso
// de a rede trocar a ordem. No exemplo, o 9 foi enviado depois do 5 e do
// 8 e já chegou: 5 e 8 são reenviados na hora, sem esperar timeout.
// Isso também vale para uma retransmissão que se perdeu de novo.
//
// Timer: se nada de novo for confirmado por um RTO inteiro, reenvia tudo
// o que está sem confirmação há mais de um RTO.
// =====================================================================

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"raposa/internal/proto"
)

type sackSender struct {
	link    Link
	options SendOptions
	stats   *Stats
	rto     *rtoEstimator
	source  *packetSource

	inFlight   map[uint32]*sentPacket
	base       uint32 // mais antigo ainda sem ACK cumulativo
	nextSeq    uint32 // próximo pacote novo
	peerWindow int    // janela anunciada pelo receptor (controle de fluxo)

	// Quando foi enviado o pacote mais recente que já sabemos que chegou.
	// Tudo que foi enviado bem antes disso e não chegou, se perdeu.
	latestDeliveredSentAt time.Time

	timerDeadline time.Time
	timeoutsInRow int
}

// =====================================================================
// EMISSOR
// =====================================================================

// SendSACK envia "input" com Selective Repeat + SACK e termina com um FIN.
func SendSACK(link Link, input io.Reader, options SendOptions, stats *Stats) error {
	options.Window = normalizeWindow(options.Window)

	sender := &sackSender{
		link:       link,
		options:    options,
		stats:      stats,
		rto:        newRTOEstimator(options.RTO),
		source:     newPacketSource(input),
		inFlight:   make(map[uint32]*sentPacket),
		peerWindow: options.Window,
	}

	for {
		if options.OnProgress != nil {
			stats.RTO = sender.rto.Current()
			options.OnProgress()
		}

		// PASSO 1: enquanto couber na janela, envia pacotes novos.
		for !sender.source.finished && sender.windowHasRoom() {
			packet, err := sender.source.Next()
			if err != nil {
				return err
			}
			if err := sender.sendNewPacket(packet); err != nil {
				return err
			}
		}

		// PASSO 2: terminou?
		if sender.source.finished && sender.base == sender.nextSeq {
			stats.SHA256 = sender.source.Hash()
			return nil
		}

		// PASSO 3: espera um ACK até o timer estourar.
		packet, err := sender.link.Receive(sender.timerDeadline)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			if err := sender.handleTimeout(); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}

		// PASSO 4: processa o ACK e reenvia o que se perdeu.
		if err := sender.handleAck(packet); err != nil {
			return err
		}
	}
}

// windowHasRoom: respeita a nossa janela E a janela anunciada pelo receptor.
func (sender *sackSender) windowHasRoom() bool {
	limit := max(min(sender.options.Window, sender.peerWindow), 1)
	sender.stats.Window = limit
	return int(sender.nextSeq-sender.base) < limit
}

func (sender *sackSender) sendNewPacket(packet *proto.Packet) error {
	if sender.base == sender.nextSeq {
		sender.restartTimer() // janela estava vazia: liga o timer
	}
	pending := &sentPacket{packet: packet}
	sender.inFlight[packet.Seq] = pending
	sender.nextSeq++
	return sender.transmit(pending)
}

func (sender *sackSender) transmit(pending *sentPacket) error {
	pending.sentAt = time.Now()
	sender.stats.Packets++
	return sender.link.Send(pending.packet)
}

// retransmit reenvia UM pacote (e não a janela inteira, como no GBN).
func (sender *sackSender) retransmit(pending *sentPacket) error {
	pending.packet.Flags |= proto.FlagRetrans
	pending.retransmitted = true
	sender.stats.Retrans++
	return sender.transmit(pending)
}

func (sender *sackSender) restartTimer() {
	sender.timerDeadline = time.Now().Add(sender.rto.Current())
}

// handleTimeout: nada novo foi confirmado por um RTO inteiro.
// Reenvia tudo o que está sem confirmação há mais de um RTO.
func (sender *sackSender) handleTimeout() error {
	sender.timeoutsInRow++
	if sender.timeoutsInRow > maxRetries {
		return fmt.Errorf("sem resposta do receptor (seq %d, %d timeouts seguidos)", sender.base, maxRetries)
	}
	sender.stats.Timeouts++

	expiredBefore := time.Now().Add(-sender.rto.Current())
	sender.rto.Backoff() // dobra o RTO até chegar uma medição nova

	resent := 0
	for seq := sender.base; seq < sender.nextSeq; seq++ {
		pending := sender.inFlight[seq]
		if !pending.sacked && pending.sentAt.Before(expiredBefore) {
			if err := sender.retransmit(pending); err != nil {
				return err
			}
			resent++
		}
	}
	if resent == 0 { // garante que pelo menos o mais antigo vai de novo
		if err := sender.retransmit(sender.inFlight[sender.base]); err != nil {
			return err
		}
	}
	sender.restartTimer()
	return nil
}

// handleAck processa um ACK: cumulativo, eco e bitmap SACK.
func (sender *sackSender) handleAck(ack *proto.Packet) error {
	if ack.Type == proto.TypeErr {
		return proto.ParseError(ack)
	}
	if ack.Type != proto.TypeAck || ack.Ack > sender.nextSeq {
		return nil // não é ACK, ou é inválido
	}

	sender.measureRTT(ack.Seq)
	sender.peerWindow = int(ack.Window)
	madeProgress := false

	// 1) Cumulativo: tudo abaixo de ack.Ack chegou. Desliza a janela.
	if ack.Ack > sender.base {
		for seq := sender.base; seq < ack.Ack; seq++ {
			pending := sender.inFlight[seq]
			sender.markDelivered(pending)
			sender.stats.BytesAcked += int64(len(pending.packet.Payload))
			delete(sender.inFlight, seq)
		}
		sender.base = ack.Ack
		madeProgress = true
	}

	// 2) Eco: o pacote que gerou este ACK chegou.
	if pending, found := sender.inFlight[ack.Seq]; found && !pending.sacked {
		sender.markDelivered(pending)
		madeProgress = true
	}

	// 3) Bitmap SACK: os 64 pacotes anteriores ao eco que também chegaram.
	for bit := range proto.SackBits {
		if ack.Sack&(1<<bit) == 0 {
			continue // bit desligado: este ainda não chegou
		}
		if ack.Seq < uint32(bit)+1 {
			break // antes do pacote 0: não existe
		}
		pending, found := sender.inFlight[ack.Seq-1-uint32(bit)]
		if found && !pending.sacked {
			sender.markDelivered(pending)
			madeProgress = true
		}
	}

	if madeProgress {
		sender.timeoutsInRow = 0
		sender.restartTimer()
	}

	return sender.retransmitLost()
}

// markDelivered marca o pacote como recebido pelo outro lado e atualiza
// "o pacote mais recente que chegou".
func (sender *sackSender) markDelivered(pending *sentPacket) {
	pending.sacked = true
	if pending.sentAt.After(sender.latestDeliveredSentAt) {
		sender.latestDeliveredSentAt = pending.sentAt
	}
}

// retransmitLost reenvia os pacotes considerados perdidos: os que foram
// enviados (com folga de RTT/4) ANTES de um pacote que já chegou.
func (sender *sackSender) retransmitLost() error {
	reorderTolerance := max(sender.rto.SmoothedRTT()/4, time.Millisecond)
	lostIfSentBefore := sender.latestDeliveredSentAt.Add(-reorderTolerance)

	for seq := sender.base; seq < sender.nextSeq; seq++ {
		pending := sender.inFlight[seq]
		if pending.sacked || !pending.sentAt.Before(lostIfSentBefore) {
			continue
		}
		// Ao reenviar, sentAt vira "agora": só será considerado perdido
		// de novo se chegar algum pacote enviado depois desta retransmissão.
		sender.stats.FastRetrans++
		if err := sender.retransmit(pending); err != nil {
			return err
		}
	}
	return nil
}

func (sender *sackSender) measureRTT(echoSeq uint32) {
	pending, found := sender.inFlight[echoSeq]
	if !found || pending.retransmitted {
		return
	}
	sample := time.Since(pending.sentAt)
	sender.rto.AddSample(sample)
	sender.stats.addRTT(sample)
}

// =====================================================================
// RECEPTOR
// =====================================================================

// RecvSACK guarda os pacotes fora de ordem e grava em ordem no "output".
func RecvSACK(link Link, output io.Writer, options ReceiveOptions) (*Result, error) {
	receiver := newStreamReceiver(link, output, options)
	buffered := make(map[uint32]*proto.Packet) // chegaram adiantados, esperando a vez

	sendAck := func(echo uint32) *proto.Packet {
		ack := &proto.Packet{
			Type: proto.TypeAck,
			Seq:  echo, // eco: o pacote que gerou este ACK
			Ack:  receiver.expected,
			Sack: sackBitmap(buffered, receiver.expected, echo),
			// Janela anunciada: quantos pacotes a partir de Ack o receptor
			// aceita guardar (é o tamanho do buffer de reordenação). Os
			// pacotes já guardados estão DENTRO dessa faixa, então não são
			// descontados (como a rwnd do TCP).
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

		// Guarda o pacote, se for novo e couber no buffer.
		_, alreadyBuffered := buffered[packet.Seq]
		switch {
		case packet.Seq < receiver.expected || alreadyBuffered:
			receiver.result.Duplicates++
		case packet.Seq < receiver.expected+proto.MaxWindow:
			buffered[packet.Seq] = packet
		}

		// Grava em ordem tudo o que já está contíguo a partir de "expected".
		for {
			next, found := buffered[receiver.expected]
			if !found {
				break
			}
			delete(buffered, receiver.expected)

			if next.Type == proto.TypeFin {
				return receiver.finish(next, sendAck)
			}
			if err := receiver.write(next); err != nil {
				return receiver.result, err
			}
		}

		sendAck(packet.Seq)
	}
}

// sackBitmap monta os 64 bits olhando para trás a partir do eco:
// bit i ligado = já temos o pacote (echo-1-i). Tudo abaixo de "expected"
// já foi gravado, então também conta como recebido.
func sackBitmap(buffered map[uint32]*proto.Packet, expected, echo uint32) uint64 {
	var bitmap uint64
	for bit := range proto.SackBits {
		if echo < uint32(bit)+1 {
			break // antes do pacote 0
		}
		seq := echo - 1 - uint32(bit)
		_, isBuffered := buffered[seq]
		if seq < expected || isBuffered {
			bitmap |= 1 << bit
		}
	}
	return bitmap
}
