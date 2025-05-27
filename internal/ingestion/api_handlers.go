package ingestion

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

// Handlers wraps the ingestion manager to provide HTTP handlers
type Handlers struct {
	manager               *Manager
	logger                logger.Logger
	frameVisualizationMgr *FrameVisualizationManager
}

// NewHandlers creates a new handlers wrapper
func NewHandlers(manager *Manager, logger logger.Logger) *Handlers {
	frameVisMgr := NewFrameVisualizationManager(manager, logger)
	
	// Set the frame visualization manager as the frame observer
	manager.SetFrameObserver(frameVisMgr)
	
	return &Handlers{
		manager:               manager,
		logger:                logger.WithField("component", "ingestion_handlers"),
		frameVisualizationMgr: frameVisMgr,
	}
}

// RegisterRoutes registers all ingestion API routes
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	// API v1 routes
	api := router.PathPrefix("/api/v1").Subrouter()

	// Stream management endpoints
	api.HandleFunc("/streams", h.manager.HandleListStreams).Methods("GET")
	api.HandleFunc("/streams/{id}", h.manager.HandleGetStream).Methods("GET")
	api.HandleFunc("/streams/{id}", h.manager.HandleDeleteStream).Methods("DELETE")
	api.HandleFunc("/streams/{id}/stats", h.manager.HandleStreamStats).Methods("GET")

	// System stats endpoint
	api.HandleFunc("/stats", h.manager.HandleStats).Methods("GET")

	// Stream control endpoints
	api.HandleFunc("/streams/{id}/pause", h.manager.HandlePauseStream).Methods("POST")
	api.HandleFunc("/streams/{id}/resume", h.manager.HandleResumeStream).Methods("POST")

	// Video stats endpoint (all streams are video-aware now)
	api.HandleFunc("/streams/stats/video", h.manager.HandleVideoStats).Methods("GET")

	// Frame-based data endpoints
	api.HandleFunc("/streams/{id}/data", h.manager.HandleStreamData).Methods("GET")
	api.HandleFunc("/streams/{id}/buffer", h.manager.HandleStreamBuffer).Methods("GET")
	api.HandleFunc("/streams/{id}/preview", h.manager.HandleStreamPreview).Methods("GET")

	// A/V sync endpoint
	api.HandleFunc("/streams/{id}/sync", h.manager.HandleStreamSync).Methods("GET")

	// Iframe endpoint
	api.HandleFunc("/streams/{id}/iframe", h.manager.HandleStreamIframe).Methods("GET")

	// Parameter set monitoring endpoint
	api.HandleFunc("/streams/{id}/parameters", h.manager.HandleStreamParameters).Methods("GET")

	// Register frame visualization routes
	h.frameVisualizationMgr.RegisterVisualizationRoutes(router)

	h.logger.Info("Ingestion routes registered")
}

// API Response DTOs
type StreamListResponse struct {
	Streams []StreamDTO `json:"streams"`
	Count   int         `json:"count"`
	Time    time.Time   `json:"time"`
}

