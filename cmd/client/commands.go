package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"raposa/internal/cdn"
	"raposa/internal/netem"
	"raposa/internal/proto"
	"raposa/internal/transport"
)

// client guarda a conexão com o servidor escolhido e as preferências.
type client struct {
	conn     *netem.Conn // socket (embrulhado no simulador de rede)
	servers  []string    // candidatos da mini-CDN
	pings    int
	password string

	algorithm string
	window    int
	rto       time.Duration // 0 = adaptativo

	server        *net.UDPAddr       // servidor escolhido
	pingRTT       time.Duration      // RTT medido até ele
	link          *transport.UDPLink // conversa com ele
	nextRequestID uint32             // cada pedido leva um número (no campo Seq)
	remoteFiles   []remoteFile       // última lista recebida (usada no TAB)

	report *report
}

type remoteFile struct {
	Name string
	Size int64
}

// =====================================================================
// MINI-CDN E LOGIN
// =====================================================================

// chooseServer mede o RTT de cada servidor, mostra a tabela e escolhe o menor.
func (current *client) chooseServer() error {
	fmt.Printf("Medindo o RTT dos servidores (%d pings cada):\n", current.pings)
	best, measurements, err := cdn.ChooseFastest(current.conn, current.servers, current.pings)

	for _, measurement := range measurements {
		if measurement.Err != nil {
			fmt.Printf("  %-22s  sem resposta (%v)\n", addressText(measurement.Address), measurement.Err)
			continue
		}
		marker := " "
		if measurement.Address == best {
			marker = "*"
		}
		fmt.Printf(" %s%-22s  RTT mediano %8.2f ms   (%d de %d pings perdidos)\n",
			marker, measurement.Address, milliseconds(measurement.RTT), measurement.Lost, measurement.Sent)
		if measurement.Address == best {
			current.pingRTT = measurement.RTT
		}
	}
	if err != nil {
		return err
	}

	fmt.Printf("Servidor escolhido: %s\n\n", best)
	current.server = best
	current.link = transport.NewUDPLink(current.conn, best)
	current.report.server = best.String()
	current.report.pingRTT = current.pingRTT
	return nil
}

// login: HELLO -> CHALLENGE -> AUTH (HMAC da senha) -> AUTH_OK
func (current *client) login() error {
	current.link.ConnID = 0 // sessão nova
	hello := &proto.Packet{Type: proto.TypeHello}
	challenge, err := transport.Exchange(current.link, hello, current.requestTimeout())
	if err != nil {
		return err
	}
	if challenge.Type != proto.TypeChallenge {
		return fmt.Errorf("resposta inesperada ao HELLO: %s", challenge.Type)
	}

	// A partir daqui, todos os pacotes levam o ConnID da sessão.
	current.link.ConnID = challenge.ConnID

	proof := proto.AuthProof(current.password, challenge.Payload)
	reply, err := transport.Exchange(current.link, current.newRequest(proto.TypeAuth, proof), current.requestTimeout())
	if err != nil {
		current.link.ConnID = 0
		return err
	}
	if reply.Type != proto.TypeAuthOK {
		return fmt.Errorf("resposta inesperada ao AUTH: %s", reply.Type)
	}
	fmt.Printf("Login aceito (sessão %08x)\n", current.link.ConnID)
	return nil
}

// withSession roda a ação e, se o servidor disser que a sessão não existe
// mais (expirou ou o servidor reiniciou), faz login de novo e tenta mais uma vez.
func (current *client) withSession(action func() error) error {
	err := action()

	// errors.As procura, dentro do erro, um erro do tipo *proto.RemoteError.
	var remoteError *proto.RemoteError
	if errors.As(err, &remoteError) && remoteError.Code == proto.ErrCodeUnknownSession {
		fmt.Println("A sessão expirou; fazendo login de novo...")
		if err := current.login(); err != nil {
			return err
		}
		return action()
	}
	return err
}

// requestTimeout: quanto esperar uma resposta antes de reenviar um pedido.
// Usa o RTT medido no ping (com folga), ou o -rto fixo se foi informado.
func (current *client) requestTimeout() time.Duration {
	if current.rto > 0 {
		return current.rto
	}
	return max(2*current.pingRTT, 200*time.Millisecond)
}

// lingerTime: depois de receber o FIN, quanto tempo continuar respondendo
// (caso o servidor não receba nossa confirmação e repita o FIN).
func (current *client) lingerTime() time.Duration {
	return max(3*current.requestTimeout(), 500*time.Millisecond)
}

