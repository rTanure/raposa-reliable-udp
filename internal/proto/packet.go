// Pacote proto: formato dos pacotes do RAPOSA (o "cabeçalho próprio").
// A documentação completa está em docs/header.md.
package proto

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	MaxPayload = 1200 // bytes de dados por pacote (evita fragmentação IP)
	MaxWindow  = 4096 // maior janela permitida, em pacotes (cabe nos 16 bits de Window)
	SackBits   = 64   // quantos pacotes o bitmap do SACK cobre
)

// Layout do header (30 bytes, todos os números em big-endian):
//
//	offset  tamanho  campo
//	0       1        Type      tipo da mensagem
//	1       1        Flags     sinalizações (bit 0 = retransmissão)
//	2       2        Window    janela anunciada pelo receptor
//	4       4        ConnID    identificador da sessão
//	8       4        Seq       número de sequência
//	12      4        Ack       ACK cumulativo (próximo Seq esperado)
//	16      8        Sack      bitmap: bit i = recebi o pacote Seq-1-i (antes do eco)
//	24      2        Length    tamanho do payload
//	26      4        Checksum  CRC32 do pacote inteiro
const (
	offType     = 0
	offFlags    = 1
	offWindow   = 2
	offConnID   = 4
	offSeq      = 8
	offAck      = 12
	offSack     = 16
	offLength   = 24
	offChecksum = 26
	HeaderSize  = 30
)

// FlagRetrans marca que o pacote é uma retransmissão (ajuda no Wireshark).
const FlagRetrans uint8 = 1

// Type é o tipo da mensagem. "type Type uint8" cria um tipo novo que,
// por baixo, é um número de 8 bits.
type Type uint8

const (
	TypeHello     Type = 1  // cliente -> servidor: quero conectar
	TypeChallenge Type = 2  // servidor -> cliente: ConnID + desafio (nonce)
	TypeAuth      Type = 3  // cliente -> servidor: HMAC(senha, nonce)
	TypeAuthOK    Type = 4  // servidor -> cliente: senha correta
	TypeList      Type = 5  // pedido da lista de arquivos
	TypeGet       Type = 6  // pedido de download
	TypePut       Type = 7  // pedido de upload
	TypeOK        Type = 8  // servidor aceitou o upload: pode enviar
	TypeData      Type = 9  // pedaço do arquivo
	TypeAck       Type = 10 // confirmação (cumulativa + SACK)
	TypeFin       Type = 11 // fim do arquivo; payload = SHA-256 do conteúdo
	TypeErr       Type = 12 // erro; payload = código + mensagem
	TypePing      Type = 13 // medição de RTT (mini-CDN)
	TypePong      Type = 14 // resposta do ping
)

var typeNames = map[Type]string{
	TypeHello: "HELLO", TypeChallenge: "CHALLENGE", TypeAuth: "AUTH", TypeAuthOK: "AUTH_OK",
	TypeList: "LIST", TypeGet: "GET", TypePut: "PUT", TypeOK: "OK",
	TypeData: "DATA", TypeAck: "ACK", TypeFin: "FIN", TypeErr: "ERR",
	TypePing: "PING", TypePong: "PONG",
}

// String é chamado automaticamente quando imprimimos um Type com fmt/log.
func (packetType Type) String() string {
	if name, ok := typeNames[packetType]; ok {
		return name
	}
	return "DESCONHECIDO"
}

var (
	ErrShort       = errors.New("proto: pacote menor que o header")
	ErrBadLength   = errors.New("proto: tamanho do payload inconsistente")
	ErrBadChecksum = errors.New("proto: checksum inválido")
)

// Packet é o pacote já "decodificado", fácil de usar no código.
type Packet struct {
	Type    Type
	Flags   uint8
	Window  uint16
	ConnID  uint32
	Seq     uint32
	Ack     uint32
	Sack    uint64
	Payload []byte
}

// Marshal transforma o Packet em bytes prontos para o UDP.
func (packet *Packet) Marshal() []byte {
	buffer := make([]byte, HeaderSize+len(packet.Payload))
	buffer[offType] = byte(packet.Type)
	buffer[offFlags] = packet.Flags
	binary.BigEndian.PutUint16(buffer[offWindow:], packet.Window)
	binary.BigEndian.PutUint32(buffer[offConnID:], packet.ConnID)
	binary.BigEndian.PutUint32(buffer[offSeq:], packet.Seq)
	binary.BigEndian.PutUint32(buffer[offAck:], packet.Ack)
	binary.BigEndian.PutUint64(buffer[offSack:], packet.Sack)
	binary.BigEndian.PutUint16(buffer[offLength:], uint16(len(packet.Payload)))
	copy(buffer[HeaderSize:], packet.Payload)

	// O checksum é calculado com o próprio campo ainda zerado.
	binary.BigEndian.PutUint32(buffer[offChecksum:], crc32.ChecksumIEEE(buffer))
	return buffer
}

// Unmarshal faz o caminho inverso e recusa pacotes curtos, com tamanho
// errado ou corrompidos (checksum diferente).
func Unmarshal(buffer []byte) (*Packet, error) {
	if len(buffer) < HeaderSize {
		return nil, ErrShort
	}
	length := int(binary.BigEndian.Uint16(buffer[offLength:]))
	if len(buffer) != HeaderSize+length {
		return nil, ErrBadLength
	}

	received := binary.BigEndian.Uint32(buffer[offChecksum:])
	withoutChecksum := make([]byte, len(buffer))
	copy(withoutChecksum, buffer)
	binary.BigEndian.PutUint32(withoutChecksum[offChecksum:], 0)
	if crc32.ChecksumIEEE(withoutChecksum) != received {
		return nil, ErrBadChecksum
	}

	return &Packet{
		Type:    Type(buffer[offType]),
		Flags:   buffer[offFlags],
		Window:  binary.BigEndian.Uint16(buffer[offWindow:]),
		ConnID:  binary.BigEndian.Uint32(buffer[offConnID:]),
		Seq:     binary.BigEndian.Uint32(buffer[offSeq:]),
		Ack:     binary.BigEndian.Uint32(buffer[offAck:]),
		Sack:    binary.BigEndian.Uint64(buffer[offSack:]),
		Payload: append([]byte(nil), buffer[HeaderSize:]...),
	}, nil
}
