# Tarefas do trabalho prático (CSI510 - RAPOSA)

Cada tarefa tem o objetivo, como fazer e o critério de "pronto".

## 1. Validar o Stop-and-Wait

**Objetivo:** ter certeza de que a base confiável funciona antes de construir em cima.

**Como fazer:**
- Gerar arquivos de teste: `head -c 10M /dev/urandom > t10.bin` e `head -c 200K /dev/urandom > t200k.bin`.
- Rodar `cmd/recv` e `cmd/send` em três situações: sem rede simulada; com `-loss 0.05 -rto 50ms` nos dois lados; com `-delay 25ms -rto 200ms` e o arquivo de 200 KB.
- Em cada uma, comparar `sha256sum` do original e do recebido.
- Anotar throughput e retransmissões de cada rodada. Servem de **baseline** no relatório (Stop-and-Wait envia 1 pacote por RTT, então o máximo é ~MSS/RTT).

**Pronto quando:** os hashes são idênticos nas 3 situações e há números anotados.

---

## 2. Documentar o header e organizar o código dele

**Objetivo:** ter o cabeçalho documentado (entregável do enunciado) e fácil de alterar.

**Como fazer:**
- Em `internal/proto/packet.go`, trocar os números soltos (`b[16:]`, `b[18:]`) por constantes nomeadas (`offLength`, `offChecksum`...).
- Escrever `docs/header.md` com: tabela de campos (nome, offset, tamanho, função), a lista de tipos de mensagem, o cálculo do checksum e um exemplo de pacote em hexadecimal.
- Decidir já se haverá `Flags` com significado (por exemplo, bit "retransmissão") e se haverá byte de versão.
- Atualizar este documento a cada mudança no header.

**Pronto quando:** `docs/header.md` descreve exatamente o que o código faz e os testes do `proto` passam.

---

## 3. Decidir o algoritmo de janela: Go-Back-N ou Selective Repeat

**Objetivo:** fixar a escolha antes de mexer no transporte, porque ela muda o header.

**Como fazer:**
- **Go-Back-N:** mais simples. O receptor só aceita o pacote esperado, e o sender reenvia tudo a partir do primeiro não confirmado quando há perda. Desperdiça banda com 5% de perda.
- **Selective Repeat (recomendado):** o receptor guarda os pacotes fora de ordem, e o sender reenvia só os que faltam. O ACK carrega `Ack` cumulativo (próximo `Seq` esperado) e um **bitmap SACK de 64 bits**, em que o bit *i* indica se o pacote `Ack+1+i` já chegou.
- Registrar a decisão e o motivo no relatório. Se for Selective Repeat, incluir o campo `Sack` (8 bytes) no header e atualizar `docs/header.md`.

**Pronto quando:** a decisão está escrita e o header reflete ela.

---

## 4. Implementar a janela deslizante

**Objetivo:** enviar vários pacotes sem esperar ACK de cada um, para ocupar o enlace.

**Como fazer:**
- **Sender:** manter `base` (menor Seq não confirmado), `nextSeq` e a janela `W` configurável por flag. Pode enviar enquanto `nextSeq < base + W`. Guardar por pacote: bytes, hora do último envio, número de tentativas e se foi confirmado.
- **Receiver:** guardar pacotes fora de ordem em um `map[uint32][]byte`, escrever em ordem assim que o próximo esperado chegar, e anunciar a janela livre no campo `Window` (controle de fluxo).
- **Concorrência:** uma goroutine só lê o socket e joga os pacotes em um canal. Uma segunda goroutine (o loop de eventos) é dona de todo o estado e usa `select` sobre: canal de pacotes, ticker de timeout e leitura do arquivo. Assim não há acesso concorrente ao estado.
- **Timeout:** um ticker curto (por exemplo, RTO/4) varre os pacotes enviados e reenvia os expirados. Opcional: retransmissão rápida quando o SACK mostra um buraco.
- A fórmula de referência é `throughput máximo = W x MSS / RTT`. Escolha `W` grande o bastante para o cenário de 400 ms (por exemplo, 256 ou mais).
- Rodar com `go test -race` e com `go run -race`.

**Pronto quando:** o hash bate com perda de 5% e jitter, e o throughput se aproxima de `W x MSS / RTT` nos cenários sem perda.