// newRequest monta um pedido com o próximo número.
func (current *client) newRequest(packetType proto.Type, payload []byte) *proto.Packet {
	current.nextRequestID++
	return &proto.Packet{Type: packetType, Seq: current.nextRequestID, Payload: payload}
}

// transferRequest é o payload de LIST/GET/PUT com o algoritmo e a janela.
func (current *client) transferRequest(name string) []byte {
	return proto.TransferRequest{Algorithm: current.algorithm, Window: current.window, Name: name}.Encode()
}

// =====================================================================
// LIST
// =====================================================================

func (current *client) list() ([]remoteFile, error) {
	request := current.newRequest(proto.TypeList, current.transferRequest(""))

	// A lista vem como uma transferência normal (pode ter vários pacotes).
	var listing bytes.Buffer
	options := transport.ReceiveOptions{RTO: current.requestTimeout(), Request: request, Linger: current.lingerTime()}
	if _, err := transport.Receive(current.algorithm, current.link, &listing, options); err != nil {
		return nil, err
	}

	var files []remoteFile
	for _, line := range strings.Split(strings.TrimSpace(listing.String()), "\n") {
		name, sizeText, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		size, _ := strconv.ParseInt(sizeText, 10, 64)
		files = append(files, remoteFile{Name: name, Size: size})
	}
	current.remoteFiles = files
	return files, nil
}

func printFiles(files []remoteFile) {
	if len(files) == 0 {
		fmt.Println("O servidor não tem arquivos.")
		return
	}
	fmt.Printf("%-40s %12s\n", "ARQUIVO", "TAMANHO")
	for _, file := range files {
		fmt.Printf("%-40s %12s\n", file.Name, humanBytes(file.Size))
	}
}

// =====================================================================
// GET (download): o servidor envia, o cliente recebe
// =====================================================================

func (current *client) download(name, outPath string) error {
	if outPath == "" {
		outPath = filepath.Base(name)
	}

	// Grava em "arquivo.part" e só renomeia se o hash conferir.
	partialPath := outPath + ".part"
	file, err := os.Create(partialPath)
	if err != nil {
		return err
	}
	defer os.Remove(partialPath) // não faz nada se já foi renomeado

	progress := newProgressBar(current.knownSize(name))
	request := current.newRequest(proto.TypeGet, current.transferRequest(name))
	options := transport.ReceiveOptions{
		RTO:        current.requestTimeout(),
		Request:    request,
		Linger:     current.lingerTime(),
		OnProgress: progress.updateBytes,
	}

	start := time.Now()
	result, err := transport.Receive(current.algorithm, current.link, file, options)
	elapsed := time.Since(start)
	if result != nil && !result.CompletedAt.IsZero() {
		elapsed = result.CompletedAt.Sub(start) // não conta o tempo de linger
	}
	progress.finish(result)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(partialPath, outPath); err != nil {
		return err
	}

	current.report.download(name, outPath, current, result, elapsed)
	return nil
}

// knownSize procura o tamanho do arquivo na última lista (para a barra de progresso).
func (current *client) knownSize(name string) int64 {
	for _, file := range current.remoteFiles {
		if file.Name == name {
			return file.Size
		}
	}
	return 0
}

// =====================================================================
// PUT (upload): o cliente envia, o servidor recebe
// =====================================================================

func (current *client) upload(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s não é um arquivo", path)
	}

	name := filepath.Base(path)
	request := current.newRequest(proto.TypePut, current.transferRequest(name))
	reply, err := transport.Exchange(current.link, request, current.requestTimeout())
	if err != nil {
		return err
	}
	if reply.Type != proto.TypeOK {
		return fmt.Errorf("resposta inesperada ao PUT: %s", reply.Type)
	}

	stats := &transport.Stats{}
	progress := newProgressBar(info.Size())
	options := transport.SendOptions{
		Window:     current.window,
		RTO:        current.rto,
		OnProgress: func() { progress.updateStats(stats) },
	}

	start := time.Now()
	err = transport.Send(current.algorithm, current.link, file, options, stats)
	elapsed := time.Since(start)
	progress.done()
	if err != nil {
		return err
	}

	// Se chegamos aqui, o servidor confirmou o FIN, e ele só confirma
	// o FIN quando o SHA-256 confere.
	current.report.upload(path, info.Size(), current, stats, elapsed)
	return nil
}

// =====================================================================
// AUXILIARES
// =====================================================================

// humanBytes mostra 83886080 como "80.0 MB".
func humanBytes(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(size)/(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	}
	return fmt.Sprintf("%d B", size)
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func addressText(address *net.UDPAddr) string {
	if address == nil {
		return "?"
	}
	return address.String()
}
