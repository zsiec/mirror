package ingestion

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/zsiec/mirror/internal/ingestion/backpressure"
	"github.com/zsiec/mirror/internal/ingestion/recovery"
)

// HandleStreamData - GET /api/v1/streams/{id}/data
// Returns a stream of frame data (video-aware)
func (m *Manager) HandleStreamData(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	// Set headers for streaming
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Get recent frames for streaming
	previewData, frameCount := handler.GetFramePreview(5.0) // Get last 5 seconds

	// Write frame statistics first
	stats := handler.GetStats()
	header := fmt.Sprintf("STREAM_DATA_FRAMES:%d_KEYFRAMES:%d_COUNT:%d\n",
		stats.FramesAssembled, stats.KeyframeCount, frameCount)
	w.Write([]byte(header))

	// Write preview data
	w.Write(previewData)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// HandleStreamBuffer - GET /api/v1/streams/{id}/buffer
// Returns frame buffer statistics
func (m *Manager) HandleStreamBuffer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get stream and handler
	stream, err := m.registry.Get(r.Context(), streamID)
	if err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", err)
		return
	}

	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream handler not found", nil)
		return
	}

	stats := handler.GetStats()

	// Return frame buffer stats
	response := struct {
		Capacity        int64   `json:"capacity"`
		Used            int64   `json:"used"`
		Available       int64   `json:"available"`
		FramesAssembled uint64  `json:"frames_assembled"`
		FramesDropped   uint64  `json:"frames_dropped"`
		QueuePressure   float64 `json:"queue_pressure"`
		Keyframes       uint64  `json:"keyframes"`
		PFrames         uint64  `json:"p_frames"`
		BFrames         uint64  `json:"b_frames"`
		StreamID        string  `json:"stream_id"`
		Codec           string  `json:"codec"`
		GOP             struct {
			TotalGOPs       uint64  `json:"total_gops"`
			CurrentGOPSize  int64   `json:"current_gop_size"`
			AverageGOPSize  float64 `json:"average_gop_size"`
			AverageDuration int64   `json:"average_duration_ms"`
			IFrameRatio     float64 `json:"i_frame_ratio"`
			PFrameRatio     float64 `json:"p_frame_ratio"`
			BFrameRatio     float64 `json:"b_frame_ratio"`
		} `json:"gop"`
		GOPBuffer struct {
			GOPCount      int    `json:"gop_count"`
			FrameCount    int    `json:"frame_count"`
			TotalBytes    int64  `json:"total_bytes"`
			Duration      int64  `json:"duration_ms"`
			DroppedGOPs   uint64 `json:"dropped_gops"`
			DroppedFrames uint64 `json:"dropped_frames"`
		} `json:"gop_buffer"`
	}{
		Capacity:        100, // Default frame buffer capacity
		Used:            stats.QueueDepth,
		Available:       100 - stats.QueueDepth,
		FramesAssembled: stats.FramesAssembled,
		FramesDropped:   stats.FramesDropped,
		QueuePressure:   stats.QueuePressure,
		Keyframes:       stats.KeyframeCount,
		PFrames:         stats.PFrameCount,
		BFrames:         stats.BFrameCount,
		StreamID:        stream.ID,
		Codec:           stats.Codec,
		GOP: struct {
			TotalGOPs       uint64  `json:"total_gops"`
			CurrentGOPSize  int64   `json:"current_gop_size"`
			AverageGOPSize  float64 `json:"average_gop_size"`
			AverageDuration int64   `json:"average_duration_ms"`
			IFrameRatio     float64 `json:"i_frame_ratio"`
			PFrameRatio     float64 `json:"p_frame_ratio"`
			BFrameRatio     float64 `json:"b_frame_ratio"`
		}{
			TotalGOPs:       stats.GOPStats.TotalGOPs,
			CurrentGOPSize:  stats.GOPStats.CurrentGOPSize,
			AverageGOPSize:  stats.GOPStats.AverageGOPSize,
			AverageDuration: stats.GOPStats.AverageDuration.Milliseconds(),
			IFrameRatio:     stats.GOPStats.IFrameRatio,
			PFrameRatio:     stats.GOPStats.PFrameRatio,
			BFrameRatio:     stats.GOPStats.BFrameRatio,
		},
		GOPBuffer: struct {
			GOPCount      int    `json:"gop_count"`
			FrameCount    int    `json:"frame_count"`
			TotalBytes    int64  `json:"total_bytes"`
			Duration      int64  `json:"duration_ms"`
			DroppedGOPs   uint64 `json:"dropped_gops"`
			DroppedFrames uint64 `json:"dropped_frames"`
		}{
			GOPCount:      stats.GOPBufferStats.GOPCount,
			FrameCount:    stats.GOPBufferStats.FrameCount,
			TotalBytes:    stats.GOPBufferStats.TotalBytes,
			Duration:      stats.GOPBufferStats.Duration.Milliseconds(),
			DroppedGOPs:   stats.GOPBufferStats.DroppedGOPs,
			DroppedFrames: stats.GOPBufferStats.DroppedFrames,
		},
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleStreamPreview - GET /api/v1/streams/{id}/preview
// Returns a preview of recent frames
func (m *Manager) HandleStreamPreview(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get duration parameter
	durationStr := r.URL.Query().Get("duration")
	duration := 1.0 // Default 1 second
	if d, err := strconv.ParseFloat(durationStr, 64); err == nil && d > 0 {
		duration = d
	}

	// Get handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	// Get frame preview
	previewData, frameCount := handler.GetFramePreview(duration)

	response := struct {
		StreamID   string    `json:"stream_id"`
		Duration   float64   `json:"duration_seconds"`
		FrameCount int       `json:"frame_count"`
		Preview    string    `json:"preview"`
		Timestamp  time.Time `json:"timestamp"`
	}{
		StreamID:   streamID,
		Duration:   duration,
		FrameCount: frameCount,
		Preview:    string(previewData),
		Timestamp:  time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleStreamBackpressure - GET /api/v1/streams/{id}/backpressure
// Returns backpressure statistics and current state
func (m *Manager) HandleStreamBackpressure(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	stats := handler.GetStats()

	response := struct {
		StreamID   string                  `json:"stream_id"`
		Pressure   float64                 `json:"current_pressure"`
		Rate       int64                   `json:"current_rate_bps"`
		Statistics backpressure.Statistics `json:"statistics"`
		ShouldDrop bool                    `json:"should_drop_gop"`
		Timestamp  time.Time               `json:"timestamp"`
	}{
		StreamID:   streamID,
		Pressure:   stats.BackpressureStats.CurrentPressure,
		Rate:       stats.BackpressureStats.CurrentRate,
		Statistics: stats.BackpressureStats,
		ShouldDrop: stats.BackpressureStats.CurrentPressure >= 0.9,
		Timestamp:  time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleStreamBackpressureControl - POST /api/v1/streams/{id}/backpressure/control
// Allows manual control of backpressure settings
func (m *Manager) HandleStreamBackpressureControl(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	var request struct {
		Action   string  `json:"action"`   // "set_pressure", "reset", "drop_gop"
		Pressure float64 `json:"pressure"` // For set_pressure
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, "Invalid request", err)
		return
	}

	switch request.Action {
	case "set_pressure":
		// Validate pressure is between 0 and 1
		if request.Pressure < 0 || request.Pressure > 1 {
			writeError(r.Context(), w, http.StatusBadRequest,
				"Pressure must be between 0 and 1", nil)
			return
		}
		handler.bpController.UpdatePressure(request.Pressure)
	case "reset":
		handler.bpController.UpdatePressure(0.0)
	case "drop_gop":
		handler.dropOldestGOP()
	default:
		writeError(r.Context(), w, http.StatusBadRequest, "Invalid action", nil)
		return
	}

	response := struct {
		StreamID  string    `json:"stream_id"`
		Action    string    `json:"action"`
		Success   bool      `json:"success"`
		Timestamp time.Time `json:"timestamp"`
	}{
		StreamID:  streamID,
		Action:    request.Action,
		Success:   true,
		Timestamp: time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleStreamRecovery - GET /api/v1/streams/{id}/recovery
// Returns error recovery statistics and current state
func (m *Manager) HandleStreamRecovery(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	stats := handler.GetStats()

	response := struct {
		StreamID         string              `json:"stream_id"`
		State            string              `json:"state"`
		IsHealthy        bool                `json:"is_healthy"`
		RecoveryCount    uint64              `json:"recovery_count"`
		CorruptionCount  uint64              `json:"corruption_count"`
		ResyncCount      uint64              `json:"resync_count"`
		LastRecoveryTime time.Time           `json:"last_recovery_time"`
		Statistics       recovery.Statistics `json:"statistics"`
		Timestamp        time.Time           `json:"timestamp"`
	}{
		StreamID:         streamID,
		State:            getRecoveryStateString(stats.RecoveryStats.State),
		IsHealthy:        stats.RecoveryStats.IsHealthy,
		RecoveryCount:    stats.RecoveryStats.RecoveryCount,
		CorruptionCount:  stats.RecoveryStats.CorruptionCount,
		ResyncCount:      stats.RecoveryStats.ResyncCount,
		LastRecoveryTime: stats.RecoveryStats.LastRecoveryTime,
		Statistics:       stats.RecoveryStats,
		Timestamp:        time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// getRecoveryStateString converts recovery state to string
func getRecoveryStateString(state recovery.RecoveryState) string {
	switch state {
	case recovery.StateNormal:
		return "normal"
	case recovery.StateRecovering:
		return "recovering"
	case recovery.StateResyncing:
		return "resyncing"
	case recovery.StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}
