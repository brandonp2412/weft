#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

output=docs/screenshots
mkdir -p "$output"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

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
        # XWD can carry an unused alpha channel with zeroes over sixel pixels.
        ffmpeg -nostdin -loglevel error -y -i "$work/window.xwd" -frames:v 1 -pix_fmt rgb24 -threads 1 -update 1 "$work/$1.png"
    }

    capture kitten-preview
    xdotool key --clearmodifiers ctrl+k
    capture room-picker
    if cmp -s "$work/kitten-preview.png" "$work/room-picker.png"; then
        echo "The room picker did not open" >&2
        exit 1
    fi
    xdotool key Escape ctrl+q
' preview "$work"

# Check the theme colors and kitten photo before replacing the published PNGs.
python3 - "$work" <<'PY'
from collections import Counter
import pathlib
import subprocess
import sys

for name, colors in {
    "kitten-preview": ("78DBC2", "EBCB8B", "152223"),
    "room-picker": ("78DBC2", "152223"),
}.items():
    path = pathlib.Path(sys.argv[1]) / f"{name}.png"
    with path.open("rb") as png:
        # PNG IHDR color type 2 explicitly encodes RGB without alpha.
        if png.read(26)[25] != 2:
            sys.exit(f"{name}: expected an opaque RGB PNG")
    rgb = subprocess.check_output([
        "ffmpeg", "-v", "error", "-i", str(path),
        "-f", "rawvideo", "-pix_fmt", "rgb24", "-threads", "1", "-",
    ])
    palette = Counter(zip(rgb[::3], rgb[1::3], rgb[2::3]))
    for color in colors:
        if tuple(bytes.fromhex(color)) not in palette:
            sys.exit(f"{name}: missing expected color #{color}")
    if name == "kitten-preview":
        # The photo adds many shades that cover more pixels than antialiased text.
        photo_colors = sum(30 <= count <= 1200 for count in palette.values())
        if photo_colors < 48:
            sys.exit(f"{name}: kitten photo is missing from the capture")
PY

mv "$work/kitten-preview.png" "$work/room-picker.png" "$output/"
