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
- Janela deslizante com dois algoritmos: **Selective Repeat com SACK** (padrão) e **Go-Back-N**
- RTO adaptativo (SRTT + 4×RTTVAR, como no TCP) com backoff exponencial
- Cabeçalho próprio de 30 bytes: tipo, flags, janela anunciada, ConnID, Seq, Ack, SACK, tamanho e CRC-32 ([docs/header.md](docs/header.md))
- Autenticação por desafio: a senha nunca trafega (HMAC-SHA256 sobre um nonce)
- Listagem, download e upload de arquivos
- Integridade com SHA-256: o hash vai no FIN e o receptor confere antes de confirmar
- Mini-CDN: seleção automática do servidor com menor RTT (PING/PONG, mediana)
- Tratamento de erros: senha errada, arquivo inexistente, nome inválido, sessão expirada, servidor fora do ar, pacotes corrompidos
- Cliente interativo com histórico e autocompletar (TAB)
- Simulador de rede embutido (atraso, jitter, perda e corrupção)
- Script de benchmark dos 4 cenários, gráficos e dissector do Wireshark

## Estrutura do projeto

```
cmd/
  server/          servidor (distribuidor de pacotes + uma sessão por cliente)
  client/          cliente (modo interativo e modo de comando único)
internal/
  proto/           cabeçalho, tipos de mensagem, erros e autenticação
  transport/       janela deslizante: SACK, Go-Back-N, RTO, Link
  cdn/             escolha do servidor de menor RTT
  netem/           simulador de rede (atraso, jitter, perda, corrupção)
scripts/
  bench.sh         roda os 4 cenários e grava resultados/resultados.csv
  plot.py          gera os gráficos a partir do CSV
tools/wireshark/   dissector Lua do protocolo
docs/              cabeçalho, funcionamento interno e enunciado
```

## Requisitos

