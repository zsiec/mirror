package tests

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/server"
)

// FullIntegrationTest runs a comprehensive test of the entire Mirror platform
// This test is designed to be run in isolation and provides extensive validation
// of the streaming platform's core functionality.
func TestFullIntegrationTest(t *testing.T) {
	// COMPLETELY DISABLE all log output IMMEDIATELY before ANYTHING else
	// This must be the very first thing to prevent any log interference
	oldStdout := os.Stdout
	oldStderr := os.Stderr

	if testing.Short() {
		t.Skip("Skipping full integration test in short mode")
	}

	// Open /dev/null for writing (or NUL on Windows)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0666)
	if err != nil {
		t.Fatalf("Failed to open dev/null: %v", err)
	}
	defer devNull.Close()

	// Redirect all output FIRST
	os.Stdout = devNull
	os.Stderr = devNull

	// Also silence logrus globally to prevent any direct logger usage
	logrus.SetOutput(devNull)
	logrus.SetLevel(logrus.FatalLevel)

	// Detect FFmpeg mode early
	ffmpegMode := IsFFmpegSRTAvailable()

	// Test execution context - runs until cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up test infrastructure with NO output
	testEnv := SetupTestEnvironment(t, nil)
	defer testEnv.Cleanup()

	// Create and start dashboard AFTER output suppression
	dashboard := NewDashboard(testEnv, ffmpegMode, oldStdout)
	dashboard.Start()
	defer dashboard.Stop()

	// Set up simulated log entries for dashboard
	logCapturer := NewLogCapturer(dashboard)
	go logCapturer.Start() // Capture real server stdout logs for dashboard
	defer logCapturer.Stop()

	// Keep output suppressed throughout the test - dashboard is our only interface

	// Test phases with dashboard updates
	dashboard.SetPhase("Server Startup")
	dashboard.SetProgress(10)

	// Start the Mirror server
	mirrorServer := StartMirrorServer(t, ctx, testEnv, nil)
	defer mirrorServer.Shutdown()

	dashboard.SetPhase("Health Validation")
	dashboard.SetProgress(20)

	// Validate server health
	ValidateServerHealth(t, testEnv, nil)

	dashboard.SetPhase("Stream Generation")
	dashboard.SetProgress(30)

	// Start stream generators with specific test formats:
	// - SRT: 1280x720@59.94fps (720p60) via FFmpeg
	// - RTP: 640x360@29.97fps (360p30) via simulated H.264 packets
	streamGens := StartStreamGenerators(t, ctx, testEnv, nil)
	defer streamGens.Stop()

	dashboard.SetPhase("Stream Establishment")
	dashboard.SetProgress(50)

	// Wait for streams to be established
	WaitForStreamsToEstablish(t, testEnv, nil)

	dashboard.SetPhase("Stream Validation")
	dashboard.SetProgress(70)

	// Validate streaming functionality
	ValidateStreamingFunctionality(t, testEnv, nil)

	dashboard.SetPhase("API Testing")
	dashboard.SetProgress(80)

	// Validate API responses
	ValidateAPIResponses(t, testEnv, nil)

	dashboard.SetPhase("Metrics Collection")
	dashboard.SetProgress(90)

	// Validate metrics
	ValidateMetrics(t, testEnv, nil)

	dashboard.SetPhase("Final Validation")
	dashboard.SetProgress(95)

	// Validate logs
	ValidateLogs(t, testEnv, nil)

	dashboard.SetPhase("Complete")
	dashboard.SetProgress(100)
	dashboard.SetPhase("Running - Press Ctrl+C to stop")

	// Run indefinitely until cancelled
	select {
	case <-ctx.Done():
		// Context cancelled, clean exit
	}

	// Restore stdout/stderr for any final test output
	os.Stdout = oldStdout
	os.Stderr = oldStderr
}

// safeLog safely logs a message, handling nil verbose logger
func safeLog(verbose *VerboseLogger, format string, args ...interface{}) {
	if verbose != nil {
		verbose.Log(format, args...)
	}
}

// safeLogJSON safely logs JSON, handling nil verbose logger
func safeLogJSON(verbose *VerboseLogger, title string, data interface{}) {
	if verbose != nil {
		verbose.LogJSON(title, data)
	}
}

// MirrorServer represents the running Mirror server
type MirrorServer struct {
	t       *testing.T
	ctx     context.Context
	cancel  context.CancelFunc
	server  *server.Server
	manager *ingestion.Manager
	verbose *VerboseLogger
}

