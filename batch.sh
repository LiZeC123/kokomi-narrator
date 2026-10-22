#!/usr/bin/env bash
# Convert every .txt file in a directory with ttsctl.
#
# Flow per file:
#   add (or update if already registered) -> make -> merge
#
# A failed file is skipped and the next file continues. The exit code is 1
# if at least one file failed, otherwise 0.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$SCRIPT_DIR"

usage() {
	cat <<'EOF'
Usage: batch.sh [-c config.yaml] [directory]

Convert every .txt file in directory (default: current directory) with:
  add (or update if already registered) -> make -> merge

Options:
  -c, --config PATH  config file. Default: <directory>/config.yaml, then
                     the project's config.yaml
  -h, --help         show this help

Environment:
  TTSCTL_BIN         path to the ttsctl binary

Relative paths inside config.yaml are resolved from the target directory.
Failed files are skipped; processing continues with the next file.
Exit code is 1 if any file failed, 0 otherwise.
EOF
}

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

config_arg=""
while [[ $# -gt 0 ]]; do
	case "$1" in
		-c | --config)
			[[ $# -ge 2 ]] || die "$1 requires a value"
			config_arg="$2"
			shift 2
			;;
		-h | --help)
			usage
			exit 0
			;;
		--)
			shift
			break
			;;
		-*)
			die "unknown option: $1"
			;;
		*)
			break
			;;
	esac
done

if [[ $# -gt 1 ]]; then
	usage >&2
	exit 1
fi

workdir="${1:-.}"
[[ -d "$workdir" ]] || die "not a directory: $workdir"
workdir="$(cd "$workdir" && pwd)" || die "cannot enter $workdir"

ttsctl_bin="${TTSCTL_BIN:-}"
if [[ -z "$ttsctl_bin" ]]; then
	if [[ -x "$PROJECT_ROOT/ttsctl" ]]; then
		ttsctl_bin="$PROJECT_ROOT/ttsctl"
	elif [[ -x "$PROJECT_ROOT/kokomi-narrator" ]]; then
		ttsctl_bin="$PROJECT_ROOT/kokomi-narrator"
	elif command -v ttsctl >/dev/null 2>&1; then
		ttsctl_bin="$(command -v ttsctl)"
	else
		die "ttsctl binary not found; build it or set TTSCTL_BIN"
	fi
fi
if [[ "$ttsctl_bin" != /* ]]; then
	if [[ "$ttsctl_bin" == */* ]]; then
		ttsctl_bin="$(cd "$(dirname "$ttsctl_bin")" && pwd)/$(basename "$ttsctl_bin")" ||
			die "cannot resolve ttsctl path: $ttsctl_bin"
	else
		ttsctl_bin="$(command -v "$ttsctl_bin")" ||
			die "ttsctl binary not found: $ttsctl_bin"
	fi
fi
[[ -x "$ttsctl_bin" ]] || die "ttsctl binary is not executable: $ttsctl_bin"

if [[ -n "$config_arg" ]]; then
	if [[ "$config_arg" != /* ]]; then
		config_arg="$(cd "$(dirname "$config_arg")" && pwd)/$(basename "$config_arg")" ||
			die "cannot resolve config path: $config_arg"
	fi
	config="$config_arg"
elif [[ -f "$workdir/config.yaml" ]]; then
	config="$workdir/config.yaml"
elif [[ -f "$PROJECT_ROOT/config.yaml" ]]; then
	config="$PROJECT_ROOT/config.yaml"
else
	die "config.yaml not found; use -c or place one in $workdir"
fi
[[ -f "$config" ]] || die "config file not found: $config"

echo "workdir: $workdir"
echo "config:  $config"
echo "ttsctl:  $ttsctl_bin"
echo

shopt -s nullglob nocaseglob
paths=("$workdir"/*.txt)
files=()
if [[ ${#paths[@]} -gt 0 ]]; then
	for path in "${paths[@]}"; do
		[[ -f "$path" ]] && files+=("$path")
	done
fi

if [[ ${#files[@]} -eq 0 ]]; then
	echo "no .txt files found in $workdir"
	exit 0
fi

# ttsctl only accepts plain filenames in the current directory.
cd "$workdir" || die "cannot enter $workdir"

total=${#files[@]}
ok=0
failed=0

for path in "${files[@]}"; do
	base="${path##*/}"
	echo "===== [$((ok + failed + 1))/$total] $base ====="

	# A new file is added; an existing file fails add and is updated instead.
	add_out=$("$ttsctl_bin" --config "$config" add -- "$base" 2>&1)
	add_rc=$?
	if [[ $add_rc -eq 0 ]]; then
		[[ -n "$add_out" ]] && printf '%s\n' "$add_out"
	else
		[[ $add_rc -eq 130 ]] && { echo "interrupted" >&2; exit 130; }
		# Suppress the expected "already added" error and try update.
		update_out=$("$ttsctl_bin" --config "$config" update -- "$base" 2>&1)
		update_rc=$?
		if [[ $update_rc -ne 0 ]]; then
			[[ $update_rc -eq 130 ]] && { echo "interrupted" >&2; exit 130; }
			[[ -n "$add_out" ]] && printf '%s\n' "$add_out" >&2
			[[ -n "$update_out" ]] && printf '%s\n' "$update_out" >&2
			echo "SKIP $base: add/update failed" >&2
			failed=$((failed + 1))
			echo
			continue
		fi
		[[ -n "$update_out" ]] && printf '%s\n' "$update_out"
	fi

	"$ttsctl_bin" --config "$config" make -- "$base"
	make_rc=$?
	if [[ $make_rc -ne 0 ]]; then
		[[ $make_rc -eq 130 ]] && { echo "interrupted" >&2; exit 130; }
		echo "SKIP $base: make failed" >&2
		failed=$((failed + 1))
		echo
		continue
	fi

	"$ttsctl_bin" --config "$config" merge -- "$base"
	merge_rc=$?
	if [[ $merge_rc -ne 0 ]]; then
		[[ $merge_rc -eq 130 ]] && { echo "interrupted" >&2; exit 130; }
		echo "SKIP $base: merge failed" >&2
		failed=$((failed + 1))
		echo
		continue
	fi

	ok=$((ok + 1))
	echo "OK $base"
	echo
done

echo "Batch finished: $ok succeeded, $failed failed, $total total"
if [[ $failed -gt 0 ]]; then
	exit 1
fi
