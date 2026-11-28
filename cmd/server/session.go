package main

// Uma sessão = um cliente conectado. Roda numa goroutine própria e
// recebe os pacotes do distribuidor (server.serve) pelo link.Inbox.
//
// Fluxo de uma sessão:
//
//	cliente                         servidor
//	HELLO          ------------->   (cria a sessão, sorteia ConnID e nonce)
//	               <-------------   CHALLENGE (nonce)
//	AUTH (HMAC)    ------------->
//	               <-------------   AUTH_OK  (ou ERR)
//	LIST/GET       ------------->
//	               <-------------   DATA ... DATA FIN   (GBN ou SACK)
//	PUT            ------------->
//	               <-------------   OK
//	DATA ... FIN   ------------->   (servidor confere o SHA-256)

import (
	"crypto/hmac"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"raposa/internal/proto"
	"raposa/internal/transport"
)

const (
	sessionIdleTimeout = 60 * time.Second // sem nenhum pedido por 1 min: encerra
	maxAuthAttempts    = 3
)

type session struct {
	server *server
	link   *transport.ChannelLink
	connID uint32
	peer   string // endereço do cliente
	nonce  []byte // desafio do login

	authenticated bool
	failedLogins  int

	// Pedidos podem chegar repetidos (o cliente reenvia se a resposta
	// atrasar). Cada pedido tem um número (campo Seq). Se chegar um
	// número que já tratamos, só reenviamos a resposta guardada.
	lastRequestID uint32
	lastReply     *proto.Packet

	// Resposta ao último FIN recebido (upload). Se o emissor repetir o
	// FIN (nossa resposta se perdeu), reenviamos daqui do laço da sessão.
	lastFinalReply *proto.Packet
}

// run é o laço principal da sessão.
func (current *session) run() {
	defer current.server.removeSession(current.connID)

	// recover: se algo der muito errado (panic) nesta sessão, registra
	// o erro e encerra só ela, sem derrubar o servidor inteiro.
	defer func() {
		if problem := recover(); problem != nil {
			current.logf("erro inesperado, sessão encerrada: %v", problem)
		}
	}()

	for {
		packet, err := current.link.Receive(time.Now().Add(sessionIdleTimeout))
		if err != nil {
			current.logf("encerrada por inatividade")
			return
		}
		// DATA ou FIN aqui é sobra do último upload: o emissor não recebeu
		// nossa confirmação final e está reenviando. Repetimos a resposta.
		// (Tem que ser DATA também: o emissor pode já saber que o FIN chegou
		// e estar reenviando só um pacote cujo ACK se perdeu.)
		if isStreamPacket(packet.Type) && current.lastFinalReply != nil {
			current.link.Send(current.lastFinalReply)
			continue
		}
		if !isRequest(packet.Type) {
			continue // ACK ou dado atrasado de uma transferência que já acabou
		}

		// Pedido repetido?
		if packet.Seq <= current.lastRequestID {
			if current.lastReply != nil {
				current.link.Send(current.lastReply)
			}
			continue
		}
		current.lastRequestID = packet.Seq
		current.lastReply = nil

		if keepGoing := current.handle(packet); !keepGoing {
			return
		}
	}
}

// handle trata um pedido novo. Devolve false para encerrar a sessão.
func (current *session) handle(packet *proto.Packet) bool {
	if packet.Type == proto.TypeAuth {
		return current.handleAuth(packet)
	}

	if !current.authenticated {
		current.replyError(proto.ErrCodeNotAuthenticated, "faça login antes")
		return true
	}

	request, err := proto.DecodeTransferRequest(packet.Payload)
	if err == nil && !transport.ValidAlgorithm(request.Algorithm) {
		err = fmt.Errorf("algoritmo desconhecido %q", request.Algorithm)
	}
	if err != nil {
		current.replyError(proto.ErrCodeBadRequest, err.Error())
		return true
	}

	switch packet.Type {
	case proto.TypeList:
		current.sendList(request)
	case proto.TypeGet:
		current.sendFile(request)
	case proto.TypePut:
		current.receiveFile(request)
	}
	return true
}

// handleAuth confere o HMAC enviado pelo cliente.
func (current *session) handleAuth(packet *proto.Packet) bool {
	expected := proto.AuthProof(current.server.password, current.nonce)

	// hmac.Equal compara em tempo constante (não dá pistas pelo tempo).
	if hmac.Equal(packet.Payload, expected) {
		current.authenticated = true
		current.reply(&proto.Packet{Type: proto.TypeAuthOK})
		current.logf("login aceito")
		return true
	}

	current.failedLogins++
	current.logf("senha errada (%d/%d)", current.failedLogins, maxAuthAttempts)
	current.replyError(proto.ErrCodeAuthFailed, "senha incorreta")
	return current.failedLogins < maxAuthAttempts
}

