#!/bin/sh
#
# Build the simplewan + luci-app-simplewan packages with an OpenWrt SDK and
# assemble a single opkg feed directory.
#
# Usage:
#   scripts/build-feed.sh /path/to/openwrt-sdk [output-dir]
#
# Then sign the index (see README) and publish the output dir.
#
set -eu

SDK="${1:?usage: build-feed.sh /path/to/openwrt-sdk [output-dir]}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${2:-$REPO_ROOT/public}"

SHA="$(cd "$REPO_ROOT" && git rev-parse HEAD)"

cp -r "$REPO_ROOT/openwrt/simplewan" "$SDK/package/simplewan"
cp -r "$REPO_ROOT/luci-app-simplewan" "$SDK/package/luci-app-simplewan"

cd "$SDK"
./scripts/feeds update -a
./scripts/feeds install golang luci-base

# Build the exact committed revision.
sed -i.bak "s/^PKG_SOURCE_VERSION:=.*/PKG_SOURCE_VERSION:=$SHA/" package/simplewan/Makefile

make defconfig
make package/simplewan/compile V=s
make package/luci-app-simplewan/compile V=s

mkdir -p "$OUT"
rm -f "$OUT"/*.ipk
for p in simplewan luci-app-simplewan; do
	find bin/packages -name "${p}_*.ipk" -exec cp {} "$OUT/" \;
done

# ipkg-make-index.sh hashes packages via $MKHASH (normally exported by make).
export MKHASH="$SDK/staging_dir/host/bin/mkhash"
( cd "$OUT" && "$SDK/scripts/ipkg-make-index.sh" . > Packages && gzip -fk Packages )

echo
echo "Feed assembled in: $OUT"
echo "Sign it with:"
echo "  usign -S -m \"$OUT/Packages\" -s <path-to-secret> -x \"$OUT/Packages.sig\""
echo "and copy feed/simplewan-feed.pub alongside the packages."