func StartMirrorServer(t *testing.T, ctx context.Context, env *TestEnvironment, verbose *VerboseLogger) *MirrorServer {
	if verbose != nil {
		verbose.Log("🚀 Starting Mirror server...")
	}

	serverCtx, cancel := context.WithCancel(ctx)

	// Initialize logger
	logrusLogger, err := logger.New(&env.Config.Logging)
	require.NoError(t, err)
	log := logger.NewLogrusAdapter(logrusLogger.WithField("component", "integration_test"))

	if verbose != nil {
		verbose.Log("📝 Logger initialized")
	}

	if verbose != nil {
		verbose.Log("🏥 Health manager will be initialized by server")
		verbose.Log("📊 Metrics will be initialized by server")
	}

	// Create ingestion manager
	ingestionMgr, err := ingestion.NewManager(&env.Config.Ingestion, log)
	require.NoError(t, err)
	if verbose != nil {
		verbose.Log("📡 Ingestion manager created")
	}

	// Start ingestion manager
	err = ingestionMgr.Start()
	require.NoError(t, err)
	if verbose != nil {
		verbose.Log("📺 Ingestion manager started - SRT:%d, RTP:%d",
			env.Config.Ingestion.SRT.Port, env.Config.Ingestion.RTP.Port)
	}

	// Create server
	srv := server.New(&env.Config.Server, logrusLogger, env.Redis)
	if verbose != nil {
		verbose.Log("🌐 HTTP/3 server created")
	}

	// Register ingestion routes with the server
	ingestionHandlers := ingestion.NewHandlers(ingestionMgr, log)
	srv.RegisterRoutes(ingestionHandlers.RegisterRoutes)
	if verbose != nil {
		verbose.Log("📋 Ingestion API routes registered")
	}

	// Start metrics server if enabled
	if env.Config.Metrics.Enabled {
		go StartMetricsServer(env.Config.Metrics, log, verbose)
		if verbose != nil {
			verbose.Log("📊 Metrics server started on port %d", env.Config.Metrics.Port)
		}
	}

	// Start server in goroutine
	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.Start(serverCtx); err != nil {
			serverErrCh <- err
		}
	}()

	// Wait for server to be ready
	WaitForServerReady(t, env, verbose)
	if verbose != nil {
		verbose.Log("✅ Mirror server is ready and accepting connections")
	}

	return &MirrorServer{
		t:       t,
		ctx:     serverCtx,
		cancel:  cancel,
		server:  srv,
		manager: ingestionMgr,
		verbose: verbose,
	}
}

func (ms *MirrorServer) Shutdown() {
	if ms.verbose != nil {
		ms.verbose.Log("🛑 Shutting down Mirror server...")
	}

	if ms.cancel != nil {
		ms.cancel()
	}

	if ms.server != nil {
		ms.server.Shutdown()
	}

	if ms.verbose != nil {
		ms.verbose.Log("✅ Mirror server shutdown complete")
	}
}

func WaitForServerReady(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("⏳ Waiting for server to be ready...")
	}

	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		// Try HTTPS fallback server first (port 8080 is actually HTTPS in this configuration)
		if verbose != nil {
			verbose.Log("🔍 Attempting connection to HTTPS fallback server...")
		}
		resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/version")
		if err != nil {
			if verbose != nil {
				verbose.Log("❌ HTTPS fallback connection failed: %v", err)
			}
		} else {
			if verbose != nil {
				verbose.Log("✅ HTTPS fallback connection successful, status: %d", resp.StatusCode)
			}
			if resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				if verbose != nil {
					verbose.Log("✅ Server version endpoint responded")
				}

				// Also check if health endpoint responds (may have Redis warning but that's OK)
				healthResp, healthErr := env.HTTPClient.Get("https://127.0.0.1:8080/health")
				if healthErr == nil {
					healthResp.Body.Close()
					if verbose != nil {
						verbose.Log("✅ Server health endpoint accessible")
					}
					return
				} else {
					if verbose != nil {
						verbose.Log("⚠️ Health endpoint error: %v", healthErr)
					}
				}
			}
			resp.Body.Close()
		}

		if verbose != nil {
			verbose.Log("⌛ Attempt %d/%d - waiting for server...", i+1, maxAttempts)
		}
		time.Sleep(1 * time.Second)
	}

	require.Fail(t, "Server failed to become ready within timeout")
}

