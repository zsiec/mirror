package ingestion

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/zsiec/mirror/internal/ingestion/sync"
)

// SyncStatusResponse represents the sync status for a stream
type SyncStatusResponse struct {
	StreamID string                 `json:"stream_id"`
	Status   sync.SyncStatusType    `json:"status"`
	Video    *TrackStatusDTO        `json:"video,omitempty"`
	Audio    *TrackStatusDTO        `json:"audio,omitempty"`
	Drift    *DriftStatusDTO        `json:"drift,omitempty"`
	Time     time.Time              `json:"time"`
}

// TrackStatusDTO represents the status of a single track
type TrackStatusDTO struct {
	Type         string        `json:"type"`
	LastPTS      int64         `json:"last_pts"`
	LastDTS      int64         `json:"last_dts"`
	BaseTime     time.Time     `json:"base_time"`
	PacketCount  int64         `json:"packet_count"`
	DroppedCount int64         `json:"dropped_count"`
	WrapCount    int           `json:"wrap_count"`
	JumpCount    int           `json:"jump_count"`
	ErrorCount   int           `json:"error_count"`
}

// DriftStatusDTO represents drift statistics
type DriftStatusDTO struct {
	CurrentDrift  time.Duration `json:"current_drift"`
	MaxDrift      time.Duration `json:"max_drift"`
	MinDrift      time.Duration `json:"min_drift"`
	AvgDrift      time.Duration `json:"avg_drift"`
	SampleCount   int           `json:"sample_count"`
	LastCorrected time.Time     `json:"last_corrected,omitempty"`
}

// HandleStreamSync - GET /api/v1/streams/{id}/sync
// Returns A/V synchronization status for a stream
func (m *Manager) HandleStreamSync(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	streamID := vars["id"]

	// Get handler
	handler, exists := m.GetStreamHandler(streamID)
	if !exists {
		writeError(r.Context(), w, http.StatusNotFound, "Stream not found", nil)
		return
	}

	// Get sync manager from handler
	syncManager := handler.GetSyncManager()
	if syncManager == nil {
		writeError(r.Context(), w, http.StatusNotFound, "Sync not available for this stream", nil)
		return
	}

	// Get sync statistics
	stats := syncManager.GetStatistics()
	
	// Build response
	response := SyncStatusResponse{
		StreamID: streamID,
		Status:   sync.SyncStatusSynced, // Default status
		Time:     time.Now(),
	}

	// Check if in sync
	if inSync, ok := stats["in_sync"].(bool); ok && !inSync {
		response.Status = sync.SyncStatusDrifting
	}

	// Add video track info if available
	if videoStats, ok := stats["video"].(map[string]interface{}); ok {
		response.Video = &TrackStatusDTO{
			Type: "video",
		}
		if lastPTS, ok := videoStats["last_pts"].(int64); ok {
			response.Video.LastPTS = lastPTS
		}
		if lastDTS, ok := videoStats["last_dts"].(int64); ok {
			response.Video.LastDTS = lastDTS
		}
		if frameCount, ok := videoStats["frame_count"].(uint64); ok {
			response.Video.PacketCount = int64(frameCount)
		}
		if droppedCount, ok := videoStats["dropped_count"].(int64); ok {
			response.Video.DroppedCount = droppedCount
		}
		if wrapCount, ok := videoStats["wrap_count"].(int); ok {
			response.Video.WrapCount = wrapCount
		}
		if jumpCount, ok := videoStats["jump_count"].(int); ok {
			response.Video.JumpCount = jumpCount
		}
		if errorCount, ok := videoStats["error_count"].(int); ok {
			response.Video.ErrorCount = errorCount
		}
	}

	// Add audio track info if available
	if audioStats, ok := stats["audio"].(map[string]interface{}); ok {
		response.Audio = &TrackStatusDTO{
			Type: "audio",
		}
		if lastPTS, ok := audioStats["last_pts"].(int64); ok {
			response.Audio.LastPTS = lastPTS
		}
		if lastDTS, ok := audioStats["last_dts"].(int64); ok {
			response.Audio.LastDTS = lastDTS
		}
		if frameCount, ok := audioStats["frame_count"].(uint64); ok {
			response.Audio.PacketCount = int64(frameCount)
		}
		if droppedCount, ok := audioStats["dropped_count"].(int64); ok {
			response.Audio.DroppedCount = droppedCount
		}
		if wrapCount, ok := audioStats["wrap_count"].(int); ok {
			response.Audio.WrapCount = wrapCount
		}
		if jumpCount, ok := audioStats["jump_count"].(int); ok {
			response.Audio.JumpCount = jumpCount
		}
		if errorCount, ok := audioStats["error_count"].(int); ok {
			response.Audio.ErrorCount = errorCount
		}
	}

	// Add drift info if available
	if response.Video != nil && response.Audio != nil {
		response.Drift = &DriftStatusDTO{}
		
		if currentDriftMs, ok := stats["current_drift_ms"].(int64); ok {
			response.Drift.CurrentDrift = time.Duration(currentDriftMs) * time.Millisecond
		}
		if avgDriftMs, ok := stats["avg_drift_ms"].(int64); ok {
			response.Drift.AvgDrift = time.Duration(avgDriftMs) * time.Millisecond
		}
		// TODO: Add max/min drift when available in stats
		if correctionCount, ok := stats["correction_count"].(int); ok {
			response.Drift.SampleCount = correctionCount
		}
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		m.logger.WithError(err).Error("Failed to encode sync response")
		writeError(r.Context(), w, http.StatusInternalServerError, "Failed to encode response", err)
	}
}