- **Go 1.25** ou mais novo (https://go.dev/dl/)
- Linux, macOS ou Windows (testado no Linux)
- Para os gráficos: Python 3 com `matplotlib` (`pip install matplotlib`)
- Opcional: Wireshark, para as capturas

Dependência Go: [`github.com/ergochat/readline`](https://github.com/ergochat/readline) (prompt interativo), baixada automaticamente pelo `go build`.

## Como compilar

```bash
go build -o bin/ ./cmd/...      # gera bin/server e bin/client
go test ./...                   # testes do transporte (perda, corrupção, SACK x GBN)
```

## Como executar

### Servidor

Rode uma instância em cada máquina ("Toca I" e "Toca II"). A pasta `-dir` tem os arquivos oferecidos e também recebe os uploads.

```bash
bin/server -name "Toca I" -dir arquivos -password minhasenha
# ou com a senha numa variável de ambiente:
RAPOSA_PASSWORD=minhasenha bin/server -dir arquivos -addr :9000
```

### Cliente interativo

```bash
bin/client -servers 192.168.0.10:9000,192.168.0.11:9000
```

O cliente mede o RTT dos servidores, escolhe o mais rápido, pede a senha e abre o prompt:

```
raposa [192.168.0.10:9000 sack/1024]> ls
raposa [192.168.0.10:9000 sack/1024]> get filme.mp4
raposa [192.168.0.10:9000 sack/1024]> put relatorio.pdf
raposa [192.168.0.10:9000 sack/1024]> algo gbn
raposa [192.168.0.10:9000 gbn/1024]> janela 64
raposa [192.168.0.10:9000 gbn/64]> ping        # mede de novo e troca de servidor se precisar
raposa [192.168.0.10:9000 gbn/64]> ajuda
```

TAB completa comandos, nomes de arquivos do servidor (`get`) e caminhos locais (`put`). As setas ↑↓ navegam no histórico.

### Cliente em modo de comando único (scripts)

```bash
bin/client -servers a:9000,b:9000 -password minhasenha ping
bin/client -servers a:9000 -password minhasenha list
bin/client -servers a:9000 -password minhasenha get filme.mp4
bin/client -servers a:9000 -password minhasenha -algo gbn -window 64 put filme.mp4
```

As opções vêm **antes** do comando.

### Opções principais

| Opção | Onde | Padrão | Descrição |
|---|---|---|---|
| `-servers` | cliente | `127.0.0.1:9000` | servidores separados por vírgula (mini-CDN) |
| `-password` | ambos | `$RAPOSA_PASSWORD` | senha |
| `-algo` | cliente | `sack` | `sack` (Selective Repeat) ou `gbn` (Go-Back-N) |
| `-window` | cliente | `1024` | janela em pacotes (1 a 4096; 1 = Stop-and-Wait) |
| `-rto` | ambos | `0` | timeout fixo; 0 = adaptativo |
| `-csv`, `-label` | cliente | | grava as métricas de cada transferência em CSV |
| `-dir`, `-addr`, `-name` | servidor | `arquivos`, `:9000`, `RAPOSA` | pasta, endereço e nome nos logs |
| `-delay`, `-jitter`, `-loss`, `-corrupt` | ambos | `0` | rede simulada nos **envios** daquele lado |

## Simulando a rede

Cada lado aplica atraso e perda nos pacotes que **envia**. Para um RTT de 100 ms
com 5% de perda, use metade do RTT em cada lado:

```bash
bin/server -dir arquivos -password x -delay 50ms -loss 0.05
bin/client -password x -delay 50ms -loss 0.05 get t10.bin
```

Para testar a mini-CDN numa máquina só, suba dois servidores em portas diferentes
com atrasos diferentes:

```bash
bin/server -name "Toca I"  -addr :9000 -dir arquivos -password x -delay 10ms &
bin/server -name "Toca II" -addr :9001 -dir arquivos -password x -delay 80ms &
bin/client -servers 127.0.0.1:9000,127.0.0.1:9001 -password x ping
```

## Cenários de teste

| Cenário | RTT    | Perda | Exemplo                         |
|---------|--------|-------|---------------------------------|
| 1       | 25 ms  | 0%    | Servidor ou CDN próximos        |
| 2       | 100 ms | 0%    | Brasil → EUA ou nuvem internacional |
| 3       | 100 ms | 5%    | Brasil → EUA + Wi-Fi congestionado  |
| 4       | 400 ms | 5%    | Satélite GEO ou enlace congestionado |

```bash
scripts/bench.sh                       # 4 cenários, SACK, janela 4096, arquivo de 80 MB, 3 repetições
ALGO=gbn WINDOW=64 scripts/bench.sh    # os mesmos cenários com Go-Back-N
MODE=janela scripts/bench.sh           # impacto do tamanho da janela (100 ms, 5% de perda)
python3 scripts/plot.py                # gera resultados/graficos/*.png e uma tabela resumo
```

O script mede throughput, RTT, retransmissões e taxa de perda estimada, e grava tudo em `resultados/resultados.csv`.

## Wireshark

```bash
wireshark -X lua_script:tools/wireshark/raposa.lua
```

O dissector mostra cada pacote como `DATA seq=5 [RETRANSMISSÃO]` ou `ACK ack=5 eco=9 sack=[6,7,9]`.
Filtros úteis: `raposa.flags.retrans == 1` (retransmissões) e `raposa.type == 13 || raposa.type == 14` (pings da mini-CDN).

A perda do simulador embutido acontece **antes** do socket, então não aparece na captura.
Para capturar perdas reais, use o `tc` do Linux no lugar dele:

```bash
sudo tc qdisc add dev lo root netem delay 50ms loss 5%
# ... rode o servidor e o cliente sem -delay/-loss e capture em "lo" ...
sudo tc qdisc del dev lo root
```

## Documentação

- [docs/explicacao.md](docs/explicacao.md): o projeto explicado passo a passo, com diagramas (comece por aqui)
- [docs/header.md](docs/header.md): especificação do cabeçalho e das mensagens
- [docs/funcionalidades.md](docs/funcionalidades.md): como cada parte funciona por dentro
- [docs/definicao-trabalho.md](docs/definicao-trabalho.md): enunciado

## Equipe

- 22.2.8003 - Richardy Rodrigues Tanure
- 22.2.8005 - Lucas Amaral Leme

## Licença

_Preencher_
