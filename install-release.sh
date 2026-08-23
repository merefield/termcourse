#!/bin/sh

set -eu

repository=${TERMCOURSE_REPOSITORY:-merefield/termcourse}
release_tag=${TERMCOURSE_VERSION:-latest}
bin_dir=${TERMCOURSE_BIN_DIR:-/usr/local/bin}
binary_name=${TERMCOURSE_BIN_NAME:-termcourse}
github_url=${TERMCOURSE_GITHUB_URL:-https://github.com}
github_api_url=${TERMCOURSE_GITHUB_API_URL:-https://api.github.com}

usage() {
  cat <<'EOF'
Install a pre-built Termcourse release from GitHub.

Usage: install-release.sh [--version TAG] [--bin-dir DIR] [--help]

Options:
  --version TAG  Install a specific release tag instead of the latest release.
  --bin-dir DIR  Install into DIR instead of /usr/local/bin.
  --help         Show this help.

Environment:
  TERMCOURSE_VERSION         Release tag to install (default: latest).
  TERMCOURSE_BIN_DIR         Installation directory (default: /usr/local/bin).
  TERMCOURSE_BIN_NAME        Installed binary name (default: termcourse).
  TERMCOURSE_REPOSITORY      GitHub owner/repository (default: merefield/termcourse).
  TERMCOURSE_GITHUB_URL      GitHub web base URL (primarily for testing/mirrors).
  TERMCOURSE_GITHUB_API_URL  GitHub API base URL (primarily for testing/mirrors).
EOF
}

fail() {
  printf 'termcourse installer: %s\n' "$*" >&2
  exit 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || fail "--version requires a release tag"
      release_tag=$2
      shift 2
      ;;
    --bin-dir)
      [ "$#" -ge 2 ] || fail "--bin-dir requires a directory"
      bin_dir=$2
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

case "$repository" in
  /*|*/|*//*|*[!A-Za-z0-9._/-]*) fail "invalid TERMCOURSE_REPOSITORY: $repository" ;;
esac
repository_owner=${repository%%/*}
repository_name=${repository#*/}
if [ -z "$repository_owner" ] || [ -z "$repository_name" ] || [ "$repository_name" != "${repository_name#*/}" ]; then
  fail "TERMCOURSE_REPOSITORY must have the form owner/repository"
fi

[ -n "$bin_dir" ] || fail "TERMCOURSE_BIN_DIR must not be empty"
if { [ -e "$bin_dir" ] || [ -L "$bin_dir" ]; } && [ ! -d "$bin_dir" ]; then
  fail "TERMCOURSE_BIN_DIR exists and is not a directory: $bin_dir"
fi
case "$binary_name" in
  ''|*/*) fail "TERMCOURSE_BIN_NAME must be a single file name" ;;
esac

for command_name in tar awk sed tr install mktemp; do
  command -v "$command_name" >/dev/null 2>&1 || fail "required command not found: $command_name"
done

if command -v curl >/dev/null 2>&1; then
  download() {
    curl --fail --silent --show-error --location --retry 3 --output "$2" "$1"
  }
elif command -v wget >/dev/null 2>&1; then
  download() {
    wget --quiet --output-document="$2" "$1"
  }
else
  fail "curl or wget is required to download a release"
fi

case "$(uname -s)" in
  Linux) release_os=linux ;;
  Darwin) release_os=darwin ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) release_arch=amd64 ;;
  arm64|aarch64) release_arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/termcourse-release-install.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

if [ "$release_tag" = latest ]; then
  printf 'Resolving the latest Termcourse release...\n'
  release_json=${tmp_dir}/release.json
  download "${github_api_url}/repos/${repository}/releases/latest" "$release_json"
  release_tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"/]*\)".*/\1/p' "$release_json" | sed -n '1p')
  [ -n "$release_tag" ] || fail "could not determine the latest release tag"
fi

case "$release_tag" in
  ''|*[!A-Za-z0-9._-]*) fail "invalid release tag: $release_tag" ;;
esac

release_version=${release_tag#v}
[ -n "$release_version" ] || fail "invalid release tag: $release_tag"
archive_name=termcourse_${release_version}_${release_os}_${release_arch}.tar.gz
release_url=${github_url}/${repository}/releases/download/${release_tag}
archive_path=${tmp_dir}/${archive_name}
checksums_path=${tmp_dir}/checksums.txt

printf 'Downloading Termcourse %s for %s/%s...\n' "$release_tag" "$release_os" "$release_arch"
download "${release_url}/${archive_name}" "$archive_path"
download "${release_url}/checksums.txt" "$checksums_path"

checksum_count=$(awk -v name="$archive_name" '$2 == name || $2 == "*" name { count++ } END { print count + 0 }' "$checksums_path")
[ "$checksum_count" -eq 1 ] || fail "checksums.txt does not contain exactly one checksum for $archive_name"
expected_hash=$(awk -v name="$archive_name" '$2 == name || $2 == "*" name { print $1 }' "$checksums_path")
case "$expected_hash" in
  *[!0-9A-Fa-f]*|'') fail "invalid SHA-256 checksum for $archive_name" ;;
esac
[ "${#expected_hash}" -eq 64 ] || fail "invalid SHA-256 checksum for $archive_name"

if command -v sha256sum >/dev/null 2>&1; then
  actual_hash=$(sha256sum "$archive_path" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  actual_hash=$(shasum -a 256 "$archive_path" | awk '{ print $1 }')
else
  fail "sha256sum or shasum is required to verify the release"
fi

expected_hash=$(printf '%s' "$expected_hash" | tr 'A-F' 'a-f')
actual_hash=$(printf '%s' "$actual_hash" | tr 'A-F' 'a-f')
[ "$actual_hash" = "$expected_hash" ] || fail "SHA-256 checksum verification failed for $archive_name"
printf 'Verified the release checksum.\n'

member_count=$(tar -tzf "$archive_path" | awk '$0 == "termcourse" { count++ } END { print count + 0 }')
[ "$member_count" -eq 1 ] || fail "release archive does not contain exactly one root-level termcourse binary"
extract_dir=${tmp_dir}/extract
mkdir "$extract_dir"
tar -xzf "$archive_path" -C "$extract_dir" termcourse
candidate=${extract_dir}/termcourse
[ -x "$candidate" ] || fail "the downloaded termcourse binary is not executable"

version_output=$("$candidate" --version 2>&1) || fail "the downloaded termcourse binary failed its version check"
[ "$version_output" = "termcourse $release_version" ] ||
  fail "the downloaded binary reported an unexpected version: $version_output"

target=${bin_dir}/${binary_name}
if [ -w "$bin_dir" ] || { [ ! -e "$bin_dir" ] && [ -w "$(dirname "$bin_dir")" ]; }; then
  mkdir -p "$bin_dir"
  install -m 0755 "$candidate" "$target"
else
  command -v sudo >/dev/null 2>&1 || fail "$bin_dir is not writable and sudo is unavailable; set TERMCOURSE_BIN_DIR to a writable directory"
  sudo mkdir -p "$bin_dir"
  sudo install -m 0755 "$candidate" "$target"
fi

printf 'Installed %s to %s (%s).\n' "$binary_name" "$target" "$version_output"
case ":${PATH}:" in
  *:"$bin_dir":*) ;;
  *) printf 'Add %s to PATH before invoking %s.\n' "$bin_dir" "$binary_name" ;;
esac
