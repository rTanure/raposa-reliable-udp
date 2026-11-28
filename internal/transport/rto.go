package transport

import "time"

// RTO adaptativo (mesmo cálculo do TCP, RFC 6298).
//
// O timeout precisa ser um pouco maior que o RTT: se for menor, o emissor
// retransmite pacotes que não se perderam; se for muito maior, demora
// para perceber uma perda. Como o RTT muda (25 ms, 400 ms...), o RTO
// é calculado a partir das medições:
//
//	SRTT   = média suavizada do RTT
//	RTTVAR = quanto o RTT costuma variar
//	RTO    = SRTT + 4 x RTTVAR
//
// A cada timeout o RTO dobra (backoff), até chegar uma medição nova.
const (
	initialRTO = 1 * time.Second
	minRTO     = 200 * time.Millisecond
	maxRTO     = 60 * time.Second
)

type rtoEstimator struct {
	fixed     time.Duration // se > 0, ignora as medições e usa sempre este valor (flag -rto)
	srtt      time.Duration
	rttvar    time.Duration
	hasSample bool
	backoff   uint // quantas vezes dobrar o RTO
}

func newRTOEstimator(fixed time.Duration) *rtoEstimator {
	return &rtoEstimator{fixed: fixed}
}

// Current devolve o RTO atual.
func (estimator *rtoEstimator) Current() time.Duration {
	rto := initialRTO
	switch {
	case estimator.fixed > 0:
		rto = estimator.fixed
	case estimator.hasSample:
		rto = estimator.srtt + 4*estimator.rttvar
		if rto < minRTO {
			rto = minRTO
		}
	}

	rto = rto << estimator.backoff // "<< n" multiplica por 2^n
	if rto > maxRTO {
		rto = maxRTO
	}
	return rto
}

// AddSample registra um RTT medido (só de pacotes não retransmitidos).
func (estimator *rtoEstimator) AddSample(sample time.Duration) {
	if !estimator.hasSample {
		estimator.srtt = sample
		estimator.rttvar = sample / 2
		estimator.hasSample = true
	} else {
		difference := estimator.srtt - sample
		if difference < 0 {
			difference = -difference
		}
		estimator.rttvar = (3*estimator.rttvar + difference) / 4 // 0,75 x RTTVAR + 0,25 x |SRTT - R|
		estimator.srtt = (7*estimator.srtt + sample) / 8         // 0,875 x SRTT + 0,125 x R
	}
	estimator.backoff = 0
}

// SmoothedRTT devolve o RTT médio suavizado (0 se ainda não há medição).
func (estimator *rtoEstimator) SmoothedRTT() time.Duration {
	return estimator.srtt
}

// Backoff dobra o RTO depois de um timeout.
func (estimator *rtoEstimator) Backoff() {
	if estimator.backoff < 6 {
		estimator.backoff++
	}
}