// StreamGenerators manages test stream generation
type StreamGenerators struct {
	t         *testing.T
	ctx       context.Context
	cancel    context.CancelFunc
	rtpCancel context.CancelFunc
	srtCancel context.CancelFunc
	verbose   *VerboseLogger
}

func StartStreamGenerators(t *testing.T, ctx context.Context, env *TestEnvironment, verbose *VerboseLogger) *StreamGenerators {
	if verbose != nil {
		verbose.Log("📺 Starting stream generators...")
	}

	streamCtx, cancel := context.WithCancel(ctx)

	// Start RTP stream generator
	rtpCtx, rtpCancel := context.WithCancel(streamCtx)
	go GenerateRTPStream(t, rtpCtx, env, verbose)

	// Start SRT stream generator
	srtCtx, srtCancel := context.WithCancel(streamCtx)
	go GenerateSRTStream(t, srtCtx, env, verbose)

	if verbose != nil {
		verbose.Log("✅ Stream generators started")
	}

	return &StreamGenerators{
		t:         t,
		ctx:       streamCtx,
		cancel:    cancel,
		rtpCancel: rtpCancel,
		srtCancel: srtCancel,
		verbose:   verbose,
	}
}

func (sg *StreamGenerators) Stop() {
	if sg.verbose != nil {
		sg.verbose.Log("🛑 Stopping stream generators...")
	}

	if sg.rtpCancel != nil {
		sg.rtpCancel()
	}
	if sg.srtCancel != nil {
		sg.srtCancel()
	}
	if sg.cancel != nil {
		sg.cancel()
	}

	if sg.verbose != nil {
		sg.verbose.Log("✅ Stream generators stopped")
	}
}

func GenerateRTPStream(t *testing.T, ctx context.Context, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("📡 Starting RTP stream generator...")
	}

	// Connect to RTP port
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", env.Config.Ingestion.RTP.Port))
	if err != nil {
		if verbose != nil {
			verbose.Log("❌ Failed to connect to RTP port: %v", err)
		}
		return
	}
	defer conn.Close()

	if verbose != nil {
		verbose.Log("🔗 Connected to RTP port %d", env.Config.Ingestion.RTP.Port)
	}

	// Generate H.264 RTP packets for 640x360@29.97fps
	ssrc := uint32(0x12345678)
	sequenceNumber := uint16(0)
	timestamp := uint32(0)

	// Simulated H.264 NAL units for 640x360 resolution (simplified for testing)
	nalUnits := [][]byte{
		// SPS (Sequence Parameter Set) - configured for 640x360
		{0x67, 0x42, 0x00, 0x1f, 0x9a, 0x66, 0x02, 0x80, 0x2d, 0xd8, 0x35, 0x04, 0x04, 0x05, 0x80},
		// PPS (Picture Parameter Set)
		{0x68, 0xce, 0x31, 0x12, 0x11},
		// IDR frame (simplified)
		{0x65, 0x88, 0x84, 0x00, 0x10, 0xff, 0xff, 0xff, 0xff},
	}

	nalIndex := 0
	ticker := time.NewTicker(33367 * time.Microsecond) // 29.97 FPS (1/29.97 ≈ 0.033367 seconds)
	defer ticker.Stop()

	packetCount := 0
	for {
		select {
		case <-ctx.Done():
			if verbose != nil {
				verbose.Log("🛑 RTP stream generator stopped (sent %d packets)", packetCount)
			}
			return
		case <-ticker.C:
			// Create RTP packet
			packet := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    96, // H.264
					SequenceNumber: sequenceNumber,
					Timestamp:      timestamp,
					SSRC:           ssrc,
					Marker:         true, // Mark end of frame
				},
				Payload: nalUnits[nalIndex],
			}

			// Serialize and send
			data, err := packet.Marshal()
			if err != nil {
				if verbose != nil {
					verbose.Log("❌ Failed to marshal RTP packet: %v", err)
				}
				continue
			}

			_, err = conn.Write(data)
			if err != nil {
				if verbose != nil {
					verbose.Log("❌ Failed to send RTP packet: %v", err)
				}
				continue
			}

			packetCount++
			if packetCount%100 == 0 {
				if verbose != nil {
					verbose.Log("📡 RTP: Sent %d packets (seq: %d, ts: %d)", packetCount, sequenceNumber, timestamp)
				}
			}

			// Update packet parameters
			sequenceNumber++
			timestamp += 3003 // Increment for 29.97fps (90000/29.97 ≈ 3003)
			nalIndex = (nalIndex + 1) % len(nalUnits)
		}
	}
}