---

## 5. RTO adaptativo

**Objetivo:** o timeout se ajustar ao RTT medido, para não retransmitir à toa nem esperar demais.

**Como fazer:**
- A cada ACK de um pacote **que não foi retransmitido** (regra de Karn), medir `R = agora - horaDoEnvio`.
- Primeira amostra: `SRTT = R`, `RTTVAR = R/2`. Depois:
  `RTTVAR = 0,75 x RTTVAR + 0,25 x |SRTT - R|` e `SRTT = 0,875 x SRTT + 0,125 x R`.
- `RTO = SRTT + 4 x RTTVAR`, limitado a um mínimo (~50 ms) e um máximo (~3 s). RTO inicial de 1 s.
- A cada timeout, dobrar o RTO (backoff exponencial) até uma nova amostra válida.
- Guardar as amostras de RTT: elas alimentam a métrica de RTT do relatório.

**Pronto quando:** em 25 ms e em 400 ms de RTT o RTO converge perto do RTT sem retransmissões espúrias no cenário sem perda.

---

## 6. Métricas e barra de progresso baseada em ACK

**Objetivo:** coletar tudo que o enunciado pede medir (RTT, throughput, taxa de perda, retransmissões).

**Como fazer:**
- Criar `internal/metrics` com contadores atômicos: pacotes enviados, retransmissões, ACKs recebidos, duplicados, descartados por checksum, janela atual, bytes confirmados e amostras de RTT.
- Taxa de perda estimada = `retransmissões / pacotes enviados`. Registrar também o que o `netem` descartou (valor "real" para comparar).
- Exportar um CSV por execução com cenário, RTT, perda configurada, janela, tempo, throughput, retransmissões.
- Trocar a barra de progresso para contar **bytes confirmados** (callback do motor quando um ACK chega), em vez de bytes lidos.

**Pronto quando:** cada execução gera uma linha de CSV com todas as métricas.

---

## 7. Demultiplexar sessões (ConnID)

**Objetivo:** o servidor atender mais de um cliente e identificar de quem é cada pacote.

**Como fazer:**
- O servidor tem um único loop lendo o socket. Para cada pacote, olha o `ConnID` e entrega ao canal da sessão correspondente (`map[uint32]*Session`).
- Cada sessão roda em sua própria goroutine, com timeout de inatividade que remove a sessão do mapa.
- O `ConnID` é sorteado pelo servidor (`crypto/rand`) ao aceitar a autenticação. Pacotes de uma sessão vindos de endereço diferente do registrado são ignorados (por enquanto).
- Adaptar `SendStream`/`RecvStream` para receber o canal da sessão em vez de ler o socket diretamente.

**Pronto quando:** dois clientes baixam arquivos ao mesmo tempo, sem misturar dados.

---

## 8. Handshake e autenticação por senha

**Objetivo:** exigir senha sem enviá-la em texto puro.

**Como fazer:**
- Fluxo: `HELLO` -> servidor responde `CHALLENGE` com um nonce aleatório de 16 bytes -> cliente envia `AUTH` com `HMAC-SHA256(senha, nonce)` -> servidor confere e responde `AUTH_OK` com o `ConnID`, ou `ERR`.
- Comparar com `hmac.Equal` (tempo constante).
- Senha vem de flag ou variável de ambiente, nunca fixa no código.
- O handshake também precisa de retransmissão com timeout.
- Limitar tentativas erradas por endereço.
- Pedidos de LIST, GET e PUT sem sessão autenticada devolvem `ERR`.

**Pronto quando:** senha certa entra, senha errada recebe erro claro, e o tráfego capturado não revela a senha.

---

## 9. LIST (lista de arquivos)

**Objetivo:** o cliente pedir e exibir os arquivos disponíveis.

**Como fazer:**
- O servidor lê o diretório configurado (`os.ReadDir`), monta uma lista com nome e tamanho e envia pelo mesmo `SendStream` (a lista pode passar de um pacote).
- Formato simples: uma linha por arquivo, `nome<TAB>tamanho`.
- O cliente imprime em tabela.
- Só listar arquivos regulares, sem subdiretórios.

**Pronto quando:** `raposa-client list` mostra os arquivos do servidor.

