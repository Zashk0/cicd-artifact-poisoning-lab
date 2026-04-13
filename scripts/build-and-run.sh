#!/usr/bin/env bash
set -euo pipefail

# Replicates scripts/build-v54.sh — downloads from Pages, verifies same-origin checksum, executes
# This is the consumer that turns artifact poisoning into RCE.

# UPDATE THIS to your own GitHub username when you set up the lab:
NIGHTLY_BASE_URL="https://Zashk0.github.io/cicd-artifact-poisoning-lab/nightlies/latest"

archive_url="${NIGHTLY_BASE_URL}/myapp-linux-amd64.tar.gz"
checksum_url="${NIGHTLY_BASE_URL}/myapp-linux-amd64.tar.gz.sha256"

tmp_dir=$(mktemp -d)
archive_path="${tmp_dir}/myapp-linux-amd64.tar.gz"
checksum_path="${tmp_dir}/myapp-linux-amd64.tar.gz.sha256"

echo "[CONSUMER] Downloading binary from: $archive_url"
curl -sfL -o "${archive_path}" "${archive_url}"

echo "[CONSUMER] Downloading checksum from same origin: $checksum_url"
curl -sfL -o "${checksum_path}" "${checksum_url}"

# THE BROKEN VERIFICATION — checksum from same origin as binary
expected="$(awk '{print $1}' "${checksum_path}")"
actual="$(sha256sum "${archive_path}" | awk '{print $1}')"

echo "[CONSUMER] Expected: $expected"
echo "[CONSUMER] Actual:   $actual"

if [ "${expected}" != "${actual}" ]; then
  echo "[CONSUMER] CHECKSUM MISMATCH — refusing to execute"
  exit 1
fi

echo "[CONSUMER] Checksum valid — extracting and executing"
tar -xzf "${archive_path}" -C "${tmp_dir}"
chmod +x "${tmp_dir}/myapp-linux-amd64"

# THE RCE — execute the downloaded binary
"${tmp_dir}/myapp-linux-amd64" 2>&1 | tee /tmp/binary-output.log
