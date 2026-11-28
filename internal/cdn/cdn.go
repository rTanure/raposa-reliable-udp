// Pacote cdn: escolha automática do servidor com menor RTT (mini-CDN).
//
// É a mesma ideia do mapeamento das CDNs: antes de baixar, o cliente
// descobre qual réplica está "mais perto" (menor atraso) e usa ela.
package cdn

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"raposa/internal/proto"
)

const pingTimeout = 2 * time.Second

// Measurement é o resultado das medições de um servidor.
type Measurement struct {
	Address *net.UDPAddr
	RTT     time.Duration // mediana dos pings que voltaram
	Sent    int
	Lost    int
	Err     error // não deu para medir (endereço inválido, nenhum ping voltou...)
}

// MeasureRTT manda "count" pings e calcula a mediana do RTT.
// A mediana é melhor que a média porque um ping muito atrasado não
// distorce o resultado.
func MeasureRTT(conn net.PacketConn, address *net.UDPAddr, count int) Measurement {
	measurement := Measurement{Address: address, Sent: count}
	var samples []time.Duration
	buffer := make([]byte, 2048)

	for number := 1; number <= count; number++ {
		ping := &proto.Packet{Type: proto.TypePing, Seq: uint32(number)}
		start := time.Now()
		if _, err := conn.WriteTo(ping.Marshal(), address); err != nil {
			measurement.Err = err
			return measurement
		}

		if waitForPong(conn, address, uint32(number), buffer) {
			samples = append(samples, time.Since(start))
		} else {
			measurement.Lost++
		}
	}
	conn.SetReadDeadline(time.Time{})

	if len(samples) == 0 {
		measurement.Err = errors.New("nenhum ping respondido")
		return measurement
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	measurement.RTT = samples[len(samples)/2]
	return measurement
}

// waitForPong espera o PONG com o número certo (ignora pongs atrasados).
func waitForPong(conn net.PacketConn, address *net.UDPAddr, number uint32, buffer []byte) bool {
	conn.SetReadDeadline(time.Now().Add(pingTimeout))
	for {
		bytesRead, from, err := conn.ReadFrom(buffer)
		if err != nil {
			return false // timeout
		}
		if from.String() != address.String() {
			continue
		}
		pong, err := proto.Unmarshal(buffer[:bytesRead])
		if err == nil && pong.Type == proto.TypePong && pong.Seq == number {
			return true
		}
	}
}

// ChooseFastest mede todos os servidores e devolve o de menor RTT,
// junto com as medições (para mostrar a tabela ao usuário).
func ChooseFastest(conn net.PacketConn, addresses []string, count int) (*net.UDPAddr, []Measurement, error) {
	var measurements []Measurement
	var best *Measurement

	for _, text := range addresses {
		address, err := net.ResolveUDPAddr("udp", text)
		if err != nil {
			measurements = append(measurements, Measurement{Err: fmt.Errorf("%s: %w", text, err)})
			continue
		}
		measurements = append(measurements, MeasureRTT(conn, address, count))
	}

	for index := range measurements {
		candidate := &measurements[index]
		if candidate.Err == nil && (best == nil || candidate.RTT < best.RTT) {
			best = candidate
		}
	}
	if best == nil {
		return nil, measurements, errors.New("nenhum servidor respondeu ao ping")
	}
	return best.Address, measurements, nil
}
