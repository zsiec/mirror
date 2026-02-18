# Scripts

Build tools and environment setup scripts for the Mirror platform.

## srt-env.sh

Configures the SRT (Secure Reliable Transport) library environment for Go CGO compilation. Acts as a command wrapper — pass a command as arguments and it runs with the correct environment.

```bash
# Run tests with SRT environment
./scripts/srt-env.sh go test ./...

# Build with SRT environment
./scripts/srt-env.sh go build ./cmd/mirror
```

### What It Does

1. Checks that `pkg-config` is installed
2. Checks that the SRT library is discoverable via `pkg-config`
3. Sets `CGO_CFLAGS` and `CGO_LDFLAGS` from `pkg-config --cflags srt` and `pkg-config --libs srt`
4. Appends common Homebrew (macOS) and system (Linux) paths to `PKG_CONFIG_PATH`
5. Validates flags are non-empty
6. Runs the provided command with `exec "$@"`

### Debug Mode

```bash
VERBOSE=1 ./scripts/srt-env.sh go test ./...
SRT_DEBUG=1 ./scripts/srt-env.sh go test ./...
```

Prints the configured `CGO_CFLAGS`, `CGO_LDFLAGS`, and `PKG_CONFIG_PATH` values.

### SRT Installation

If `pkg-config` can't find SRT, the script prints platform-specific installation instructions:
- **macOS**: `brew install srt`
- **Linux**: `sudo apt-get install libsrt-openssl-dev` or equivalent

## fix-newlines.sh

Ensures all text files in the repository end with a trailing newline (POSIX convention).

```bash
./scripts/fix-newlines.sh              # Fix all files
./scripts/fix-newlines.sh --dry-run    # Preview changes
./scripts/fix-newlines.sh --verbose    # Detailed output
```

### How It Works

1. Finds all tracked and untracked-but-not-ignored files via `git ls-files`
2. Identifies text files using a file-extension allowlist (`.go`, `.md`, `.yaml`, `.json`, `.sh`, etc.)
3. Falls back to the `file` command for ambiguous extensions
4. Appends a newline to files missing one

## Files

- `srt-env.sh`: SRT library environment setup and command wrapper
- `fix-newlines.sh`: Trailing newline fixer for text files
