package transport

import (
	"time"

	"raposa/internal/proto"
)

// Stats são as métricas do lado que ENVIA (as que o trabalho pede).
type Stats struct {
	Packets     int   // pacotes enviados, contando retransmissões
	Retrans     int   // total de retransmissões (timeout + fast retransmit)
	FastRetrans int   // retransmissões disparadas sem esperar timeout
	Timeouts    int   // quantas vezes o timer estourou
	BytesAcked  int64 // bytes de dados já confirmados pelo receptor
	RTTCount    int   // quantas amostras de RTT foram coletadas
	RTTSum      time.Duration
	Window      int // janela efetiva no momento
	RTO         time.Duration
	SHA256      [32]byte // hash de tudo que foi enviado (vai no FIN)
}

// addRTT registra uma amostra de RTT.
// "(stats *Stats)" antes do nome é o "receptor": é assim que Go define um
// método. Equivale a um método da classe Stats, e "stats" faz o papel de "this".
func (stats *Stats) addRTT(sample time.Duration) {
	stats.RTTCount++
	stats.RTTSum += sample
}

// AvgRTT devolve o RTT médio (0 se ainda não há amostras).
func (stats *Stats) AvgRTT() time.Duration {
	if stats.RTTCount == 0 {
		return 0
	}
	return stats.RTTSum / time.Duration(stats.RTTCount)
}

// LossRate estima a perda: fração dos pacotes enviados que precisou ser reenviada.
func (stats *Stats) LossRate() float64 {
	if stats.Packets == 0 {
		return 0
	}
	return float64(stats.Retrans) / float64(stats.Packets)
}

// Result são as métricas do lado que RECEBE.
type Result struct {
	Bytes           int64    // bytes gravados
	Packets         int      // pacotes de dados válidos que chegaram
	Duplicates      int      // pacotes que já tínhamos (chegaram de novo)
	RetransReceived int      // pacotes que vieram marcados como retransmissão
	SenderSHA256    [32]byte // hash calculado por quem enviou (veio no FIN)
	ReceivedSHA256  [32]byte // hash do que nós gravamos

	CompletedAt time.Time     // quando o FIN chegou (antes do linger)
	FinalReply  *proto.Packet // resposta dada ao FIN (ACK, ou ERR se o hash não bateu)
}
