package ingestion

import (
	"context"
	"fmt"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/srt"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// HandleSRTStreamWithBackpressure integrates SRT connection with backpressure handling
func (m *Manager) HandleSRTStreamWithBackpressure(conn *srt.Connection) error {
	// Create adapter
	adapter := NewSRTConnectionAdapter(conn)

	// Set memory eviction callback
	m.memoryController.SetEvictionCallback(func(streamID string, bytes int64) {
		m.logger.WithFields(map[string]interface{}{
			"stream_id": streamID,
			"bytes":     bytes,
		}).Warn("Memory pressure - requesting eviction")

		// Could implement stream priority-based eviction here
	})

	// Create and start handler using the unified video-aware handler
	handler, err := m.CreateStreamHandler(conn.GetStreamID(), adapter)
	if err != nil {
		return err
	}

	// Handler is already started, wait for completion
	go func() {
		<-handler.ctx.Done()
		m.RemoveStreamHandler(conn.GetStreamID())
	}()

	return nil
}

// HandleRTPStreamWithBackpressure integrates RTP session with backpressure handling
func (m *Manager) HandleRTPStreamWithBackpressure(session *rtp.Session) error {
	// Detect codec from session
	codecType := DetectCodecFromRTPSession(session)
	if codecType == types.CodecUnknown {
		// Default to H.264 for video sessions
		m.logger.Warn("Could not detect codec, defaulting to H.264")
		codecType = types.CodecH264
	}

	// Create adapter
	adapter := NewRTPConnectionAdapter(session, codecType)

	// Create and start handler using the unified video-aware handler
	handler, err := m.CreateStreamHandler(session.GetStreamID(), adapter)
	if err != nil {
		return err
	}

	// Handler is already started, wait for completion
	go func() {
		<-handler.ctx.Done()
		m.RemoveStreamHandler(session.GetStreamID())
	}()

	return nil
}

// GetStreamMetrics returns metrics for a specific stream
func (m *Manager) GetStreamMetrics(streamID string) (*StreamMetrics, error) {
	ctx := context.Background()

	// Get stream from registry
	stream, err := m.registry.Get(ctx, streamID)
	if err != nil {
		return nil, fmt.Errorf("stream not found: %w", err)
	}

	// Get stats from stream handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		return nil, fmt.Errorf("stream handler not found for stream %s", streamID)
	}
	stats := handler.GetStats()

	// Get memory usage
	memUsage := m.memoryController.GetStreamUsage(streamID)
	memPressure := m.memoryController.GetPressure()

	metrics := &StreamMetrics{
		StreamID:        streamID,
		Type:            string(stream.Type),
		Status:          string(stream.Status),
		Bitrate:         stream.Bitrate,
		FramesAssembled: stats.FramesAssembled,
		FramesDropped:   stats.FramesDropped,
		QueueDepth:      stats.QueueDepth,
		QueuePressure:   stats.QueuePressure,
		MemoryUsage:     memUsage,
		MemoryPressure:  memPressure,
		CreatedAt:       stream.CreatedAt.Unix(),
		UpdatedAt:       stream.LastHeartbeat.Unix(),
	}

	return metrics, nil
}

// StreamMetrics contains detailed metrics for a stream
type StreamMetrics struct {
	StreamID        string  `json:"stream_id"`
	Type            string  `json:"type"`
	Status          string  `json:"status"`
	Bitrate         int64   `json:"bitrate"`
	FramesAssembled uint64  `json:"frames_assembled"`
	FramesDropped   uint64  `json:"frames_dropped"`
	QueueDepth      int64   `json:"queue_depth"`
	QueuePressure   float64 `json:"queue_pressure"`
	MemoryUsage     int64   `json:"memory_usage"`
	MemoryPressure  float64 `json:"memory_pressure"`
	CreatedAt       int64   `json:"created_at"`
	UpdatedAt       int64   `json:"updated_at"`
}

// MonitorSystemHealth monitors overall system health and applies global backpressure if needed
func (m *Manager) MonitorSystemHealth(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get memory stats
			memStats := m.memoryController.Stats()

			// Log high memory pressure
			if memStats.GlobalPressure > 0.8 {
				m.logger.WithFields(map[string]interface{}{
					"pressure":       memStats.GlobalPressure,
					"usage":          memStats.GlobalUsage,
					"limit":          memStats.GlobalLimit,
					"active_streams": memStats.ActiveStreams,
				}).Warn("High memory pressure detected")

				// Could implement global backpressure here
				// For example, pause new connections or reduce quality
			}

			// Get buffer pool stats
			m.handlersMu.RLock()
			activeHandlers := len(m.streamHandlers)
			m.handlersMu.RUnlock()

			m.logger.WithFields(map[string]interface{}{
				"memory_pressure": memStats.GlobalPressure,
				"active_buffers":  activeHandlers,
				"total_drops":     0,
				"memory_usage_gb": float64(memStats.GlobalUsage) / (1024 * 1024 * 1024),
			}).Debug("System health metrics")
		}
	}
}