type StreamDTO struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	SourceAddr    string         `json:"source_addr"`
	Status        string         `json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	LastHeartbeat time.Time      `json:"last_heartbeat"`
	VideoCodec    string         `json:"video_codec"`
	Resolution    string         `json:"resolution"`
	Bitrate       int64          `json:"bitrate"`
	FrameRate     float64        `json:"frame_rate"`
	Stats         StreamStatsDTO `json:"stats"`
}

type StreamStatsDTO struct {
	BytesReceived    int64               `json:"bytes_received"`
	PacketsReceived  int64               `json:"packets_received"`
	PacketsLost      int64               `json:"packets_lost"`
	Bitrate          int64               `json:"bitrate"`
	FrameBufferStats FrameBufferStatsDTO `json:"frame_buffer_stats"`
	ConnectionStats  *ConnectionStatsDTO `json:"connection_stats,omitempty"`
}

type ConnectionStatsDTO struct {
	PacketsLost      int64   `json:"packets_lost"`
	PacketsRetrans   int64   `json:"packets_retrans"`
	RTTMs            float64 `json:"rtt_ms"`
	BandwidthMbps    float64 `json:"bandwidth_mbps"`
	DeliveryDelayMs  float64 `json:"delivery_delay_ms"`
	ConnectionTimeMs int64   `json:"connection_time_ms"`
}

// FrameBufferStatsDTO contains video-aware buffer statistics
type FrameBufferStatsDTO struct {
	Capacity        int64   `json:"capacity"`         // Max frames in buffer
	Used            int64   `json:"used"`             // Current frames in buffer
	Available       int64   `json:"available"`        // Available frame slots
	FramesAssembled uint64  `json:"frames_assembled"` // Total frames assembled
	FramesDropped   uint64  `json:"frames_dropped"`   // Frames dropped due to pressure
	QueuePressure   float64 `json:"queue_pressure"`   // Current pressure (0-1)
	Keyframes       uint64  `json:"keyframes"`        // Number of keyframes
	PFrames         uint64  `json:"p_frames"`         // Number of P frames
	BFrames         uint64  `json:"b_frames"`         // Number of B frames
}

// FramePreviewResponse contains frame preview data
type FramePreviewResponse struct {
	StreamID   string      `json:"stream_id"`
	FrameCount int64       `json:"frame_count"`
	Frames     []FrameData `json:"frames"`
}

// FrameData contains individual frame information
type FrameData struct {
	Data      []byte `json:"data"`
	Timestamp int64  `json:"timestamp"`
}

// Error response
type ErrorResponse struct {
	Error   string    `json:"error"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// SuccessResponse for simple success messages
type SuccessResponse struct {
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// Helper functions
func writeJSON(ctx context.Context, w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.FromContext(ctx).WithError(err).Error("Failed to encode JSON response")
	}
}

func writeError(ctx context.Context, w http.ResponseWriter, status int, message string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	response := ErrorResponse{
		Error:   http.StatusText(status),
		Message: message,
		Time:    time.Now(),
	}

	if errMsg != "" {
		response.Message = message + ": " + errMsg
	}

	writeJSON(ctx, w, status, response)
}

// responseWriter wraps http.ResponseWriter to capture context
type responseWriter struct {
	http.ResponseWriter
	ctx interface{}
}

func (rw *responseWriter) Context() interface{} {
	return rw.ctx
}

func convertStreamToDTO(stream *registry.Stream) StreamDTO {
	return StreamDTO{
		ID:            stream.ID,
		Type:          string(stream.Type),
		SourceAddr:    stream.SourceAddr,
		Status:        string(stream.Status),
		CreatedAt:     stream.CreatedAt,
		LastHeartbeat: stream.LastHeartbeat,
		VideoCodec:    stream.VideoCodec,
		Resolution:    stream.Resolution,
		Bitrate:       stream.Bitrate,
		FrameRate:     stream.FrameRate,
		Stats: StreamStatsDTO{
			BytesReceived:   stream.BytesReceived,
			PacketsReceived: stream.PacketsReceived,
			PacketsLost:     stream.PacketsLost,
			Bitrate:         stream.Bitrate,
		},
	}
}

