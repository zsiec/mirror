package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// FrameVisualizationManager handles frame data streaming for visualization
type FrameVisualizationManager struct {
	manager *Manager
	logger  logger.Logger

	// WebSocket connections for real-time streaming
	connections map[string]*FrameStreamConnection
	connMutex   sync.RWMutex

	// Frame capture controls
	captureEnabled bool
	captureBuffer  *FrameCaptureBuffer
	captureMutex   sync.RWMutex
}

// FrameStreamConnection represents a WebSocket connection for frame streaming
type FrameStreamConnection struct {
	conn     *websocket.Conn
	streamID string
	send     chan *FrameVisualizationData
	ctx      context.Context
	cancel   context.CancelFunc
}

// FrameCaptureBuffer stores captured frames for detailed analysis
type FrameCaptureBuffer struct {
	frames  []CapturedFrame
	maxSize int
	index   int
	mutex   sync.RWMutex
	enabled bool
}

// CapturedFrame represents a captured frame with full metadata
type CapturedFrame struct {
	Frame          *types.VideoFrame `json:"frame"`
	CaptureTime    time.Time         `json:"capture_time"`
	ProcessingTime time.Duration     `json:"processing_time"`
	Stats          FrameStats        `json:"stats"`
}

// FrameVisualizationData contains real-time frame data for visualization
type FrameVisualizationData struct {
	StreamID   string         `json:"stream_id"`
	Timestamp  time.Time      `json:"timestamp"`
	FrameType  string         `json:"frame_type"`
	FrameSize  int            `json:"frame_size"`
	PTS        int64          `json:"pts"`
	DTS        int64          `json:"dts"`
	Duration   int64          `json:"duration"`
	IsKeyframe bool           `json:"is_keyframe"`
	NALUnits   []NALUnitInfo  `json:"nal_units"`
	Bitrate    float64        `json:"bitrate"`
	Codec      string         `json:"codec"`
	Resolution ResolutionInfo `json:"resolution"`
	GOP        GOPInfo        `json:"gop"`
	Timing     TimingInfo     `json:"timing"`
	Flags      []string       `json:"flags"`
	QP         int            `json:"qp"`
	PSNR       float64        `json:"psnr"`
}

// NALUnitInfo contains information about individual NAL units
type NALUnitInfo struct {
	Type       uint8  `json:"type"`
	TypeName   string `json:"type_name"`
	Size       int    `json:"size"`
	Importance uint8  `json:"importance"`
	RefIdc     uint8  `json:"ref_idc"`
}

// ResolutionInfo contains resolution and video format information
type ResolutionInfo struct {
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ChromaFmt string `json:"chroma_format"`
	BitDepth  int    `json:"bit_depth"`
	Profile   string `json:"profile"`
	Level     string `json:"level"`
}

// GOPInfo contains GOP-related information
type GOPInfo struct {
	GOPID       uint64 `json:"gop_id"`
	Position    int    `json:"position"`
	GOPSize     int    `json:"gop_size"`
	GOPDuration int64  `json:"gop_duration"`
	IsLastInGOP bool   `json:"is_last_in_gop"`
}

// TimingInfo contains frame timing information
type TimingInfo struct {
	CaptureTime       time.Time     `json:"capture_time"`
	CompleteTime      time.Time     `json:"complete_time"`
	PresentationTime  time.Time     `json:"presentation_time"`
	ProcessingLatency time.Duration `json:"processing_latency"`
	DriftCorrected    bool          `json:"drift_corrected"`
}

// FrameStats contains frame processing statistics
type FrameStats struct {
	AssemblyTime        time.Duration `json:"assembly_time"`
	PacketCount         int           `json:"packet_count"`
	DroppedPackets      int           `json:"dropped_packets"`
	RetransmissionCount int           `json:"retransmission_count"`
	JitterMs            float64       `json:"jitter_ms"`
	BufferDepth         int           `json:"buffer_depth"`
}

// FrameCaptureControl contains capture control settings
type FrameCaptureControl struct {
	Enabled   bool   `json:"enabled"`
	MaxFrames int    `json:"max_frames"`
	AutoStop  bool   `json:"auto_stop"`
	StreamID  string `json:"stream_id,omitempty"`
}

