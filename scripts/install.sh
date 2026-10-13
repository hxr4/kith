#!/bin/sh
# Install (or update / remove) the kith CLI for the current user on macOS.
#   scripts/install.sh              build from source and install to ~/.local/bin
#   scripts/install.sh --uninstall  remove the binary, completion and zshrc block
# Env: KITH_PREFIX (default ~/.local)
set -eu

PREFIX="${KITH_PREFIX:-$HOME/.local}"
BIN="$PREFIX/bin"
ZFUNC="$PREFIX/share/zsh/site-functions"
ZSHRC="${ZDOTDIR:-$HOME}/.zshrc"
BEGIN='# >>> kith >>>'
END='# <<< kith <<<'

cd "$(dirname "$0")/.."

remove_block() {
	[ -f "$ZSHRC" ] || return 0
	if grep -q "$BEGIN" "$ZSHRC"; then
		sed -i '' "/$BEGIN/,/$END/d" "$ZSHRC"
	fi
}

if [ "${1:-}" = "--uninstall" ]; then
	if [ -x "$BIN/kith" ] && "$BIN/kith" daemon status 2>/dev/null | grep -q "service: installed"; then
		echo "Note: the kith LaunchAgent is still installed; run 'kith daemon uninstall' first to remove it."
	fi
	rm -f "$BIN/kith" "$ZFUNC/_kith"
	remove_block
	echo "Removed kith from $BIN (your identity, peers and tasks in ~/.kith are kept)."
	exit 0
fi

command -v go >/dev/null 2>&1 || { echo "Go is required to build kith (brew install go)." >&2; exit 1; }

mkdir -p "$BIN" "$ZFUNC"
echo "Building kith..."
CGO_ENABLED=0 go build -trimpath -o "$BIN/kith.new" ./cmd/kith
mv -f "$BIN/kith.new" "$BIN/kith"

# zsh completion (commands, flags, task ids, peer names).
"$BIN/kith" completion zsh > "$ZFUNC/_kith"
if [ -f "$ZSHRC" ] && grep -q "$BEGIN" "$ZSHRC"; then
	: # already configured
else
	cat >> "$ZSHRC" <<ZRC

$BEGIN
# kith CLI: PATH and tab completion (remove with scripts/install.sh --uninstall)
case ":\$PATH:" in *":$BIN:"*) ;; *) export PATH="$BIN:\$PATH" ;; esac
fpath=("$ZFUNC" \$fpath)
autoload -Uz compinit && compinit -i
$END
ZRC
	echo "Added a kith block to $ZSHRC (PATH + completion)."
fi

echo "Installed $("$BIN/kith" --version) → $BIN/kith"
if "$BIN/kith" daemon status 2>/dev/null | grep -q "daemon:  running"; then
	echo "A daemon is running on the previous binary; apply the update with: kith daemon restart"
fi
case ":$PATH:" in
	*":$BIN:"*) echo "Ready: try 'kith status'. (Open a new terminal or run 'exec zsh' for tab completion.)" ;;
	*) echo "Open a new terminal (or run 'exec zsh'), then try 'kith status'." ;;
esac