// convertStreamToDTOWithStats converts a stream to DTO using real-time handler stats
func convertStreamToDTOWithStats(stream *registry.Stream, handlerStats *StreamStats, handler *StreamHandler) StreamDTO {
	dto := convertStreamToDTO(stream)
	if handlerStats != nil {
		// Override with real-time handler statistics
		dto.Stats.BytesReceived = int64(handlerStats.BytesProcessed)
		dto.Stats.PacketsReceived = int64(handlerStats.PacketsReceived)
		dto.Stats.Bitrate = int64(handlerStats.Bitrate)

		// Update top-level metadata with real-time values
		dto.Bitrate = int64(handlerStats.Bitrate)

		// Update video codec with detected codec from handler
		if handlerStats.Codec != "" && handlerStats.Codec != "unknown" {
			dto.VideoCodec = handlerStats.Codec
		}

		// Calculate frame rate from GOP stats if available
		if handlerStats.GOPStats.AverageDuration > 0 && handlerStats.GOPStats.AverageGOPSize > 0 {
			avgFramesPerGOP := float64(handlerStats.GOPStats.AverageGOPSize)
			avgGOPDurationSec := handlerStats.GOPStats.AverageDuration.Seconds()
			if avgGOPDurationSec > 0 {
				dto.FrameRate = avgFramesPerGOP / avgGOPDurationSec
			}
		}

		// Use resolution and framerate from handler stats
		if handlerStats.Resolution.Width > 0 && handlerStats.Resolution.Height > 0 {
			dto.Resolution = handlerStats.Resolution.String()
		}
		if handlerStats.Framerate > 0 {
			dto.FrameRate = handlerStats.Framerate
		}

		// Add connection-level statistics if available
		if handlerStats.ConnectionStats != nil {
			dto.Stats.ConnectionStats = &ConnectionStatsDTO{
				PacketsLost:      handlerStats.ConnectionStats.PacketsLost,
				PacketsRetrans:   handlerStats.ConnectionStats.PacketsRetrans,
				RTTMs:            handlerStats.ConnectionStats.RTTMs,
				BandwidthMbps:    handlerStats.ConnectionStats.BandwidthMbps,
				DeliveryDelayMs:  handlerStats.ConnectionStats.DeliveryDelayMs,
				ConnectionTimeMs: handlerStats.ConnectionStats.ConnectionTimeMs,
			}
			// Override packets lost with connection-level data
			dto.Stats.PacketsLost = handlerStats.ConnectionStats.PacketsLost
		}

		// Populate frame buffer stats from handler
		dto.Stats.FrameBufferStats = FrameBufferStatsDTO{
			Capacity:        100, // Default frame buffer capacity
			Used:            handlerStats.QueueDepth,
			Available:       100 - handlerStats.QueueDepth,
			FramesAssembled: handlerStats.FramesAssembled,
			FramesDropped:   handlerStats.FramesDropped,
			QueuePressure:   handlerStats.QueuePressure,
			Keyframes:       handlerStats.KeyframeCount,
			PFrames:         handlerStats.PFrameCount,
			BFrames:         handlerStats.BFrameCount,
		}
	}
	return dto
}

// HandleListStreams - GET /api/v1/streams
func (m *Manager) HandleListStreams(w http.ResponseWriter, r *http.Request) {
	streams, err := m.registry.List(r.Context())
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, "Failed to list streams", err)
		return
	}

	response := StreamListResponse{
		Streams: make([]StreamDTO, 0, len(streams)),
		Count:   len(streams),
		Time:    time.Now(),
	}

	for _, stream := range streams {
		// Try to get real-time stats from handler if available (race-condition safe)
		if handler, stats, exists := m.GetStreamHandlerAndStats(stream.ID); exists {
			// Use real-time stats from handler
			dto := convertStreamToDTOWithStats(stream, &stats, handler)
			response.Streams = append(response.Streams, dto)
		} else {
			// Fallback to registry stats when handler not available
			dto := convertStreamToDTO(stream)
			response.Streams = append(response.Streams, dto)
		}
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleGetStream - GET /api/v1/streams/{id}
func (m *Manager) HandleGetStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	stream, err := m.registry.Get(r.Context(), streamID)
	if err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", err)
		return
	}

	// Try to get real-time stats from handler if available
	handler, exists := m.GetStreamHandler(streamID)
	var dto StreamDTO
	if exists {
		// Use async stats collection with timeout to prevent deadlock
		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
		defer cancel()

		statsChannel := make(chan StreamStats, 1)
		go func() {
			stats := handler.GetStats()
			select {
			case statsChannel <- stats:
			case <-ctx.Done():
			}
		}()

		select {
		case stats := <-statsChannel:
			// Use real-time stats
			dto = convertStreamToDTOWithStats(stream, &stats, handler)
		case <-ctx.Done():
			// Fall back to registry stats on timeout
			dto = convertStreamToDTO(stream)
		}
	} else {
		// Fallback to registry stats
		dto = convertStreamToDTO(stream)
	}

	writeJSON(r.Context(), w, http.StatusOK, dto)
}