// FrameListResponse contains list of captured frames
type FrameListResponse struct {
	Frames    []CapturedFrame `json:"frames"`
	Count     int             `json:"count"`
	Enabled   bool            `json:"enabled"`
	Timestamp time.Time       `json:"timestamp"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for demo
	},
}

// NewFrameVisualizationManager creates a new frame visualization manager
func NewFrameVisualizationManager(manager *Manager, logger logger.Logger) *FrameVisualizationManager {
	fvm := &FrameVisualizationManager{
		manager:     manager,
		logger:      logger.WithField("component", "frame_visualization"),
		connections: make(map[string]*FrameStreamConnection),
		captureBuffer: &FrameCaptureBuffer{
			frames:  make([]CapturedFrame, 0, 1000),
			maxSize: 1000,
			enabled: false,
		},
	}

	// Start cleanup goroutine for orphaned connections
	go fvm.cleanupOrphanedConnections()

	return fvm
}

// cleanupOrphanedConnections periodically checks for connections to non-existent streams
func (fvm *FrameVisualizationManager) cleanupOrphanedConnections() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		fvm.connMutex.RLock()
		connectionsToClose := make([]string, 0)
		
		for connKey, streamConn := range fvm.connections {
			// Check if stream still exists
			if _, exists := fvm.manager.GetStreamHandler(streamConn.streamID); !exists {
				connectionsToClose = append(connectionsToClose, connKey)
			}
		}
		fvm.connMutex.RUnlock()

		// Close orphaned connections
		for _, connKey := range connectionsToClose {
			fvm.connMutex.Lock()
			if streamConn, exists := fvm.connections[connKey]; exists {
				fvm.logger.WithField("stream_id", streamConn.streamID).Info("Cleaning up orphaned WebSocket connection")
				streamConn.cancel()
				delete(fvm.connections, connKey)
			}
			fvm.connMutex.Unlock()
		}
	}
}

// RegisterVisualizationRoutes registers frame visualization routes
func (fvm *FrameVisualizationManager) RegisterVisualizationRoutes(router *mux.Router) {
	api := router.PathPrefix("/api/v1").Subrouter()

	// WebSocket endpoint for real-time frame streaming
	api.HandleFunc("/streams/{id}/frames/live", fvm.HandleFrameStream).Methods("GET")

	// Frame capture control endpoints
	api.HandleFunc("/frames/capture/start", fvm.HandleStartCapture).Methods("POST")
	api.HandleFunc("/frames/capture/stop", fvm.HandleStopCapture).Methods("POST")
	api.HandleFunc("/frames/capture/status", fvm.HandleCaptureStatus).Methods("GET")
	api.HandleFunc("/frames/capture/clear", fvm.HandleClearCapture).Methods("POST")

	// Captured frames access
	api.HandleFunc("/frames/captured", fvm.HandleListCapturedFrames).Methods("GET")
	api.HandleFunc("/frames/captured/{index}", fvm.HandleGetCapturedFrame).Methods("GET")

	// Frame analysis endpoints
	api.HandleFunc("/streams/{id}/frames/analysis", fvm.HandleFrameAnalysis).Methods("GET")
	api.HandleFunc("/streams/{id}/frames/types", fvm.HandleFrameTypes).Methods("GET")

	fvm.logger.Info("Frame visualization routes registered")
}

// HandleFrameStream handles WebSocket connections for real-time frame streaming
func (fvm *FrameVisualizationManager) HandleFrameStream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Verify stream exists
	_, err := fvm.manager.registry.Get(r.Context(), streamID)
	if err != nil {
		http.Error(w, "Stream not found", http.StatusNotFound)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fvm.logger.WithError(err).Error("Failed to upgrade to WebSocket")
		return
	}

	ctx, cancel := context.WithCancel(r.Context())

	// Create connection wrapper
	streamConn := &FrameStreamConnection{
		conn:     conn,
		streamID: streamID,
		send:     make(chan *FrameVisualizationData, 100),
		ctx:      ctx,
		cancel:   cancel,
	}

	// Register connection
	connKey := fmt.Sprintf("%s_%p", streamID, conn)
	fvm.connMutex.Lock()
	fvm.connections[connKey] = streamConn
	fvm.connMutex.Unlock()

	fvm.logger.WithField("stream_id", streamID).Info("WebSocket connection established for frame streaming")

	// Start frame monitoring for this stream
	go fvm.monitorFrames(streamConn)

	// Handle WebSocket communication
	go fvm.handleWebSocketConnection(streamConn, connKey)
}

// monitorFrames monitors stream handler existence and cleans up when stream is removed
func (fvm *FrameVisualizationManager) monitorFrames(streamConn *FrameStreamConnection) {
	defer func() {
		close(streamConn.send)
		streamConn.cancel()
	}()

	// Check if stream handler exists initially
	_, exists := fvm.manager.GetStreamHandler(streamConn.streamID)
	if !exists {
		fvm.logger.WithField("stream_id", streamConn.streamID).Error("Stream handler not found")
		return
	}

	// Monitor stream handler existence
	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	fvm.logger.WithField("stream_id", streamConn.streamID).Info("WebSocket monitoring started - frames will be sent via observer pattern")

	for {
		select {
		case <-streamConn.ctx.Done():
			return
		case <-ticker.C:
			// Check if stream handler still exists
			if _, stillExists := fvm.manager.GetStreamHandler(streamConn.streamID); !stillExists {
				fvm.logger.WithField("stream_id", streamConn.streamID).Info("Stream handler no longer exists, closing connection")
				return
			}
		}
	}
}


// getFrameFlags returns frame flags based on frame type
func (fvm *FrameVisualizationManager) getFrameFlags(frameType string) []string {
	switch frameType {
	case "I":
		return []string{"keyframe", "reference"}
	case "P":
		return []string{"reference"}
	case "B":
		return []string{"discardable"}
	default:
		return []string{}
	}
}

// OnFrameProcessed implements FrameObserver interface
func (fvm *FrameVisualizationManager) OnFrameProcessed(frame *types.VideoFrame, streamHandler *StreamHandler) {
	// Convert real frame to visualization data
	frameData := fvm.convertFrameToVisualizationData(frame, streamHandler)
	
	// Add to capture buffer if enabled
	fvm.addToCaptureBuffer(frameData)
	
	// Send to any active WebSocket connections for this stream
	fvm.connMutex.RLock()
	for _, streamConn := range fvm.connections {
		if streamConn.streamID == frame.StreamID {
			select {
			case streamConn.send <- frameData:
			default:
				// Channel full, skip frame
				fvm.logger.Debug("Frame visualization channel full, skipping frame")
			}
		}
	}
	fvm.connMutex.RUnlock()
}

// convertFrameToVisualizationData converts a real VideoFrame to FrameVisualizationData
func (fvm *FrameVisualizationManager) convertFrameToVisualizationData(frame *types.VideoFrame, streamHandler *StreamHandler) *FrameVisualizationData {
	// Get stream stats for additional context
	stats := streamHandler.GetStats()
	
	// Convert NAL units
	nalUnits := make([]NALUnitInfo, len(frame.NALUnits))
	for i, nal := range frame.NALUnits {
		nalUnits[i] = NALUnitInfo{
			Type:       nal.Type,
			TypeName:   fvm.getNALTypeName(nal.Type),
			Size:       len(nal.Data),
			Importance: nal.RefIdc,
			RefIdc:     nal.RefIdc,
		}
	}
	
	// Convert frame type to string
	frameTypeStr := frame.Type.String()
	
	return &FrameVisualizationData{
		StreamID:   frame.StreamID,
		Timestamp:  frame.CompleteTime,
		FrameType:  frameTypeStr,
		FrameSize:  frame.TotalSize,
		PTS:        frame.PTS,
		DTS:        frame.DTS,
		Duration:   frame.Duration,
		IsKeyframe: frame.IsKeyframe(),
		NALUnits:   nalUnits,
		Bitrate:    float64(stats.Bitrate),
		Codec:      string(stats.Codec),
		Resolution: ResolutionInfo{
			Width:     int(stats.Resolution.Width),
			Height:    int(stats.Resolution.Height),
			ChromaFmt: "4:2:0", // Default, could be enhanced
			BitDepth:  8,       // Default, could be enhanced
			Profile:   "Unknown", // Could be enhanced from SPS
			Level:     "Unknown", // Could be enhanced from SPS
		},
		GOP: GOPInfo{
			GOPID:       frame.GOPId,
			Position:    frame.GOPPosition,
			GOPSize:     0, // Would need to be calculated
			GOPDuration: 0, // Would need to be calculated
			IsLastInGOP: false, // Would need to be determined
		},
		Timing: TimingInfo{
			CaptureTime:       frame.CaptureTime,
			CompleteTime:      frame.CompleteTime,
			ProcessingLatency: time.Since(frame.CaptureTime),
		},
		Flags: fvm.getFrameFlags(frameTypeStr),
		QP:    0,    // Would need to be extracted from frame data
		PSNR:  0.0,  // Would need to be calculated
	}
}

// getNALTypeName returns a human-readable name for NAL unit type
func (fvm *FrameVisualizationManager) getNALTypeName(nalType uint8) string {
	// H.264 NAL unit types (simplified)
	switch nalType {
	case 1:
		return "Coded slice (non-IDR)"
	case 5:
		return "Coded slice (IDR)"
	case 6:
		return "SEI"
	case 7:
		return "SPS"
	case 8:
		return "PPS"
	case 9:
		return "AUD"
	default:
		return fmt.Sprintf("NAL type %d", nalType)
	}
}

// addToCaptureBuffer adds frame data to capture buffer if enabled
func (fvm *FrameVisualizationManager) addToCaptureBuffer(frameData *FrameVisualizationData) {
	fvm.captureMutex.RLock()
	enabled := fvm.captureBuffer.enabled
	fvm.captureMutex.RUnlock()

	if !enabled {
		return
	}

	// Convert to captured frame
	capturedFrame := CapturedFrame{
		Frame: &types.VideoFrame{
			ID:           uint64(frameData.PTS), // Use PTS as frame ID for demo
			StreamID:     frameData.StreamID,
			Type:         fvm.parseFrameType(frameData.FrameType),
			TotalSize:    frameData.FrameSize,
			PTS:          frameData.PTS,
			DTS:          frameData.DTS,
			Duration:     frameData.Duration,
			CaptureTime:  frameData.Timing.CaptureTime,
			CompleteTime: frameData.Timing.CompleteTime,
			QP:           frameData.QP,
			PSNR:         frameData.PSNR,
		},
		CaptureTime:    frameData.Timestamp,
		ProcessingTime: frameData.Timing.ProcessingLatency,
		Stats: FrameStats{
			AssemblyTime:   frameData.Timing.ProcessingLatency,
			PacketCount:    len(frameData.NALUnits),
			DroppedPackets: 0,
			JitterMs:       0.5,
			BufferDepth:    10,
		},
	}

	fvm.captureMutex.Lock()
	defer fvm.captureMutex.Unlock()

	if len(fvm.captureBuffer.frames) >= fvm.captureBuffer.maxSize {
		// Ring buffer behavior
		fvm.captureBuffer.frames[fvm.captureBuffer.index] = capturedFrame
		fvm.captureBuffer.index = (fvm.captureBuffer.index + 1) % fvm.captureBuffer.maxSize
	} else {
		fvm.captureBuffer.frames = append(fvm.captureBuffer.frames, capturedFrame)
	}
}

// parseFrameType converts string frame type to types.FrameType
func (fvm *FrameVisualizationManager) parseFrameType(frameType string) types.FrameType {
	switch frameType {
	case "I":
		return types.FrameTypeI
	case "P":
		return types.FrameTypeP
	case "B":
		return types.FrameTypeB
	case "IDR":
		return types.FrameTypeIDR
	default:
		return types.FrameTypeP
	}
}

// handleWebSocketConnection handles WebSocket communication
func (fvm *FrameVisualizationManager) handleWebSocketConnection(streamConn *FrameStreamConnection, connKey string) {
	defer func() {
		streamConn.conn.Close()
		fvm.connMutex.Lock()
		delete(fvm.connections, connKey)
		fvm.connMutex.Unlock()
		fvm.logger.WithField("stream_id", streamConn.streamID).Info("WebSocket connection closed")
	}()

	// Handle incoming messages (for control)
	go func() {
		for {
			_, _, err := streamConn.conn.ReadMessage()
			if err != nil {
				streamConn.cancel()
				return
			}
		}
	}()

	// Send frame data
	for {
		select {
		case frameData, ok := <-streamConn.send:
			if !ok {
				streamConn.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := streamConn.conn.WriteJSON(frameData); err != nil {
				fvm.logger.WithError(err).Error("Failed to send frame data over WebSocket")
				return
			}

		case <-streamConn.ctx.Done():
			return
		}
	}
}

// HandleStartCapture starts frame capture
func (fvm *FrameVisualizationManager) HandleStartCapture(w http.ResponseWriter, r *http.Request) {
	var control FrameCaptureControl
	if err := json.NewDecoder(r.Body).Decode(&control); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	fvm.captureMutex.Lock()
	fvm.captureBuffer.enabled = true
	if control.MaxFrames > 0 {
		fvm.captureBuffer.maxSize = control.MaxFrames
	}
	fvm.captureMutex.Unlock()

	writeJSON(r.Context(), w, http.StatusOK, SuccessResponse{
		Message:   "Frame capture started",
		Timestamp: time.Now(),
	})
}

// HandleStopCapture stops frame capture
func (fvm *FrameVisualizationManager) HandleStopCapture(w http.ResponseWriter, r *http.Request) {
	fvm.captureMutex.Lock()
	fvm.captureBuffer.enabled = false
	fvm.captureMutex.Unlock()

	writeJSON(r.Context(), w, http.StatusOK, SuccessResponse{
		Message:   "Frame capture stopped",
		Timestamp: time.Now(),
	})
}

// HandleCaptureStatus returns capture status
func (fvm *FrameVisualizationManager) HandleCaptureStatus(w http.ResponseWriter, r *http.Request) {
	fvm.captureMutex.RLock()
	status := FrameCaptureControl{
		Enabled:   fvm.captureBuffer.enabled,
		MaxFrames: fvm.captureBuffer.maxSize,
	}
	frameCount := len(fvm.captureBuffer.frames)
	fvm.captureMutex.RUnlock()

	response := struct {
		FrameCaptureControl
		CapturedFrames int `json:"captured_frames"`
	}{
		FrameCaptureControl: status,
		CapturedFrames:      frameCount,
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleClearCapture clears captured frames
func (fvm *FrameVisualizationManager) HandleClearCapture(w http.ResponseWriter, r *http.Request) {
	fvm.captureMutex.Lock()
	fvm.captureBuffer.frames = fvm.captureBuffer.frames[:0]
	fvm.captureBuffer.index = 0
	fvm.captureMutex.Unlock()

	writeJSON(r.Context(), w, http.StatusOK, SuccessResponse{
		Message:   "Captured frames cleared",
		Timestamp: time.Now(),
	})
}

// HandleListCapturedFrames returns list of captured frames
func (fvm *FrameVisualizationManager) HandleListCapturedFrames(w http.ResponseWriter, r *http.Request) {
	fvm.captureMutex.RLock()
	frames := make([]CapturedFrame, len(fvm.captureBuffer.frames))
	copy(frames, fvm.captureBuffer.frames)
	enabled := fvm.captureBuffer.enabled
	fvm.captureMutex.RUnlock()

	response := FrameListResponse{
		Frames:    frames,
		Count:     len(frames),
		Enabled:   enabled,
		Timestamp: time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}

// HandleGetCapturedFrame returns a specific captured frame
func (fvm *FrameVisualizationManager) HandleGetCapturedFrame(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	indexStr := vars["index"]

	index, err := strconv.Atoi(indexStr)
	if err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, "Invalid frame index", err)
		return
	}

	fvm.captureMutex.RLock()
	defer fvm.captureMutex.RUnlock()

	if index < 0 || index >= len(fvm.captureBuffer.frames) {
		writeError(r.Context(), w, http.StatusNotFound, "Frame not found", nil)
		return
	}

	frame := fvm.captureBuffer.frames[index]
	writeJSON(r.Context(), w, http.StatusOK, frame)
}

// HandleFrameAnalysis provides frame analysis for a stream
func (fvm *FrameVisualizationManager) HandleFrameAnalysis(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Verify stream exists
	_, err := fvm.manager.registry.Get(r.Context(), streamID)
	if err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", err)
		return
	}

	// Get handler stats
	handler, exists := fvm.manager.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream handler not found", nil)
		return
	}

	stats := handler.GetStats()

	analysis := struct {
		StreamID       string    `json:"stream_id"`
		TotalFrames    uint64    `json:"total_frames"`
		KeyFrames      uint64    `json:"keyframes"`
		PFrames        uint64    `json:"p_frames"`
		BFrames        uint64    `json:"b_frames"`
		FrameRate      float64   `json:"frame_rate"`
		AverageBitrate float64   `json:"average_bitrate"`
		GOPSize        int       `json:"gop_size"`
		Timestamp      time.Time `json:"timestamp"`
	}{
		StreamID:       streamID,
		TotalFrames:    stats.FramesAssembled,
		KeyFrames:      stats.KeyframeCount,
		PFrames:        stats.PFrameCount,
		BFrames:        stats.BFrameCount,
		FrameRate:      stats.Framerate,
		AverageBitrate: stats.Bitrate,
		GOPSize:        int(stats.GOPStats.AverageGOPSize),
		Timestamp:      time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, analysis)
}

// HandleFrameTypes returns frame type distribution for a stream
func (fvm *FrameVisualizationManager) HandleFrameTypes(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Verify stream exists
	_, err := fvm.manager.registry.Get(r.Context(), streamID)
	if err != nil {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", err)
		return
	}

	// Get handler stats
	handler, exists := fvm.manager.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream handler not found", nil)
		return
	}

	stats := handler.GetStats()

	response := struct {
		StreamID   string            `json:"stream_id"`
		FrameTypes map[string]uint64 `json:"frame_types"`
		Timestamp  time.Time         `json:"timestamp"`
	}{
		StreamID: streamID,
		FrameTypes: map[string]uint64{
			"I": stats.KeyframeCount,
			"P": stats.PFrameCount,
			"B": stats.BFrameCount,
		},
		Timestamp: time.Now(),
	}

	writeJSON(r.Context(), w, http.StatusOK, response)
}
