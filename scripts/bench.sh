#!/usr/bin/env bash
# Experimentos de desempenho do trabalho (seção 5 do enunciado).
#
# Sobe um servidor local com o atraso/perda de cada cenário, faz upload de um
# arquivo de 80 MB e grava as métricas em resultados/resultados.csv.
# Depois rode:  python3 scripts/plot.py   para gerar os gráficos.
#
# Uso:
#   scripts/bench.sh                         # 4 cenários, SACK, janela 4096, 3 repetições
#   ALGO=gbn WINDOW=64 scripts/bench.sh      # mesmo com Go-Back-N
#   MODE=janela scripts/bench.sh             # varre a janela (16..4096) no cenário 3
#   RUNS=1 SIZE_MB=20 scripts/bench.sh       # rodada rápida
#   ONLY="cenario3 cenario4" scripts/bench.sh  # só alguns cenários
#
# Como a rede é simulada: o atraso de cada lado é RTT/2 (ida + volta = RTT)
# e a perda é aplicada nos DOIS sentidos (dados e ACKs), cada um com a taxa
# do cenário.
set -euo pipefail
cd "$(dirname "$0")/.."

ALGO=${ALGO:-sack}
WINDOW=${WINDOW:-4096}
RUNS=${RUNS:-3}
SIZE_MB=${SIZE_MB:-80}
MODE=${MODE:-cenarios}
OUT=${OUT:-resultados}
PORT=${PORT:-9100}
PASSWORD=bench

mkdir -p bin "$OUT/servidor"
FILE="$OUT/teste-${SIZE_MB}MB.bin"
if [ ! -f "$FILE" ]; then
  echo "Gerando $FILE com dados aleatórios..."
  head -c "${SIZE_MB}M" /dev/urandom > "$FILE"
fi
go build -o bin/ ./cmd/...

# run_scenario <rótulo> <RTT em ms> <perda 0..1> <janela>
run_scenario() {
  local label=$1 rtt=$2 loss=$3 window=$4
  local delay
  delay="$(awk "BEGIN { print $rtt / 2 }")ms"

  bin/server -addr ":$PORT" -dir "$OUT/servidor" -password "$PASSWORD" \
    -delay "$delay" -loss "$loss" > "$OUT/servidor-$label.log" 2>&1 &
  local server_pid=$!
  sleep 0.5

  for run in $(seq 1 "$RUNS"); do
    echo "=== $label: RTT ${rtt} ms, perda $loss, $ALGO janela $window ($run/$RUNS)"
    bin/client -servers "127.0.0.1:$PORT" -password "$PASSWORD" -pings 3 \
      -algo "$ALGO" -window "$window" -delay "$delay" -loss "$loss" \
      -csv "$OUT/resultados.csv" -label "$label" put "$FILE" \
      | tr '\r' '\n' | grep -E '^(tempo|throughput|RTT|retransmi|perda|SHA)'
  done

  kill "$server_pid"
  wait "$server_pid" 2>/dev/null || true
}

if [ "$MODE" = "janela" ]; then
  for window in 16 64 256 1024 4096; do
    run_scenario "janela-$window" 100 0.05 "$window"
  done
else
  # ONLY="cenario3 cenario4" roda só os cenários listados
  ONLY=${ONLY:-cenario1 cenario2 cenario3 cenario4}
  [[ " $ONLY " == *" cenario1 "* ]] && run_scenario cenario1 25 0 "$WINDOW"
  [[ " $ONLY " == *" cenario2 "* ]] && run_scenario cenario2 100 0 "$WINDOW"
  [[ " $ONLY " == *" cenario3 "* ]] && run_scenario cenario3 100 0.05 "$WINDOW"
  [[ " $ONLY " == *" cenario4 "* ]] && run_scenario cenario4 400 0.05 "$WINDOW"
fi

echo
echo "Métricas em $OUT/resultados.csv. Gráficos: python3 scripts/plot.py"
