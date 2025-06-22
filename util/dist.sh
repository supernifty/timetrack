#!/usr/bin/env bash

# go install fyne.io/tools/cmd/fyne@latest
# brew install create-dmg

BUILD=1
ICONS=1

if [[ $BUILD == "1" ]]; then
  echo "$(date) build amd64..."
  rm timetrack-amd64 || true
  CGO_ENABLED=1 GOARCH=amd64 go build -o timetrack-amd64 -ldflags -s
  echo "$(date) build amd64 done"
  rm timetrack-arm64 || true
  GOARCH=arm64 go build -o timetrack-arm64 -ldflags -s
  echo "$(date) build arm64 done"
  rm timetrack-universal || true
  lipo -create -output timetrack-universal timetrack-amd64 timetrack-arm64
  echo "$(date) build fat binary: done"
fi

if [[ $ICONS == "1" ]]; then
  echo "$(date) build iconset..."
  mkdir -p timetrack.iconset
  sips -z 16 16     assets/icon.png --out timetrack.iconset/icon_16x16.png
  sips -z 32 32     assets/icon.png --out timetrack.iconset/icon_16x16@2x.png
  sips -z 32 32     assets/icon.png --out timetrack.iconset/icon_32x32.png
  sips -z 64 64     assets/icon.png --out timetrack.iconset/icon_32x32@2x.png
  sips -z 128 128   assets/icon.png --out timetrack.iconset/icon_128x128.png
  sips -z 256 256   assets/icon.png --out timetrack.iconset/icon_128x128@2x.png
  sips -z 256 256   assets/icon.png --out timetrack.iconset/icon_256x256.png
  sips -z 512 512   assets/icon.png --out timetrack.iconset/icon_256x256@2x.png
  sips -z 512 512   assets/icon.png --out timetrack.iconset/icon_512x512.png
  cp assets/icon.png timetrack.iconset/icon_512x512@2x.png
  echo "$(date) build iconset: done"
fi

# build a distribution
echo "$(date) build distribution..."
rm -r dist || true
mkdir -p dist/timetrack.app/Contents/Resources
mkdir -p dist/timetrack.app/Contents/MacOS
cp assets/Info.plist dist/timetrack.app/Contents/Info.plist
# Usage: iconutil --convert ( icns | iconset) [--output file] file [icon-name]
iconutil --convert icns --output dist/timetrack.app/Contents/Resources/icon.icns timetrack.iconset
cp timetrack-universal dist/timetrack.app/Contents/MacOS/timetrack

# makes the dmg
echo "$(date) build dmg..."
rm timetrack.dmg || true
create-dmg \
  --volname "timetrack installer" \
  --window-pos 200 120 \
  --window-size 500 300 \
  --icon timetrack.app 125 100 \
  --icon-size 100 \
  --app-drop-link 375 100 \
  "dist/timetrack.dmg" \
  "dist/timetrack.app"

echo "$(date) done"
