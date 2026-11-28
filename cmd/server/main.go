// Servidor RAPOSA: serve os arquivos de um diretório (list, get, put),
// exige senha e responde PING para a escolha de servidor (mini-CDN).
//
// Uso:
//
//	go run ./cmd/server -dir arquivos -password segredo [-addr :9000] [-delay 50ms -loss 0.05]
package main

import (
	"crypto/rand"
	"encoding/binary"
	"flag"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"raposa/internal/netem"
	"raposa/internal/proto"
	"raposa/internal/transport"
)

// server guarda o que é compartilhado por todas as sessões.
type server struct {
	conn     net.PacketConn // socket (embrulhado no simulador de rede)
	name     string         // nome para os logs (ex.: "Toca I")
	dir      string         // pasta com os arquivos servidos
	password string
	rto      time.Duration // 0 = adaptativo

	// Várias goroutines mexem no mapa de sessões, então ele é protegido
	// por um Mutex: só quem fez Lock() pode mexer até chamar Unlock().
	mutex    sync.Mutex
	sessions map[uint32]*session // ConnID -> sessão

	corrupted int // pacotes descartados por checksum inválido
}

func main() {
	// flag.String devolve um PONTEIRO; por isso lá embaixo usamos *addr.
	addr := flag.String("addr", ":9000", "endereço UDP para escutar")
	name := flag.String("name", "RAPOSA", "nome do servidor nos logs (ex.: \"Toca I\")")
	dir := flag.String("dir", "arquivos", "pasta com os arquivos disponíveis (uploads também vão para cá)")
	password := flag.String("password", "", "senha dos clientes (ou variável RAPOSA_PASSWORD)")
	rto := flag.Duration("rto", 0, "timeout de retransmissão fixo (0 = adaptativo)")
	delay := flag.Duration("delay", 0, "atraso simulado nos envios do servidor (ex: 50ms)")
	jitter := flag.Duration("jitter", 0, "variação do atraso simulado")
	loss := flag.Float64("loss", 0, "perda simulada nos envios do servidor (0 a 1)")
	corrupt := flag.Float64("corrupt", 0, "chance de corromper um pacote enviado (0 a 1)")
	flag.Parse()

	if *password == "" {
		*password = os.Getenv("RAPOSA_PASSWORD")
	}
	if *password == "" {
		log.Fatal("defina a senha com -password ou com a variável RAPOSA_PASSWORD")
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}

	conn := listenUDP(*addr)
	defer conn.Close() // "defer" roda isto quando a função main terminar

	srv := &server{
		conn:     netem.Wrap(conn, netem.Config{Delay: *delay, Jitter: *jitter, Loss: *loss, Corrupt: *corrupt}),
		name:     *name,
		dir:      *dir,
		password: *password,
		rto:      *rto,
		sessions: make(map[uint32]*session),
	}
	log.SetPrefix("[" + *name + "] ")
	log.Printf("ouvindo em %s, servindo a pasta %q", conn.LocalAddr(), *dir)
	srv.serve()
}

// serve é o DISTRIBUIDOR: lê todos os pacotes do socket e decide para
// onde cada um vai. Ele nunca bloqueia esperando uma sessão.
func (srv *server) serve() {
	buffer := make([]byte, 65535)
	for {
		bytesRead, from, err := srv.conn.ReadFrom(buffer)
		if err != nil {
			log.Println("erro de leitura:", err)
			continue
		}

		packet, err := proto.Unmarshal(buffer[:bytesRead])
		if err != nil {
			// Corrompido: descarta (para quem enviou, é como uma perda).
			// Loga só de vez em quando para não encher a tela.
			srv.corrupted++
			if srv.corrupted%50 == 1 {
				log.Printf("pacote inválido descartado (%v); total até agora: %d", err, srv.corrupted)
			}
			continue
		}

		switch packet.Type {
		case proto.TypePing:
			// PING não precisa de login: responde na hora, ecoando o número.
			pong := &proto.Packet{Type: proto.TypePong, Seq: packet.Seq}
			srv.conn.WriteTo(pong.Marshal(), from)
		case proto.TypeHello:
			srv.startSession(from)
		default:
			srv.deliver(packet, from)
		}
	}
}

// startSession cria uma sessão nova e manda o desafio (CHALLENGE).
func (srv *server) startSession(from net.Addr) {
	connID := randomConnID()
	nonce := make([]byte, 16)
	rand.Read(nonce)

	newSession := &session{
		server: srv,
		connID: connID,
		nonce:  nonce,
		link:   transport.NewChannelLink(srv.conn, from, connID),
		peer:   from.String(),
	}

	srv.mutex.Lock()
	srv.sessions[connID] = newSession
	srv.mutex.Unlock()

	log.Printf("[sessão %08x] nova conexão de %s", connID, from)
	newSession.link.Send(&proto.Packet{Type: proto.TypeChallenge, Payload: nonce})
	go newSession.run() // cada sessão roda em paralelo
}

// deliver entrega o pacote para a sessão dona do ConnID.
func (srv *server) deliver(packet *proto.Packet, from net.Addr) {
	srv.mutex.Lock()
	target := srv.sessions[packet.ConnID]
	srv.mutex.Unlock()

	if target == nil {
		// Sessão não existe (expirou ou nunca existiu). Só avisa se for
		// um pedido; ACKs e dados atrasados são ignorados em silêncio.
		if isRequest(packet.Type) {
			reply := proto.NewErrorPacket(proto.ErrCodeUnknownSession, "sessão expirada; conecte de novo")
			reply.ConnID = packet.ConnID
			srv.conn.WriteTo(reply.Marshal(), from)
		}
		return
	}
	if from.String() != target.peer {
		return // ConnID certo vindo de outro endereço: ignora
	}

	// select com "default": se a fila da sessão estiver cheia, descarta
	// em vez de travar o distribuidor (é como uma perda na rede).
	select {
	case target.link.Inbox <- packet:
	default:
	}
}

func (srv *server) removeSession(connID uint32) {
	srv.mutex.Lock()
	delete(srv.sessions, connID)
	srv.mutex.Unlock()
}

// randomConnID sorteia um número de 32 bits diferente de zero.
func randomConnID() uint32 {
	var bytes [4]byte
	for {
		rand.Read(bytes[:])
		if id := binary.BigEndian.Uint32(bytes[:]); id != 0 {
			return id
		}
	}
}

// listenUDP abre o socket e aumenta o buffer de recepção, para que o
// sistema operacional não descarte pacotes quando a janela é grande.
func listenUDP(address string) *net.UDPConn {
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatal(err)
	}
	conn.SetReadBuffer(8 << 20) // 8 MB (o Linux pode limitar a menos)
	conn.SetWriteBuffer(8 << 20)
	return conn
}
