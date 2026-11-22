package proto

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	HeaderSize = 22
	MaxPayload = 1200 // evita fragmentação IP
)

type Type uint8

const (
	TypeHello Type = iota + 1
	TypeChallenge
	TypeAuth
	TypeAuthOK
	TypeListReq
	TypeListResp
	TypeGet
	TypePut
	TypeMeta
	TypeData
	TypeAck
	TypeFin
	TypeErr
	TypePing
	TypePong
)

var (
	ErrShort       = errors.New("proto: pacote menor que o header")
	ErrBadLength   = errors.New("proto: tamanho do payload inconsistente")
	ErrBadChecksum = errors.New("proto: checksum inválido")
)

// Layout (big-endian):
type Packet struct {
	Type    Type
	Flags   uint8
	Window  uint16
	ConnID  uint32
	Seq     uint32
	Ack     uint32
	Payload []byte
}

func (p *Packet) Marshal() []byte {
	b := make([]byte, HeaderSize+len(p.Payload))
	b[0] = byte(p.Type)
	b[1] = p.Flags
	binary.BigEndian.PutUint16(b[2:], p.Window)
	binary.BigEndian.PutUint32(b[4:], p.ConnID)
	binary.BigEndian.PutUint32(b[8:], p.Seq)
	binary.BigEndian.PutUint32(b[12:], p.Ack)
	binary.BigEndian.PutUint16(b[16:], uint16(len(p.Payload)))
	copy(b[HeaderSize:], p.Payload)
	binary.BigEndian.PutUint32(b[18:], crc32.ChecksumIEEE(b))
	return b
}

func Unmarshal(b []byte) (*Packet, error) {
	if len(b) < HeaderSize {
		return nil, ErrShort
	}
	length := int(binary.BigEndian.Uint16(b[16:]))
	if len(b) != HeaderSize+length {
		return nil, ErrBadLength
	}
	got := binary.BigEndian.Uint32(b[18:])
	tmp := make([]byte, len(b))
	copy(tmp, b)
	binary.BigEndian.PutUint32(tmp[18:], 0)
	if crc32.ChecksumIEEE(tmp) != got {
		return nil, ErrBadChecksum
	}
	return &Packet{
		Type:    Type(b[0]),
		Flags:   b[1],
		Window:  binary.BigEndian.Uint16(b[2:]),
		ConnID:  binary.BigEndian.Uint32(b[4:]),
		Seq:     binary.BigEndian.Uint32(b[8:]),
		Ack:     binary.BigEndian.Uint32(b[12:]),
		Payload: append([]byte(nil), b[HeaderSize:]...),
	}, nil
}
