#!/usr/bin/env bats

setup() {
  export TEST_ROOT
  TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/termcourse-release-install-test.XXXXXX")"
  export FIXTURE_DIR="$TEST_ROOT/fixture"
  export FIXTURE_TAG=v1.2.3
  export FIXTURE_ASSET=termcourse_1.2.3_linux_amd64.tar.gz
  export FIXTURE_VERSION_OUTPUT="termcourse 1.2.3"
  export CURL_LOG="$TEST_ROOT/curl.log"
  export FAKE_UNAME_S=Linux
  export FAKE_UNAME_M=x86_64
  mkdir -p "$TEST_ROOT/fakebin" "$TEST_ROOT/bin" "$FIXTURE_DIR/package"

  cat > "$FIXTURE_DIR/package/termcourse" <<'EOF'
#!/bin/sh
printf '%s\n' "$FIXTURE_VERSION_OUTPUT"
EOF
  chmod +x "$FIXTURE_DIR/package/termcourse"
  tar -czf "$FIXTURE_DIR/archive.tar.gz" -C "$FIXTURE_DIR/package" termcourse
  export FIXTURE_HASH
  FIXTURE_HASH=$(sha256sum "$FIXTURE_DIR/archive.tar.gz" | awk '{ print $1 }')

  cat > "$TEST_ROOT/fakebin/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) printf '%s\n' "$FAKE_UNAME_S" ;;
  -m) printf '%s\n' "$FAKE_UNAME_M" ;;
  *) exit 2 ;;
esac
EOF
  chmod +x "$TEST_ROOT/fakebin/uname"

  cat > "$TEST_ROOT/fakebin/curl" <<'EOF'
#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      output=$2
      shift 2
      ;;
    --retry)
      shift 2
      ;;
    --*)
      shift
      ;;
    *)
      url=$1
      shift
      ;;
  esac
done
[ -n "$output" ] && [ -n "$url" ] || exit 2
printf '%s\n' "$url" >> "$CURL_LOG"
case "$url" in
  */repos/*/releases/latest)
    printf '{"tag_name":"%s"}\n' "$FIXTURE_TAG" > "$output"
    ;;
  */checksums.txt)
    printf '%s  %s\n' "$FIXTURE_HASH" "$FIXTURE_ASSET" > "$output"
    ;;
  */"$FIXTURE_ASSET")
    cp "$FIXTURE_DIR/archive.tar.gz" "$output"
    ;;
  *)
    printf 'unexpected URL: %s\n' "$url" >&2
    exit 3
    ;;
esac
EOF
  chmod +x "$TEST_ROOT/fakebin/curl"
}

teardown() {
  rm -rf "$TEST_ROOT"
}

@test "release installer resolves, verifies, and installs the latest release" {
  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" \
    sh ./install-release.sh

  [ "$status" -eq 0 ]
  [ -x "$TEST_ROOT/bin/termcourse" ]
  [[ "$output" == *"Verified the release checksum."* ]]
  [[ "$output" == *"Installed termcourse to $TEST_ROOT/bin/termcourse (termcourse 1.2.3)."* ]]
  grep -q '/repos/merefield/termcourse/releases/latest$' "$CURL_LOG"
  grep -q '/releases/download/v1.2.3/termcourse_1.2.3_linux_amd64.tar.gz$' "$CURL_LOG"

  run "$TEST_ROOT/bin/termcourse" --version
  [ "$status" -eq 0 ]
  [ "$output" = "termcourse 1.2.3" ]
}

@test "release installer supports an explicit version and Darwin ARM64" {
  export FAKE_UNAME_S=Darwin
  export FAKE_UNAME_M=arm64
  export FIXTURE_TAG=v1.2.3+build.1
  export FIXTURE_ASSET=termcourse_1.2.3+build.1_darwin_arm64.tar.gz
  export FIXTURE_VERSION_OUTPUT="termcourse 1.2.3+build.1"

  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" \
    sh ./install-release.sh --version v1.2.3+build.1

  [ "$status" -eq 0 ]
  [ -x "$TEST_ROOT/bin/termcourse" ]
  ! grep -q '/releases/latest$' "$CURL_LOG"
  grep -Fq '/releases/download/v1.2.3+build.1/termcourse_1.2.3+build.1_darwin_arm64.tar.gz' "$CURL_LOG"
}

