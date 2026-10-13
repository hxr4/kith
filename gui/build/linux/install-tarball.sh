#!/bin/sh
# Install Kith from the portable archive for the current user
# (~/.local/bin + desktop entry). Debian/Ubuntu/Raspberry Pi OS users can
# use the .deb instead. Needs GTK4 + WebKitGTK 6 for the app; the kith CLI
# has no dependencies (headless machines can use it alone).
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
BIN="${HOME}/.local/bin"
APPS="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
ICONS="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/256x256/apps"
mkdir -p "$BIN" "$APPS" "$ICONS"
install -m 0755 "$here/kith" "$BIN/kith"
install -m 0755 "$here/kith-gui" "$BIN/kith-gui"
install -m 0644 "$here/appicon.png" "$ICONS/kith-gui.png"
sed "s|^Exec=.*|Exec=$BIN/kith-gui|" "$here/kith-gui.desktop" > "$APPS/kith-gui.desktop"
echo "Installed kith and Kith (app) to $BIN."
case ":$PATH:" in *":$BIN:"*) ;; *) echo "Add $BIN to your PATH to use the kith command." ;; esac
echo "Start the background service:  kith daemon install   (headless Pi: it also enables start at boot)"
