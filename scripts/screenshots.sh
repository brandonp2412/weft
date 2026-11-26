#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

output=docs/screenshots
mkdir -p "$output"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf 'image_previews = true\n' > "$work/settings.toml"

# Use a private display and explicit preferences, even when run locally.
# Set COLORTERM inside xterm: xterm can override the inherited value.
TZ=UTC LC_ALL=C.UTF-8 xvfb-run -a -s '-screen 0 1600x1000x24' bash -euo pipefail -c '
    work=$1
    xterm -ti vt340 -tn xterm-256color -T "hidden weft preview" -geometry 110x36+0+0 -fa "DejaVu Sans Mono" -fs 13 \
        -b 16 -bg "#171923" -fg "#e5e7eb" +sb \
        -e env -u NO_COLOR -u CLICOLOR_FORCE COLORTERM=truecolor TMPDIR="$work" "$PWD/bin/weft" --config "$work/settings.toml" --demo &
    terminal=$!
    trap '\''kill "$terminal" 2>/dev/null || true; wait "$terminal" 2>/dev/null || true'\'' EXIT

    window=$(timeout 10s xdotool search --sync --onlyvisible --pid "$terminal" --name "^hidden weft preview$")
    xdotool windowfocus --sync "$window"

    capture() {
        sleep 1
        xwd -silent -id "$window" -out "$work/window.xwd"
        ffmpeg -nostdin -loglevel error -y -i "$work/window.xwd" -frames:v 1 -threads 1 -update 1 "$work/$1.png"
    }

    capture conversation
    xdotool key --clearmodifiers ctrl+k
    capture room-picker
    if cmp -s "$work/conversation.png" "$work/room-picker.png"; then
        echo "The room picker did not open" >&2
        exit 1
    fi
    xdotool key Escape ctrl+q
' preview "$work"

# Reject monochrome or reduced-palette screenshots before replacing the previews.
python3 - "$work" <<'PY'
import pathlib
import subprocess
import sys

for name, colors in {
    "conversation": ("78DBC2", "EBCB8B", "152223"),
    "room-picker": ("78DBC2", "152223"),
}.items():
    path = pathlib.Path(sys.argv[1]) / f"{name}.png"
    rgb = subprocess.check_output([
        "ffmpeg", "-v", "error", "-i", str(path),
        "-f", "rawvideo", "-pix_fmt", "rgb24", "-threads", "1", "-",
    ])
    pixels = set(zip(rgb[::3], rgb[1::3], rgb[2::3]))
    for color in colors:
        if tuple(bytes.fromhex(color)) not in pixels:
            sys.exit(f"{name}: missing expected color #{color}")
PY

mv "$work/conversation.png" "$work/room-picker.png" "$output/"
