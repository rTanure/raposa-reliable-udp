// Pacote netem: simula uma rede ruim (atraso, jitter, perda e corrupção)
// por cima de um socket UDP de verdade. Só afeta os ENVIOS; a leitura
// passa direto.
package netem

import (
	"math/rand"
	"net"
	"sync/atomic"
	"time"
)

// Config descreve a rede que queremos simular.
type Config struct {
	Delay   time.Duration // atraso de cada pacote (só ida)
	Jitter  time.Duration // variação aleatória somada ao atraso (±Jitter)
	Loss    float64       // chance de descartar o pacote (0.05 = 5%)
	Corrupt float64       // chance de estragar um byte do pacote (testa o checksum)
}

// Conn é o socket "com defeito".
//
// O campo net.PacketConn sem nome é um "embedding": a Conn ganha de graça
// todos os métodos do socket original (ReadFrom, Close, SetReadDeadline...).
// Só o WriteTo é reescrito lá embaixo.
type Conn struct {
	net.PacketConn
	cfg   Config
	queue chan delayedPacket // fila de pacotes esperando a hora de sair

	// atomic: contadores seguros para usar em várias goroutines ao mesmo tempo
	Sent      atomic.Uint64
	Dropped   atomic.Uint64
	Corrupted atomic.Uint64
}

// delayedPacket é um pacote guardado até o instante sendAt.
type delayedPacket struct {
	sendAt time.Time
	data   []byte
	addr   net.Addr
}

// Wrap encapsula o socket verdadeiro no simulador.
func Wrap(realConn net.PacketConn, cfg Config) *Conn {
	conn := &Conn{PacketConn: realConn, cfg: cfg}

	if cfg.Delay > 0 || cfg.Jitter > 0 {
		conn.queue = make(chan delayedPacket, 65536)
		go conn.delayLoop() // "go" roda a função em paralelo (goroutine)
	}
	return conn
}

// delayLoop tira pacotes da fila, um de cada vez, e envia na hora certa.
//
// Usar UMA fila só garante que os pacotes saem na mesma ordem em que
// entraram. Se cada pacote tivesse seu próprio timer, eles poderiam sair
// trocados, e o Go-Back-N descartaria pacotes bons.
func (conn *Conn) delayLoop() {
	for pending := range conn.queue { // fica esperando até chegar algo na fila
		time.Sleep(time.Until(pending.sendAt))
		conn.PacketConn.WriteTo(pending.data, pending.addr)
	}
}

// WriteTo substitui o envio normal: talvez descarta, estraga ou atrasa.
func (conn *Conn) WriteTo(data []byte, addr net.Addr) (int, error) {
	conn.Sent.Add(1)

	// Perda: finge que enviou, mas não envia nada.
	if rand.Float64() < conn.cfg.Loss {
		conn.Dropped.Add(1)
		return len(data), nil
	}

	// Copia os bytes, porque quem chamou pode reaproveitar o slice "data".
	dataCopy := append([]byte(nil), data...)

	// Corrupção: inverte os bits de um byte qualquer. O checksum do
	// receptor tem que perceber e descartar o pacote.
	if rand.Float64() < conn.cfg.Corrupt {
		conn.Corrupted.Add(1)
		dataCopy[rand.Intn(len(dataCopy))] ^= 0xFF
	}

	// Sem atraso configurado: envia na hora.
	if conn.queue == nil {
		return conn.PacketConn.WriteTo(dataCopy, addr)
	}

	// Calcula o atraso deste pacote (atraso fixo ± jitter).
	delay := conn.cfg.Delay
	if conn.cfg.Jitter > 0 {
		variation := (rand.Float64()*2 - 1) * float64(conn.cfg.Jitter) // entre -Jitter e +Jitter
		delay += time.Duration(variation)
		if delay < 0 {
			delay = 0
		}
	}

	conn.queue <- delayedPacket{sendAt: time.Now().Add(delay), data: dataCopy, addr: addr}
	return len(data), nil
}
