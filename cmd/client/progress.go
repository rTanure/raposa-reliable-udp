package main

import (
	"fmt"
	"strings"
	"time"

	"raposa/internal/transport"
)

// progressBar desenha "[█████░░░░░] 50%" no terminal.
type progressBar struct {
	total     int64 // 0 = tamanho desconhecido (download)
	start     time.Time
	lastDrawn time.Time
}

func newProgressBar(total int64) *progressBar {
	now := time.Now()
	return &progressBar{total: total, start: now, lastDrawn: now} // primeiro desenho só após 100 ms
}

// updateStats é usado no upload: mostra o que o servidor já confirmou.
func (progress *progressBar) updateStats(stats *transport.Stats) {
	if !progress.shouldDraw() {
		return
	}
	extra := fmt.Sprintf("janela %d | RTO %v | retrans %d", stats.Window, stats.RTO.Round(time.Millisecond), stats.Retrans)
	progress.draw(stats.BytesAcked, extra)
}

// updateBytes é usado no download: mostra o que já foi gravado.
func (progress *progressBar) updateBytes(received int64) {
	if progress.shouldDraw() {
		progress.draw(received, "")
	}
}

// finish desenha a última vez e pula a linha.
func (progress *progressBar) finish(result *transport.Result) {
	if result != nil {
		progress.draw(result.Bytes, "")
	}
	progress.done()
}

func (progress *progressBar) done() {
	fmt.Println()
}

// shouldDraw limita a 10 desenhos por segundo (desenhar sempre deixaria lento).
func (progress *progressBar) shouldDraw() bool {
	if time.Since(progress.lastDrawn) < 100*time.Millisecond {
		return false
	}
	progress.lastDrawn = time.Now()
	return true
}

func (progress *progressBar) draw(done int64, extra string) {
	const width = 30
	speed := float64(done) / 1e6 / time.Since(progress.start).Seconds()

	if progress.total <= 0 {
		// Tamanho desconhecido: só quantidade e velocidade.
		fmt.Printf("\r%s recebidos | %.2f MB/s   ", humanBytes(done), speed)
		return
	}

	fraction := float64(done) / float64(progress.total)
	filled := int(fraction * width)
	barText := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)

	// "\r" volta ao início da linha, então a barra se sobrescreve.
	fmt.Printf("\r[%s] %5.1f%% | %.2f MB/s | %s   ", barText, fraction*100, speed, extra)
}
