package main

// Modo interativo: um prompt "raposa>" com histórico (setas ↑ ↓),
// autocompletar com TAB e edição de linha. Quem faz isso é a biblioteca
// github.com/ergochat/readline (fork mantido da chzyer/readline).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ergochat/readline"

	"raposa/internal/proto"
	"raposa/internal/transport"
)

// Cores ANSI: sequências especiais que o terminal entende como "mude a cor".
const (
	colorReset = "\033[0m"
	colorBold  = "\033[1m"
	colorDim   = "\033[2m"
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorCyan  = "\033[36m"
)

func paint(color, text string) string {
	return color + text + colorReset
}

const helpText = `Comandos:
  list | ls                 lista os arquivos do servidor
  get <arquivo> [destino]   baixa um arquivo (TAB completa o nome)
  put <arquivo>             envia um arquivo local (TAB completa o caminho)
  ping                      mede o RTT dos servidores e troca para o mais rápido
  algo [sack|gbn]           mostra ou troca o algoritmo da janela
  janela [n]                mostra ou troca o tamanho da janela (1 a 4096)
  status                    mostra servidor, sessão e configurações
  limpar                    limpa a tela
  ajuda                     mostra esta ajuda
  sair                      encerra (Ctrl+D também)`

// interactive é o laço do modo interativo.
func (current *client) interactive() {
	fmt.Println(paint(colorBold+colorCyan, "RAPOSA") + " - transferência confiável de arquivos sobre UDP")
	fmt.Println(paint(colorDim, "digite 'ajuda' para ver os comandos; TAB completa\n"))

	if err := current.chooseServer(); err != nil {
		fmt.Println(paint(colorRed, "erro: "+err.Error()))
		return
	}

	shell, err := readline.NewFromConfig(&readline.Config{
		Prompt:          current.prompt(),
		AutoComplete:    current.completer(),
		HistoryFile:     historyPath(),
		InterruptPrompt: "^C",
		EOFPrompt:       "sair",
	})
	if err != nil {
		fmt.Println(paint(colorRed, "erro ao abrir o terminal: "+err.Error()))
		return
	}
	defer shell.Close()

	if !current.interactiveLogin(shell) {
		return
	}

	for {
		line, err := shell.ReadLine()
		if errors.Is(err, readline.ErrInterrupt) {
			continue // Ctrl+C só limpa a linha
		}
		if errors.Is(err, io.EOF) {
			return // Ctrl+D sai
		}

		fields := strings.Fields(line) // separa a linha em palavras
		if len(fields) == 0 {
			continue
		}
		if quit := current.execute(fields); quit {
			return
		}
		shell.SetPrompt(current.prompt())
	}
}

// interactiveLogin pergunta a senha (sem mostrar na tela) se ela não veio
// por flag/variável. Dá 3 chances.
func (current *client) interactiveLogin(shell *readline.Instance) bool {
	askPassword := current.password == ""
	for attempt := 1; attempt <= 3; attempt++ {
		if askPassword {
			// Configuração de senha: herda o terminal e esconde o que é digitado.
			passwordConfig := shell.GeneratePasswordConfig()
			passwordConfig.Prompt = "senha: "
			passwordConfig.MaskRune = '*'
			typed, err := shell.ReadLineWithConfig(passwordConfig)
			if err != nil {
				fmt.Println(paint(colorRed, "não consegui ler a senha: "+err.Error()))
				return false
			}
			current.password = typed
		}

		err := current.login()
		if err == nil {
			return true
		}
		fmt.Println(paint(colorRed, "login falhou: "+err.Error()))

		var remoteError *proto.RemoteError
		if !errors.As(err, &remoteError) || remoteError.Code != proto.ErrCodeAuthFailed {
			return false // problema de rede, não de senha
		}
		askPassword = true
	}
	return false
}

// execute roda um comando. Devolve true para sair.
func (current *client) execute(fields []string) bool {
	command, args := fields[0], fields[1:]
	var err error

	switch command {
	case "sair", "exit", "quit":
		return true
	case "ajuda", "help", "?":
		fmt.Println(helpText)
	case "limpar", "clear":
		fmt.Print("\033[H\033[2J") // código ANSI para limpar a tela
	case "list", "ls":
		err = current.withSession(func() error {
			files, err := current.list()
			if err == nil {
				printFiles(files)
			}
			return err
		})
	case "get":
		if len(args) == 0 || len(args) > 2 {
			err = errors.New("uso: get <arquivo> [destino]")
			break
		}
		destination := ""
		if len(args) == 2 {
			destination = args[1]
		}
		err = current.withSession(func() error { return current.download(args[0], destination) })
	case "put":
		if len(args) != 1 {
			err = errors.New("uso: put <arquivo>")
			break
		}
		err = current.withSession(func() error { return current.upload(args[0]) })
	case "ping":
		err = current.remeasure()
	case "algo":
		err = current.setAlgorithm(args)
	case "janela":
		err = current.setWindow(args)
	case "status":
		current.printStatus()
	default:
		err = fmt.Errorf("comando desconhecido %q (digite 'ajuda')", command)
	}

	if err != nil {
		fmt.Println(paint(colorRed, "erro: "+err.Error()))
	}
	return false
}