@test "release installer rejects invalid semantic versions" {
  run env PATH="$TEST_ROOT/fakebin:$PATH" TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" \
    sh ./install-release.sh --version v1.2.3-01

  [ "$status" -eq 1 ]
  [[ "$output" == *"invalid semantic release tag"* ]]
  [ ! -e "$CURL_LOG" ]
}

@test "release installer creates a nested user bin directory" {
  run env PATH="$TEST_ROOT/fakebin:$PATH" TERMCOURSE_BIN_DIR="$TEST_ROOT/nested/user/bin" sh ./install-release.sh

  [ "$status" -eq 0 ]
  [ -x "$TEST_ROOT/nested/user/bin/termcourse" ]
}

@test "release installer refuses a checksum mismatch" {
  export FIXTURE_HASH=0000000000000000000000000000000000000000000000000000000000000000

  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" \
    sh ./install-release.sh

  [ "$status" -eq 1 ]
  [[ "$output" == *"SHA-256 checksum verification failed"* ]]
  [ ! -e "$TEST_ROOT/bin/termcourse" ]
}

@test "release installer refuses a binary with the wrong version" {
  export FIXTURE_VERSION_OUTPUT="termcourse 9.9.9"

  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" \
    sh ./install-release.sh

  [ "$status" -eq 1 ]
  [[ "$output" == *"downloaded binary reported an unexpected version: termcourse 9.9.9"* ]]
  [ ! -e "$TEST_ROOT/bin/termcourse" ]
}

@test "release installer rejects unsupported platforms before downloading" {
  export FAKE_UNAME_S=FreeBSD

  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" \
    sh ./install-release.sh

  [ "$status" -eq 1 ]
  [[ "$output" == *"unsupported operating system: FreeBSD"* ]]
  [ ! -e "$CURL_LOG" ]
}

@test "release installer rejects a bin path that is not a directory" {
  occupied_path="$TEST_ROOT/not-a-directory"
  printf 'occupied\n' > "$occupied_path"

  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_BIN_DIR="$occupied_path" \
    sh ./install-release.sh

  [ "$status" -eq 1 ]
  [[ "$output" == *"TERMCOURSE_BIN_DIR exists and is not a directory: $occupied_path"* ]]
  [ ! -e "$CURL_LOG" ]

  symlink_path="$TEST_ROOT/file-symlink"
  ln -s "$occupied_path" "$symlink_path"

  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_BIN_DIR="$symlink_path" \
    sh ./install-release.sh

  [ "$status" -eq 1 ]
  [[ "$output" == *"TERMCOURSE_BIN_DIR exists and is not a directory: $symlink_path"* ]]
  [ ! -e "$CURL_LOG" ]
}

@test "release installer requires an owner and repository" {
  run env \
    PATH="$TEST_ROOT/fakebin:$PATH" \
    TERMCOURSE_REPOSITORY=termcourse \
    TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" \
    sh ./install-release.sh

  [ "$status" -eq 1 ]
  [[ "$output" == *"TERMCOURSE_REPOSITORY must have the form owner/repository"* ]]
  [ ! -e "$CURL_LOG" ]
}

@test "release installer rejects a directory at the final target" {
  mkdir "$TEST_ROOT/bin/termcourse"
  run env PATH="$TEST_ROOT/fakebin:$PATH" TERMCOURSE_BIN_DIR="$TEST_ROOT/bin" sh ./install-release.sh

  [ "$status" -eq 1 ]
  [[ "$output" == *"installation target exists and is a directory"* ]]
}
