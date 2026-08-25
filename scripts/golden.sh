#!/bin/bash
# Snapshot and compare normalized gallery state across scraper changes.
set -euo pipefail

usage() { echo "usage: scripts/golden.sh save|diff <name>"; exit 2; }

[ $# -eq 2 ] || usage
mode=$1
name=$2
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/.golden/$name/state.txt"

state() {
	local export_dir
	export_dir=$(mktemp -d)
	trap 'rm -rf "$export_dir"' RETURN
	(cd "$root" && go run ./cmd/parser export-json -out "$export_dir") >/dev/null

	cd "$root/downloads" || return 0
	shopt -s nullglob
	for d in */*/; do
		t=${d%/}
		echo "== $t"
		if [ -f "$export_dir/downloads/$t/posts.json" ]; then
			python3 -c '
import json,sys
posts=json.load(open(sys.argv[1]))
print("\n".join(sorted(str(p.get("tweet_id","")) for p in posts)))
' "$export_dir/downloads/$t/posts.json"
		fi
		(cd "$t" && find . -maxdepth 1 -type f ! -name "*.bak" ! -name .DS_Store -print | sed 's|^\./||' | sort)
	done
}

case $mode in
save)
	mkdir -p "$(dirname "$out")"
	state > "$out"
	echo "saved $out"
	;;
diff)
	diff -u "$out" <(state) && echo "no differences"
	;;
*)
	usage
	;;
esac
