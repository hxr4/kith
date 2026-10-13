#!/bin/sh
# Stamp a release version into the packaging metadata without regenerating
# it (`wails3 update build-assets` would overwrite Kith's customizations).
# Usage: build/set-version.sh 1.2.3
set -eu
v="$1"
cd "$(dirname "$0")"
perl -pi -e "s/^(\s*version:\s*)\"[^\"]*\"/\${1}\"$v\"/" config.yml linux/nfpm/nfpm.yaml
perl -0pi -e "s|(<key>CFBundleShortVersionString</key>\s*<string>)[^<]*|\${1}$v|; s|(<key>CFBundleVersion</key>\s*<string>)[^<]*|\${1}$v|" darwin/Info.plist darwin/Info.dev.plist
perl -pi -e "s/(\"(?:file_version|ProductVersion)\":\s*\")[^\"]*/\${1}$v/" windows/info.json
perl -pi -e "s/(!define INFO_PRODUCTVERSION \")[^\"]*/\${1}$v/" windows/nsis/wails_tools.nsh
echo "version set to $v"