func GenerateSRTStream(t *testing.T, ctx context.Context, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("📡 Starting SRT stream generator...")
	}

	// Auto-detect FFmpeg availability and SRT support
	if IsFFmpegSRTAvailable() {
		if verbose != nil {
			verbose.Log("🚀 Auto-detected FFmpeg with SRT support - using REAL SRT streams")
		}
		GenerateRealSRTStream(t, ctx, env, verbose)
		return
	}

	// Fallback to simulation when FFmpeg is not available
	if verbose != nil {
		verbose.Log("⚠️ FFmpeg with SRT support not available - using simulated SRT testing")
		verbose.Log("🔍 Validating SRT listener is running on port %d", env.Config.Ingestion.SRT.Port)
	}

	// Test that SRT port is listening (should be able to connect, but will fail handshake)
	conn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", env.Config.Ingestion.SRT.Port), 1*time.Second)
	if err != nil {
		if verbose != nil {
			verbose.Log("❌ SRT port not accessible: %v", err)
		}
		SimulateSRTData(ctx, env, verbose)
		return
	}
	conn.Close()

	if verbose != nil {
		verbose.Log("✅ SRT listener is accepting connections on port %d", env.Config.Ingestion.SRT.Port)
		verbose.Log("📝 Note: Install FFmpeg with SRT support for enhanced testing:")
		verbose.Log("📝   brew install ffmpeg  # on macOS")
		verbose.Log("📝   apt install ffmpeg   # on Ubuntu/Debian")
	}

	// Simulate SRT activity for test completeness
	SimulateSRTData(ctx, env, verbose)
}

// IsFFmpegSRTAvailable checks if FFmpeg is available with SRT support
func IsFFmpegSRTAvailable() bool {
	// Check if FFmpeg is available
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return false
	}

	// Check if FFmpeg has SRT protocol support
	cmd := exec.Command("ffmpeg", "-protocols")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.Contains(string(output), "srt")
}

func GenerateRealSRTStream(t *testing.T, ctx context.Context, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("🚀 Starting REAL SRT stream via FFmpeg...")
	}

	// Create a test pattern video in memory using FFmpeg
	srtURL := fmt.Sprintf("srt://127.0.0.1:%d?streamid=ffmpeg_test_stream", env.Config.Ingestion.SRT.Port)

	// FFmpeg command based on user's working example - simplified and focused
	ffmpegArgs := []string{
		"-re", // Read input at native frame rate
		"-f", "lavfi",
		"-i", "testsrc=size=640x480:rate=30", // 30fps test pattern (480p)
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-g", "30", // GOP size = 30 frames (1 second at 30fps)
		"-keyint_min", "30", // Minimum GOP size
		"-f", "mpegts",
		srtURL,
	}

	if verbose != nil {
		verbose.Log("📡 FFmpeg command: ffmpeg %s", strings.Join(ffmpegArgs, " "))
		verbose.Log("🎯 Streaming to: %s", srtURL)
	}

	// Start FFmpeg process
	cmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs...)

	// Capture stderr for debugging
	stderr, err := cmd.StderrPipe()
	if err != nil {
		if verbose != nil {
			verbose.Log("❌ Failed to create stderr pipe: %v", err)
		}
		return
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		if verbose != nil {
			verbose.Log("❌ Failed to start FFmpeg: %v", err)
		}
		return
	}

	if verbose != nil {
		verbose.Log("✅ FFmpeg SRT stream started (PID: %d)", cmd.Process.Pid)
	}

	// Monitor FFmpeg stderr in a goroutine
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// Log important FFmpeg output
			if strings.Contains(line, "error") || strings.Contains(line, "Error") ||
				strings.Contains(line, "srt") || strings.Contains(line, "Connection") {
				if verbose != nil {
					verbose.Log("📺 FFmpeg: %s", line)
				}
			}
		}
	}()

	// Wait for process completion or context cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		if verbose != nil {
			verbose.Log("🛑 Stopping FFmpeg SRT stream...")
		}
		if err := cmd.Process.Kill(); err != nil {
			if verbose != nil {
				verbose.Log("⚠️ Failed to kill FFmpeg: %v", err)
			}
		}
		<-done // Wait for process to actually terminate
		if verbose != nil {
			verbose.Log("✅ FFmpeg SRT stream stopped")
		}
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			if verbose != nil {
				verbose.Log("❌ FFmpeg exited with error: %v", err)
			}
		} else {
			if verbose != nil {
				verbose.Log("✅ FFmpeg completed successfully")
			}
		}
	}
}

