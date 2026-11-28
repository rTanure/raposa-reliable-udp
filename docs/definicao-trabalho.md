# CSI510 – Redes de Computadores II
## Trabalho Prático

## 1) Objetivo

O objetivo deste trabalho é desenvolver um **protocolo de transferência confiável de arquivos sobre UDP**, inspirado em conceitos utilizados por protocolos modernos de transporte, como o QUIC.

Além da implementação do protocolo, os alunos deverão realizar **experimentos de desempenho** e analisar como aspectos como atraso, perda de pacotes, tamanho da janela e posicionamento dos servidores impactam a distribuição de conteúdo na Internet.

O trabalho integra conceitos abordados na disciplina, incluindo:

- Protocolos de transporte;
- Desempenho de redes;
- QUIC;
- Distribuição de conteúdo (CDNs);
- Computação em nuvem.

## 2) Organização

- O trabalho poderá ser realizado em grupos de **3 alunos**.
- Linguagem de programação e sistema operacional são de livre escolha.
- Valor: **22 pontos**.
- Apresentação e entrega final do relatório: **até 15/12/2026**.

## 3) Desenvolvimento

Deverá ser implementado um sistema cliente-servidor para transferência de arquivos utilizando UDP.

**O servidor deverá:**

- a. Disponibilizar uma lista de arquivos para os clientes;
- b. Permitir download dos arquivos disponíveis;
- c. Exigir autenticação simples (senha).

**O cliente deverá:**

- a. Conectar ao servidor;
- b. Solicitar a lista de arquivos disponíveis;
- c. Realizar downloads e uploads de arquivos.

**Tratamento de erros** – cliente e servidor deverão:

- Tratar exceções;
- Detectar falhas de comunicação e dados corrompidos;
- Emitir mensagens de erro adequadas.

## 4) Protocolo de transporte

Como o UDP não oferece confiabilidade, o grupo deverá implementar mecanismos para garantir:

- Entrega confiável;
- Entrega em ordem;
- Controle de retransmissões;
- Detecção de perdas;
- Controle por temporização (timeout).

### Cabeçalho próprio

O grupo deverá **definir e documentar** o cabeçalho utilizado pelo protocolo. Exemplos:

- Número de sequência;
- ACK;
- Tipo da mensagem;
- Tamanho do pacote;
- Checksum;
- Janela anunciada.

### Janela deslizante

Deverá ser implementado um mecanismo de janela deslizante.

> Soluções baseadas apenas em Stop-and-Wait serão aceitas, porém sofrerão redução na pontuação referente ao desempenho.

### Integridade

O arquivo recebido deverá ser validado através de hash (SHA-256 ou equivalente).

O relatório deverá apresentar o **hash original** do arquivo e o **hash recebido**.

## 5) Avaliação de desempenho

O protocolo deverá ser avaliado experimentalmente em **4 cenários**. Os grupos deverão medir:

- RTT;
- Throughput;
- Taxa de perda;
- Número de retransmissões.

Os testes deverão ser realizados variando:

- RTT;
- Taxa de perda.

Os experimentos deverão ser realizados utilizando **arquivos de pelo menos 80 MB**. A implementação pode utilizar atrasos artificiais (`sleep`) na aplicação ou ferramentas do sistema operacional.

| Cenário | RTT | Loss | Exemplos |
|:---:|:---:|:---:|---|
| 1 | 25 ms | 0% | Servidor ou CDN próximos |
| 2 | 100 ms | 0% | Brasil → EUA ou nuvem internacional |
| 3 | 100 ms | 5% | Brasil → EUA + Wi-Fi congestionado |
| 4 | 400 ms | 5% | Satélite GEO ou enlaces muito congestionados |

Os resultados deverão ser apresentados em **gráficos**.

## 6) Mini-CDN

Cada grupo deverá executar **duas instâncias do servidor em máquinas diferentes**.

O cliente deverá medir o RTT para os servidores e **selecionar automaticamente** aquele que apresentar menor atraso.

O relatório deverá responder:

1. Qual servidor foi escolhido?
2. Qual o impacto no desempenho?
3. Como esse mecanismo se relaciona com o processo de mapeamento utilizado por CDNs?

## 7) Testes e capturas

Os grupos deverão incluir:

- Capturas do Wireshark;
- Evidências de ACKs e retransmissões;
- Evidências do processo de seleção do servidor.

## 8) Funcionalidades extras (bônus) – até 10 pontos

### Dashboard Web

Interface Web apresentando:

- RTT;
- Throughput;
- Perdas;
- Retransmissões;
- Janela atual.

### Funcionalidades inspiradas no QUIC

Implementar uma ou mais funcionalidades inspiradas no protocolo QUIC:

- Multiplexação de fluxos;
- Estabelecimento rápido de conexão;
- Criptografia ponta a ponta;
- Migração de conexão.

O grupo deverá **justificar tecnicamente** sua implementação.

### Implantação em nuvem

Implantação do servidor em uma plataforma IaaS:

- AWS;
- Azure;
- Google Cloud;
- Oracle Cloud.

Para este caso, o grupo deverá **comparar os resultados obtidos localmente e na nuvem**.

## 9) Entrega e apresentação

Submeter via Moodle:

- Código-fonte completo;
- Arquivo **README** com instruções para compilação, configuração e execução do sistema;
- **Relatório final** (seguir o modelo da SBC).

A apresentação ocorrerá em data e horário previamente agendados, na **sala A409**.

---

## FAQ (Frequently Asked Questions)

**Nosso grupo já tem 3 alunos. Um colega que está sem grupo pode se juntar a nós?**
Não.

**Estamos com dificuldade para fazer o trabalho e só conseguimos desenvolver o básico.**
Todo e qualquer esforço do aluno/grupo será bem recompensado. Um relatório bem elaborado, explicando as limitações e sugerindo melhorias no trabalho, pode contribuir para a nota.

**Posso utilizar IA Generativa para auxiliar no desenvolvimento do trabalho?**
Modelos de LLMs são ferramentas poderosas. O uso de IA generativa é permitido como ferramenta de apoio, porém todos os integrantes devem compreender integralmente a implementação. Lembre-se de que você não terá acesso a esse recurso durante a apresentação do trabalho.

**Um integrante do meu grupo não está familiarizado com o conteúdo apresentado. Corremos o risco de tirarmos zero?**
Caso algum integrante não domine o conteúdo apresentado, há risco de zerar a nota do grupo. Neste caso, o ideal é adotar uma abordagem simples, garantindo que todos compreendam o que será apresentado. Normalmente, o professor direciona as "dúvidas" aos que demonstram menor conhecimento. É essencial alinhar o conhecimento do grupo com o que está sendo desenvolvido.

**Um dos integrantes tem um compromisso e não poderá estar presente no dia da apresentação.**
Marque um dia e horário em que todos possam apresentar.