<p align="center">
  <img src="assets/logo.png" alt="Logo do RAPOSA" width="320">
</p>

<h1 align="center">RAPOSA</h1>

<p align="center">
  <b>R</b>eliable <b>A</b>RQ <b>P</b>rotocol <b>O</b>ver UDP with <b>S</b>erver <b>A</b>uto-selection
</p>

<p align="center">
  Trabalho Prático de CSI510 – Redes de Computadores II (UFOP)
</p>

---

## Sobre

O RAPOSA é um protocolo de transferência confiável de arquivos sobre UDP, inspirado em conceitos do QUIC. O cliente mede o RTT de dois servidores e escolhe automaticamente o mais rápido, simulando o mapeamento usado por CDNs.

## Funcionalidades

- Entrega confiável e em ordem sobre UDP (ACKs, timeout e retransmissão)
- Janela deslizante
- Cabeçalho próprio (número de sequência, ACK, tipo, tamanho, checksum, janela anunciada)
- Autenticação simples por senha
- Listagem, download e upload de arquivos
- Validação de integridade com SHA-256
- Mini-CDN: seleção automática do servidor com menor RTT
- Tratamento de erros (falhas de comunicação e dados corrompidos)

## Estrutura do projeto

```
raposa/
├── assets/        # logo e imagens
├── src/
│   ├── server/    # servidor (Toca I e Toca II)
│   └── client/    # cliente
├── docs/          # especificação do cabeçalho e relatório
├── tests/         # cenários de desempenho
└── README.md
```

## Requisitos

- Linguagem: _preencher_
- Sistema operacional: _preencher_
- Dependências: _preencher_

## Como compilar

```bash
# preencher
```

## Como executar

**Servidor** (rodar uma instância em cada máquina):

```bash
# preencher
```

**Cliente**:

```bash
# preencher
```

## Cenários de teste

| Cenário | RTT    | Perda | Exemplo                         |
|---------|--------|-------|---------------------------------|
| 1       | 25 ms  | 0%    | Servidor ou CDN próximos        |
| 2       | 100 ms | 0%    | Brasil → EUA ou nuvem internacional |
| 3       | 100 ms | 5%    | Brasil → EUA + Wi-Fi congestionado  |
| 4       | 400 ms | 5%    | Satélite GEO ou enlace congestionado |

Os testes usam arquivos de pelo menos 80 MB e medem RTT, throughput, taxa de perda e número de retransmissões.

## Equipe

- 22.2.8003 - Richardy Rodrigues Tanure
- 22.2.8005 - Lucas Amaral Leme

## Licença

_Preencher_