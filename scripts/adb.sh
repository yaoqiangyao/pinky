#!/bin/sh
# Builds the adb that Pinky ships (scripts/dist.sh), from source, so that
# people need not install Android's platform tools: AOSP's adb as
# android-tools (https://github.com/nmeum/android-tools) packages it, the
# same version as Google's platform-tools, without fastboot and the rest,
# and linked statically against its libraries, so that it needs only the
# system's own. scripts/adb/CMakeLists.txt is the build.
#
#   scripts/adb.sh <prefix> [arm64|amd64|windows-amd64|linux-amd64]
#
# Without a second argument it builds for this Mac's processor.
#
# - arm64, amd64: macOS 12 and later (Pinky.app's minimum), with the
#   system's zlib; a Mac with Apple silicon builds the Intel adb too.
# - windows-amd64 (Pinky's Windows package): a 32-bit adb.exe, as Google's
#   is, cross-compiled with Ubuntu 24.04's mingw-w64 in a Docker container.
#   For USB it loads AOSP's AdbWinApi.dll and AdbWinUsbApi.dll, which go
#   next to it: the 32-bit ones Google's platform-tools carry, from AOSP's
#   platform/development at android-17.0.0_r1 (Apache-2.0), which AOSP
#   builds for 32-bit Windows only.
# - linux-amd64: a static executable built with musl in an Alpine
#   container, which runs on any x86-64 Linux.
#
# <prefix> gets adb (adb.exe and the two DLLs on Windows), NOTICE.txt (the
# licenses of everything in it) and BUILD.txt (what it was built from).
# The sources are checked against their SHA-256 and kept in adb-sources,
# next to <prefix>. Building needs CMake 3.22 or later (brew install
# cmake) on macOS, and Docker for Windows and Linux.
set -eu

# android-tools 37.0.0p1 is AOSP's android-17.0.0_r1, Google's
# platform-tools 37.0.0.
ANDROID_TOOLS_VERSION=37.0.0p1
ANDROID_TOOLS_SHA256=2c6de1abe16b211c7d9f59276217e14fd9e0e18b20e331d3c84358f5dffd576e
ANDROID_TOOLS_URL=https://github.com/nmeum/android-tools/releases/download/$ANDROID_TOOLS_VERSION/android-tools-$ANDROID_TOOLS_VERSION.tar.xz

# The libraries adb links, besides BoringSSL, fmt and libusb, which
# android-tools carries. protobuf 3.21 is the last one without Abseil.
PROTOBUF_VERSION=3.21.12
PROTOBUF_SHA256=4eab9b524aa5913c6fffb20b2a8abf5ef7f95a80bc0701f3a6dbb4c607f73460
PROTOBUF_URL=https://github.com/protocolbuffers/protobuf/releases/download/v21.12/protobuf-cpp-$PROTOBUF_VERSION.tar.gz
BROTLI_VERSION=1.2.1
BROTLI_SHA256=98d35d11a78dc453aebb7d10c3748f3f73ff78c8267c0dc697b8631f67bf7e8f
BROTLI_URL=https://github.com/google/brotli/archive/refs/tags/v$BROTLI_VERSION.tar.gz
LZ4_VERSION=1.10.0
LZ4_SHA256=537512904744b35e232912055ccf8ec66d768639ff3abe5788d90d792ec5f48b
LZ4_URL=https://github.com/lz4/lz4/releases/download/v$LZ4_VERSION/lz4-$LZ4_VERSION.tar.gz
ZSTD_VERSION=1.5.7
ZSTD_SHA256=eb33e51f49a15e023950cd7825ca74a4a2b43db8354825ac24fc1b7ee09e6fa3
ZSTD_URL=https://github.com/facebook/zstd/releases/download/v$ZSTD_VERSION/zstd-$ZSTD_VERSION.tar.gz
ZLIB_VERSION=1.3.2
ZLIB_SHA256=d7a0654783a4da529d1bb793b7ad9c3318020af77667bcae35f95d0e42a792f3
ZLIB_URL=https://github.com/madler/zlib/releases/download/v$ZLIB_VERSION/zlib-$ZLIB_VERSION.tar.xz

