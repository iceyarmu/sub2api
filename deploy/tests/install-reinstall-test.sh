#!/bin/bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

# Load functions without invoking the installer's main entry point.
source <(sed '$d' "$ROOT_DIR/deploy/install.sh")
INSTALL_DIR="$TEMP_DIR/install"
mkdir -p "$INSTALL_DIR"
printf 'old binary' > "$INSTALL_DIR/sub2api"

github_api_curl() {
    printf '%s\n' "$@" > "$TEMP_DIR/api-args"
    if [[ "${!#}" == */releases ]]; then
        printf '{"tag_name":"v0.2.15"}\n'
    else
        printf '200'
    fi
}
get_current_version() { printf '0.2.15'; }
systemctl() { return 0; }
chown() { return 0; }
download_and_extract() {
    test "$LATEST_VERSION" = 'v0.2.15'
    test "$DOWNLOAD_GITHUB_REPO" = 'iceyarmu/sub2api'
    printf 'fresh binary' > "$INSTALL_DIR/sub2api"
}

install_version '0.2.15' > "$TEMP_DIR/output"
grep -Fxq 'https://api.github.com/repos/iceyarmu/sub2api/releases/tags/v0.2.15' "$TEMP_DIR/api-args"
test "$(cat "$INSTALL_DIR/sub2api")" = 'fresh binary'
test "$(cat "$INSTALL_DIR/sub2api.backup.0.2.15")" = 'old binary'
list_versions > "$TEMP_DIR/versions"
grep -Fxq 'https://api.github.com/repos/iceyarmu/sub2api/releases' "$TEMP_DIR/api-args"
grep -Fq 'v0.2.15' "$TEMP_DIR/versions"
echo 'Same-version reinstall and fork release validation passed'
