#!/bin/bash
# SRT environment setup — no longer needed.
#
# SRT is now implemented in pure Go (github.com/zsiec/srtgo) and does not
# require the Haivision C library or any CGo flags.
#
# This script is kept as a pass-through shim so that existing workflows
# that source or invoke it continue to work.
#
# Usage (still works, just passes through):
#   ./scripts/srt-env.sh go test ./...
#   source scripts/srt-env.sh  (no-op)

if [[ $# -gt 0 ]]; then
    exec "$@"
fi
