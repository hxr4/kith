#!/bin/sh
# Builds Kith.app (universal) with the kith CLI inside, and optionally a dmg.
#   sh scripts/bundle.sh [version] [--dmg]
# Output: mac/build/Kith.app (and mac/build/Kith.dmg). Unsigned builds are
# ad-hoc signed so notifications and the login item work locally.
set -eu

cd "$(dirname "$0")/.."
MAC=$(pwd)
ROOT=$(cd .. && pwd)
VERSION=${1:-$(git -C "$ROOT" describe --tags --always 2>/dev/null | sed 's/^v//' || echo 0.0.0)}
DMG=0
for a in "$@"; do [ "$a" = "--dmg" ] && DMG=1; done
ARCHS=${ARCHS:-"arm64 x86_64"}

OUT="$MAC/build"
APP="$OUT/Kith.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Helpers" "$APP/Contents/Resources"

echo "==> Swift app ($ARCHS)"
set --
for a in $ARCHS; do set -- "$@" --arch "$a"; done
swift build -c release "$@" --product Kith
BIN=$(swift build -c release "$@" --product Kith --show-bin-path)
cp "$BIN/Kith" "$APP/Contents/MacOS/Kith"

echo "==> kith CLI $VERSION"
TMP=$(mktemp -d)
LIPO=""
for a in $ARCHS; do
	goarch=$a
	[ "$a" = x86_64 ] && goarch=amd64
	(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=$goarch go build -trimpath \
		-ldflags "-s -w -X kith/internal/config.KithVersion=$VERSION" -o "$TMP/kith-$goarch" ./cmd/kith)
	LIPO="$LIPO $TMP/kith-$goarch"
done
# shellcheck disable=SC2086
lipo -create $LIPO -output "$APP/Contents/Helpers/kith"
rm -rf "$TMP"

cp "$ROOT/gui/build/darwin/icons.icns" "$APP/Contents/Resources/AppIcon.icns"
sed -e "s/@VERSION@/$VERSION/g" "$MAC/Info.plist" > "$APP/Contents/Info.plist"
printf 'APPL????' > "$APP/Contents/PkgInfo"

echo "==> sign (ad-hoc)"
codesign --force --options runtime --timestamp=none -s - "$APP/Contents/Helpers/kith"
codesign --force --options runtime --timestamp=none -s - "$APP"
codesign --verify --strict "$APP"

if [ "$DMG" = 1 ]; then
	echo "==> dmg"
	STAGE=$(mktemp -d)
	cp -R "$APP" "$STAGE/"
	ln -s /Applications "$STAGE/Applications"
	rm -f "$OUT/Kith.dmg"
	hdiutil create -quiet -volname "Kith" -srcfolder "$STAGE" -ov -format UDZO "$OUT/Kith.dmg"
	rm -rf "$STAGE"
fi
echo "built $APP ($VERSION)"
