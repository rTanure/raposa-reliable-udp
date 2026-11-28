package transport

// Peças usadas pelos dois algoritmos (GBN e SACK):
//   - packetSource:   corta o arquivo em pacotes DATA e termina com um FIN
//                     que leva o SHA-256 de tudo que foi enviado;
//   - streamReceiver: a parte comum dos receptores (esperar o primeiro
//                     pacote, gravar, conferir o hash no FIN);
//   - Exchange:       pedido/resposta com retransmissão (login, PUT...).

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"time"

	"raposa/internal/proto"
)

const (
	AlgorithmGBN  = "gbn"
	AlgorithmSACK = "sack"
)

// idleTimeout: se o outro lado sumir no meio da transferência, desiste.
const idleTimeout = 15 * time.Second

// ValidAlgorithm diz se o nome do algoritmo existe.
func ValidAlgorithm(name string) bool {
	return name == AlgorithmGBN || name == AlgorithmSACK
}

// SendOptions são os parâmetros de quem envia.
type SendOptions struct {
	Window     int           // tamanho da janela, em pacotes
	RTO        time.Duration // 0 = adaptativo; > 0 = fixo
	OnProgress func()        // opcional: chamada a cada volta do laço
}

// ReceiveOptions são os parâmetros de quem recebe.
type ReceiveOptions struct {
	RTO time.Duration // usado para reenviar o pedido e no encerramento

	// Request: o pedido que fez o outro lado começar a enviar (ex.: GET).
	// Enquanto nenhum dado chegar, ele é reenviado (o pedido pode ter se perdido).
	Request *proto.Packet

	// Reply: a resposta que já demos ao pedido do outro lado (ex.: OK do PUT).
	// Se o pedido chegar de novo, é porque a resposta se perdeu: reenviamos.
	Reply *proto.Packet

	// Linger: quanto tempo continuar respondendo FINs repetidos depois do fim.
	Linger time.Duration

	OnProgress func(receivedBytes int64) // opcional
}

// Send envia "input" com o algoritmo escolhido.
func Send(algorithm string, link Link, input io.Reader, options SendOptions, stats *Stats) error {
	switch algorithm {
	case AlgorithmGBN:
		return SendGBN(link, input, options, stats)
	case AlgorithmSACK:
		return SendSACK(link, input, options, stats)
	}
	return fmt.Errorf("algoritmo desconhecido: %q", algorithm)
}

// Receive recebe para "output" com o algoritmo escolhido.
func Receive(algorithm string, link Link, output io.Writer, options ReceiveOptions) (*Result, error) {
	switch algorithm {
	case AlgorithmGBN:
		return RecvGBN(link, output, options)
	case AlgorithmSACK:
		return RecvSACK(link, output, options)
	}
	return nil, fmt.Errorf("algoritmo desconhecido: %q", algorithm)
}

func normalizeWindow(window int) int {
	if window < 1 {
		return 1
	}
	if window > proto.MaxWindow {
		return proto.MaxWindow
	}
	return window
}

// sentPacket é um pacote que já foi enviado e ainda espera confirmação.
type sentPacket struct {
	packet        *proto.Packet
	sentAt        time.Time // quando foi enviado pela última vez (para medir RTT)
	retransmitted bool      // se já foi reenviado alguma vez
	sacked        bool      // (só SACK) já sabemos que este chegou
}

// =====================================================================
// LADO QUE ENVIA: packetSource
// =====================================================================

type packetSource struct {
	input    io.Reader
	hasher   hash.Hash
	chunk    []byte
	nextSeq  uint32
	finished bool // true depois de gerar o FIN
}

func newPacketSource(input io.Reader) *packetSource {
	hasher := sha256.New()
	return &packetSource{
		input:  io.TeeReader(input, hasher), // tudo que for lido também passa pelo hash
		hasher: hasher,
		chunk:  make([]byte, proto.MaxPayload),
	}
}

// Next devolve o próximo pacote: DATA com até 1200 bytes, ou o FIN
// (com o SHA-256 no payload) quando o arquivo acabar.
func (source *packetSource) Next() (*proto.Packet, error) {
	bytesRead, err := io.ReadFull(source.input, source.chunk)

	// ReadFull devolve EOF / ErrUnexpectedEOF quando o arquivo acaba;
	// isso não é erro de verdade. Qualquer outro erro é.
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	packet := &proto.Packet{Type: proto.TypeData, Seq: source.nextSeq}
	if bytesRead > 0 {
		packet.Payload = append([]byte(nil), source.chunk[:bytesRead]...) // cópia: "chunk" é reutilizado
	} else {
		packet.Type = proto.TypeFin
		packet.Payload = source.hasher.Sum(nil)
		source.finished = true
	}
	source.nextSeq++
	return packet, nil
}

// Hash devolve o SHA-256 de tudo que foi lido (válido depois do FIN).
func (source *packetSource) Hash() [32]byte {
	var sum [32]byte
	copy(sum[:], source.hasher.Sum(nil))
	return sum
}

// =====================================================================
// LADO QUE RECEBE: streamReceiver
// =====================================================================

type streamReceiver struct {
	link     Link
	output   io.Writer
	hasher   hash.Hash
	options  ReceiveOptions
	result   *Result
	expected uint32 // próximo Seq que precisamos gravar
	started  bool   // já chegou algum pacote de dados?
	attempts int    // quantas vezes reenviamos o pedido
}

func newStreamReceiver(link Link, output io.Writer, options ReceiveOptions) *streamReceiver {
	hasher := sha256.New()
	return &streamReceiver{
		link:    link,
		output:  io.MultiWriter(output, hasher), // grava E calcula o hash
		hasher:  hasher,
		options: options,
		result:  &Result{},
	}
}

