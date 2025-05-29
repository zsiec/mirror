//go:build integration

package srt_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	gosrt "github.com/datarhei/gosrt"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/buffer"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/ingestion/srt"
	"github.com/zsiec/mirror/internal/ingestion/testdata"
	"github.com/zsiec/mirror/tests"
)

func TestSRTIntegration_StreamIngestion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	ctx := context.Background()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create test Redis registry
	redisClient := tests.SetupTestRedis(t)
	reg := registry.NewRedisRegistry(redisClient, logger)

	// Create buffer pool
	bufferPool := buffer.NewBufferPool(1024*1024, 10, logger) // 1MB buffers

	// Create SRT listener config
	cfg := &config.SRTConfig{
		Enabled:           true,
		ListenAddr:        "127.0.0.1",
		Port:              20080 + rand.Intn(1000), // Random port to avoid conflicts
		FlowControlWindow: 25600,
		InputBandwidth:    0,
		MaxBandwidth:      -1,
		Latency:           120,
		PayloadSize:       1316,
		PeerIdleTimeout:   5 * time.Second,
	}

	// Create and start listener
	codecsCfg := &config.CodecsConfig{
		Supported: []string{"h264", "hevc"},
		Preferred: "hevc",
	}
	listener := srt.NewListener(cfg, codecsCfg, reg, bufferPool, logger)
	// Set faster stats update for testing (500ms instead of 5s)
	listener.SetTestStatsInterval(500 * time.Millisecond)
	err := listener.Start()
	require.NoError(t, err)
	defer listener.Stop()

	// Give listener time to start
	time.Sleep(100 * time.Millisecond)

	t.Run("single stream connection", func(t *testing.T) {
		streamID := "test-stream-srt-1"

		// Create SRT client
		clientConfig := gosrt.DefaultConfig()
		clientConfig.StreamId = streamID

		addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
		conn, err := gosrt.Dial("srt", addr, clientConfig)
		require.NoError(t, err)
		defer conn.Close()

		// Generate and send test data
		testData := testdata.CreateTestSRTPayload(streamID, 200) // 200ms of data

		// Send data in chunks
		chunkSize := 1316 // SRT payload size
		for i := 0; i < len(testData); i += chunkSize {
			end := i + chunkSize
			if end > len(testData) {
				end = len(testData)
			}

			n, err := conn.Write(testData[i:end])
			require.NoError(t, err)
			assert.Equal(t, end-i, n)

			time.Sleep(2 * time.Millisecond) // Faster streaming for tests
		}

		// Verify stream was registered
		time.Sleep(100 * time.Millisecond)
		stream, err := reg.Get(ctx, streamID)
		require.NoError(t, err)
		assert.Equal(t, streamID, stream.ID)
		assert.Equal(t, registry.StreamTypeSRT, stream.Type)
		assert.Equal(t, registry.StatusActive, stream.Status)

		// Verify data was buffered
		buf := bufferPool.Get(streamID)
		require.NotNil(t, buf)
		stats := buf.Stats()
		assert.Greater(t, stats.Written, int64(0))

		// Close connection
		conn.Close()

		// Wait for cleanup
		time.Sleep(200 * time.Millisecond)

		// Verify stream was unregistered
		_, err = reg.Get(ctx, streamID)
		assert.Error(t, err)
	})

	t.Run("multiple concurrent streams", func(t *testing.T) {
		numStreams := 3
		streams := make([]string, numStreams)
		conns := make([]gosrt.Conn, numStreams)

		// Connect multiple streams
		for i := 0; i < numStreams; i++ {
			streamID := fmt.Sprintf("test-stream-srt-concurrent-%d", i)
			streams[i] = streamID

			clientConfig := gosrt.DefaultConfig()
			clientConfig.StreamId = streamID

			addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
			conn, err := gosrt.Dial("srt", addr, clientConfig)
			require.NoError(t, err)
			conns[i] = conn
		}

		// Send data to all streams concurrently
		errCh := make(chan error, numStreams)
		for i := 0; i < numStreams; i++ {
			go func(idx int) {
				testData := testdata.CreateTestSRTPayload(streams[idx], 100) // 500ms of data
				_, err := conns[idx].Write(testData)
				errCh <- err
			}(i)
		}

		// Wait for all sends to complete
		for i := 0; i < numStreams; i++ {
			err := <-errCh
			assert.NoError(t, err)
		}

		// Verify all streams are active
		time.Sleep(100 * time.Millisecond)
		activeStreams, err := reg.List(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(activeStreams), numStreams)

		// Clean up
		for _, conn := range conns {
			conn.Close()
		}
	})

	t.Run("stream with invalid ID", func(t *testing.T) {
		invalidIDs := []string{
			"",                   // Empty
			"stream with spaces", // Spaces
			"stream@invalid",     // Special characters
			"-invalid-start",     // Starts with hyphen
			"very-long-stream-id-that-exceeds-the-maximum-allowed-length-of-64-characters-for-stream-ids", // Too long
		}

		for _, streamID := range invalidIDs {
			t.Run(streamID, func(t *testing.T) {
				clientConfig := gosrt.DefaultConfig()
				clientConfig.StreamId = streamID

				addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
				conn, err := gosrt.Dial("srt", addr, clientConfig)

				// Connection should fail or be rejected
				if err == nil {
					conn.Close()
					t.Errorf("Expected connection to fail for invalid stream ID: %s", streamID)
				}
			})
		}
	})

	t.Run("connection limit", func(t *testing.T) {
		streamID := "test-stream-limit"
		maxConns := 5 // Per-stream limit

		conns := make([]gosrt.Conn, 0)
		defer func() {
			for _, conn := range conns {
				conn.Close()
			}
		}()

		// Connect up to the limit
		for i := 0; i < maxConns; i++ {
			clientConfig := gosrt.DefaultConfig()
			clientConfig.StreamId = streamID

			addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
			conn, err := gosrt.Dial("srt", addr, clientConfig)
			require.NoError(t, err)
			conns = append(conns, conn)
		}

		// Try to exceed the limit
		clientConfig := gosrt.DefaultConfig()
		clientConfig.StreamId = streamID

		addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
		conn, err := gosrt.Dial("srt", addr, clientConfig)

		// This should fail
		if err == nil {
			conn.Close()
			t.Error("Expected connection to fail when exceeding limit")
		}
	})

	t.Run("reconnection after disconnect", func(t *testing.T) {
		streamID := "test-stream-reconnect"

		// First connection
		clientConfig := gosrt.DefaultConfig()
		clientConfig.StreamId = streamID

		addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
		conn1, err := gosrt.Dial("srt", addr, clientConfig)
		require.NoError(t, err)

		// Send some data
		testData := testdata.CreateTestSRTPayload(streamID, 500)
		_, err = conn1.Write(testData)
		require.NoError(t, err)

		// Close first connection
		conn1.Close()
		time.Sleep(100 * time.Millisecond)

		// Reconnect with same stream ID
		conn2, err := gosrt.Dial("srt", addr, clientConfig)
		require.NoError(t, err)
		defer conn2.Close()

		// Send more data
		_, err = conn2.Write(testData)
		require.NoError(t, err)

		// Wait for status to update to active
		time.Sleep(200 * time.Millisecond)

		// Verify stream is active
		stream, err := reg.Get(ctx, streamID)
		require.NoError(t, err)
		assert.Equal(t, registry.StatusActive, stream.Status)
	})
}

