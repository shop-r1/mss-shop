#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd -- "${script_dir}/.." && pwd)"
mss_mcp_binary="${MSS_MCP_BIN:-mss-mcp}"
required_version="v1.3.6"

if ! command -v "${mss_mcp_binary}" >/dev/null 2>&1; then
  echo "mss-mcp is not installed; install the official ${required_version} release bundle" >&2
  exit 127
fi

version_output="$("${mss_mcp_binary}" -version 2>&1)"
case "${version_output}" in
  "mss-mcp ${required_version} "*) ;;
  *)
    echo "mss-mcp ${required_version} is required; found: ${version_output}" >&2
    exit 2
    ;;
esac

exec "${mss_mcp_binary}" -root "${repository_root}"
