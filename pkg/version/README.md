# Version Package

The `version` package provides build-time version information for the Mirror application.

## API

### GetInfo

```go
info := version.GetInfo()  // returns Info struct
```

### Info Struct

```go
type Info struct {
    Version   string `json:"version"`
    GitCommit string `json:"git_commit"`
    BuildTime string `json:"build_time"`
    GoVersion string `json:"go_version"`
    OS        string `json:"os"`
    Arch      string `json:"arch"`
}

func (i Info) String() string  // "Mirror 1.0.0 (commit: abc123, built: ..., go: ..., os/arch: darwin/arm64)"
func (i Info) Short() string   // "Mirror 1.0.0"
```

## Build-Time Injection

Version variables are set via `-ldflags` during build:

```go
var (
    Version   = "dev"                // set via ldflags
    GitCommit = "unknown"            // set via ldflags
    BuildTime = "unknown"            // set via ldflags
    GoVersion = runtime.Version()    // auto-detected
    OS        = runtime.GOOS         // auto-detected
    Arch      = runtime.GOARCH       // auto-detected
)
```

The Makefile injects these automatically:

```makefile
LDFLAGS := -ldflags "\
    -X github.com/zsiec/mirror/pkg/version.Version=$(VERSION) \
    -X github.com/zsiec/mirror/pkg/version.GitCommit=$(GIT_COMMIT) \
    -X github.com/zsiec/mirror/pkg/version.BuildTime=$(BUILD_TIME)"
```

## Usage

```go
import "github.com/zsiec/mirror/pkg/version"

// Get full version info
info := version.GetInfo()
fmt.Println(info.String())

// JSON serialization (e.g., for /version endpoint)
data, _ := json.Marshal(info)

// Access individual fields
fmt.Println(version.Version)    // "dev" or "v1.0.0"
fmt.Println(version.GitCommit)  // "abc1234"
```

## Default Values

When not injected at build time:
- **Version**: `"dev"`
- **GitCommit**: `"unknown"`
- **BuildTime**: `"unknown"`
- **GoVersion**: Auto-detected from `runtime.Version()`
- **OS**: Auto-detected from `runtime.GOOS`
- **Arch**: Auto-detected from `runtime.GOARCH`

## Files

- `version.go`: Info struct, GetInfo(), String(), Short(), package-level variables
- `version_test.go`: Tests
