#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
COMPOSE=$ROOT/deploy/compose/compose.yaml
HT_STATE=${HT_STATE:-$HOME/.local/state/host-telemetry}
export HT_STATE

usage() {
	echo "usage: tools/stack.sh {start|stop|status|grafana}"
	exit 2
}

wait_url() {
	i=0
	while [ "$i" -lt 60 ]; do
		if curl -fsS -o /dev/null "$1" 2>/dev/null; then
			return 0
		fi
		sleep 1
		i=$((i + 1))
	done
	return 1
}

fail() {
	docker compose -f "$COMPOSE" ps || true
	docker compose -f "$COMPOSE" logs --tail 20 victoriametrics victorialogs || true
	exit 1
}

start() {
	mkdir -p "$HT_STATE/m5/vmdata" "$HT_STATE/m5/vlogsdata" "$HT_STATE/m5/grafana"
	docker compose -f "$COMPOSE" up -d
	wait_url http://127.0.0.1:18428/health || fail
	wait_url http://127.0.0.1:19428/health || fail
	echo "victoriametrics http://127.0.0.1:18428"
	echo "victorialogs http://127.0.0.1:19428"
}

stop() {
	docker compose -f "$COMPOSE" --profile grafana down --remove-orphans
	containers=$(docker ps -a --filter label=com.docker.compose.project=ht-m5 -q)
	if [ -n "$containers" ]; then
		echo "containers still present: $containers" >&2
		exit 1
	fi
	networks=$(docker network ls --filter name=ht-m5 -q)
	if [ -n "$networks" ]; then
		echo "networks still present: $networks" >&2
		exit 1
	fi
	echo clean
}

status() {
	docker compose -f "$COMPOSE" --profile grafana ps
	printf 'victoriametrics '
	curl -fsS http://127.0.0.1:18428/health 2>/dev/null || printf 'unreachable'
	printf '\n'
	printf 'victorialogs '
	curl -fsS http://127.0.0.1:19428/health 2>/dev/null || printf 'unreachable'
	printf '\n'
}

grafana() {
	docker compose -f "$COMPOSE" up -d victoriametrics
	docker compose -f "$COMPOSE" --profile grafana up -d grafana
	if ! wait_url http://127.0.0.1:3000/api/health; then
		docker compose -f "$COMPOSE" --profile grafana ps || true
		docker compose -f "$COMPOSE" --profile grafana logs --tail 20 grafana || true
		exit 1
	fi
	curl -fsS http://127.0.0.1:3000/api/health
	printf '\n'
}

[ "$#" -eq 1 ] || usage

case $1 in
start) start ;;
stop) stop ;;
status) status ;;
grafana) grafana ;;
*) usage ;;
esac