func TestSRTIntegration_Metrics(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	ctx := context.Background()
	logger := logrus.New()

	// Create test Redis registry
	redisClient := tests.SetupTestRedis(t)
	reg := registry.NewRedisRegistry(redisClient, logger)

	// Create buffer pool
	bufferPool := buffer.NewBufferPool(1024*1024, 10, logger)

	// Create SRT listener
	cfg := &config.SRTConfig{
		Enabled:           true,
		ListenAddr:        "127.0.0.1",
		Port:              20080 + rand.Intn(1000), // Random port to avoid conflicts
		PayloadSize:       1316,
		PeerIdleTimeout:   5 * time.Second,
		FlowControlWindow: 25600,
		InputBandwidth:    0,
		MaxBandwidth:      -1,
		Latency:           120,
	}

	codecsCfg := &config.CodecsConfig{
		Supported: []string{"h264", "hevc"},
		Preferred: "hevc",
	}
	listener := srt.NewListener(cfg, codecsCfg, reg, bufferPool, logger)
	// Set faster stats update for testing (500ms instead of 5s)
	listener.SetTestStatsInterval(500 * time.Millisecond)
	err := listener.Start()
	require.NoError(t, err)
	defer listener.Stop()

	time.Sleep(100 * time.Millisecond)

	t.Run("stream metrics tracking", func(t *testing.T) {
		streamID := "test-stream-metrics"

		// Connect
		clientConfig := gosrt.DefaultConfig()
		clientConfig.StreamId = streamID

		addr := fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port)
		conn, err := gosrt.Dial("srt", addr, clientConfig)
		require.NoError(t, err)
		defer conn.Close()

		// Send known amount of data
		dataSize := 100000 // 100KB
		testData := make([]byte, dataSize)
		for i := range testData {
			testData[i] = byte(i % 256)
		}

		n, err := conn.Write(testData)
		require.NoError(t, err)
		assert.Equal(t, dataSize, n)

		// Keep connection alive and wait for metrics to update (stats update every 500ms in test mode)
		var stream *registry.Stream
		require.Eventually(t, func() bool {
			// Send a small keepalive packet to prevent connection timeout
			conn.Write([]byte("keepalive"))

			stream, err = reg.Get(ctx, streamID)
			if err != nil || stream == nil {
				return false
			}
			return stream.BytesReceived > 0 && stream.PacketsReceived > 0
		}, 1*time.Second, 100*time.Millisecond, "Stats should show data received")

		// Verify final stats
		assert.Greater(t, stream.BytesReceived, int64(0))
		assert.Greater(t, stream.PacketsReceived, int64(0))

		// Check listener metrics
		assert.Equal(t, 1, listener.GetActiveSessions())
	})
}
