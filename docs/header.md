# Cabeçalho do protocolo RAPOSA

Todo pacote RAPOSA é um datagrama UDP com um **cabeçalho fixo de 30 bytes**
seguido de até **1200 bytes de payload**. O limite de 1200 bytes evita a
fragmentação IP (30 + 1200 + 8 do UDP + 20 do IP = 1258 bytes, abaixo do MTU
de 1500 da Ethernet). Todos os números estão em **big-endian** (ordem de rede).

Código: `internal/proto/packet.go`. Dissector do Wireshark: `tools/wireshark/raposa.lua`.

## Campos

```
 0               1               2               3
 0 1 2 3 4 5 6 7 0 1 2 3 4 5 6 7 0 1 2 3 4 5 6 7 0 1 2 3 4 5 6 7
+---------------+---------------+-------------------------------+
|     Type      |     Flags     |            Window             |   0
+---------------+---------------+-------------------------------+
|                            ConnID                             |   4
+---------------------------------------------------------------+
|                              Seq                              |   8
+---------------------------------------------------------------+
|                              Ack                              |  12
+---------------------------------------------------------------+
|                                                               |  16
+                         Sack (64 bits)                        +
|                                                               |  20
+-------------------------------+-------------------------------+
|            Length             |                               |  24
+-------------------------------+           Checksum            +
|                               |                               |  28
+-------------------------------+-------------------------------+
|                    Payload (0 a 1200 bytes)                   |  30
```

| Offset | Tamanho | Campo | Função |
|---:|---:|---|---|
| 0 | 1 | **Type** | tipo da mensagem (tabela abaixo) |
| 1 | 1 | **Flags** | bit 0 = `RETRANS`: o pacote é uma retransmissão. Demais bits reservados (0) |
| 2 | 2 | **Window** | janela anunciada pelo receptor: quantos pacotes ele ainda consegue guardar (controle de fluxo) |
| 4 | 4 | **ConnID** | identificador da sessão, sorteado pelo servidor no `CHALLENGE`. O servidor separa os clientes por ele |
| 8 | 4 | **Seq** | DATA/FIN: número de sequência do pacote. ACK: **eco**, o Seq do pacote que gerou este ACK. Pedidos: número do pedido (detecta pedidos repetidos). PING/PONG: número do ping |
| 12 | 4 | **Ack** | ACK cumulativo: "recebi tudo abaixo de Ack" (= próximo Seq esperado) |
| 16 | 8 | **Sack** | bitmap do SACK: o bit *i* ligado indica que o pacote `Ack + 1 + i` já chegou (cobre 64 pacotes) |
| 24 | 2 | **Length** | tamanho do payload em bytes |
| 26 | 4 | **Checksum** | CRC-32 (IEEE) do pacote inteiro, calculado com este campo zerado |
| 30 | Length | **Payload** | dados |

## Tipos de mensagem

| Valor | Tipo | Sentido | Payload |
|---:|---|---|---|
| 1 | `HELLO` | cliente → servidor | vazio |
| 2 | `CHALLENGE` | servidor → cliente | nonce aleatório de 16 bytes. O `ConnID` do cabeçalho é a nova sessão |
| 3 | `AUTH` | cliente → servidor | `HMAC-SHA256(senha, nonce)` (32 bytes) |
| 4 | `AUTH_OK` | servidor → cliente | vazio |
| 5 | `LIST` | cliente → servidor | pedido de transferência (abaixo), sem nome |
| 6 | `GET` | cliente → servidor | pedido de transferência com o nome do arquivo |
| 7 | `PUT` | cliente → servidor | pedido de transferência com o nome do arquivo |
| 8 | `OK` | servidor → cliente | vazio: upload aceito, pode enviar |
| 9 | `DATA` | quem envia → quem recebe | pedaço do arquivo |
| 10 | `ACK` | quem recebe → quem envia | vazio (a informação está em Ack, Sack, Seq e Window) |
| 11 | `FIN` | quem envia → quem recebe | SHA-256 (32 bytes) de todo o conteúdo enviado |
| 12 | `ERR` | qualquer | 1 byte de código + mensagem em UTF-8 |
| 13 | `PING` | cliente → servidor | vazio (não exige login) |
| 14 | `PONG` | servidor → cliente | vazio; o Seq repete o do PING |

**Pedido de transferência** (payload de LIST/GET/PUT): texto `algoritmo\njanela\nnome`,
por exemplo `sack\n256\nfilme.mp4`. Quem **envia** os dados usa esse algoritmo
(`sack` ou `gbn`) e essa janela.

**Códigos de erro:** 1 senha inválida, 2 não autenticado, 3 arquivo não encontrado,
4 nome inválido, 5 hash divergente, 6 sessão desconhecida, 7 pedido inválido, 8 erro interno.

## Checksum

1. Monta o pacote com o campo Checksum = 0.
2. Calcula o CRC-32 (polinômio IEEE, o mesmo do Ethernet e do zip) de todos os bytes.
3. Grava o resultado nos bytes 26 a 29.

Na recepção, o pacote é **descartado em silêncio** se tiver menos de 30 bytes,
se `30 + Length` for diferente do tamanho recebido ou se o CRC não conferir.
Para o protocolo isso equivale a uma perda, e a retransmissão resolve.

O CRC detecta corrupção acidental, mas não protege contra alteração
intencional. A integridade do arquivo inteiro é garantida pelo SHA-256 do FIN.

## Exemplo

Um ACK da sessão `0xABCD1234` dizendo "tenho tudo até o 4, também tenho
6, 7 e 9, e este ACK foi gerado pelo pacote 9; ainda cabem 1021 pacotes":

```
0a 00 03 fd  ab cd 12 34  00 00 00 09  00 00 00 05
00 00 00 00 00 00 00 0b   00 00  05 26 f5 af
│  │  └─┬─┘  └───┬────┘   └──┬───┘   └──┬───┘
│  │  Window   ConnID      Seq=9      Ack=5
│  Flags=0
Type=10 (ACK)
Sack = 0x0b = 1011 em binário → bits 0, 1 e 3 → pacotes 6, 7 e 9
Length = 0, Checksum = 0x0526f5af
```

## Decisões de projeto

- **ConnID de 32 bits** (inspirado no QUIC): o servidor identifica a sessão
  pelo ConnID, e não só pelo IP e porta. Isso abre caminho para a migração de conexão.
- **ACK com eco do Seq**: o emissor mede o RTT exato de cada pacote e, no SACK,
  sabe exatamente qual pacote chegou, mesmo fora da faixa do bitmap.
- **SACK com bitmap de 64 bits**: tamanho fixo (cabeçalho simples) e redundância.
  Cada ACK repete o estado de 64 pacotes, então perder alguns ACKs não atrapalha.
- **SHA-256 no FIN**: a verificação de integridade faz parte do próprio protocolo.
  Se o hash não conferir, o receptor responde `ERR` em vez de confirmar o FIN.
