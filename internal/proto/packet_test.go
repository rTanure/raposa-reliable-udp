package proto

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := &Packet{Type: TypeData, Window: 64, ConnID: 7, Seq: 42, Ack: 41, Payload: []byte("olá raposa")}
	out, err := Unmarshal(in.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.Seq != in.Seq || out.Ack != in.Ack ||
		out.ConnID != in.ConnID || out.Window != in.Window || !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("diferente: %+v vs %+v", in, out)
	}
}

func TestCorruptionDetected(t *testing.T) {
	b := (&Packet{Type: TypeData, Payload: []byte("dados")}).Marshal()
	b[HeaderSize+1] ^= 0xFF // corrompe um byte do payload
	if _, err := Unmarshal(b); err != ErrBadChecksum {
		t.Fatalf("esperava ErrBadChecksum, veio %v", err)
	}
}

func TestShort(t *testing.T) {
	if _, err := Unmarshal([]byte{1, 2, 3}); err != ErrShort {
		t.Fatalf("esperava ErrShort, veio %v", err)
	}
}
