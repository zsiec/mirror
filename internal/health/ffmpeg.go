package health

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FFmpegChecker checks FFmpeg availability and functionality.
type FFmpegChecker struct {
	binaryPath string
	timeout    time.Duration
}

// NewFFmpegChecker creates a new FFmpeg health checker.
func NewFFmpegChecker(binaryPath string) *FFmpegChecker {
	if binaryPath == "" {
		// Try to find FFmpeg in PATH
		if path, err := exec.LookPath("ffmpeg"); err == nil {
			binaryPath = path
		}
	}
	
	return &FFmpegChecker{
		binaryPath: binaryPath,
		timeout:    5 * time.Second,
	}
}

// Name returns the name of the checker.
func (f *FFmpegChecker) Name() string {
	return "ffmpeg"
}

// Check performs the FFmpeg health check.
func (f *FFmpegChecker) Check(ctx context.Context) error {
	// Check 1: FFmpeg binary availability
	if err := f.checkBinary(ctx); err != nil {
		return fmt.Errorf("ffmpeg binary check failed: %w", err)
	}

	// Check 2: Essential codec availability
	if err := f.checkCodecs(ctx); err != nil {
		return fmt.Errorf("codec availability check failed: %w", err)
	}

	return nil
}

// checkBinary verifies FFmpeg binary is available and working
func (f *FFmpegChecker) checkBinary(ctx context.Context) error {
	if f.binaryPath == "" {
		return fmt.Errorf("ffmpeg binary not found in PATH")
	}

	// Check if binary exists and is executable
	if !filepath.IsAbs(f.binaryPath) {
		if _, err := exec.LookPath(f.binaryPath); err != nil {
			return fmt.Errorf("ffmpeg binary not executable: %w", err)
		}
	}

	// Create a context with timeout for the command
	cmdCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	// Run ffmpeg -version to verify it works
	cmd := exec.CommandContext(cmdCtx, f.binaryPath, "-version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ffmpeg version check failed: %w", err)
	}

	// Verify output contains expected information
	outputStr := string(output)
	if !strings.Contains(outputStr, "ffmpeg version") {
		return fmt.Errorf("unexpected ffmpeg version output")
	}

	return nil
}


// checkCodecs verifies essential video codecs are available
func (f *FFmpegChecker) checkCodecs(ctx context.Context) error {
	// Create a context with timeout for the command
	cmdCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	// Check available decoders
	cmd := exec.CommandContext(cmdCtx, f.binaryPath, "-decoders")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get decoder list: %w", err)
	}

	outputStr := string(output)
	
	// Check for essential video codecs
	requiredCodecs := []string{"h264", "hevc", "av1"}
	missingCodecs := []string{}

	for _, codec := range requiredCodecs {
		if !strings.Contains(outputStr, codec) {
			missingCodecs = append(missingCodecs, codec)
		}
	}

	if len(missingCodecs) > 0 {
		return fmt.Errorf("missing essential codecs: %v", missingCodecs)
	}

	return nil
}

// GetFFmpegInfo returns detailed information about FFmpeg installation
func (f *FFmpegChecker) GetFFmpegInfo(ctx context.Context) (map[string]interface{}, error) {
	info := make(map[string]interface{})
	
	// Binary path
	info["binary_path"] = f.binaryPath
	
	// FFmpeg version
	if version, err := f.getFFmpegVersion(ctx); err == nil {
		info["version"] = version
	}
	
	// FFmpeg binary availability
	info["ffmpeg_available"] = f.binaryPath != ""
	
	// Available hardware accelerators
	if hwAccel, err := f.getHardwareAccelerators(ctx); err == nil {
		info["hardware_accelerators"] = hwAccel
	}
	
	// Available codecs
	if codecs, err := f.getAvailableCodecs(ctx); err == nil {
		info["codecs"] = codecs
	}
	
	return info, nil
}

func (f *FFmpegChecker) getFFmpegVersion(ctx context.Context) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, f.binaryPath, "-version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}
	
	return "", fmt.Errorf("no version information found")
}

func (f *FFmpegChecker) getHardwareAccelerators(ctx context.Context) ([]string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, f.binaryPath, "-hwaccels")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\n")
	accelerators := []string{}
	
	// Skip header lines and parse accelerator names
	for i, line := range lines {
		if i < 2 { // Skip header
			continue
		}
		line = strings.TrimSpace(line)
		if line != "" {
			accelerators = append(accelerators, line)
		}
	}
	
	return accelerators, nil
}

func (f *FFmpegChecker) getAvailableCodecs(ctx context.Context) (map[string][]string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, f.binaryPath, "-codecs")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	codecs := map[string][]string{
		"video_decoders": []string{},
		"audio_decoders": []string{},
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 8 {
			continue
		}
		
		// Parse codec line format: " DEV.LS h264                 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10"
		if line[0] == ' ' && len(line) > 8 {
			flags := line[1:7]
			if strings.Contains(flags, "D") { // Decoder available
				parts := strings.Fields(line[7:])
				if len(parts) > 0 {
					codecName := parts[0]
					if strings.Contains(flags, "V") { // Video codec
						codecs["video_decoders"] = append(codecs["video_decoders"], codecName)
					} else if strings.Contains(flags, "A") { // Audio codec
						codecs["audio_decoders"] = append(codecs["audio_decoders"], codecName)
					}
				}
			}
		}
	}
	
	return codecs, nil
}
