#!/bin/sh
# Publishes a release that scripts/dist.sh built and `release manifest`
# finished (latest.json, the cask pinky.rb, SHA256SUMS), for the release
# workflow and scripts/release.sh (see docs/RELEASING.md):
#
#   scripts/publish.sh <dir>
#
# It creates the GitHub release v<version> in RELEASES_REPO as a draft,
# uploads every file in <dir> to it, latest.json last, and publishes it:
# as the latest release, unless a newer one is out already, so that
# releases/latest/download/latest.json (and pinky.rb, which the Homebrew
# tap takes) is always the newest version's. A release that is published
# already keeps its files.
#
# Environment:
#   RELEASES_REPO  the repository of the releases (yaoqiangyao/pinky)
#   TAP_REPO       the Homebrew tap the release page names (DIYgod/homebrew-tap)
#   GH_TOKEN       for gh, which must be able to write RELEASES_REPO
#                  (default: gh's own login)
set -eu

dir=${1:?usage: scripts/publish.sh <dir>}
case $dir in /*) ;; *) dir=$(pwd)/$dir ;; esac
cd "$(dirname "$0")/.."
repo=${RELEASES_REPO:-yaoqiangyao/pinky}
tap=${TAP_REPO:-DIYgod/homebrew-tap}
# brew's name of the tap: DIYgod/homebrew-tap is diygod/tap.
tapname=$(printf '%s/%s' "${tap%%/*}" "${tap#*/homebrew-}" | tr '[:upper:]' '[:lower:]')

say() { printf '==> %s\n' "$*" >&2; }
die() {
	printf 'publish: %s\n' "$*" >&2
	exit 1
}

[ -f "$dir/latest.json" ] || die "$dir has no latest.json (go run ./scripts/release manifest)"
work=$(mktemp -d "${TMPDIR:-/tmp}/pinky-publish.XXXXXX")
trap 'rm -rf "$work"' EXIT
go build -o "$work/release" ./scripts/release
release() { "$work/release" "$@"; }
version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$dir/latest.json" | head -1)
release newer "$version" || die "latest.json has no release version"
tag=v$version

# The release page: the notes, in English and then in each language
# latest.json has them in, then where to get Pinky.
{
	release notes -page -manifest "$dir/latest.json"
	cat <<EOF

---

**Windows:** \`pinky-setup-$version-amd64.exe\`, or \`pinky-$version-windows-amd64.zip\` to run it without installing. For iPhones on iOS 27 or later.
**Claude Desktop:** with Pinky installed, open \`pinky-$version.mcpb\` to add Pinky to Claude Desktop as an extension; Settings › Agents in Pinky connects Claude Code, Codex, Cursor and others with a click.
EOF
} >"$work/notes.md"

state=$(gh release view "$tag" --repo "$repo" --json isDraft --jq .isDraft 2>/dev/null || echo none)
if [ "$state" = false ]; then
	say "$repo has published $tag already: its files stay as they are"
else
	if [ "$state" = none ]; then
		say "creating the draft release $tag in $repo"
		gh release create "$tag" --repo "$repo" --draft --title "Pinky $version" --notes-file "$work/notes.md"
	else
		gh release edit "$tag" --repo "$repo" --title "Pinky $version" --notes-file "$work/notes.md"
	fi
	say "uploading $(find "$dir" -maxdepth 1 -type f ! -name '.*' | wc -l | tr -d ' ') files"
	find "$dir" -maxdepth 1 -type f ! -name '.*' ! -name latest.json -exec gh release upload "$tag" --repo "$repo" --clobber {} +
	# The manifest last, so that it never names a file still on its way.
	gh release upload "$tag" --repo "$repo" --clobber "$dir/latest.json"
	latest=$(gh release view --repo "$repo" --json tagName --jq .tagName 2>/dev/null || true)
	if release newer "$version" "${latest#v}"; then
		gh release edit "$tag" --repo "$repo" --draft=false --latest
	else
		say "$latest is newer: $tag is published without becoming the latest release"
		gh release edit "$tag" --repo "$repo" --draft=false --latest=false
	fi
	say "published https://github.com/$repo/releases/tag/$tag"
fi