# Windows: AOSP's USB libraries and their header, from the git repository
# (gitiles serves a file's bytes in base64 with ?format=TEXT).
AOSP_DEVELOPMENT=https://android.googlesource.com/platform/development/+/refs/tags/android-17.0.0_r1
ADBWINAPI_DLL_SHA256=d60103a5e99bc9888f786ee916f5d6e45493c3247972cb053833803de7e95cf9
ADBWINUSBAPI_DLL_SHA256=25207c506d29c4e8dceb61b4bd50e8669ba26012988a43fbf26a890b1e60fc97
ADBWINAPI_DEF_SHA256=99a718cb83509a19540411e764d88499de48246f3f0709dfac79871e2023db00
ADB_API_H_SHA256=11b9d11e6b0e7725ade38353cbac431376c8017f368229d5437e8d492aa24f16

usage="usage: scripts/adb.sh <prefix> [arm64|amd64|windows-amd64|linux-amd64]"
prefix=${1:?$usage}
target=${2:-}
case $prefix in /*) ;; *) prefix=$(pwd)/$prefix ;; esac
here=$(cd "$(dirname "$0")" && pwd)
sources=$(dirname "$prefix")/adb-sources

sha256() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		sha256sum "$1" | cut -d' ' -f1
	fi
}

# gh_fetch downloads a file of a GitHub release through GitHub's API, with
# gh (signed in, or GH_TOKEN), for when github.com's release downloads fail:
# they answered 504 for hours on 9 November 2026 (Release v2026.1109.13).
gh_fetch() {
	case $1 in
	https://github.com/*/*/releases/download/*/*) ;;
	*) return 1 ;;
	esac
	command -v gh >/dev/null 2>&1 || return 1
	path=${1#https://github.com/}
	rest=${path#*/releases/download/}
	echo "Downloading ${rest#*/} through GitHub's API"
	gh release download "${rest%%/*}" --repo "${path%%/releases/download/*}" \
		--pattern "${rest#*/}" --output "$2" --clobber
}

# fetch downloads a file, unless it is there with the right SHA-256. As in
# scripts/ffmpeg.sh, sources are downloaded here, before any container,
# which mounts the folder that holds them. aosp=1 decodes gitiles' base64.
fetch() {
	url=$1 want=$2 file=$3 aosp=${4:-}
	if [ -f "$file" ] && [ "$(sha256 "$file")" = "$want" ]; then
		return
	fi
	echo "Downloading $url"
	tries=0
	until curl -fsSL --retry 3 -o "$file.part" "$url" || gh_fetch "$url" "$file.part"; do
		tries=$((tries + 1))
		if [ $tries -ge 5 ]; then
			echo "adb.sh: could not download $url" >&2
			rm -f "$file.part"
			exit 1
		fi
		sleep $((tries * 5))
	done
	if [ -n "$aosp" ]; then
		base64 -d <"$file.part" >"$file.raw" 2>/dev/null || openssl base64 -d -A <"$file.part" >"$file.raw"
		mv "$file.raw" "$file.part"
	fi
	got=$(sha256 "$file.part")
	if [ "$got" != "$want" ]; then
		echo "adb.sh: ${file##*/} has SHA-256 $got, want $want" >&2
		rm -f "$file.part"
		exit 1
	fi
	mv "$file.part" "$file"
}

