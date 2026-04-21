#!/usr/bin/env -S bash -e

. "$(dirname "$0")/../util.sh"

set -o pipefail

OUT="/tmp/sendrecv-runtime-output"
VALUE="updated-from-flag"
MCL="$(dirname "$0")/sendrecv-runtime.mcl"

rm -f "$OUT"

$TIMEOUT "$MGMT" run --no-network --converged-timeout=15 --tmp-prefix lang "$MCL" &
pid=$!

for _ in $(seq 1 20); do
	if curl -fsS --retry 1 --retry-connrefused --retry-delay 1 -X POST --data "value=${VALUE}" http://127.0.0.1:18080/flag >/dev/null; then
		break
	fi
	sleep 1
done

for _ in $(seq 1 20); do
	if [ -f "$OUT" ] && [ "$(cat "$OUT")" = "$VALUE" ]; then
		wait $pid
		exit $?
	fi
	sleep 1
done

echo "send/recv runtime update never reached ${OUT}" >&2
wait $pid
