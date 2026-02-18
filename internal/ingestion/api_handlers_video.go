package ingestion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/zsiec/mirror/internal/ingestion/backpressure"
	"github.com/zsiec/mirror/internal/ingestion/recovery"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// HandleStreamData - GET /api/v1/streams/{id}/data
// Returns a stream of frame data (video-aware)
func (m *Manager) HandleStreamData(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get handler and stats atomically to prevent race conditions
	handler, stats, exists := m.GetStreamHandlerAndStats(streamID)
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

	_, stats, exists := m.GetStreamHandlerAndStats(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream handler not found", nil)
		return
	}

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

	// Get handler and stats atomically to prevent race conditions
	_, stats, exists := m.GetStreamHandlerAndStats(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

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

	// Get handler and stats atomically to prevent race conditions
	_, stats, exists := m.GetStreamHandlerAndStats(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

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

// HandleStreamIframe - GET /api/v1/streams/{id}/iframe
// Returns the latest iframe as JPEG
func (m *Manager) HandleStreamIframe(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	m.logger.WithField("stream_id", streamID).Info("Starting iframe request processing")

	// Get handler and stats to get codec info
	handler, stats, exists := m.GetStreamHandlerAndStats(streamID)
	if !exists {
		m.logger.WithField("stream_id", streamID).Warn("Stream not found in registry")
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	m.logger.WithFields(map[string]interface{}{
		"stream_id": streamID,
		"codec":     stats.Codec,
		"bitrate":   stats.Bitrate,
		"frames":    stats.FramesAssembled,
		"keyframes": stats.KeyframeCount,
	}).Info("Stream found, fetching iframe from GOP buffer")

	// Get latest iframe with full decoding context from GOP buffer
	// Convert codec string to codec type
	codecType := types.CodecH264 // Default
	switch strings.ToLower(stats.Codec) {
	case "h264", "avc":
		codecType = types.CodecH264
	case "h265", "hevc":
		codecType = types.CodecHEVC
	case "av1":
		codecType = types.CodecAV1
	}

	// **CHANGED: Use session parameter cache instead of GOP buffer extraction**
	iframe, paramContext := handler.GetLatestIFrameWithSessionContext()
	if iframe == nil || paramContext == nil {
		m.logger.WithField("stream_id", streamID).Warn("No iframe available in GOP buffer")
		writeError(r.Context(), w, http.StatusNotFound, "No iframe available", nil)
		return
	}

	// **DISABLED: Heavy debugging causing hangs**

	// Check if frame can be decoded with advanced validation
	canDecode, reason := paramContext.CanDecodeFrame(iframe)
	if !canDecode {
		// Get detailed session statistics for enhanced diagnostics
		sessionStats := paramContext.GetSessionStatistics()

		var referencedPPSIDs []uint8
		// Fix: Call GetDecodingRequirements only once per frame, not per NAL unit to prevent infinite loop
		if requirements, err := paramContext.GetDecodingRequirements(iframe); err == nil {
			referencedPPSIDs = append(referencedPPSIDs, requirements.RequiredPPSID)
		}

		m.logger.WithFields(map[string]interface{}{
			"stream_id":          streamID,
			"frame_id":           iframe.ID,
			"reason":             reason,
			"session_stats":      sessionStats,
			"frame_nal_units":    len(iframe.NALUnits),
			"referenced_pps_ids": referencedPPSIDs,
			"available_pps_ids":  sessionStats["pps_ids"],
		}).Warn("Frame cannot be decoded with current parameter sets")

		// Try to generate a stream with available parameter sets, even if not perfectly matched
		fallbackStream, err := paramContext.GenerateBestEffortStream(iframe)
		if err == nil && len(fallbackStream) > 0 {
			m.logger.WithFields(map[string]interface{}{
				"stream_id":     streamID,
				"frame_id":      iframe.ID,
				"fallback_size": len(fallbackStream),
				"method":        "best_effort_fallback",
			}).Info("Generated best-effort iframe stream")

			// Convert to JPEG using the fallback stream
			jpegData, err := m.convertRobustStreamToJPEG(fallbackStream, codecType)
			if err == nil {
				// Write successful response with fallback method
				w.Header().Set("Content-Type", "image/jpeg")
				w.Header().Set("X-Frame-ID", fmt.Sprintf("%d", iframe.ID))
				w.Header().Set("X-Method", "best_effort_fallback")
				w.WriteHeader(http.StatusOK)
				w.Write(jpegData)
				return
			}
		}

		// If all fallbacks fail, return detailed error
		spsIDs := safeUint8Slice(sessionStats["sps_ids"])
		ppsIDs := safeUint8Slice(sessionStats["pps_ids"])
		detailedMessage := fmt.Sprintf(
			"Frame not decodable: %s. Session stats: %d SPS (IDs: %v), %d PPS (IDs: %v), Session duration: %dms",
			reason,
			len(spsIDs),
			spsIDs,
			len(ppsIDs),
			ppsIDs,
			sessionStats["session_duration_ms"],
		)

		writeError(r.Context(), w, http.StatusServiceUnavailable, detailedMessage, nil)
		return
	}

	// Generate properly matched decodable stream
	decodableStream, err := paramContext.GenerateDecodableStream(iframe)
	if err != nil {
		m.logger.WithFields(map[string]interface{}{
			"stream_id": streamID,
			"frame_id":  iframe.ID,
			"error":     err.Error(),
		}).Error("Failed to generate decodable stream")
		writeError(r.Context(), w, http.StatusInternalServerError, "Failed to generate stream", nil)
		return
	}

	m.logger.WithFields(map[string]interface{}{
		"component":      "ingestion_manager",
		"stream_id":      streamID,
		"frame_id":       iframe.ID,
		"frame_size":     iframe.TotalSize,
		"decodable_size": len(decodableStream),
		"method":         "advanced_context",
		"stats":          paramContext.GetStatistics(),
	}).Info("Using advanced parameter context for iframe")

	// Convert using the properly matched decodable stream
	jpegData, convErr := m.convertRobustStreamToJPEG(decodableStream, codecType)
	if convErr != nil {
		m.logger.WithFields(map[string]interface{}{
			"component": "ingestion_manager",
			"stream_id": streamID,
			"error":     convErr.Error(),
		}).Error("Advanced FFmpeg conversion failed")
		writeError(r.Context(), w, http.StatusInternalServerError, "Video conversion failed", nil)
		return
	}

	// Success! Return the properly decoded JPEG with comprehensive headers
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jpegData)))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Frame-Codec", stats.Codec)
	w.Header().Set("X-Frame-ID", fmt.Sprintf("%d", iframe.ID))
	w.Header().Set("X-Frame-Size", fmt.Sprintf("%d", iframe.TotalSize))
	w.Header().Set("X-JPEG-Size", fmt.Sprintf("%d", len(jpegData)))
	w.WriteHeader(http.StatusOK)
	w.Write(jpegData)

	m.logger.WithFields(map[string]interface{}{
		"component":     "ingestion_manager",
		"stream_id":     streamID,
		"content_type":  "image/jpeg",
		"response_size": len(jpegData),
		"method":        "advanced_conversion",
		"frame_id":      iframe.ID,
		"frame_size":    iframe.TotalSize,
	}).Info("Successfully decoded iframe with advanced context")
}

// HandleStreamParameters handles GET /api/v1/streams/{id}/parameters - Production monitoring endpoint
func (m *Manager) HandleStreamParameters(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	m.logger.WithField("stream_id", streamID).Info("Getting parameter set statistics for stream")

	// Get stream handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		m.logger.WithField("stream_id", streamID).Warn("Stream not found")
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	// Get session parameter cache
	paramContext := handler.GetSessionParameterCache()
	if paramContext == nil {
		m.logger.WithField("stream_id", streamID).Warn("No parameter context available")
		writeError(r.Context(), w, http.StatusNotFound, "Parameter context not available", nil)
		return
	}

	// Get comprehensive session statistics
	sessionStats := paramContext.GetSessionStatistics()

	// Build response structure as specified in p.md
	response := struct {
		StreamID string                 `json:"stream_id"`
		Stats    map[string]interface{} `json:"statistics"`
		SPSIDs   []uint8                `json:"available_sps_ids"`
		PPSIDs   []uint8                `json:"available_pps_ids"`
	}{
		StreamID: streamID,
		Stats:    sessionStats,
		SPSIDs:   safeUint8Slice(sessionStats["sps_ids"]),
		PPSIDs:   safeUint8Slice(sessionStats["pps_ids"]),
	}

	m.logger.WithFields(map[string]interface{}{
		"stream_id":              streamID,
		"session_duration_ms":    sessionStats["session_duration_ms"],
		"total_frames_processed": sessionStats["total_frames_processed"],
		"sps_count":              len(response.SPSIDs),
		"pps_count":              len(response.PPSIDs),
		"coverage":               sessionStats["parameter_set_coverage"],
	}).Info("Retrieved parameter set statistics")

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// convertJPEGXSToJPEG converts JPEG-XS to standard JPEG
func (m *Manager) convertJPEGXSToJPEG(frame *types.VideoFrame) ([]byte, error) {
	m.logger.WithFields(map[string]interface{}{
		"frame_id":  frame.ID,
		"data_size": frame.TotalSize,
		"nal_units": len(frame.NALUnits),
	}).Info("Converting JPEG-XS frame (using placeholder for now)")

	// TODO: Implement JPEG-XS to JPEG conversion
	// For now, return placeholder
	result, err := m.generatePlaceholderJPEGData("JPEG-XS Frame", 640, 480)
	if err != nil {
		m.logger.WithError(err).Error("Failed to generate JPEG-XS placeholder")
		return nil, err
	}

	m.logger.WithField("result_size", len(result)).Info("JPEG-XS placeholder generated")
	return result, nil
}

// generatePlaceholderJPEG creates a placeholder JPEG with frame info
func (m *Manager) generatePlaceholderJPEG(frame *types.VideoFrame, codec string) ([]byte, error) {
	info := fmt.Sprintf("%s I-Frame\nSize: %d bytes\nPTS: %d\nNAL Units: %d",
		codec, frame.TotalSize, frame.PTS, len(frame.NALUnits))

	m.logger.WithFields(map[string]interface{}{
		"codec":            codec,
		"frame_id":         frame.ID,
		"frame_size":       frame.TotalSize,
		"nal_units":        len(frame.NALUnits),
		"pts":              frame.PTS,
		"placeholder_info": info,
	}).Info("Generating placeholder JPEG with frame metadata")

	result, err := m.generatePlaceholderJPEGData(info, 640, 480)
	if err != nil {
		m.logger.WithFields(map[string]interface{}{
			"codec": codec,
			"error": err.Error(),
		}).Error("Failed to generate placeholder JPEG")
		return nil, err
	}

	m.logger.WithFields(map[string]interface{}{
		"codec":       codec,
		"result_size": len(result),
		"dimensions":  "640x480",
	}).Info("Placeholder JPEG generated successfully")

	return result, nil
}

// generatePlaceholderJPEGData creates a simple JPEG with visual indicators
func (m *Manager) generatePlaceholderJPEGData(text string, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		width, height = 640, 480
	}

	// Create simple colored image
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Fill with gradient pattern to show it's a placeholder
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Create a gradient from blue to purple
			ratio := float64(x) / float64(width)
			r := uint8(20 + ratio*60)  // 20 to 80
			g := uint8(20 + ratio*40)  // 20 to 60
			b := uint8(80 + ratio*120) // 80 to 200
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}

	// Add some visual elements to indicate this is a placeholder
	// Draw diagonal lines
	for i := 0; i < width+height; i += 50 {
		for j := 0; j < 3; j++ {
			if i+j < width && i+j < height {
				for k := 0; k < height && i+j+k < width; k++ {
					img.Set(i+j+k, k, color.RGBA{255, 255, 255, 100})
				}
			}
		}
	}

	// Add corner markers
	markerSize := 20
	// Top-left
	for y := 0; y < markerSize; y++ {
		for x := 0; x < markerSize; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	// Top-right
	for y := 0; y < markerSize; y++ {
		for x := width - markerSize; x < width; x++ {
			img.Set(x, y, color.RGBA{0, 255, 0, 255})
		}
	}
	// Bottom-left
	for y := height - markerSize; y < height; y++ {
		for x := 0; x < markerSize; x++ {
			img.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	// Bottom-right
	for y := height - markerSize; y < height; y++ {
		for x := width - markerSize; x < width; x++ {
			img.Set(x, y, color.RGBA{255, 255, 0, 255})
		}
	}

	// Encode to JPEG
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// getFileExtensionForCodec returns appropriate file extension for codec type
func (m *Manager) getFileExtensionForCodec(codec types.CodecType) string {
	switch codec {
	case types.CodecH264:
		return "h264"
	case types.CodecHEVC:
		return "h265"
	case types.CodecAV1:
		return "av1"
	default:
		return "bin"
	}
}

// convertRobustStreamToJPEG converts a properly formatted H.264 stream to JPEG using FFmpeg
// This uses the decodable stream from robust parameter context matching
func (m *Manager) convertRobustStreamToJPEG(decodableStream []byte, codec types.CodecType) ([]byte, error) {
	// Create temporary directory for FFmpeg processing
	tempDir, err := os.MkdirTemp("", "mirror_robust_ffmpeg_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	m.logger.WithFields(map[string]interface{}{
		"component":   "ingestion_manager",
		"codec":       codec,
		"stream_size": len(decodableStream),
		"method":      "robust_parameter_context",
	}).Info("Starting robust FFmpeg conversion")

	// Write decodable stream to temporary file
	inputFile := filepath.Join(tempDir, "input."+m.getFileExtensionForCodec(codec))
	outputFile := filepath.Join(tempDir, "output.jpg")

	if err := os.WriteFile(inputFile, decodableStream, 0644); err != nil {
		return nil, fmt.Errorf("failed to write decodable stream to file: %w", err)
	}

	// Use FFmpeg to decode frame and convert to JPEG
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-f", m.getInputFormatFromCodec(codec),
		"-i", inputFile,
		"-vframes", "1",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"-q:v", "2", // Higher quality for robust approach
		"-loglevel", "warning",
		"-y", outputFile,
	)

	m.logger.WithFields(map[string]interface{}{
		"component":         "ingestion_manager",
		"codec":             codec,
		"cmd":               strings.Join(cmd.Args, " "),
		"input_file":        inputFile,
		"input_stream_size": len(decodableStream),
		"output_file":       outputFile,
	}).Debug("Executing FFmpeg command")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		m.logger.WithFields(map[string]interface{}{
			"component":   "ingestion_manager",
			"codec":       codec,
			"error":       err.Error(),
			"input_file":  inputFile,
			"output_file": outputFile,
			"stream_size": len(decodableStream),
			"stderr":      stderr.String(),
		}).Error("FFmpeg conversion failed")
		return nil, fmt.Errorf("robust FFmpeg conversion failed: %w", err)
	}

	// Read the generated JPEG
	jpegData, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read generated JPEG: %w", err)
	}

	m.logger.WithFields(map[string]interface{}{
		"component":   "ingestion_manager",
		"codec":       codec,
		"input_size":  len(decodableStream),
		"output_size": len(jpegData),
		"method":      "success",
	}).Info("Successfully converted robust stream to JPEG")

	return jpegData, nil
}

// safeUint8Slice safely extracts a []uint8 from an interface{} value
func safeUint8Slice(v interface{}) []uint8 {
	if v == nil {
		return nil
	}
	if s, ok := v.([]uint8); ok {
		return s
	}
	return nil
}

// getInputFormatFromCodec returns the appropriate FFmpeg input format for the codec type
func (m *Manager) getInputFormatFromCodec(codec types.CodecType) string {
	switch codec {
	case types.CodecH264:
		return "h264"
	case types.CodecHEVC:
		return "hevc"
	case types.CodecAV1:
		return "av01"
	default:
		return "h264" // fallback
	}
}
