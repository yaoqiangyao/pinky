#!/bin/sh
# Finishes a Pinky.app that `mygo build` made (make app), then makes its
# disk image again so the .dmg carries the finished app:
#
#   SIGN=<identity> [ADB=<adb folder>] scripts/macapp.sh <Pinky.app> <ffmpeg prefix> [<disk image>]
#
# - copies FFmpeg's libraries from <ffmpeg prefix>/lib (make ffmpeg) into
#   Contents/Frameworks, named by their major versions as internal/video
#   loads them; each one's install name is @rpath/<name> and its rpath is
#   @loader_path, so they find one another there whoever loads them;
# - puts FFmpeg's license and build notes in Contents/Resources;
# - copies the adb that scripts/adb.sh built into ADB (make adb), when set,
#   to Contents/Helpers/adb, where Pinky looks for its own adb, with its
#   license and build notes in Contents/Resources;
# - checks that the executable needs the macOS that Info.plist names
#   (LSMinimumSystemVersion), and the libraries and adb no newer one;
# - signs the libraries, adb and the app again with SIGN, the identity
#   mygo build signed with ("-", ad hoc, by default; a Developer ID gets
#   the hardened runtime and a timestamp, as mygo build gives it);
# - rebuilds the disk image from the finished app, keeping the Finder
#   window (.DS_Store) and volume icon that mygo build gave it, and signs
#   it with a Developer ID. Notarize the final image yourself.
set -eu

app=${1:?usage: scripts/macapp.sh <Pinky.app> <ffmpeg prefix> [<disk image>]}
ffmpeg=${2:?usage: scripts/macapp.sh <Pinky.app> <ffmpeg prefix> [<disk image>]}
dmg=${3:-}
sign=${SIGN:--}
signflags=""
if [ "$sign" != "-" ]; then
	signflags="--options runtime --timestamp"
fi
contents=$app/Contents
frameworks=$contents/Frameworks
libs="libavutil.61 libswscale.10 libavcodec.63 libavformat.63"

# hdiutil fails with "Resource busy" while Spotlight or an antivirus holds
# an image; try again, as mygo build does. A detach on GitHub's macOS
# runners once failed with "No such file or directory" (Release
# v2026.1108.20): when nothing is mounted there any more, the volume is
# gone and the detach is done; otherwise try again.
hdiutil_retry() {
	n=0
	until out=$(hdiutil "$@" 2>&1); do
		n=$((n + 1))
		case $out in
		*"Resource busy"*) [ $n -lt 5 ] && sleep $((2 << n)) && continue ;;
		*"No such file or directory"*)
			if [ "$1" = detach ] && ! mount | grep -qF " on $2 ("; then
				return 0
			fi
			[ $n -lt 3 ] && sleep $((2 << n)) && continue
			;;
		esac
		echo "hdiutil $*: $out" >&2
		return 1
	done
}

rm -rf "$frameworks"
mkdir -p "$frameworks"
for lib in $libs; do
	src=$ffmpeg/lib/$lib.dylib
	if [ ! -f "$src" ]; then
		echo "macapp: no $src; run make ffmpeg" >&2
		exit 1
	fi
	dst=$frameworks/$lib.dylib
	cp -L "$src" "$dst"
	chmod 755 "$dst"
	install_name_tool -id "@rpath/$lib.dylib" "$dst" 2>/dev/null
	for dep in $(otool -L "$dst" | awk 'NR > 2 { print $1 }'); do
		case $dep in
		@rpath/*) ;;
		*/libav*.dylib | */libsw*.dylib) install_name_tool -change "$dep" "@rpath/$(basename "$dep")" "$dst" 2>/dev/null ;;
		esac
	done
	if ! otool -l "$dst" | grep -q "path @loader_path "; then
		install_name_tool -add_rpath @loader_path "$dst" 2>/dev/null
	fi
done