---

## 10. GET com SHA-256

**Objetivo:** baixar arquivo com validação de integridade.

**Como fazer:**
- Cliente manda `GET` com o nome. O servidor valida o nome (**só `filepath.Base`**, para evitar `../` saindo do diretório), abre o arquivo e responde `META` com tamanho e SHA-256 (calculado antes ou em um passo prévio).
- Em seguida o servidor usa `SendStream` e o cliente usa `RecvStream`, gravando em arquivo temporário e calculando o hash enquanto grava (`io.MultiWriter`).
- No fim, o cliente compara tamanho e hash. Se bater, renomeia o temporário; se não, apaga e mostra erro.
- Imprimir **hash original e hash recebido**: o relatório pede os dois.

**Pronto quando:** download com hash idêntico, e erro claro quando o arquivo não existe ou o hash diverge.

---

## 11. PUT (upload)

**Objetivo:** enviar arquivo ao servidor reaproveitando o motor.

**Como fazer:**
- Cliente manda `PUT` com nome, tamanho e SHA-256. O servidor valida (autenticado, nome seguro, tamanho dentro do limite, espaço em disco) e responde `ACK`/`ERR`.
- Os papéis se invertem: o cliente chama `SendStream` e o servidor `RecvStream`.
- Servidor verifica o hash ao final e só então publica o arquivo.
- Decidir a política de sobrescrita (recusar ou flag `-force`).

**Pronto quando:** upload com hash conferido nos dois lados.

---

## 12. Tratamento de erros

**Objetivo:** cumprir o requisito de tratar exceções, detectar falhas e emitir mensagens adequadas.

**Como fazer:**
- Definir um payload de `TypeErr` com **código** (senha inválida, arquivo não encontrado, hash divergente, não autenticado, sessão expirada, interno) e **mensagem**.
- No cliente, traduzir cada código para uma mensagem amigável e um código de saída diferente de zero.
- Tratar: servidor fora do ar (limite de tentativas), conexão ociosa, disco cheio, arquivo sumindo no meio da transferência.
- No servidor, um `recover` por sessão para um erro numa sessão não derrubar o processo.
- Adicionar ao `netem` uma probabilidade `Corrupt` que inverte um byte do pacote, para provar que o checksum descarta dados corrompidos.

**Pronto quando:** cada falha acima produz uma mensagem clara, e o servidor continua de pé.

---

## 13. Mini-CDN: escolha automática do servidor

**Objetivo:** o cliente medir o RTT de duas instâncias e escolher a mais próxima.

**Como fazer:**
- Implementar `PING`/`PONG` (sem autenticação) e `internal/cdn` com: para cada servidor, enviar N pings (por exemplo, 5), calcular a **mediana** do RTT e escolher o menor.
- O cliente recebe `-servers a:9000,b:9000` e imprime uma tabela com o RTT de cada um e o escolhido.
- Subir os dois servidores em **máquinas diferentes** (dois notebooks do grupo ou duas VMs). Para forçar diferença, aplicar um atraso diferente em cada servidor.
- Opcional: o servidor responder um comando `SERVERS` com a lista, como o DNS de uma CDN.
- Guardar o log da seleção (evidência para o relatório) e medir o throughput usando o servidor escolhido **e** o outro, para responder "qual o impacto no desempenho".

**Pronto quando:** o cliente escolhe sozinho o servidor de menor RTT e há logs e medições dos dois.

---

## 14. Benchmark dos 4 cenários e gráficos

**Objetivo:** produzir os resultados experimentais do enunciado.

**Como fazer:**
- Arquivo de teste de **pelo menos 80 MB**: `head -c 80M /dev/urandom > t80.bin`.
- Cenários: (1) 25 ms / 0%, (2) 100 ms / 0%, (3) 100 ms / 5%, (4) 400 ms / 5%. Com o `netem` nos dois lados, `Delay` em cada ponta é metade do RTT.
- **Documentar a interpretação da perda** (5% por sentido ou no total) e manter a mesma em todos os cenários.
- Escolher a janela para os cenários difíceis. Fazer também uma varredura de janela (por exemplo, 16, 64, 256, 1024) em um cenário, porque o enunciado cita o impacto do tamanho da janela.
- Repetir cada cenário 3 a 5 vezes e usar média e desvio.
- Medir o baseline Stop-and-Wait em um arquivo menor e extrapolar, deixando claro que é extrapolação.
- Script em Python (matplotlib) que lê os CSVs e gera: throughput por cenário, retransmissões por cenário, throughput por tamanho de janela, evolução do RTO/RTT ao longo do tempo.

