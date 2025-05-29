package health

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewFFmpegChecker(t *testing.T) {
	tests := []struct {
		name        string
		binaryPath  string
		description string
	}{
		{
			name:        "with explicit path",
			binaryPath:  "/usr/bin/ffmpeg",
			description: "should use provided path",
		},
		{
			name:        "with empty path",
			binaryPath:  "",
			description: "should try to find ffmpeg in PATH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewFFmpegChecker(tt.binaryPath)
			assert.NotNil(t, checker, tt.description)
			assert.Equal(t, 5*time.Second, checker.timeout, "timeout should be 5 seconds")

			if tt.binaryPath != "" {
				assert.Equal(t, tt.binaryPath, checker.binaryPath, "binary path should match")
			}
		})
	}
}

func TestFFmpegChecker_Name(t *testing.T) {
	checker := NewFFmpegChecker("")
	assert.Equal(t, "ffmpeg", checker.Name(), "name should be 'ffmpeg'")
}

func TestFFmpegChecker_Check(t *testing.T) {
	tests := []struct {
		name        string
		binaryPath  string
		shouldError bool
		description string
	}{
		{
			name:        "non-existent binary",
			binaryPath:  "/nonexistent/ffmpeg",
			shouldError: true,
			description: "should fail with non-existent binary",
		},
		{
			name:        "empty binary path",
			binaryPath:  "",
			shouldError: true,
			description: "should fail with empty binary path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &FFmpegChecker{
				binaryPath: tt.binaryPath,
				timeout:    1 * time.Second,
			}

			ctx := context.Background()
			err := checker.Check(ctx)

			if tt.shouldError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

func TestFFmpegChecker_checkBinary(t *testing.T) {
	tests := []struct {
		name        string
		binaryPath  string
		shouldError bool
		description string
	}{
		{
			name:        "empty path",
			binaryPath:  "",
			shouldError: true,
			description: "should fail with empty binary path",
		},
		{
			name:        "non-existent absolute path",
			binaryPath:  "/nonexistent/ffmpeg",
			shouldError: true,
			description: "should fail with non-existent absolute path",
		},
		{
			name:        "non-existent relative path",
			binaryPath:  "nonexistent-ffmpeg",
			shouldError: true,
			description: "should fail with non-existent relative path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &FFmpegChecker{
				binaryPath: tt.binaryPath,
				timeout:    1 * time.Second,
			}

			ctx := context.Background()
			err := checker.checkBinary(ctx)

			if tt.shouldError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

func TestFFmpegChecker_checkCodecs(t *testing.T) {
	checker := &FFmpegChecker{
		binaryPath: "/nonexistent/ffmpeg",
		timeout:    1 * time.Second,
	}

	ctx := context.Background()
	err := checker.checkCodecs(ctx)

	// Should fail because ffmpeg doesn't exist
	assert.Error(t, err, "should fail with non-existent ffmpeg binary")
	assert.Contains(t, err.Error(), "failed to get decoder list", "error should mention decoder list failure")
}

func TestFFmpegChecker_GetFFmpegInfo(t *testing.T) {
	checker := &FFmpegChecker{
		binaryPath: "/nonexistent/ffmpeg",
		timeout:    1 * time.Second,
	}

	ctx := context.Background()
	info, err := checker.GetFFmpegInfo(ctx)

	// Should not return an error, but info should contain basic data
	assert.NoError(t, err, "GetFFmpegInfo should not return error")
	assert.NotNil(t, info, "info should not be nil")

	// Check basic fields
	assert.Equal(t, "/nonexistent/ffmpeg", info["binary_path"], "binary path should be set")
	assert.Equal(t, true, info["ffmpeg_available"], "ffmpeg_available is true when binary path is set")
}

func TestFFmpegChecker_getFFmpegVersion(t *testing.T) {
	checker := &FFmpegChecker{
		binaryPath: "/nonexistent/ffmpeg",
		timeout:    1 * time.Second,
	}

	ctx := context.Background()
	version, err := checker.getFFmpegVersion(ctx)

	assert.Error(t, err, "should fail with non-existent binary")
	assert.Empty(t, version, "version should be empty on error")
}

func TestFFmpegChecker_getHardwareAccelerators(t *testing.T) {
	checker := &FFmpegChecker{
		binaryPath: "/nonexistent/ffmpeg",
		timeout:    1 * time.Second,
	}

	ctx := context.Background()
	accelerators, err := checker.getHardwareAccelerators(ctx)

	assert.Error(t, err, "should fail with non-existent binary")
	assert.Nil(t, accelerators, "accelerators should be nil on error")
}

func TestFFmpegChecker_getAvailableCodecs(t *testing.T) {
	checker := &FFmpegChecker{
		binaryPath: "/nonexistent/ffmpeg",
		timeout:    1 * time.Second,
	}

	ctx := context.Background()
	codecs, err := checker.getAvailableCodecs(ctx)

	assert.Error(t, err, "should fail with non-existent binary")
	assert.Nil(t, codecs, "codecs should be nil on error")
}

// Test with a mock ffmpeg script (if we can create one)
func TestFFmpegChecker_WithMockBinary(t *testing.T) {
	// Create a temporary mock ffmpeg script
	tempDir := t.TempDir()
	mockFFmpeg := filepath.Join(tempDir, "ffmpeg")

	// Create a simple shell script that mimics ffmpeg behavior
	mockScript := `#!/bin/bash
case "$1" in
    "-version")
        echo "ffmpeg version 4.4.0"
        exit 0
        ;;
    "-decoders")
        echo "Decoders:"
        echo " V..... h264                 H.264 / AVC"
        echo " V..... hevc                 H.265 / HEVC"
        echo " V..... av1                  AOM AV1"
        exit 0
        ;;
    "-hwaccels")
        echo "Hardware acceleration methods:"
        echo "cuda"
        exit 0
        ;;
    "-codecs")
        echo " DEV.L. h264                 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10"
        echo " DEV.L. hevc                 H.265 / HEVC (High Efficiency Video Coding)"
        echo " DEA.L. aac                  AAC (Advanced Audio Coding)"
        exit 0
        ;;
    *)
        exit 1
        ;;
esac`

	err := os.WriteFile(mockFFmpeg, []byte(mockScript), 0755)
	if err != nil {
		t.Skip("Cannot create mock ffmpeg script:", err)
	}

	checker := NewFFmpegChecker(mockFFmpeg)

	t.Run("successful check", func(t *testing.T) {
		ctx := context.Background()
		err := checker.Check(ctx)
		assert.NoError(t, err, "check should succeed with mock ffmpeg")
	})

	t.Run("get version", func(t *testing.T) {
		ctx := context.Background()
		version, err := checker.getFFmpegVersion(ctx)
		assert.NoError(t, err, "should get version successfully")
		assert.Contains(t, version, "ffmpeg version", "version should contain expected text")
	})

	t.Run("get hardware accelerators", func(t *testing.T) {
		ctx := context.Background()
		accelerators, err := checker.getHardwareAccelerators(ctx)
		assert.NoError(t, err, "should get accelerators successfully")
		assert.NotNil(t, accelerators, "accelerators should not be nil")
	})

	t.Run("get available codecs", func(t *testing.T) {
		ctx := context.Background()
		codecs, err := checker.getAvailableCodecs(ctx)
		assert.NoError(t, err, "should get codecs successfully")
		assert.NotNil(t, codecs, "codecs should not be nil")
		assert.Contains(t, codecs, "video_decoders", "should have video_decoders key")
		assert.Contains(t, codecs, "audio_decoders", "should have audio_decoders key")
	})

	t.Run("get ffmpeg info", func(t *testing.T) {
		ctx := context.Background()
		info, err := checker.GetFFmpegInfo(ctx)
		assert.NoError(t, err, "should get info successfully")
		assert.Equal(t, mockFFmpeg, info["binary_path"], "binary path should match")
		assert.Equal(t, true, info["ffmpeg_available"], "ffmpeg should be available")
		assert.NotNil(t, info["version"], "version should be present")
		assert.NotNil(t, info["hardware_accelerators"], "hardware accelerators should be present")
		assert.NotNil(t, info["codecs"], "codecs should be present")
	})
}

func TestFFmpegChecker_ContextCancellation(t *testing.T) {
	// Test with a very short timeout to ensure context cancellation works
	checker := &FFmpegChecker{
		binaryPath: "sleep", // Use sleep command to simulate a long-running process
		timeout:    1 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	err := checker.checkBinary(ctx)
	assert.Error(t, err, "should fail due to context timeout")
}

func TestFFmpegChecker_AbsolutePath(t *testing.T) {
	// Test with absolute path that doesn't exist
	checker := &FFmpegChecker{
		binaryPath: "/this/path/definitely/does/not/exist/ffmpeg",
		timeout:    1 * time.Second,
	}

	ctx := context.Background()
	err := checker.checkBinary(ctx)
	assert.Error(t, err, "should fail with non-existent absolute path")
}

func TestFFmpegChecker_PathLookup(t *testing.T) {
	// Test path lookup functionality
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skip("echo command not available in PATH")
	}

	// Create checker that will look for a command in PATH
	checker := &FFmpegChecker{
		binaryPath: "echo",
		timeout:    1 * time.Second,
	}

	ctx := context.Background()

	// This should pass the binary check but fail at version check
	// because echo won't output "ffmpeg version"
	err := checker.checkBinary(ctx)
	assert.Error(t, err, "should fail at version validation")
	assert.Contains(t, err.Error(), "unexpected ffmpeg version output", "should mention version output issue")
}