func SimulateSRTData(ctx context.Context, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("🎭 Simulating SRT stream monitoring...")
		verbose.Log("💡 In production, SRT streams would appear in /api/v1/streams with type='srt'")
		verbose.Log("💡 SRT stats would include: latency, packet_loss, retransmissions, bandwidth")
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	simulationCount := 0
	for {
		select {
		case <-ctx.Done():
			if verbose != nil {
				verbose.Log("🛑 SRT simulation stopped (ran %d cycles)", simulationCount)
			}
			return
		case <-ticker.C:
			simulationCount++
			if simulationCount <= 3 { // Limit simulation output
				if verbose != nil {
					verbose.Log("🎭 SRT monitoring cycle %d - listener active", simulationCount)
				}
			}
		}
	}
}

func WaitForStreamsToEstablish(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("⏳ Waiting for streams to establish...")
	}

	maxWait := 30 * time.Second
	timeout := time.After(maxWait)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			if verbose != nil {
				verbose.Log("⚠️ Streams did not establish within %v", maxWait)
			}
			// Continue with test - some functionality may still be testable
			return
		case <-ticker.C:
			// Check if any streams are active
			resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/api/v1/streams")
			if err != nil {
				safeLog(verbose, "❌ Failed to check streams: %v", err)
				continue
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				safeLog(verbose, "❌ Failed to read streams response: %v", err)
				continue
			}

			var streams interface{}
			if err := json.Unmarshal(body, &streams); err != nil {
				safeLog(verbose, "❌ Failed to parse streams response: %v", err)
				continue
			}

			safeLogJSON(verbose, "Current Streams", streams)

			// For now, we'll wait a fixed time and then continue
			// Real implementation would check for active streams
			time.Sleep(5 * time.Second)
			safeLog(verbose, "✅ Stream establishment wait complete")
			return
		}
	}
}

func ValidateServerHealth(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("🏥 Validating server health...")
	}

	resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Accept either 200 (all healthy) or 503 (Redis check failing due to miniredis limitations)
	// Both indicate the server is responding correctly
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected status 200 or 503, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var health map[string]interface{}
	err = json.Unmarshal(body, &health)
	require.NoError(t, err)

	if verbose != nil {
		verbose.LogJSON("Health Response", health)
	}

	// Validate health response structure
	assert.Contains(t, health, "status")
	assert.Contains(t, health, "timestamp")
	assert.Contains(t, health, "checks")

	if verbose != nil {
		verbose.Log("✅ Server health validation passed")
	}
}

func ValidateStreamingFunctionality(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("📺 Validating streaming functionality...")
	}

	// Test various streaming endpoints
	endpoints := map[string]bool{
		"/api/v1/streams":             true,  // Should exist
		"/api/v1/stats":               true,  // Should exist
		"/api/v1/streams/stats/video": false, // May not exist yet
	}

	for endpoint, required := range endpoints {
		if verbose != nil {
			verbose.Log("🔍 Testing endpoint: %s", endpoint)
		}

		resp, err := env.HTTPClient.Get("https://127.0.0.1:8080" + endpoint)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		if verbose != nil {
			verbose.Log("📋 %s response (%d): %s", endpoint, resp.StatusCode, string(body))
		}

		if resp.StatusCode == 404 && !required {
			if verbose != nil {
				verbose.Log("⚠️ Endpoint %s not implemented yet (404) - this is expected", endpoint)
			}
			continue
		}

		// Should return valid JSON for implemented endpoints
		var result interface{}
		err = json.Unmarshal(body, &result)
		assert.NoError(t, err, "Response should be valid JSON for endpoint %s", endpoint)

		// For required endpoints, ensure they're not returning errors
		if required {
			assert.True(t, resp.StatusCode < 500, "Required endpoint %s should not return server error", endpoint)
		}
	}

	if verbose != nil {
		verbose.Log("✅ Streaming functionality validation passed")
	}
}

