#!/bin/bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT
source <(sed '$d' "$ROOT_DIR/deploy/install.sh")

mkdir -p "$TEST_DIR/payload"
printf '#!/bin/sh\necho 0.2.20\n' > "$TEST_DIR/payload/sub2api"
tar -czf "$TEST_DIR/archive.tar.gz" -C "$TEST_DIR/payload" sub2api
printf 'README\n' > "$TEST_DIR/payload/README"
tar -czf "$TEST_DIR/missing-binary.tar.gz" -C "$TEST_DIR/payload" README
ARCHIVE_NAME='sub2api_0.2.20_linux_amd64.tar.gz'
DIGEST=$(shasum -a 256 "$TEST_DIR/archive.tar.gz" | awk '{print $1}')
OS=linux
ARCH=amd64
LANG_CHOICE=en

get_current_version() { printf '0.2.20'; }
validate_version() { printf 'v0.2.20'; }
get_latest_version() { LATEST_VERSION=v0.2.20; }
chown() { return 0; }
sha256sum() { shasum -a 256 "$@"; }
systemctl() { printf '%s\n' "$*" >> "$CASE_DIR/service.log"; }

curl() {
    # Exercise the real download/verification path without touching the network.
    test "$1" = '-fsSL' && test "$2" = '--retry' && test "$3" = '3' || return 1
    local url=$4 output=$6
    case "$url" in
        */"$ARCHIVE_NAME")
            if [ "$SCENARIO" = archive_http_error ]; then return 22; fi
            cp "$TEST_DIR/archive.tar.gz" "$output"
            if [ "$SCENARIO" = corrupt_archive ]; then printf broken > "$output"; fi
            if [ "$SCENARIO" = missing_binary ]; then cp "$TEST_DIR/missing-binary.tar.gz" "$output"; fi
            ;;
        */checksums.txt)
            case "$SCENARIO" in
                checksum_http_error) return 22 ;;
                html) printf '<html>Gateway Time-out</html>\n' > "$output" ;;
                missing) printf '%s  other.tar.gz\n' "$DIGEST" > "$output" ;;
                similar) printf '%s  %s.bak\n' "$DIGEST" "$ARCHIVE_NAME" > "$output" ;;
                duplicate) printf '%s  %s\n%s  %s\n' "$DIGEST" "$ARCHIVE_NAME" "$DIGEST" "$ARCHIVE_NAME" > "$output" ;;
                mismatch) printf '%064d  %s\n' 0 "$ARCHIVE_NAME" > "$output" ;;
                binary_marker) printf '%s *%s\n' "$DIGEST" "$ARCHIVE_NAME" > "$output" ;;
                uppercase) printf '%s  %s\n' "${DIGEST^^}" "$ARCHIVE_NAME" > "$output" ;;
                corrupt_archive|missing_binary) printf '%s  %s\n' "$(shasum -a 256 "$TEMP_DIR/$ARCHIVE_NAME" | awk '{print $1}')" "$ARCHIVE_NAME" > "$output" ;;
                *) printf '%s  %s\n' "$DIGEST" "$ARCHIVE_NAME" > "$output" ;;
            esac
            ;;
        *) return 1 ;;
    esac
}

for operation in install_version upgrade; do
    for SCENARIO in valid binary_marker archive_http_error checksum_http_error html missing similar duplicate mismatch corrupt_archive missing_binary uppercase; do
        CASE_DIR="$TEST_DIR/$operation-$SCENARIO"
        INSTALL_DIR="$CASE_DIR/install"
        mkdir -p "$INSTALL_DIR"
        cp "$TEST_DIR/payload/sub2api" "$INSTALL_DIR/sub2api"
        printf '# old binary\n' >> "$INSTALL_DIR/sub2api"
        chmod +x "$INSTALL_DIR/sub2api"
        cp "$INSTALL_DIR/sub2api" "$CASE_DIR/original"
        : > "$CASE_DIR/service.log"
        # Do not invoke the subshell in an if: it would disable set -e inside it.
        set +e
        (set -e; "$operation" v0.2.20) > "$CASE_DIR/output" 2>&1
        status=$?
        set -e
        case "$SCENARIO" in
            valid|binary_marker|uppercase)
                test "$status" -eq 0
                cmp "$TEST_DIR/payload/sub2api" "$INSTALL_DIR/sub2api"
                grep -Fxq 'stop sub2api' "$CASE_DIR/service.log"
                grep -Fxq 'start sub2api' "$CASE_DIR/service.log"
                ;;
            *)
                test "$status" -ne 0
                cmp "$CASE_DIR/original" "$INSTALL_DIR/sub2api"
                test ! -s "$CASE_DIR/service.log"
                ;;
        esac
    done
done

echo 'Installer downloads: 24 upgrade/reinstall cases passed'