// sendList envia a lista de arquivos: uma linha "nome<TAB>tamanho" por arquivo.
func (current *session) sendList(request proto.TransferRequest) {
	entries, err := os.ReadDir(current.server.dir)
	if err != nil {
		current.replyError(proto.ErrCodeInternal, "não consegui ler a pasta de arquivos")
		return
	}

	var listing strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			continue // só arquivos normais e visíveis
		}
		fmt.Fprintf(&listing, "%s\t%d\n", entry.Name(), info.Size())
	}

	stats := &transport.Stats{}
	options := transport.SendOptions{Window: request.Window, RTO: current.server.rto}
	if err := transport.Send(request.Algorithm, current.link, strings.NewReader(listing.String()), options, stats); err != nil {
		current.logf("LIST falhou: %v", err)
		return
	}
	current.logf("LIST enviado (%d arquivos)", strings.Count(listing.String(), "\n"))
}

// sendFile é o download (GET): o servidor envia.
func (current *session) sendFile(request proto.TransferRequest) {
	path, ok := current.safePath(request.Name)
	if !ok {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		current.replyError(proto.ErrCodeNotFound, fmt.Sprintf("%q não existe no servidor", request.Name))
		return
	}
	defer file.Close()

	current.logf("GET %s (%s, janela %d)", request.Name, request.Algorithm, request.Window)
	stats := &transport.Stats{}
	start := time.Now()
	options := transport.SendOptions{Window: request.Window, RTO: current.server.rto}
	if err := transport.Send(request.Algorithm, current.link, file, options, stats); err != nil {
		current.logf("GET %s falhou: %v", request.Name, err)
		return
	}
	current.logTransfer("GET", request.Name, start, stats)
}

// receiveFile é o upload (PUT): o servidor recebe.
// Grava num arquivo temporário e só renomeia se o SHA-256 conferir.
func (current *session) receiveFile(request proto.TransferRequest) {
	path, ok := current.safePath(request.Name)
	if !ok {
		return
	}
	temporary, err := os.CreateTemp(current.server.dir, ".upload-*")
	if err != nil {
		current.replyError(proto.ErrCodeInternal, "não consegui criar o arquivo no servidor")
		return
	}
	// Se algo der errado, apaga o temporário (depois do rename ele não existe mais).
	defer os.Remove(temporary.Name())

	readyReply := &proto.Packet{Type: proto.TypeOK}
	current.reply(readyReply)
	current.logf("PUT %s (%s)", request.Name, request.Algorithm)

	start := time.Now()
	options := transport.ReceiveOptions{RTO: current.server.rto, Reply: readyReply}
	result, err := transport.Receive(request.Algorithm, current.link, temporary, options)
	if result != nil {
		current.lastFinalReply = result.FinalReply
	}
	closeErr := temporary.Close()
	if err != nil {
		current.logf("PUT %s falhou: %v", request.Name, err)
		return
	}
	if closeErr != nil {
		current.logf("PUT %s falhou ao gravar no disco: %v", request.Name, closeErr)
		return
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		current.logf("PUT %s falhou ao publicar: %v", request.Name, err)
		return
	}

	elapsed := result.CompletedAt.Sub(start)
	current.logf("PUT %s concluído: %d bytes em %v (%.2f MB/s), duplicados: %d",
		request.Name, result.Bytes, elapsed.Round(time.Millisecond),
		float64(result.Bytes)/1e6/elapsed.Seconds(), result.Duplicates)
	current.logf("    SHA-256 original: %x", result.SenderSHA256)
	current.logf("    SHA-256 recebido: %x", result.ReceivedSHA256)
}

// safePath impede nomes como "../../etc/passwd": só aceita um nome
// simples, sem pastas.
func (current *session) safePath(name string) (string, bool) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		current.replyError(proto.ErrCodeInvalidName, fmt.Sprintf("nome inválido: %q", name))
		return "", false
	}
	return filepath.Join(current.server.dir, name), true
}

// reply responde um pedido e guarda a resposta (para pedidos repetidos).
func (current *session) reply(packet *proto.Packet) {
	current.lastReply = packet
	current.link.Send(packet)
}

func (current *session) replyError(code proto.ErrorCode, message string) {
	current.logf("ERR %s: %s", code, message)
	current.reply(proto.NewErrorPacket(code, message))
}

// logf escreve no log com o ConnID na frente.
// "args ...any" quer dizer "quantos argumentos quiser, de qualquer tipo".
func (current *session) logf(format string, args ...any) {
	log.Printf("[sessão %08x] %s", current.connID, fmt.Sprintf(format, args...))
}

func (current *session) logTransfer(command, name string, start time.Time, stats *transport.Stats) {
	elapsed := time.Since(start)
	current.logf("%s %s concluído: %d bytes em %v (%.2f MB/s) | RTT médio %v | pacotes %d | retransmissões %d (fast %d, timeouts %d)",
		command, name, stats.BytesAcked, elapsed.Round(time.Millisecond), float64(stats.BytesAcked)/1e6/elapsed.Seconds(),
		stats.AvgRTT().Round(time.Microsecond), stats.Packets, stats.Retrans, stats.FastRetrans, stats.Timeouts)
	current.logf("    SHA-256 enviado: %x", stats.SHA256)
}

func isStreamPacket(packetType proto.Type) bool {
	return packetType == proto.TypeData || packetType == proto.TypeFin
}

// isRequest diz se o tipo é um pedido do cliente.
func isRequest(packetType proto.Type) bool {
	switch packetType {
	case proto.TypeAuth, proto.TypeList, proto.TypeGet, proto.TypePut:
		return true
	}
	return false
}