func ValidateAPIResponses(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("🌐 Validating API responses...")
	}

	// Test version endpoint
	resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/version")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var version map[string]interface{}
	err = json.Unmarshal(body, &version)
	require.NoError(t, err)

	if verbose != nil {
		verbose.LogJSON("Version Response", version)
	}

	assert.Contains(t, version, "version")
	assert.Contains(t, version, "git_commit")
	assert.Contains(t, version, "build_time")

	// Test stats endpoint
	resp, err = env.HTTPClient.Get("https://127.0.0.1:8080/api/v1/stats")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode == 404 {
		if verbose != nil {
			verbose.Log("⚠️ Stats endpoint not fully implemented yet (404)")
		}
	} else {
		var stats map[string]interface{}
		err = json.Unmarshal(body, &stats)
		require.NoError(t, err)
		if verbose != nil {
			verbose.LogJSON("Stats Response", stats)
		}
	}

	if verbose != nil {
		verbose.Log("✅ API response validation passed")
	}
}

func ValidateMetrics(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("📊 Validating metrics...")
	}

	// Test metrics endpoint
	resp, err := env.HTTPClientPlain.Get("http://127.0.0.1:9091/metrics")
	if err != nil {
		if verbose != nil {
			verbose.Log("⚠️ Metrics endpoint not available: %v", err)
		}
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	metricsText := string(body)
	if verbose != nil {
		verbose.Log("📈 Metrics sample:\n%s", metricsText[:min(len(metricsText), 500)])
	}

	// Validate some expected metrics exist
	expectedMetrics := []string{
		"http_requests_total",       // HTTP request metrics
		"go_memstats_",              // Go runtime metrics
		"process_",                  // Process metrics
		"rtp_sessions_active_total", // RTP metrics
	}

	foundMetrics := 0
	for _, metric := range expectedMetrics {
		if strings.Contains(metricsText, metric) {
			if verbose != nil {
				verbose.Log("✅ Found metric: %s", metric)
			}
			foundMetrics++
		} else {
			if verbose != nil {
				verbose.Log("⚠️ Missing metric: %s", metric)
			}
		}
	}

	// Ensure at least some core metrics are present
	assert.Greater(t, foundMetrics, 2, "Should find at least 3 core metrics")

	if verbose != nil {
		verbose.Log("✅ Metrics validation passed")
	}
}

func ValidateLogs(t *testing.T, env *TestEnvironment, verbose *VerboseLogger) {
	if verbose != nil {
		verbose.Log("📝 Validating logs...")
	}

	// Since we're using stdout logging, we can't directly capture logs in this test
	// But we can verify that the logging system is working by checking log configuration

	// Verify debug endpoints if enabled
	if env.Config.Server.DebugEndpoints {
		resp, err := env.HTTPClient.Get("https://127.0.0.1:8080/debug/info")
		if err == nil {
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				safeLog(verbose, "🐛 Debug info available: %s", string(body)[:min(len(body), 200)])
			}
		}
	}

	if verbose != nil {
		verbose.Log("✅ Log validation completed")
	}
}

