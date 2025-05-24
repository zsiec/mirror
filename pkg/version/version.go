package version

import (
	"fmt"
	"runtime"
)

// Build information. These variables are set at build time using ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
	GoVersion = runtime.Version()
	OS        = runtime.GOOS
	Arch      = runtime.GOARCH
)

// Info contains version information.
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// GetInfo returns the version information.
func GetInfo() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildTime: BuildTime,
		GoVersion: GoVersion,
		OS:        OS,
		Arch:      Arch,
	}
}

// String returns the version string.
func (i Info) String() string {
	return fmt.Sprintf("Mirror %s (commit: %s, built: %s, go: %s, os/arch: %s/%s)",
		i.Version, i.GitCommit, i.BuildTime, i.GoVersion, i.OS, i.Arch)
}

// Short returns a short version string.
func (i Info) Short() string {
	return fmt.Sprintf("Mirror %s", i.Version)
}