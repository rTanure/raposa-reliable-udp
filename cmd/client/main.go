// Cliente RAPOSA.
//
// Sem comando, abre o modo INTERATIVO (prompt com histórico e TAB):
//
//	go run ./cmd/client -servers 127.0.0.1:9000,127.0.0.1:9001
//
// Com comando, executa e sai (bom para scripts e benchmarks):
//
//	go run ./cmd/client [opções] ping
//	go run ./cmd/client [opções] list
//	go run ./cmd/client [opções] get <arquivo>
//	go run ./cmd/client [opções] put <arquivo>
//
// As opções vêm ANTES do comando (é assim que o pacote "flag" do Go
// funciona: ele para de ler opções no primeiro argumento que não é opção).
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"raposa/internal/netem"
	"raposa/internal/transport"
)

func main() {
	servers := flag.String("servers", "127.0.0.1:9000", "servidores separados por vírgula; o de menor RTT é escolhido")
	password := flag.String("password", "", "senha do servidor (ou variável RAPOSA_PASSWORD; no modo interativo é perguntada)")
	algorithm := flag.String("algo", "sack", "algoritmo da janela deslizante: sack ou gbn")
	window := flag.Int("window", 1024, "tamanho da janela, em pacotes (1 = stop-and-wait)")
	rto := flag.Duration("rto", 0, "timeout de retransmissão fixo (0 = adaptativo)")
	pings := flag.Int("pings", 5, "quantos pings por servidor na escolha do servidor")
	outPath := flag.String("out", "", "get: onde salvar (padrão: mesmo nome do arquivo)")
	csvPath := flag.String("csv", "", "acrescenta as métricas de cada transferência neste arquivo CSV")
	label := flag.String("label", "", "rótulo da execução no CSV (ex.: cenario1)")
	delay := flag.Duration("delay", 0, "atraso simulado nos envios do cliente (ex: 50ms)")
	jitter := flag.Duration("jitter", 0, "variação do atraso simulado")
	loss := flag.Float64("loss", 0, "perda simulada nos envios do cliente (0 a 1)")
	corrupt := flag.Float64("corrupt", 0, "chance de corromper um pacote enviado (0 a 1)")
	flag.Usage = printUsage
	flag.Parse()

	if !transport.ValidAlgorithm(*algorithm) {
		log.Fatalf("algoritmo %q não existe (use sack ou gbn)", *algorithm)
	}
	if *password == "" {
		*password = os.Getenv("RAPOSA_PASSWORD")
	}

	conn, err := net.ListenUDP("udp", nil) // nil = porta local qualquer
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadBuffer(8 << 20)
	conn.SetWriteBuffer(8 << 20)

	current := &client{
		conn:      netem.Wrap(conn, netem.Config{Delay: *delay, Jitter: *jitter, Loss: *loss, Corrupt: *corrupt}),
		servers:   strings.Split(*servers, ","),
		pings:     *pings,
		password:  *password,
		algorithm: *algorithm,
		window:    *window,
		rto:       *rto,
		report:    &report{csvPath: *csvPath, label: *label},
	}

	// flag.Args() = o que sobrou depois das opções: o comando e o arquivo.
	args := flag.Args()
	if len(args) == 0 {
		current.interactive()
		return
	}
	if err := current.runOnce(args, *outPath); err != nil {
		log.Fatalf("%s falhou: %v", args[0], err)
	}
}

// runOnce executa um único comando e sai.
func (current *client) runOnce(args []string, outPath string) error {
	command := args[0]
	if err := current.chooseServer(); err != nil {
		return err
	}
	if command == "ping" {
		return nil
	}
	if current.password == "" {
		return fmt.Errorf("defina a senha com -password ou com a variável RAPOSA_PASSWORD")
	}
	if err := current.login(); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	switch {
	case command == "list":
		files, err := current.list()
		if err == nil {
			printFiles(files)
		}
		return err
	case command == "get" && len(args) == 2:
		return current.download(args[1], outPath)
	case command == "put" && len(args) == 2:
		return current.upload(args[1])
	}
	printUsage()
	os.Exit(2)
	return nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Uso: client [opções] [comando]

Sem comando: abre o modo interativo.

Comandos:
  ping              só mede o RTT dos servidores (mini-CDN)
  list              lista os arquivos do servidor
  get <arquivo>     baixa um arquivo
  put <arquivo>     envia um arquivo

Opções:
`)
	flag.PrintDefaults()
}
