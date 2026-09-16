#!/usr/bin/env bash
set -euo pipefail

PORT="${1:-4321}"
echo "Share this URL:"
echo "  http://$(hostname).local:${PORT}"