func (current *client) setAlgorithm(args []string) error {
	if len(args) == 1 {
		if !transport.ValidAlgorithm(args[0]) {
			return fmt.Errorf("algoritmo %q não existe (use sack ou gbn)", args[0])
		}
		current.algorithm = args[0]
	}
	fmt.Println("algoritmo:", current.algorithm)
	return nil
}

func (current *client) setWindow(args []string) error {
	if len(args) == 1 {
		window, err := strconv.Atoi(args[0])
		if err != nil || window < 1 || window > proto.MaxWindow {
			return fmt.Errorf("janela deve ser um número de 1 a %d", proto.MaxWindow)
		}
		current.window = window
	}
	fmt.Println("janela:", current.window, "pacotes")
	return nil
}

func (current *client) printStatus() {
	rto := "adaptativo"
	if current.rto > 0 {
		rto = current.rto.String()
	}
	fmt.Printf("servidor:  %s (RTT %.2f ms)\n", current.server, milliseconds(current.pingRTT))
	fmt.Printf("sessão:    %08x\n", current.link.ConnID)
	fmt.Printf("algoritmo: %s | janela: %d | RTO: %s\n", current.algorithm, current.window, rto)
	if current.report.csvPath != "" {
		fmt.Printf("CSV:       %s\n", current.report.csvPath)
	}
}

// remeasure mede de novo os servidores. Se o mais rápido mudou, faz login
// nele; se continua o mesmo, mantém a sessão atual.
func (current *client) remeasure() error {
	previousServer := current.server.String()
	previousSession := current.link.ConnID

	if err := current.chooseServer(); err != nil {
		return err
	}
	if current.server.String() == previousServer {
		current.link.ConnID = previousSession
		return nil
	}
	fmt.Println("Trocando de servidor...")
	current.remoteFiles = nil
	return current.login()
}

// prompt monta "raposa [127.0.0.1:9000 sack/256]> " colorido.
func (current *client) prompt() string {
	details := fmt.Sprintf("%s %s/%d", current.server, current.algorithm, current.window)
	return paint(colorBold+colorGreen, "raposa") + " " + paint(colorDim, "["+details+"]") + "> "
}

// =====================================================================
// AUTOCOMPLETAR (TAB)
// =====================================================================

func (current *client) completer() *readline.PrefixCompleter {
	remoteNames := readline.PcItemDynamic(current.completeRemote)
	localPaths := readline.PcItemDynamic(completeLocal)

	return readline.NewPrefixCompleter(
		readline.PcItem("list"), readline.PcItem("ls"),
		readline.PcItem("get", remoteNames),
		readline.PcItem("put", localPaths),
		readline.PcItem("ping"),
		readline.PcItem("algo", readline.PcItem("sack"), readline.PcItem("gbn")),
		readline.PcItem("janela", readline.PcItem("16"), readline.PcItem("64"), readline.PcItem("256"), readline.PcItem("1024"), readline.PcItem("4096")),
		readline.PcItem("status"),
		readline.PcItem("limpar"),
		readline.PcItem("ajuda"),
		readline.PcItem("sair"),
	)
}

// completeRemote sugere os arquivos do servidor (pede a lista se ainda não tem).
func (current *client) completeRemote(string) []string {
	if len(current.remoteFiles) == 0 {
		current.list() // se der erro, só fica sem sugestões
	}
	names := make([]string, 0, len(current.remoteFiles))
	for _, file := range current.remoteFiles {
		names = append(names, file.Name)
	}
	return names
}

// completeLocal sugere arquivos e pastas locais a partir do que já foi digitado.
func completeLocal(line string) []string {
	typed := ""
	if fields := strings.Fields(line); len(fields) > 1 && !strings.HasSuffix(line, " ") {
		typed = fields[len(fields)-1]
	}

	directory := filepath.Dir(typed)
	if strings.HasSuffix(typed, "/") {
		directory = strings.TrimSuffix(typed, "/")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}

	var suggestions []string
	for _, entry := range entries {
		path := entry.Name()
		if directory != "." {
			path = filepath.Join(directory, entry.Name())
		}
		if entry.IsDir() {
			path += "/"
		}
		suggestions = append(suggestions, path)
	}
	return suggestions
}

func historyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".raposa_history")
}
