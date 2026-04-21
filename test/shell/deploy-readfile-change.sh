#!/usr/bin/env -S bash -e

. "$(dirname "$0")/../util.sh"

set -o pipefail

work="$($mktemp --tmpdir -d mgmt-deploy-readfile.XXXXXX)"
output="/tmp/deploy-readfile-output"
client_url="http://127.0.0.1:36979"
server_url="http://127.0.0.1:36980"

cleanup() {
	if [ -n "${pid:-}" ]; then
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	fi
	rm -f "$output"
	rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$work/d1/files" "$work/d2/files"

cat >"$work/d1/main.mcl" <<'EOF'
import "deploy"

file "/tmp/deploy-readfile-output" {
	state => $const.res.file.state.exists,
	content => deploy.readfile("/files/value.txt"),
}
EOF
cp "$work/d1/main.mcl" "$work/d2/main.mcl"

printf 'main: main.mcl\n' >"$work/d1/metadata.yaml"
cp "$work/d1/metadata.yaml" "$work/d2/metadata.yaml"

printf 'alpha' >"$work/d1/files/value.txt"
printf 'bravo' >"$work/d2/files/value.txt"

rm -f "$output"

$TIMEOUT "$MGMT" run \
	--hostname deploy-readfile-test \
	--tmp-prefix \
	--no-pgp \
	--client-urls="$client_url" \
	--server-urls="$server_url" \
	--converged-timeout=-1 \
	empty >"$work/run.log" 2>&1 &
pid=$!

for _ in $(seq 1 30); do
	if curl -fsS "$client_url/health" >/dev/null; then
		break
	fi
	sleep 1
done

curl -fsS "$client_url/health" >/dev/null || fail_test "embedded etcd never became healthy"

"$MGMT" deploy --no-git --seeds="$client_url" lang "$work/d1/" >"$work/deploy1.log" 2>&1

for _ in $(seq 1 60); do
	if [ -f "$output" ] && [ "$(cat "$output")" = "alpha" ]; then
		break
	fi
	sleep 1
done

[ -f "$output" ] || fail_test "first deploy never wrote ${output}"
[ "$(cat "$output")" = "alpha" ] || fail_test "first deploy wrote unexpected content: $(cat "$output")"

"$MGMT" deploy --no-git --seeds="$client_url" lang "$work/d2/" >"$work/deploy2.log" 2>&1

for _ in $(seq 1 90); do
	if [ -f "$output" ] && [ "$(cat "$output")" = "bravo" ]; then
		exit 0
	fi
	sleep 1
done

echo "second deploy never updated ${output}" >&2
echo "=== deploy1 ===" >&2
cat "$work/deploy1.log" >&2
echo "=== deploy2 ===" >&2
cat "$work/deploy2.log" >&2
echo "=== run tail ===" >&2
tail -n 120 "$work/run.log" >&2
exit 1
