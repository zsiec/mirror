package version

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetInfo(t *testing.T) {
	info := GetInfo()

	assert.Equal(t, Version, info.Version)
	assert.Equal(t, GitCommit, info.GitCommit)
	assert.Equal(t, BuildTime, info.BuildTime)
	assert.Equal(t, runtime.Version(), info.GoVersion)
	assert.Equal(t, runtime.GOOS, info.OS)
	assert.Equal(t, runtime.GOARCH, info.Arch)
}

func TestInfoString(t *testing.T) {
	info := Info{
		Version:   "1.0.0",
		GitCommit: "abc123",
		BuildTime: "2024-01-01",
		GoVersion: "go1.21",
		OS:        "linux",
		Arch:      "amd64",
	}

	str := info.String()
	assert.Contains(t, str, "Mirror 1.0.0")
	assert.Contains(t, str, "commit: abc123")
	assert.Contains(t, str, "built: 2024-01-01")
	assert.Contains(t, str, "go: go1.21")
	assert.Contains(t, str, "os/arch: linux/amd64")
}

func TestInfoShort(t *testing.T) {
	info := Info{
		Version: "1.0.0",
	}

	short := info.Short()
	assert.Equal(t, "Mirror 1.0.0", short)
}

func TestVersionVariables(t *testing.T) {
	// Test that version variables are set (even if to default values)
	assert.NotEmpty(t, Version)
	assert.NotEmpty(t, GitCommit)
	assert.NotEmpty(t, BuildTime)
	assert.NotEmpty(t, GoVersion)
	assert.NotEmpty(t, OS)
	assert.NotEmpty(t, Arch)

	// GoVersion should start with "go"
	assert.True(t, strings.HasPrefix(GoVersion, "go"))
}