mkdir -p "$sources"
fetch "$ANDROID_TOOLS_URL" "$ANDROID_TOOLS_SHA256" "$sources/android-tools-$ANDROID_TOOLS_VERSION.tar.xz"
fetch "$PROTOBUF_URL" "$PROTOBUF_SHA256" "$sources/protobuf-cpp-$PROTOBUF_VERSION.tar.gz"
fetch "$BROTLI_URL" "$BROTLI_SHA256" "$sources/brotli-$BROTLI_VERSION.tar.gz"
fetch "$LZ4_URL" "$LZ4_SHA256" "$sources/lz4-$LZ4_VERSION.tar.gz"
fetch "$ZSTD_URL" "$ZSTD_SHA256" "$sources/zstd-$ZSTD_VERSION.tar.gz"
fetch "$ZLIB_URL" "$ZLIB_SHA256" "$sources/zlib-$ZLIB_VERSION.tar.xz"
if [ "$target" = windows-amd64 ]; then
	fetch "$AOSP_DEVELOPMENT/host/windows/prebuilt/usb/AdbWinApi.dll?format=TEXT" "$ADBWINAPI_DLL_SHA256" "$sources/AdbWinApi.dll" 1
	fetch "$AOSP_DEVELOPMENT/host/windows/prebuilt/usb/AdbWinUsbApi.dll?format=TEXT" "$ADBWINUSBAPI_DLL_SHA256" "$sources/AdbWinUsbApi.dll" 1
	fetch "$AOSP_DEVELOPMENT/host/windows/prebuilt/usb/AdbWinApi.def?format=TEXT" "$ADBWINAPI_DEF_SHA256" "$sources/AdbWinApi.def" 1
	fetch "$AOSP_DEVELOPMENT/host/windows/usb/api/adb_api.h?format=TEXT" "$ADB_API_H_SHA256" "$sources/adb_api.h" 1
fi

# Windows and Linux: run this script again in a container, unless it is
# running in one. The images and packages are here, so that the hash of
# scripts/adb.sh and scripts/adb/, which names the builds that
# scripts/dist.sh and the release workflow keep, changes with them.
case $target in
windows-amd64 | linux-amd64)
	if [ -z "${ADB_IN_CONTAINER:-}" ]; then
		case $target in
		windows-amd64)
			image=ubuntu:24.04
			install="apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends ca-certificates xz-utils patch cmake ninja-build g++ nasm g++-mingw-w64-i686-posix gcc-mingw-w64-i686-posix binutils-mingw-w64-i686 mingw-w64-i686-dev >/dev/null"
			;;
		linux-amd64)
			image=alpine:3.22
			install="apk add -q build-base linux-headers cmake samurai patch xz"
			;;
		esac
		if ! docker info >/dev/null 2>&1; then
			echo "adb.sh: adb for $target is built in a Docker container, and Docker is not running here" >&2
			exit 1
		fi
		exec docker run --rm --platform linux/amd64 \
			-e ADB_IN_CONTAINER=1 -e INSTALL="$install" -e TARGET="$target" \
			-e OUT="/out/$(basename "$prefix")" -e OWNER="$(id -u):$(id -g)" \
			-v "$here/adb.sh:/pinky/adb.sh:ro" -v "$here/adb:/pinky/adb:ro" -v "$(dirname "$prefix"):/out" \
			"$image" sh -c '
				set -e
				sh -c "$INSTALL"
				status=0
				sh /pinky/adb.sh "$OUT" "$TARGET" || status=$?
				chown -R "$OWNER" "$OUT" "$OUT-work" 2>/dev/null || true
				exit $status'
	fi
	;;
esac

if ! command -v cmake >/dev/null 2>&1; then
	echo "adb.sh: building adb needs CMake 3.22 or later (brew install cmake)" >&2
	exit 1
fi
if command -v ninja >/dev/null 2>&1; then
	generator=Ninja
else
	generator="Unix Makefiles"
fi

# What the target is: the CMake flags of a build for it, and whether this
# machine runs its programs (else protoc is built once more, for here).
case $target in
windows-amd64)
	# 32-bit, as AdbWinApi.dll is, with SSE2 (which BoringSSL's assembly
	# needs, and every processor Windows 8 or later runs on has).
	cross=i686-w64-mingw32-
	flags="-DCMAKE_SYSTEM_NAME=Windows -DCMAKE_SYSTEM_PROCESSOR=x86
	-DCMAKE_C_FLAGS=-msse2 -DCMAKE_CXX_FLAGS=-msse2
	-DCMAKE_C_COMPILER=${cross}gcc-posix -DCMAKE_CXX_COMPILER=${cross}g++-posix
	-DCMAKE_RC_COMPILER=${cross}windres -DCMAKE_DLLTOOL=${cross}dlltool
	-DCMAKE_AR=$(command -v ${cross}ar) -DCMAKE_RANLIB=$(command -v ${cross}ranlib)
	-DCMAKE_ASM_NASM_COMPILER=nasm
	-DCMAKE_FIND_ROOT_PATH_MODE_PROGRAM=NEVER"
	native=
	what="Windows, 32-bit (x86), as Google's adb.exe"
	exe=adb.exe
	jobs=$(nproc)
	;;
