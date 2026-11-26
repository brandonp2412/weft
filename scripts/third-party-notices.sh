#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

go_cmd=${GO:-go}
tags=${TAGS:-goolm}
binary=$1
go_root=$("$go_cmd" env GOROOT)

print_notices() {
    local directory=$1
    local file
    for file in "$directory"/LICENSE* "$directory"/COPYING* "$directory"/NOTICE* "$directory"/PATENTS; do
        if [[ -f "$file" ]]; then
            printf '\n--- %s ---\n' "${file##*/}"
            cat "$file"
            printf '\n'
        fi
    done
}

printf 'Third-party notices for Weft\n'
printf '\nGo standard library (%s)\n' "$("$go_cmd" env GOVERSION)"
go_license_dir=$go_root
if [[ ! -f "$go_license_dir/LICENSE" && -f /usr/share/licenses/go/LICENSE ]]; then
    go_license_dir=/usr/share/licenses/go
fi
if [[ ! -f "$go_license_dir/LICENSE" ]]; then
    printf 'Could not find the Go standard library license\n' >&2
    exit 1
fi
print_notices "$go_license_dir"

vendor_dirs=$("$go_cmd" list -tags "$tags" -deps -f '{{if .Standard}}{{.Dir}}{{end}}' ./cmd/weft |
    sed -n 's|^.*/src/vendor/golang\.org/x/\([^/]*\)/.*|\1|p' |
    sort -u)
while read -r dependency; do
    [[ -n "$dependency" ]] || continue
    printf '\nGo standard library vendor copy of golang.org/x/%s\n' "$dependency"
    print_notices "$go_root/src/vendor/golang.org/x/$dependency"
done <<< "$vendor_dirs"

modules=$("$go_cmd" version -m "$binary" | awk '$1 == "dep" {print $2, $3}')
while read -r module version; do
    [[ -n "$module" ]] || continue
    directory=$("$go_cmd" list -m -f '{{.Dir}}' "$module")
    printf '\n%s %s\n' "$module" "$version"
    case "$module" in
        maunium.net/go/mautrix|go.mau.fi/util)
            printf 'MPL 2.0 corresponding source: https://proxy.golang.org/%s/@v/%s.zip\n' "$module" "$version"
            ;;
    esac
    print_notices "$directory"
done <<< "$modules"
