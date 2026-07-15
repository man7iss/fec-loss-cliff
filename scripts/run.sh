#!/usr/bin/env bash
# Build the harness and run the full matrix on a Linux host (root required for
# network namespaces, veth, netem, and per-namespace congestion-control sysctls).
#
# Usage: sudo ./scripts/run.sh [--quick] [--bytes N] [--reps N] [--cc cubic,bbr]
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "error: the matrix needs Linux netem/netns; this is $(uname -s)." >&2
	echo "Build here, then run this script on the Hetzner box." >&2
	exit 1
fi

if [[ "${EUID}" -ne 0 ]]; then
	echo "error: run as root (sudo). netns/tc/sysctl require it." >&2
	exit 1
fi

# Load the congestion-control modules we intend to sweep. tcp_bbr provides BBRv1
# in mainline; an out-of-tree build of google/bbr replaces it with BBRv3 under
# the same module name. Missing modules are skipped by the harness, not fatal.
modprobe tcp_bbr 2>/dev/null || echo "note: tcp_bbr module not available; BBR arms will be skipped"
modprobe sch_netem 2>/dev/null || true

echo "available congestion control: $(sysctl -n net.ipv4.tcp_available_congestion_control)"

go build -o feccliff ./cmd/feccliff

# Clean up any topology left by an interrupted run.
ip netns del feccli 2>/dev/null || true
ip netns del fecsrv 2>/dev/null || true
ip link del veth-cli 2>/dev/null || true

exec ./feccliff matrix --out results.csv "$@"