linux-amd64)
	flags=
	native=1
	what="Linux x86_64, static (musl)"
	exe=adb
	jobs=$(nproc)
	;;
*)
	if [ "$(uname -s)" != Darwin ]; then
		echo "adb.sh: adb is built for macOS (arm64, amd64), windows-amd64 or linux-amd64" >&2
		exit 2
	fi
	host=$(uname -m)
	case ${target:-$host} in
	arm64 | aarch64) arch=arm64 ;;
	amd64 | x86_64) arch=x86_64 ;;
	*)
		echo "adb.sh: no macOS build for the processor $target (arm64 or amd64)" >&2
		exit 2
		;;
	esac
	# Pinky.app runs on macOS 12 and later (minimumSystemVersion in
	# cmd/pinky/mygo.json).
	min=12.0
	flags="-DCMAKE_OSX_ARCHITECTURES=$arch -DCMAKE_OSX_DEPLOYMENT_TARGET=$min"
	native=
	if [ "$arch" = "$host" ]; then
		native=1
	fi
	what="macOS $min and later, $arch"
	exe=adb
	jobs=$(sysctl -n hw.ncpu)
	;;
esac

work=$prefix-work
rm -rf "$work"
mkdir -p "$work"
for f in "android-tools-$ANDROID_TOOLS_VERSION.tar.xz" "zlib-$ZLIB_VERSION.tar.xz"; do
	tar -xJf "$sources/$f" -C "$work"
done
for f in "protobuf-cpp-$PROTOBUF_VERSION.tar.gz" "brotli-$BROTLI_VERSION.tar.gz" "lz4-$LZ4_VERSION.tar.gz" "zstd-$ZSTD_VERSION.tar.gz"; do
	tar -xzf "$sources/$f" -C "$work"
done
tree=$work/android-tools-$ANDROID_TOOLS_VERSION
if [ "$target" = windows-amd64 ]; then
	# MSYS2's fixes for mingw-w64 (scripts/adb/windows.patch says which).
	patch -d "$tree" -p1 <"$here/adb/windows.patch"
	mkdir -p "$work/adbwinapi"
	cp "$sources/adb_api.h" "$sources/AdbWinApi.def" "$work/adbwinapi/"
fi

# configure and build a CMake project: <source> <build> [flags...]
cmake_build() {
	src=$1 bld=$2
	shift 2
	# shellcheck disable=SC2086 # flags splits on purpose
	cmake -S "$src" -B "$bld" -G "$generator" -DCMAKE_BUILD_TYPE=Release $flags "$@" >"$bld.log" 2>&1 || {
		tail -40 "$bld.log" >&2
		echo "adb.sh: configuring $src failed; the log is $bld.log" >&2
		exit 1
	}
	cmake --build "$bld" --parallel "$jobs" >>"$bld.log" 2>&1 || {
		tail -60 "$bld.log" >&2
		echo "adb.sh: building $src failed; the log is $bld.log" >&2
		exit 1
	}
}

# protobuf: a static libprotobuf for the target, and protoc for here.
pb=$work/protobuf-$PROTOBUF_VERSION
pbflags="-Dprotobuf_BUILD_TESTS=OFF -Dprotobuf_BUILD_SHARED_LIBS=OFF -Dprotobuf_WITH_ZLIB=OFF
	-DCMAKE_INSTALL_LIBDIR=lib -DCMAKE_POLICY_VERSION_MINIMUM=3.5"
echo "Building protobuf $PROTOBUF_VERSION for $what"
if [ -n "$native" ]; then
	# shellcheck disable=SC2086
	cmake_build "$pb" "$work/protobuf-build" $pbflags -DCMAKE_INSTALL_PREFIX="$work/protobuf"
	protoc=$work/protobuf/bin/protoc