# Every library must come from the bundle or the system.
bad=$(otool -L "$frameworks"/*.dylib | awk '/^\t/ { print $1 }' | grep -v -e '^@rpath/' -e '^/usr/lib/' -e '^/System/Library/' || true)
if [ -n "$bad" ]; then
	echo "macapp: the FFmpeg libraries need libraries outside the system:" >&2
	echo "$bad" >&2
	exit 1
fi
for f in COPYING.LGPLv2.1 BUILD.txt; do
	cp "$ffmpeg/$f" "$contents/Resources/FFmpeg-$f"
done

helpers=$contents/Helpers
rm -rf "$helpers" "$contents/Resources/adb-NOTICE.txt" "$contents/Resources/adb-BUILD.txt"
if [ -n "${ADB:-}" ]; then
	if [ ! -f "$ADB/adb" ]; then
		echo "macapp: no $ADB/adb; run make adb" >&2
		exit 1
	fi
	mkdir -p "$helpers"
	cp "$ADB/adb" "$helpers/adb"
	chmod 755 "$helpers/adb"
	bad=$(otool -L "$helpers/adb" | awk 'NR > 1 { print $1 }' | grep -v -e '^/usr/lib/' -e '^/System/Library/' || true)
	if [ -n "$bad" ]; then
		echo "macapp: adb needs libraries outside the system:" >&2
		echo "$bad" >&2
		exit 1
	fi
	cp "$ADB/NOTICE.txt" "$contents/Resources/adb-NOTICE.txt"
	cp "$ADB/BUILD.txt" "$contents/Resources/adb-BUILD.txt"
	# shellcheck disable=SC2086 # signflags splits on purpose
	codesign --force $signflags --sign "$sign" "$helpers/adb"
fi

# macOS runs no binary on a system older than the minos of its
# LC_BUILD_VERSION, whatever Info.plist says. The Go linker sets the
# executable's (13.0 in Go 1.27), so LSMinimumSystemVersion
# (macos.minimumSystemVersion in cmd/pinky/mygo.json) must name the same
# version, and no library or helper may need a newer one.
# scripts/release's TestMacOSMinimum checks the toolchain the same way.
minos() {
	otool -l "$1" | awk '$1 == "cmd" { b = ($2 == "LC_BUILD_VERSION") } b && $1 == "minos" { print $2; exit }'
}
vnum() {
	echo "$1" | awk -F. '{ printf "%d", $1 * 10000 + $2 * 100 + $3 }'
}
min=$(/usr/libexec/PlistBuddy -c "Print :LSMinimumSystemVersion" "$contents/Info.plist")
exe=$(/usr/libexec/PlistBuddy -c "Print :CFBundleExecutable" "$contents/Info.plist")
v=$(minos "$contents/MacOS/$exe")
if [ -z "$v" ] || [ "$(vnum "$v")" != "$(vnum "$min")" ]; then
	echo "macapp: $exe needs macOS ${v:-?}, but Info.plist says $min (macos.minimumSystemVersion in cmd/pinky/mygo.json)" >&2
	exit 1
fi
for f in "$frameworks"/*.dylib ${ADB:+"$helpers/adb"}; do
	v=$(minos "$f")
	if [ -z "$v" ] || [ "$(vnum "$v")" -gt "$(vnum "$min")" ]; then
		echo "macapp: $(basename "$f") needs macOS ${v:-?}, newer than the app's $min" >&2
		exit 1
	fi
done

# shellcheck disable=SC2086 # signflags splits on purpose
codesign --force $signflags --sign "$sign" "$frameworks"/*.dylib
# shellcheck disable=SC2086
codesign --force --deep $signflags --sign "$sign" "$app"
codesign --verify --deep --strict "$app"
if [ -n "${ADB:-}" ]; then
	echo "added FFmpeg's libraries and adb to $app"
else
	echo "added FFmpeg's libraries to $app"
fi

[ -n "$dmg" ] || exit 0
if [ ! -f "$dmg" ]; then
	echo "macapp: no disk image $dmg" >&2
	exit 1
fi
work=$(mktemp -d "$(dirname "$dmg")/.dmg-XXXXXX")
cleanup() {
	hdiutil detach -force "$work/mnt" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
mkdir "$work/src" "$work/mnt"

hdiutil_retry attach -readonly -noverify -noautoopen -nobrowse -mountpoint "$work/mnt" "$dmg"
volume=$(diskutil info "$work/mnt" | sed -n 's/^ *Volume Name: *//p')
for f in .DS_Store .VolumeIcon.icns; do
	if [ -f "$work/mnt/$f" ]; then
		cp "$work/mnt/$f" "$work/src/$f"
	fi
done
hdiutil_retry detach "$work/mnt"

ditto --noclone "$app" "$work/src/$(basename "$app")"
ln -s /Applications "$work/src/Applications"
hdiutil_retry create -ov -volname "${volume:-Pinky}" -srcfolder "$work/src" \
	-fs HFS+ -fsargs "-c c=64,a=16,e=16" -format UDRW "$work/rw.dmg"
hdiutil_retry attach -readwrite -noverify -noautoopen -nobrowse -mountpoint "$work/mnt" "$work/rw.dmg"
if [ -f "$work/src/.VolumeIcon.icns" ]; then
	# The Finder's "has a custom icon" flag on the volume (FinderInfo
	# offset 8).
	xattr -wx com.apple.FinderInfo "0000000000000000040000000000000000000000000000000000000000000000" "$work/mnt"
fi
rm -rf "$work/mnt/.fseventsd" "$work/mnt/.Trashes"
hdiutil_retry detach "$work/mnt"

rm -f "$dmg"
# macOS 27's hdiutil convert fails with "Resource temporarily unavailable"
# and points to diskutil, which older systems lack.
if diskutil image create from -h >/dev/null 2>&1; then
	if ! out=$(diskutil image create from --format ULMO "$work/rw.dmg" "$dmg" 2>&1); then
		echo "diskutil image create from: $out" >&2
		exit 1
	fi
else
	hdiutil_retry convert "$work/rw.dmg" -format ULMO -ov -o "$dmg"
fi
if [ "$sign" != "-" ]; then
	codesign --force --timestamp --sign "$sign" "$dmg"
fi
echo "made $dmg again from the finished app"
