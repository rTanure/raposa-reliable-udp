#!/usr/bin/env python3
"""Gera os gráficos do relatório a partir de resultados/resultados.csv.

Uso:  python3 scripts/plot.py [caminho/do/resultados.csv]
Saída: resultados/graficos/*.png
"""
import csv
import statistics
import sys
from collections import defaultdict
from pathlib import Path

import matplotlib

matplotlib.use("Agg")  # gera arquivos sem abrir janela
import matplotlib.pyplot as plt

CSV_PATH = Path(sys.argv[1] if len(sys.argv) > 1 else "resultados/resultados.csv")
OUT_DIR = CSV_PATH.parent / "graficos"

SCENARIOS = {
    "cenario1": "1: 25 ms, 0%",
    "cenario2": "2: 100 ms, 0%",
    "cenario3": "3: 100 ms, 5%",
    "cenario4": "4: 400 ms, 5%",
}
METRICS = {
    "throughput_mbps": ("Throughput (Mbit/s)", "throughput"),
    "retransmissoes": ("Retransmissões (pacotes)", "retransmissoes"),
    "rtt_ping_ms": ("RTT medido (ms)", "rtt"),
    "perda_estimada": ("Pacotes retransmitidos (% do total enviado)", "retransmitidos_pct"),
}


def load_rows():
    with CSV_PATH.open(newline="") as file:
        return [row for row in csv.DictReader(file) if row["comando"] == "put"]


def group(rows, metric):
    """{(rótulo, algoritmo): [valores]}"""
    groups = defaultdict(list)
    for row in rows:
        value = float(row[metric] or 0)
        if metric == "perda_estimada":
            value *= 100
        groups[(row["rotulo"], row["algoritmo"])].append(value)
    return groups


def mean_and_std(values):
    return statistics.mean(values), (statistics.stdev(values) if len(values) > 1 else 0)


def plot_scenarios(rows):
    scenario_rows = [row for row in rows if row["rotulo"] in SCENARIOS]
    if not scenario_rows:
        return
    labels = [label for label in SCENARIOS if any(row["rotulo"] == label for row in scenario_rows)]
    algorithms = sorted({row["algoritmo"] for row in scenario_rows})
    bar_width = 0.8 / len(algorithms)

    for metric, (title, filename) in METRICS.items():
        groups = group(scenario_rows, metric)
        figure, axes = plt.subplots(figsize=(8, 4.5))
        for index, algorithm in enumerate(algorithms):
            means, errors, positions = [], [], []
            for position, label in enumerate(labels):
                values = groups.get((label, algorithm))
                if values:
                    mean, std = mean_and_std(values)
                    means.append(mean)
                    errors.append(std)
                    positions.append(position + index * bar_width)
            bars = axes.bar(positions, means, bar_width, yerr=errors, capsize=4, label=algorithm.upper())
            axes.bar_label(bars, fmt="%.0f" if metric == "retransmissoes" else "%.1f", padding=2, fontsize=8)
        axes.set_xticks([p + bar_width * (len(algorithms) - 1) / 2 for p in range(len(labels))])
        axes.set_xticklabels([SCENARIOS[label] for label in labels])
        axes.set_xlabel("Cenário (RTT, perda)")
        axes.set_ylabel(title)
        axes.set_title(f"{title} por cenário")
        axes.legend()
        axes.grid(axis="y", alpha=0.3)
        figure.tight_layout()
        figure.savefig(OUT_DIR / f"{filename}.png", dpi=150)
        plt.close(figure)
        print("gerado:", OUT_DIR / f"{filename}.png")


def plot_window_sweep(rows):
    sweep = [row for row in rows if row["rotulo"].startswith("janela-")]
    if not sweep:
        return
    figure, axes = plt.subplots(figsize=(7, 4.5))
    for algorithm in sorted({row["algoritmo"] for row in sweep}):
        by_window = defaultdict(list)
        for row in sweep:
            if row["algoritmo"] == algorithm:
                by_window[int(row["janela"])].append(float(row["throughput_mbps"]))
        windows = sorted(by_window)
        means = [statistics.mean(by_window[window]) for window in windows]
        axes.plot(windows, means, marker="o", label=f"{algorithm.upper()} (medido)")
        for window, mean in zip(windows, means):
            axes.annotate(f"{mean:.1f}", (window, mean), textcoords="offset points", xytext=(0, -14), ha="center", fontsize=8)

    # Limite teórico: janela x 1200 bytes / RTT (100 ms)
    windows = sorted({int(row["janela"]) for row in sweep})
    axes.plot(windows, [w * 1200 * 8 / 0.1 / 1e6 for w in windows], "--", color="gray", label="limite: janela × MSS / RTT")
    # Escala log nos dois eixos: a janela cresce de 4 em 4 vezes e os valores
    # vão de menos de 1 a centenas de Mbit/s.
    axes.set_xscale("log", base=2)
    axes.set_yscale("log")
    axes.set_xticks(windows)
    axes.set_xticklabels([str(window) for window in windows])
    axes.set_xlabel("Tamanho da janela (pacotes)")
    axes.set_ylabel("Throughput (Mbit/s)")
    axes.set_title("Impacto do tamanho da janela (RTT 100 ms, 5% de perda)")
    axes.grid(alpha=0.3)
    axes.legend()
    figure.tight_layout()
    figure.savefig(OUT_DIR / "janela.png", dpi=150)
    plt.close(figure)
    print("gerado:", OUT_DIR / "janela.png")


def print_table(rows):
    print(f"\n{'rótulo':<12} {'algo':<5} {'n':>2} {'Mbit/s':>9} {'retrans':>9} {'RTT ms':>8} {'retr. %':>8}")
    for key in sorted(group(rows, "throughput_mbps")):
        throughput = mean_and_std(group(rows, "throughput_mbps")[key])[0]
        retrans = mean_and_std(group(rows, "retransmissoes")[key])[0]
        rtt = mean_and_std(group(rows, "rtt_ping_ms")[key])[0]
        loss = mean_and_std(group(rows, "perda_estimada")[key])[0]
        count = len(group(rows, "throughput_mbps")[key])
        print(f"{key[0]:<12} {key[1]:<5} {count:>2} {throughput:>9.2f} {retrans:>9.0f} {rtt:>8.1f} {loss:>8.2f}")


def main():
    if not CSV_PATH.exists():
        sys.exit(f"{CSV_PATH} não existe; rode scripts/bench.sh antes")
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    rows = load_rows()
    plot_scenarios(rows)
    plot_window_sweep(rows)
    print_table(rows)


if __name__ == "__main__":
    main()