else
	# shellcheck disable=SC2086
	cmake_build "$pb" "$work/protobuf-build" $pbflags -Dprotobuf_BUILD_PROTOC_BINARIES=OFF -DCMAKE_INSTALL_PREFIX="$work/protobuf"
	echo "Building protoc $PROTOBUF_VERSION for this machine"
	(
		flags=
		if [ "$(uname -s)" = Darwin ]; then
			flags="-DCMAKE_OSX_ARCHITECTURES=$(uname -m)"
		fi
		# shellcheck disable=SC2086
		cmake_build "$pb" "$work/protoc-build" $pbflags -DCMAKE_INSTALL_PREFIX="$work/protoc"
		cmake --install "$work/protoc-build" >/dev/null
	)
	protoc=$work/protoc/bin/protoc
fi
cmake --install "$work/protobuf-build" >/dev/null

echo "Building adb $ANDROID_TOOLS_VERSION for $what"
set -- -DVENDOR="$tree/vendor" -DADB_VERSION="$ANDROID_TOOLS_VERSION" \
	-DPROTOBUF="$work/protobuf" -DPROTOC="$protoc" \
	-DBROTLI_SRC="$work/brotli-$BROTLI_VERSION" -DLZ4_SRC="$work/lz4-$LZ4_VERSION" \
	-DZSTD_SRC="$work/zstd-$ZSTD_VERSION" -DZLIB_SRC="$work/zlib-$ZLIB_VERSION"
if [ "$target" = windows-amd64 ]; then
	set -- "$@" -DADBWINAPI="$work/adbwinapi"
fi
cmake_build "$here/adb" "$work/adb-build" "$@"

rm -rf "$prefix"
mkdir -p "$prefix"
cp "$work/adb-build/$exe" "$prefix/$exe"
case $target in
windows-amd64)
	${cross}strip "$prefix/$exe"
	cp "$sources/AdbWinApi.dll" "$sources/AdbWinUsbApi.dll" "$prefix/"
	# adb.exe may import Windows' own DLLs and AdbWinApi.dll, nothing else
	# (no libwinpthread-1.dll, no libstdc++-6.dll).
	imports=$(${cross}objdump -p "$prefix/$exe" | sed -n 's/^[[:space:]]*DLL Name: //p' | sort -u)
	echo "adb.exe imports: $(echo "$imports" | tr '\n' ' ')"
	bad=$(echo "$imports" | grep -v -i -E '^(AdbWinApi|kernel32|user32|advapi32|shell32|ole32|oleaut32|ws2_32|userenv|bcrypt|crypt32|iphlpapi|setupapi|msvcrt|ucrtbase)\.dll$|^api-ms-win-' || true)
	if [ -n "$bad" ]; then
		echo "adb.sh: adb.exe imports libraries that Windows does not have:" >&2
		echo "$bad" >&2
		exit 1
	fi
	;;
linux-amd64)
	strip "$prefix/$exe"
	# A static executable: no interpreter, no shared libraries.
	if readelf -l "$prefix/$exe" | grep -q INTERP || readelf -d "$prefix/$exe" 2>/dev/null | grep -q NEEDED; then
		echo "adb.sh: adb is not a static executable:" >&2
		readelf -d "$prefix/$exe" >&2 || true
		exit 1
	fi
	"$prefix/$exe" version
	;;
*)
	strip -x "$prefix/$exe"
	# Only the system's libraries, and the processor and macOS asked for.
	bad=$(otool -L "$prefix/$exe" | awk 'NR > 1 { print $1 }' | grep -v -e '^/usr/lib/' -e '^/System/Library/' || true)
	if [ -n "$bad" ]; then
		echo "adb.sh: adb needs libraries outside the system:" >&2
		echo "$bad" >&2
		exit 1
	fi
	got=$(lipo -archs "$prefix/$exe")
	if [ "$got" != "$arch" ]; then
		echo "adb.sh: adb is for $got, not $arch" >&2
		exit 1
	fi
	minos=$(otool -l "$prefix/$exe" | awk '/minos/ { print $2; exit }')
	if [ "$minos" != "$min" ]; then
		echo "adb.sh: adb needs macOS $minos, not $min" >&2
		exit 1
	fi
	if [ -n "$native" ]; then
		"$prefix/$exe" version
	fi
	;;