func (receiver *streamReceiver) rto() time.Duration {
	if receiver.options.RTO > 0 {
		return receiver.options.RTO
	}
	return initialRTO
}

// nextPacket devolve o próximo pacote DATA ou FIN.
// Também cuida de: reenviar o pedido, reenviar a resposta, ERR e silêncio.
func (receiver *streamReceiver) nextPacket() (*proto.Packet, error) {
	for {
		deadline := time.Now().Add(idleTimeout)
		if !receiver.started && receiver.options.Request != nil {
			deadline = time.Now().Add(receiver.rto())
		}

		packet, err := receiver.link.Receive(deadline)

		// Nada chegou ainda: talvez o pedido tenha se perdido. Reenvia.
		if errors.Is(err, os.ErrDeadlineExceeded) && !receiver.started && receiver.options.Request != nil {
			receiver.attempts++
			if receiver.attempts > maxRetries {
				return nil, fmt.Errorf("sem resposta após %d tentativas", maxRetries)
			}
			receiver.link.Send(receiver.options.Request)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("o outro lado parou de responder: %w", err)
		}

		switch packet.Type {
		case proto.TypeData, proto.TypeFin:
			receiver.started = true
			receiver.result.Packets++
			if packet.Flags&proto.FlagRetrans != 0 {
				receiver.result.RetransReceived++
			}
			return packet, nil
		case proto.TypeErr:
			return nil, proto.ParseError(packet)
		default:
			// Pedido repetido antes de começar: nossa resposta se perdeu.
			if !receiver.started && receiver.options.Reply != nil {
				receiver.link.Send(receiver.options.Reply)
			}
		}
	}
}

// write grava os dados de um pacote DATA (que chegou na ordem certa).
func (receiver *streamReceiver) write(packet *proto.Packet) error {
	if _, err := receiver.output.Write(packet.Payload); err != nil {
		return err
	}
	receiver.result.Bytes += int64(len(packet.Payload))
	receiver.expected++
	if receiver.options.OnProgress != nil {
		receiver.options.OnProgress(receiver.result.Bytes)
	}
	return nil
}

// finish trata o FIN: confere o SHA-256 e encerra.
// sendAck é a função de ACK do algoritmo (GBN e SACK montam ACKs diferentes);
// ela devolve o pacote que enviou.
func (receiver *streamReceiver) finish(fin *proto.Packet, sendAck func(echo uint32) *proto.Packet) (*Result, error) {
	receiver.expected++ // o FIN também ocupa um número de sequência
	receiver.result.CompletedAt = time.Now()
	copy(receiver.result.SenderSHA256[:], fin.Payload)
	copy(receiver.result.ReceivedSHA256[:], receiver.hasher.Sum(nil))

	// Hash diferente: NÃO confirma o FIN; avisa o emissor com um ERR.
	if !bytes.Equal(receiver.result.SenderSHA256[:], receiver.result.ReceivedSHA256[:]) {
		errorPacket := proto.NewErrorPacket(proto.ErrCodeHashMismatch, "o SHA-256 recebido não confere")
		receiver.link.Send(errorPacket)
		receiver.result.FinalReply = errorPacket
		receiver.linger()
		return receiver.result, &proto.RemoteError{Code: proto.ErrCodeHashMismatch, Message: "SHA-256 do arquivo recebido não confere"}
	}

	receiver.result.FinalReply = sendAck(fin.Seq)
	receiver.linger()
	return receiver.result, nil
}

// linger: se a resposta final se perder, o emissor vai reenviar o que
// achar que falta (o FIN ou algum DATA).
// Então ficamos um tempo (options.Linger) repetindo a resposta antes de
// encerrar. Com Linger = 0, quem chamou cuida disso (o servidor faz isso
// no laço da sessão, usando result.FinalReply, para não ficar travado).
func (receiver *streamReceiver) linger() {
	if receiver.options.Linger <= 0 {
		return
	}
	deadline := time.Now().Add(receiver.options.Linger)

	for {
		packet, err := receiver.link.Receive(deadline)
		if err != nil {
			return // o tempo acabou
		}
		// Responde a DATA também: o emissor pode já saber que o FIN chegou
		// e estar reenviando só um pacote cujo ACK se perdeu.
		if packet.Type == proto.TypeData || packet.Type == proto.TypeFin {
			receiver.link.Send(receiver.result.FinalReply)
		}
	}
}

// =====================================================================
// PEDIDO / RESPOSTA
// =====================================================================

// Exchange envia um pedido e espera a resposta, reenviando o pedido se a
// resposta demorar. Uma resposta ERR vira um erro do Go.
// Pacotes de dados/ACK atrasados que aparecerem no meio são ignorados.
func Exchange(link Link, request *proto.Packet, rto time.Duration) (*proto.Packet, error) {
	if rto <= 0 {
		rto = initialRTO
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := link.Send(request); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(rto)

		for {
			reply, err := link.Receive(deadline)
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break // demorou: sai do "for" de dentro e reenvia
			}
			if err != nil {
				return nil, err
			}

			switch reply.Type {
			case proto.TypeData, proto.TypeAck, proto.TypeFin:
				continue // sobra de uma transferência anterior
			case proto.TypeErr:
				return nil, proto.ParseError(reply)
			default:
				return reply, nil
			}
		}
	}
	return nil, fmt.Errorf("sem resposta após %d tentativas (%s)", maxRetries, request.Type)
}
