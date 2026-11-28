package proto

// Conteúdo (payload) das mensagens de controle: pedidos, erros e senha.

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

// =====================================================================
// PEDIDOS (LIST, GET e PUT)
// =====================================================================

// TransferRequest é o payload de LIST, GET e PUT.
// Quem vai ENVIAR os dados usa o algoritmo e a janela pedidos aqui.
type TransferRequest struct {
	Algorithm string // "gbn" ou "sack"
	Window    int    // janela do emissor, em pacotes
	Name      string // nome do arquivo (vazio no LIST)
}

// Encode gera o texto "algoritmo\njanela\nnome".
// Texto simples para ficar fácil de ler no Wireshark.
func (request TransferRequest) Encode() []byte {
	return []byte(fmt.Sprintf("%s\n%d\n%s", request.Algorithm, request.Window, request.Name))
}

// DecodeTransferRequest lê o texto gerado por Encode.
func DecodeTransferRequest(payload []byte) (TransferRequest, error) {
	parts := strings.SplitN(string(payload), "\n", 3)
	if len(parts) != 3 {
		return TransferRequest{}, fmt.Errorf("pedido malformado")
	}
	window, err := strconv.Atoi(parts[1])
	if err != nil {
		return TransferRequest{}, fmt.Errorf("janela inválida: %q", parts[1])
	}
	return TransferRequest{Algorithm: parts[0], Window: window, Name: parts[2]}, nil
}

// =====================================================================
// ERROS
// =====================================================================

// ErrorCode é o primeiro byte do payload de um pacote ERR.
type ErrorCode uint8

const (
	ErrCodeAuthFailed       ErrorCode = 1 // senha errada
	ErrCodeNotAuthenticated ErrorCode = 2 // pedido antes do login
	ErrCodeNotFound         ErrorCode = 3 // arquivo não existe
	ErrCodeInvalidName      ErrorCode = 4 // nome com "/" ou ".."
	ErrCodeHashMismatch     ErrorCode = 5 // SHA-256 não bateu
	ErrCodeUnknownSession   ErrorCode = 6 // ConnID desconhecido (sessão expirou)
	ErrCodeBadRequest       ErrorCode = 7 // pedido malformado
	ErrCodeInternal         ErrorCode = 8 // erro no servidor (disco, etc.)
)

var errorCodeNames = map[ErrorCode]string{
	ErrCodeAuthFailed:       "senha inválida",
	ErrCodeNotAuthenticated: "não autenticado",
	ErrCodeNotFound:         "arquivo não encontrado",
	ErrCodeInvalidName:      "nome de arquivo inválido",
	ErrCodeHashMismatch:     "hash divergente",
	ErrCodeUnknownSession:   "sessão desconhecida",
	ErrCodeBadRequest:       "pedido inválido",
	ErrCodeInternal:         "erro interno",
}

func (code ErrorCode) String() string {
	if name, ok := errorCodeNames[code]; ok {
		return name
	}
	return fmt.Sprintf("código %d", uint8(code))
}

// RemoteError é um erro que veio do outro lado dentro de um pacote ERR.
//
// Em Go, qualquer tipo que tenha o método "Error() string" pode ser usado
// como "error". Não precisa declarar "implements": basta ter o método.
type RemoteError struct {
	Code    ErrorCode
	Message string
}

func (remoteError *RemoteError) Error() string {
	return fmt.Sprintf("%s: %s", remoteError.Code, remoteError.Message)
}

// NewErrorPacket monta um pacote ERR: 1 byte de código + texto.
func NewErrorPacket(code ErrorCode, message string) *Packet {
	payload := append([]byte{byte(code)}, message...)
	return &Packet{Type: TypeErr, Payload: payload}
}

// ParseError lê um pacote ERR.
func ParseError(packet *Packet) *RemoteError {
	if len(packet.Payload) == 0 {
		return &RemoteError{Code: ErrCodeInternal, Message: "erro sem descrição"}
	}
	return &RemoteError{Code: ErrorCode(packet.Payload[0]), Message: string(packet.Payload[1:])}
}

// =====================================================================
// AUTENTICAÇÃO
// =====================================================================

// AuthProof calcula HMAC-SHA256(senha, nonce).
// A senha nunca viaja pela rede: o servidor manda um número aleatório
// (nonce) e o cliente prova que sabe a senha devolvendo este HMAC.
// Como o nonce muda a cada conexão, capturar o HMAC não serve para nada.
func AuthProof(password string, nonce []byte) []byte {
	mac := hmac.New(sha256.New, []byte(password))
	mac.Write(nonce)
	return mac.Sum(nil)
}