esac

# The licenses of everything adb.exe or adb holds.
v=$tree/vendor
{
	echo "adb, built for Pinky from android-tools $ANDROID_TOOLS_VERSION (AOSP's adb, android-17.0.0_r1). The licenses of what it holds follow."
	license() {
		echo
		echo "================================================================================"
		echo "$1"
		echo "================================================================================"
		echo
		cat "$2"
	}
	license "adb, libbase, libcutils, liblog, libziparchive, libcrypto_utils, diagnose_usb (AOSP), and android-tools' build: Apache License 2.0" "$tree/LICENSE"
	if [ "$target" = windows-amd64 ]; then
		echo
		echo "AdbWinApi.dll and AdbWinUsbApi.dll are AOSP's, from platform/development at android-17.0.0_r1 (host/windows/prebuilt/usb), under the Apache License 2.0 above."
	fi
	license "BoringSSL" "$v/boringssl/LICENSE"
	license "libusb: GNU Lesser General Public License 2.1 (statically linked; its source is in android-tools' release, see BUILD.txt, and scripts/adb.sh in Pinky's repository builds and links it again)" "$v/libusb/COPYING"
	license "fmt" "$v/fmtlib/LICENSE"
	license "Protocol Buffers $PROTOBUF_VERSION" "$pb/LICENSE"
	license "Brotli $BROTLI_VERSION" "$work/brotli-$BROTLI_VERSION/LICENSE"
	license "LZ4 $LZ4_VERSION (lib)" "$work/lz4-$LZ4_VERSION/lib/LICENSE"
	license "Zstandard $ZSTD_VERSION" "$work/zstd-$ZSTD_VERSION/LICENSE"
	case $target in
	arm64 | amd64 | '') ;;
	*) license "zlib $ZLIB_VERSION" "$work/zlib-$ZLIB_VERSION/LICENSE" ;;
	esac
} >"$prefix/NOTICE.txt"
{
	echo "adb from android-tools $ANDROID_TOOLS_VERSION, built for Pinky for $what by scripts/adb.sh."
	echo "Sources:"
	echo "  $ANDROID_TOOLS_URL"
	echo "    SHA-256 $ANDROID_TOOLS_SHA256"
	echo "  $PROTOBUF_URL"
	echo "    SHA-256 $PROTOBUF_SHA256"
	echo "  $BROTLI_URL"
	echo "    SHA-256 $BROTLI_SHA256"
	echo "  $LZ4_URL"
	echo "    SHA-256 $LZ4_SHA256"
	echo "  $ZSTD_URL"
	echo "    SHA-256 $ZSTD_SHA256"
	case $target in
	arm64 | amd64 | '') ;;
	*)
		echo "  $ZLIB_URL"
		echo "    SHA-256 $ZLIB_SHA256"
		;;
	esac
	if [ "$target" = windows-amd64 ]; then
		echo "  $AOSP_DEVELOPMENT/host/windows/prebuilt/usb/AdbWinApi.dll"
		echo "    SHA-256 $ADBWINAPI_DLL_SHA256"
		echo "  $AOSP_DEVELOPMENT/host/windows/prebuilt/usb/AdbWinUsbApi.dll"
		echo "    SHA-256 $ADBWINUSBAPI_DLL_SHA256"
		echo "  $AOSP_DEVELOPMENT/host/windows/prebuilt/usb/AdbWinApi.def"
		echo "    SHA-256 $ADBWINAPI_DEF_SHA256"
		echo "  $AOSP_DEVELOPMENT/host/windows/usb/api/adb_api.h"
		echo "    SHA-256 $ADB_API_H_SHA256"
	fi
	echo "Without mDNS: Pinky finds the phones on the network itself."
	echo "SHA-256 of $exe: $(sha256 "$prefix/$exe")"
} >"$prefix/BUILD.txt"
rm -rf "$work"
echo "adb $ANDROID_TOOLS_VERSION is in $prefix"