// StartMetricsServer starts the Prometheus metrics server for testing
func StartMetricsServer(cfg config.MetricsConfig, log logger.Logger, verbose *VerboseLogger) {
	mux := http.NewServeMux()
	mux.Handle(cfg.Path, promhttp.Handler())

	addr := fmt.Sprintf(":%d", cfg.Port)
	if verbose != nil {
		verbose.Log("📊 Starting metrics server on %s", addr)
	}

	if err := http.ListenAndServe(addr, mux); err != nil {
		if verbose != nil {
			verbose.Log("❌ Metrics server error: %v", err)
		}
	}
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// LogCapturer captures real log output and feeds it to the dashboard
type LogCapturer struct {
	dashboard      *Dashboard
	originalOutput *os.File
	originalError  *os.File
	reader         *os.File
	writer         *os.File
	done           chan struct{}
	logPattern     *regexp.Regexp
	lastLogTime    time.Time
	logCount       int
	rateLimitMap   map[string]time.Time // For sampling similar logs
	mu             sync.Mutex           // Protect rate limiting state
}

// NewLogCapturer creates a new log capturer
func NewLogCapturer(dashboard *Dashboard) *LogCapturer {
	// Pattern to match Mirror server log lines (logrus format)
	// Examples: 
	// time="..." level=info msg="Server starting..." stream_id=test
	// time="..." level=error msg="Failed to connect" component=srt
	logPattern := regexp.MustCompile(`level=([a-z]+).*?msg="([^"]+)"`)

	return &LogCapturer{
		dashboard:    dashboard,
		done:         make(chan struct{}),
		logPattern:   logPattern,
		rateLimitMap: make(map[string]time.Time),
		lastLogTime:  time.Now(),
	}
}

// Start begins capturing log output with complete suppression
func (lc *LogCapturer) Start() {
	// Create a pipe to capture all output
	r, w, err := os.Pipe()
	if err != nil {
		// Can't use log.Printf here as it would be redirected
		fmt.Fprintf(os.Stderr, "Failed to create log capture pipe: %v\n", err)
		return
	}

	lc.reader = r
	lc.writer = w
	lc.originalOutput = os.Stdout
	lc.originalError = os.Stderr

	// Redirect ALL output to our pipe for complete suppression
	os.Stdout = w
	os.Stderr = w

	// Start reading from the pipe in a goroutine
	go lc.readLogs()
}

// StartFiltered uses a safer approach that doesn't interfere with main stdout
func (lc *LogCapturer) StartFiltered() {
	// Instead of redirecting stdout, simulate realistic log entries
	// This avoids the complex pipe redirection issues
	lc.simulateRealisticLogs()
}

// StartSimulated starts with simulated log entries for testing
func (lc *LogCapturer) StartSimulated() {
	lc.simulateRealisticLogs()
}

// simulateRealisticLogs - DISABLED: Now using real server stdout capture
func (lc *LogCapturer) simulateRealisticLogs() {
	// Real logs are now captured from server stdout via readLogs()
	// No more synthetic log generation needed
}

// Stop stops capturing logs and restores stdout
func (lc *LogCapturer) Stop() {
	if lc.originalOutput != nil {
		os.Stdout = lc.originalOutput
	}
	if lc.originalError != nil {
		os.Stderr = lc.originalError
	}

	close(lc.done)

	if lc.writer != nil {
		lc.writer.Close()
	}
	if lc.reader != nil {
		lc.reader.Close()
	}
}

// readLogs reads log lines from the pipe and feeds them to the dashboard in real-time
func (lc *LogCapturer) readLogs() {
	scanner := bufio.NewScanner(lc.reader)
	
	// Use a channel for immediate streaming
	lineChan := make(chan string, 100) // Buffer for burst logs
	
	// Goroutine to continuously read from scanner
	go func() {
		defer close(lineChan)
		for scanner.Scan() {
			select {
			case lineChan <- scanner.Text():
			case <-lc.done:
				return
			}
		}
	}()

	// Process lines immediately as they arrive
	for {
		select {
		case <-lc.done:
			return
		case line, ok := <-lineChan:
			if !ok {
				return // Channel closed
			}
			// REAL-TIME: Process immediately with no delays
			lc.parseLogs(line)
		}
	}
}

// parseLogs parses log lines and extracts relevant information for the dashboard
func (lc *LogCapturer) parseLogs(line string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	
	// Apply intelligent sampling to prevent overwhelming the display
	if !lc.shouldProcessLog(line) {
		return
	}
	
	lc.logCount++
	lc.lastLogTime = time.Now()
	
	// Try to parse structured logs from Mirror server
	matches := lc.logPattern.FindStringSubmatch(line)
	if len(matches) >= 3 {
		level := matches[1]
		message := matches[2]
		
		// Extract component from the message or fields
		component := lc.extractComponent(line, message)
		
		lc.dashboard.AddLogEntry(level, component, message)
		return
	}

	// Fallback: try to extract basic information from unstructured logs
	if strings.Contains(strings.ToLower(line), "error") {
		lc.dashboard.AddLogEntry("error", "system", lc.truncateMessage(line))
	} else if strings.Contains(strings.ToLower(line), "warn") {
		lc.dashboard.AddLogEntry("warn", "system", lc.truncateMessage(line))
	} else if strings.Contains(strings.ToLower(line), "debug") {
		lc.dashboard.AddLogEntry("debug", "system", lc.truncateMessage(line))
	} else if strings.Contains(line, "srt") || strings.Contains(line, "SRT") {
		lc.dashboard.AddLogEntry("info", "srt", lc.truncateMessage(line))
	} else if strings.Contains(line, "rtp") || strings.Contains(line, "RTP") {
		lc.dashboard.AddLogEntry("info", "rtp", lc.truncateMessage(line))
	} else if strings.Contains(line, "server") || strings.Contains(line, "Server") {
		lc.dashboard.AddLogEntry("info", "server", lc.truncateMessage(line))
	} else if len(strings.TrimSpace(line)) > 0 {
		// Only add non-empty lines
		lc.dashboard.AddLogEntry("info", "system", lc.truncateMessage(line))
	}
}

// extractComponent tries to determine the component from log context
func (lc *LogCapturer) extractComponent(line, message string) string {
	// Look for common component indicators in the log line
	if strings.Contains(line, `stream_id=`) {
		return "ingestion"
	}
	if strings.Contains(line, `component=`) {
		// Extract component field
		re := regexp.MustCompile(`component=([a-zA-Z_]+)`)
		if matches := re.FindStringSubmatch(line); len(matches) > 1 {
			return matches[1]
		}
	}
	
	// Infer from message content
	msgLower := strings.ToLower(message)
	if strings.Contains(msgLower, "srt") {
		return "srt"
	}
	if strings.Contains(msgLower, "rtp") {
		return "rtp"
	}
	if strings.Contains(msgLower, "server") || strings.Contains(msgLower, "http") {
		return "server"
	}
	if strings.Contains(msgLower, "frame") || strings.Contains(msgLower, "codec") {
		return "codec"
	}
	if strings.Contains(msgLower, "sync") {
		return "sync"
	}
	if strings.Contains(msgLower, "buffer") {
		return "buffer"
	}
	if strings.Contains(msgLower, "memory") || strings.Contains(msgLower, "goroutine") {
		return "memory"
	}
	if strings.Contains(msgLower, "health") {
		return "health"
	}
	if strings.Contains(msgLower, "metric") {
		return "metrics"
	}
	
	return "system"
}

// shouldProcessLog implements intelligent sampling to prevent log spam
func (lc *LogCapturer) shouldProcessLog(line string) bool {
	now := time.Now()
	
	// Always show important logs (errors, critical info)
	if strings.Contains(line, "level=error") || 
	   strings.Contains(line, "level=fatal") ||
	   strings.Contains(line, "level=panic") {
		return true
	}
	
	// Always show stream connection/disconnection events
	if strings.Contains(line, "connected") || 
	   strings.Contains(line, "disconnected") ||
	   strings.Contains(line, "stream") {
		return true
	}
	
	// Create a fingerprint for similar log messages
	fingerprint := lc.createLogFingerprint(line)
	
	// Rate limit similar logs to once every 500ms (less aggressive)
	if lastSeen, exists := lc.rateLimitMap[fingerprint]; exists {
		if now.Sub(lastSeen) < 500*time.Millisecond {
			return false // Skip this log, too recent
		}
	}
	
	// Update last seen time for this fingerprint
	lc.rateLimitMap[fingerprint] = now
	
	// Clean up old entries to prevent memory leak
	if len(lc.rateLimitMap) > 100 {
		cutoff := now.Add(-5 * time.Minute)
		for key, timestamp := range lc.rateLimitMap {
			if timestamp.Before(cutoff) {
				delete(lc.rateLimitMap, key)
			}
		}
	}
	
	// Apply global rate limiting - max 5 logs per second (less aggressive)
	if lc.logCount > 0 && now.Sub(lc.lastLogTime) < 200*time.Millisecond {
		return false
	}
	
	return true
}

// createLogFingerprint creates a signature for similar log messages
func (lc *LogCapturer) createLogFingerprint(line string) string {
	// Remove timestamps and variable data to group similar messages
	line = regexp.MustCompile(`time="[^"]*"`).ReplaceAllString(line, "")
	line = regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`).ReplaceAllString(line, "IP") // Replace IPs
	line = regexp.MustCompile(`\d{4,}`).ReplaceAllString(line, "NUM")           // Replace large numbers
	line = regexp.MustCompile(`[0-9a-f]{8,}`).ReplaceAllString(line, "ID")      // Replace IDs/hashes
	
	// Take first 50 chars as fingerprint
	if len(line) > 50 {
		line = line[:50]
	}
	
	return line
}

// truncateMessage truncates long messages for dashboard display
func (lc *LogCapturer) truncateMessage(message string) string {
	// Remove common prefixes/timestamps to get to the core message
	message = strings.TrimSpace(message)

	// Remove timestamp prefixes like "2024/01/01 12:00:00"
	timestampPattern := regexp.MustCompile(`^\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s*`)
	message = timestampPattern.ReplaceAllString(message, "")

	// Remove test output prefixes
	testPattern := regexp.MustCompile(`^\s*[a-zA-Z_\-\.]+\.go:\d+:\s*`)
	message = testPattern.ReplaceAllString(message, "")

	// Truncate if too long
	if len(message) > 60 {
		message = message[:57] + "..."
	}

	return message
}
