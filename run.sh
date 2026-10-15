#!/bin/sh
# usage: ./run.sh [target...]
# env: EVENTS, CONCURRENCY, RUNS, DOCKER (docker or podman)
set -eu
cd "$(dirname "$0")"

D=${DOCKER:-$(command -v docker || command -v podman)}
EVENTS=${EVENTS:-1000000}
CONCURRENCY=${CONCURRENCY:-32}
RUNS=${RUNS:-50}
IDLE=${IDLE:-30}
TARGETS=${*:-impactite plausible goatcounter}
OUT=results/$(date -u +%Y-%m-%dT%H%M%SZ).jsonl

say() { printf '[*] %s\n' "$*" >&2; }

# containers reach each other by name on this network, nothing is published on the host
net() { $D network create bench >/dev/null 2>&1 || true; }

wait_http() {
	for _ in $(seq 180); do
		$D run --rm --network bench docker.io/library/alpine:3 wget -q -O /dev/null "$1" 2>/dev/null && return
		sleep 1
	done
	say "timed out waiting for $1"
	exit 1
}

random() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

mib() { awk -v b="$1" 'BEGIN { printf "%.1f", b / 1048576 }'; }

memory() {
	total=0
	for c in $CONTAINERS; do
		bytes=$($D stats --no-stream --format '{{.MemUsage}}' "$c" | awk '{
			n = $1 + 0; u = $1; sub(/^[0-9.]+/, "", u)
			k = (u ~ /i/) ? 1024 : 1000
			m = (u ~ /^[Kk]/) ? k : (u ~ /^M/) ? k * k : (u ~ /^G/) ? k * k * k : 1
			printf "%d", n * m }')
		total=$((total + bytes))
	done
	mib "$total"
}

disk() {
	total=0
	for v in $VOLUMES; do
		kib=$($D run --rm -v "$v:/v:ro" docker.io/library/alpine:3 du -sk /v | cut -f1)
		total=$((total + kib * 1024))
	done
	mib "$total"
}

images() {
	total=0
	for i in $IMAGES; do total=$((total + $($D image inspect --format '{{.Size}}' "$i"))); done
	mib "$total"
}

# cpu time of all the tool's containers in microseconds, from their cgroups, linux only
cpu() {
	total=0
	for c in $CONTAINERS; do
		pid=$($D inspect --format '{{.State.Pid}}' "$c")
		group=$(cut -d: -f3 "/proc/$pid/cgroup" 2>/dev/null | head -1)
		usec=$(awk '/^usage_usec/ { print $2 }' "/sys/fs/cgroup$group/cpu.stat" 2>/dev/null)
		total=$((total + ${usec:-0}))
	done
	echo "$total"
}

down() {
	$D rm -f $CONTAINERS >/dev/null 2>&1 || true
	$D volume rm -f $VOLUMES >/dev/null 2>&1 || true
}

net
mkdir -p results
say "building the runner"
$D build -q -t bench-runner runner >/dev/null
cpu=$(awk -F': ' '/model name/ { print $2; exit }' /proc/cpuinfo 2>/dev/null || sysctl -n machdep.cpu.brand_string 2>/dev/null || echo unknown)
memory=$(awk '/MemTotal/ { printf "%d", $2 / 1024 }' /proc/meminfo 2>/dev/null || echo 0)
printf '{"machine":{"cpu":"%s","cores":%s,"memory_mib":%s,"kernel":"%s","engine":"%s"},"events":%s,"concurrency":%s,"runs":%s}\n' \
	"$cpu" "$(getconf _NPROCESSORS_ONLN)" "${memory:-0}" "$(uname -sr)" \
	"$($D --version)" "$EVENTS" "$CONCURRENCY" "$RUNS" >"$OUT"

for target in $TARGETS; do
	. "targets/$target.sh"
	for kind in $KINDS; do
		say "$target, $kind api"
		down
		up "$kind"
		say "idle for ${IDLE}s"
		sleep 10
		before=$(cpu)
		sleep "$IDLE"
		idle_cpu=$(awk -v used="$(($(cpu) - before))" -v span="$IDLE" 'BEGIN { printf "%.2f", used / span / 10000 }')
		idle_memory=$(memory)
		before=$(cpu)
		result=$($D run --rm --network bench bench-runner -kind "$kind" -url "$URL" -host "${HOST:-}" \
			-site bench.test -key "$KEY" -events "$EVENTS" -concurrency "$CONCURRENCY" -runs "$RUNS")
		cpu_seconds=$(awk -v used="$(($(cpu) - before))" 'BEGIN { printf "%.1f", used / 1000000 }')
		printf '%s,"target":"%s","version":"%s","idle_memory_mib":%s,"idle_cpu_percent":%s,"cpu_seconds":%s,"memory_mib":%s,"disk_mib":%s,"image_mib":%s}\n' \
			"${result%\}}" "$target" "$VERSION" "$idle_memory" "$idle_cpu" "$cpu_seconds" "$(memory)" "$(disk)" "$(images)" >>"$OUT"
		down
	done
done

say "results in $OUT"
$D run --rm -v "$PWD/results:/results:ro,z" bench-runner -report "/$OUT"
