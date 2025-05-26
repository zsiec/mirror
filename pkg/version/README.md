# Version Package

The version package provides version information and build metadata for the Mirror application. This package is designed to be imported by external applications and tools that need to query version information.

## Overview

The version package exposes build-time information including version numbers, git commit hashes, and build timestamps. This information is injected at build time using Go's `-ldflags` mechanism.

## Public API

### Version Information

```go
// GetVersion returns the application version
func GetVersion() string

// GetGitCommit returns the git commit hash
func GetGitCommit() string  

// GetBuildTime returns the build timestamp
func GetBuildTime() string

// GetFullVersion returns comprehensive version info
func GetFullVersion() VersionInfo
```

### VersionInfo Structure

```go
type VersionInfo struct {
    Version   string `json:"version"`
    GitCommit string `json:"git_commit"`
    BuildTime string `json:"build_time"`
    GoVersion string `json:"go_version"`
}
```

## Usage Examples

### Basic Version Queries

```go
import "github.com/zsiec/mirror/pkg/version"

// Get simple version string
ver := version.GetVersion()
fmt.Printf("Mirror v%s\n", ver)

// Get full version information
info := version.GetFullVersion()
fmt.Printf("Version: %s\n", info.Version)
fmt.Printf("Commit: %s\n", info.GitCommit)
fmt.Printf("Built: %s\n", info.BuildTime)
```

### JSON Serialization

```go
// Serialize version info to JSON
info := version.GetFullVersion()
data, err := json.Marshal(info)
if err != nil {
    log.Fatal(err)
}
fmt.Println(string(data))
```

### HTTP Endpoint Integration

```go
// Typical usage in HTTP handlers
func versionHandler(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(version.GetFullVersion())
}
```

## Build Integration

### Makefile Integration

The version information is typically injected during build:

```makefile
VERSION ?= $(shell git describe --tags --always)
GIT_COMMIT ?= $(shell git rev-parse HEAD)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "\
    -X github.com/zsiec/mirror/pkg/version.version=$(VERSION) \
    -X github.com/zsiec/mirror/pkg/version.gitCommit=$(GIT_COMMIT) \
    -X github.com/zsiec/mirror/pkg/version.buildTime=$(BUILD_TIME)"

build:
    go build $(LDFLAGS) -o bin/mirror ./cmd/mirror
```

### CI/CD Integration

In GitHub Actions or other CI systems:

```yaml
- name: Build with version info
  run: |
    VERSION=${GITHUB_REF#refs/tags/}
    GIT_COMMIT=${GITHUB_SHA}
    BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    
    go build -ldflags "\
      -X github.com/zsiec/mirror/pkg/version.version=$VERSION \
      -X github.com/zsiec/mirror/pkg/version.gitCommit=$GIT_COMMIT \
      -X github.com/zsiec/mirror/pkg/version.buildTime=$BUILD_TIME" \
      -o mirror ./cmd/mirror
```

## Default Values

When version information is not injected at build time, the package provides sensible defaults:

- **Version**: "dev" (indicates development build)
- **GitCommit**: "unknown" (no git information available)
- **BuildTime**: "unknown" (no build timestamp available)
- **GoVersion**: Retrieved from `runtime.Version()`

## Command Line Integration

The main application uses this package to implement version commands:

```bash
# Display version information
./mirror version

# Output version in JSON format  
./mirror version --json

# Show only version number
./mirror version --short
```

## Testing

The package includes comprehensive tests for:

### Version Information Accuracy
- Default values when no build info provided
- Proper handling of injected build-time values
- JSON serialization/deserialization

### Integration Testing
- Version endpoint HTTP responses
- Command-line version output
- Build system integration

```go
func TestVersionInfo(t *testing.T) {
    info := GetFullVersion()
    
    // Version should never be empty
    assert.NotEmpty(t, info.Version)
    
    // Go version should be detected
    assert.Contains(t, info.GoVersion, "go")
    
    // Should serialize to valid JSON
    data, err := json.Marshal(info)
    assert.NoError(t, err)
    assert.NotEmpty(t, data)
}
```

## Best Practices

### Version Format
- Use semantic versioning (e.g., "v1.2.3")
- Include pre-release identifiers for development (e.g., "v1.2.3-beta.1")
- Use git describe for automatic version generation

### Git Commit Format
- Use full SHA-1 hash for uniqueness
- Consider short hash (8 characters) for display purposes
- Include dirty flag for uncommitted changes

### Build Time Format
- Use ISO 8601 format (RFC 3339)
- Always use UTC timezone
- Include seconds for precision

### Error Handling
- Always provide fallback values
- Log warnings when version info is missing
- Don't fail application startup due to version issues

## Security Considerations

### Information Disclosure
The version package exposes build information that may be sensitive:
- Git commit hashes can reveal repository structure
- Build times can indicate release schedules
- Version numbers may expose vulnerability windows

### Recommendation
- Consider excluding detailed version info in production
- Use generic version strings for public-facing APIs
- Implement version info access controls if needed

## Performance Considerations

### Build-Time Injection
- Version information is embedded at compile time
- No runtime overhead for version queries
- No external dependencies or file reads

### Memory Usage
- Minimal memory footprint (< 1KB)
- All version strings are constants
- No dynamic allocation during queries

## Future Enhancements

Potential improvements for the version package:

- **Extended Metadata**: Include build environment, target platform
- **Dependency Versions**: Track major dependency versions
- **Feature Flags**: Include compile-time feature toggles
- **License Information**: Embed license and copyright info
- **Update Checking**: Support for version update notifications