// HandleStreamStats - GET /api/v1/streams/{id}/stats
func (m *Manager) HandleStreamStats(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get stream to verify it exists
	stream, err := m.registry.Get(r.Context(), streamID)
	if err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", err)
		return
	}

	// Get handler stats for real-time data
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream handler not found", nil)
		return
	}

	// Use async stats collection with timeout to prevent deadlock
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
	defer cancel()

	statsChannel := make(chan StreamStats, 1)
	go func() {
		stats := handler.GetStats()
		select {
		case statsChannel <- stats:
		case <-ctx.Done():
		}
	}()

	var stats StreamStats
	select {
	case stats = <-statsChannel:
		// Got real-time stats
	case <-ctx.Done():
		// Timeout - return error since this endpoint specifically requires handler stats
		writeError(r.Context(), w, http.StatusServiceUnavailable, "Stats collection timeout", nil)
		return
	}

	// Build response with video-aware stats (use real-time handler stats)
	response := StreamStatsDTO{
		BytesReceived:   int64(stats.BytesProcessed),
		PacketsReceived: int64(stats.PacketsReceived),
		PacketsLost:     stream.PacketsLost, // Keep registry value for lost packets
		Bitrate:         int64(stats.Bitrate),
		FrameBufferStats: FrameBufferStatsDTO{
			Capacity:        100, // Default frame buffer capacity
			Used:            stats.QueueDepth,
			Available:       100 - stats.QueueDepth,
			FramesAssembled: stats.FramesAssembled,
			FramesDropped:   stats.FramesDropped,
			QueuePressure:   stats.QueuePressure,
			Keyframes:       stats.KeyframeCount,
			PFrames:         stats.PFrameCount,
			BFrames:         stats.BFrameCount,
		},
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleDeleteStream - DELETE /api/v1/streams/{id}
func (m *Manager) HandleDeleteStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Terminate the stream
	if err := m.TerminateStream(r.Context(), streamID); err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Failed to terminate stream", err)
		return
	}

	writeJSON(r.Context(), w, http.StatusOK, SuccessResponse{
		Message:   "Stream terminated successfully",
		Data:      map[string]string{"stream_id": streamID},
		Timestamp: time.Now(),
	})
}

// HandlePauseStream - POST /api/v1/streams/{id}/pause
func (m *Manager) HandlePauseStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	if err := m.PauseStream(r.Context(), streamID); err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Failed to pause stream", err)
		return
	}

	writeJSON(r.Context(), w, http.StatusOK, SuccessResponse{
		Message:   "Stream paused successfully",
		Data:      map[string]string{"stream_id": streamID},
		Timestamp: time.Now(),
	})
}

// HandleResumeStream - POST /api/v1/streams/{id}/resume
func (m *Manager) HandleResumeStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	if err := m.ResumeStream(r.Context(), streamID); err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Failed to resume stream", err)
		return
	}

	writeJSON(r.Context(), w, http.StatusOK, SuccessResponse{
		Message:   "Stream resumed successfully",
		Data:      map[string]string{"stream_id": streamID},
		Timestamp: time.Now(),
	})
}

// HandleStats - GET /api/v1/stats
func (m *Manager) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := m.GetStats(r.Context())
	writeJSON(r.Context(), w, http.StatusOK, stats)
}

// HandleVideoStats - GET /api/v1/streams/stats/video
func (m *Manager) HandleVideoStats(w http.ResponseWriter, r *http.Request) {
	// Get stats from all stream handlers (all are video-aware now)
	m.handlersMu.RLock()
	// Create a copy of handlers to avoid holding lock during GetStats calls
	handlers := make(map[string]*StreamHandler, len(m.streamHandlers))
	for streamID, handler := range m.streamHandlers {
		handlers[streamID] = handler
	}
	m.handlersMu.RUnlock()

	stats := make(map[string]interface{})
	for streamID, handler := range handlers {
		stats[streamID] = handler.GetStats()
	}

	// Convert to response format
	response := struct {
		Streams   map[string]interface{} `json:"streams"`
		Count     int                    `json:"count"`
		Timestamp time.Time              `json:"timestamp"`
	}{
		Streams:   stats,
		Count:     len(stats),
		Timestamp: time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}