**Pronto quando:** existe uma tabela e gráficos legíveis dos 4 cenários.

---

## 15. Capturas do Wireshark

**Objetivo:** apresentar evidências de ACKs, retransmissões e seleção de servidor.

**Como fazer:**
- Atenção: o `netem` da aplicação descarta **antes** de o pacote chegar ao socket, então a perda não aparece na captura. Para as capturas, use também o `tc netem` do Linux (`sudo tc qdisc add dev lo root netem delay 50ms loss 5%`) e remova depois (`sudo tc qdisc del dev lo root`).
- Capturar em `lo` (ou na interface real nas duas máquinas) com o filtro `udp.port == 9000`.
- Mostrar: pacotes DATA com `Seq` repetido (retransmissões), ACKs com SACK, os PING/PONG da seleção de servidor e o handshake sem a senha.
- Opcional: dissector em Lua que decodifica o header, para o Wireshark exibir `Type`, `Seq` e `Ack` com nome.
- Salvar os `.pcapng` e os prints no repositório.

**Pronto quando:** há prints legíveis das três evidências.

---

## 16. README

**Objetivo:** entregável do Moodle com instruções de compilar, configurar e executar.

**Como fazer:**
- Pré-requisitos (versão do Go), `go build ./...`, exemplos de comandos de servidor e cliente (list, get, put, seleção de servidor), tabela de flags, como rodar o benchmark e gerar gráficos.
- Estrutura de pastas e link para `docs/`.
- Testar o README em uma máquina limpa, seguindo só o que está escrito.

**Pronto quando:** outra pessoa roda o sistema só lendo o README.

---

## 17. Relatório final (modelo SBC)

**Objetivo:** documento principal da avaliação.

**Como fazer:**
- Usar o template da SBC. Seções: introdução, projeto do protocolo (header, tipos de mensagem, diagramas de estado e sequência), implementação (arquitetura, janela, RTO, concorrência), metodologia dos experimentos, resultados (os 4 cenários, hash original e recebido, gráficos), mini-CDN (as 3 perguntas do enunciado), limitações e melhorias, conclusão.
- Responder explicitamente: qual servidor foi escolhido, qual o impacto no desempenho, e como isso se relaciona com o mapeamento usado por CDNs.
- Incluir as capturas do Wireshark.
- O FAQ do enunciado diz que um relatório bem feito, com limitações e melhorias, ajuda na nota, então não esconder o que não ficou pronto.

**Pronto quando:** o relatório está revisado por todos do grupo.

---

## 18. Bônus (só depois de tudo acima)

Em ordem de custo e benefício:
1. **Dashboard web:** servidor HTTP no cliente com SSE enviando RTT, throughput, perdas, retransmissões e janela atual lidos do pacote `metrics`.
2. **Criptografia ponta a ponta:** AES-GCM no payload com chave derivada por X25519 + HKDF e a senha. Justificar tecnicamente. Manter uma flag para desligar nas capturas.
3. **Implantação em nuvem:** servidor em uma VM (AWS, Azure, GCP ou Oracle), repetir os cenários e **comparar com os resultados locais**.
4. **Multiplexação de fluxos** ou **migração de conexão** (o `ConnID` já ajuda).

---

## 19. Ensaio da apresentação

**Objetivo:** todos dominarem toda a implementação (regra do FAQ).

**Como fazer:**
- Marcar uma data em que **todos** possam estar na sala A409.
- Cada integrante explica, sem ajuda de IA, uma parte: header, janela e RTO, handshake e senha, mini-CDN, resultados.
- Roteiro de demo: `list`, `get` com hash, perda simulada, seleção de servidor, gráficos.
- Ter um plano B caso a rede da sala falhe (demo local com `netem`).

**Pronto quando:** qualquer um do grupo consegue responder perguntas sobre qualquer parte.