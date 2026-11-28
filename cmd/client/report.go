package main

// Resumo da transferência na tela e, se pedido, uma linha no CSV.

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"raposa/internal/transport"
)

type report struct {
	csvPath string
	label   string
	server  string
	pingRTT time.Duration // RTT medido pelos pings da mini-CDN
}

var csvHeader = []string{
	"data_hora", "rotulo", "comando", "servidor", "arquivo", "algoritmo", "janela",
	"bytes", "segundos", "throughput_mbps", "rtt_ping_ms", "rtt_medio_ms",
	"pacotes", "retransmissoes", "fast_retrans", "timeouts", "perda_estimada", "descartados_netem",
}

// upload: o cliente foi o emissor, então tem todas as métricas.
func (current *report) upload(path string, size int64, client *client, stats *transport.Stats, elapsed time.Duration) {
	seconds := elapsed.Seconds()
	megabits := float64(size) * 8 / 1e6

	fmt.Println("---------------- upload concluído ----------------")
	fmt.Printf("arquivo:          %s (%s)\n", path, humanBytes(size))
	fmt.Printf("algoritmo:        %s, janela %d\n", client.algorithm, client.window)
	fmt.Printf("tempo:            %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("throughput:       %.2f Mbit/s (%.2f MB/s)\n", megabits/seconds, float64(size)/1e6/seconds)
	fmt.Printf("RTT (ping):       %.2f ms\n", milliseconds(current.pingRTT))
	fmt.Printf("RTT médio (ACKs): %.2f ms\n", milliseconds(stats.AvgRTT()))
	fmt.Printf("pacotes enviados: %d\n", stats.Packets)
	fmt.Printf("retransmissões:   %d (fast retransmit %d, timeouts %d)\n", stats.Retrans, stats.FastRetrans, stats.Timeouts)
	fmt.Printf("retransmitido:    %.2f%% dos pacotes (no SACK ≈ taxa de perda; no GBN bem maior)\n", 100*stats.LossRate())
	fmt.Printf("perda simulada:   %d de %d pacotes descartados pelo netem\n", client.conn.Dropped.Load(), client.conn.Sent.Load())
	fmt.Printf("SHA-256 enviado:  %x\n", stats.SHA256)
	fmt.Println("integridade:      OK (o servidor só confirma o FIN se o SHA-256 conferir)")

	current.appendCSV([]string{
		"put", path, client.algorithm, strconv.Itoa(client.window),
		strconv.FormatInt(size, 10), format(seconds), format(megabits / seconds),
		format(milliseconds(current.pingRTT)), format(milliseconds(stats.AvgRTT())),
		strconv.Itoa(stats.Packets), strconv.Itoa(stats.Retrans), strconv.Itoa(stats.FastRetrans),
		strconv.Itoa(stats.Timeouts), format(stats.LossRate()), strconv.FormatUint(client.conn.Dropped.Load(), 10),
	})
}

// download: o servidor foi o emissor; aqui temos as métricas do receptor.
// (As do emissor aparecem no log do servidor.)
func (current *report) download(name, outPath string, client *client, result *transport.Result, elapsed time.Duration) {
	seconds := elapsed.Seconds()
	megabits := float64(result.Bytes) * 8 / 1e6

	fmt.Println("---------------- download concluído ----------------")
	fmt.Printf("arquivo:            %s -> %s (%s)\n", name, outPath, humanBytes(result.Bytes))
	fmt.Printf("algoritmo:          %s, janela %d\n", client.algorithm, client.window)
	fmt.Printf("tempo:              %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("throughput:         %.2f Mbit/s (%.2f MB/s)\n", megabits/seconds, float64(result.Bytes)/1e6/seconds)
	fmt.Printf("RTT (ping):         %.2f ms\n", milliseconds(current.pingRTT))
	fmt.Printf("pacotes recebidos:  %d (duplicados %d, retransmitidos %d)\n", result.Packets, result.Duplicates, result.RetransReceived)
	fmt.Printf("corrompidos:        %d descartados pelo checksum\n", client.link.Corrupted)
	fmt.Printf("SHA-256 original:   %x\n", result.SenderSHA256)
	fmt.Printf("SHA-256 recebido:   %x\n", result.ReceivedSHA256)
	fmt.Println("integridade:        OK")

	current.appendCSV([]string{
		"get", name, client.algorithm, strconv.Itoa(client.window),
		strconv.FormatInt(result.Bytes, 10), format(seconds), format(megabits / seconds),
		format(milliseconds(current.pingRTT)), "",
		strconv.Itoa(result.Packets), strconv.Itoa(result.RetransReceived), "", "", "",
		strconv.FormatUint(client.conn.Dropped.Load(), 10),
	})
}

// appendCSV acrescenta uma linha (e o cabeçalho, se o arquivo for novo).
func (current *report) appendCSV(fields []string) {
	if current.csvPath == "" {
		return
	}
	_, statErr := os.Stat(current.csvPath)
	isNew := os.IsNotExist(statErr)

	file, err := os.OpenFile(current.csvPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("não consegui gravar o CSV: %v", err)
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if isNew {
		writer.Write(csvHeader)
	}
	row := append([]string{time.Now().Format(time.RFC3339), current.label}, fields[:1]...)
	row = append(row, current.server)
	row = append(row, fields[1:]...)
	writer.Write(row)
	writer.Flush()
	fmt.Printf("métricas gravadas em %s\n", current.csvPath)
}

func format(value float64) string {
	return strconv.FormatFloat(value, 'f', 4, 64)
}
