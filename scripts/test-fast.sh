#!/usr/bin/env bash
# Run the unit suite like `go test -count=1 ./...`, but split the slow packages
# into shards that run as separate processes at the same time.
#
#   scripts/test-fast.sh            # default shard counts
#   CLI_SHARDS=8 scripts/test-fast.sh
#
# Most internal/cli and internal/reviewtransaction tests set HOME, change
# directory or swap package variables, so they cannot use t.Parallel. Separate
# processes give each shard its own environment instead, with no test changes.
# `go test ./...` stays the reference command; CI runs it unchanged.
set -euo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
module=$(go list -m)

# Tuned on a 10-core Apple silicon machine: 6/3 shards took 137 s, 8/4 took
# 119 s, 10/5 took 116 s. Past 8/4 the git subprocesses saturate the system.
cli_shards=${CLI_SHARDS:-8}
review_shards=${REVIEW_SHARDS:-4}

work=$(mktemp -d "${TMPDIR:-/tmp}/brunnr-test-fast.XXXXXX")
trap 'rm -rf "$work"' EXIT

pids=()
names=()

# run_job NAME COMMAND... runs a command in the background, logging to $work.
run_job() {
	local name=$1
	shift
	(
		start=$(date +%s)
		status=0
		"$@" >"$work/$name.log" 2>&1 || status=$?
		echo "$status $(($(date +%s) - start))" >"$work/$name.result"
	) &
	pids+=("$!")
	names+=("$name")
}

# shard_package PKG SHARDS compiles PKG's test binary once and runs its
# top-level tests round-robin across SHARDS processes.
shard_package() {
	local pkg=$1 shards=$2
	local base
	base=$(basename "$pkg")
	go test -c -o "$work/$base.test" "./$pkg"
	(cd "$root/$pkg" && "$work/$base.test" -test.list '.*') | grep -E '^(Test|Example|Fuzz)' >"$work/$base.list"

	local shard
	for ((shard = 0; shard < shards; shard++)); do
		local pattern
		pattern=$(awk -v n="$shards" -v s="$shard" 'NR % n == s' "$work/$base.list" | paste -sd'|' -)
		[ -n "$pattern" ] || continue
		run_job "$base-$((shard + 1))" \
			bash -c 'cd "$1" && exec "$2" -test.count=1 -test.timeout=30m -test.run "^($3)\$"' \
			_ "$root/$pkg" "$work/$base.test" "$pattern"
	done
}

shard_package internal/cli "$cli_shards"
shard_package internal/reviewtransaction "$review_shards"

rest=()
while IFS= read -r pkg; do
	case $pkg in
	"$module/internal/cli" | "$module/internal/reviewtransaction") ;;
	*) rest+=("$pkg") ;;
	esac
done < <(go list ./...)
run_job rest go test -count=1 "${rest[@]}"

failed=0
for i in "${!pids[@]}"; do
	wait "${pids[$i]}" || true
	read -r status elapsed <"$work/${names[$i]}.result"
	if [ "$status" -eq 0 ]; then
		printf 'ok    %-26s %4ss\n' "${names[$i]}" "$elapsed"
	else
		failed=1
		printf 'FAIL  %-26s %4ss\n' "${names[$i]}" "$elapsed"
		grep -E -- '^(--- FAIL|FAIL|panic:)' "$work/${names[$i]}.log" | head -20 || true
		tail -n 30 "$work/${names[$i]}.log"
	fi
done

if [ "$failed" -ne 0 ]; then
	echo "FAIL"
	exit 1
fi
echo "PASS"
