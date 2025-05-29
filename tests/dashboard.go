package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zsiec/mirror/tests/ui"
)

// Dashboard provides a real-time CLI dashboard for integration test visualization
type Dashboard struct {
	env        *TestEnvironment
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	stats      DashboardStats
	startTime  time.Time
	isRunning  bool
	ffmpegMode bool
	output     *os.File               // Output file for dashboard display
	richModel  *ui.RichDashboardModel // Rich Bubble Tea model
	richProg   *tea.Program           // Bubble Tea program
}

// DashboardStats holds comprehensive statistics for display
type DashboardStats struct {
	// Core Status
	ServerStatus string
	SRTSessions  int
	RTPSessions  int
	TotalStreams int
	TestPhase    string
	Progress     int // 0-100

	// Network Metrics
	PacketsRTP     int64
	PacketsSRT     int64
	BitrateRTP     float64
	BitrateSRT     float64
	BitrateHistory []float64 // For sparklines
	PacketLossRTP  float64
	PacketLossSRT  float64
	JitterRTP      float64
	LatencyRTP     float64

	// Memory & Resources
	MemoryUsed    int64
	MemoryLimit   int64
	MemoryPercent float64
	BufferUsage   float64
	CPUUsage      float64
	Goroutines    int

	// Stream Details
	StreamsActive []StreamInfo
	StreamStats   map[string]StreamDetailedStats

	// Video Processing
	FramesProcessed int64
	FramesDropped   int64
	GOPsProcessed   int64
	CodecStats      map[string]int

	// Buffer Status
	BufferStats map[string]BufferInfo

	// Connection Quality
	ConnectionQuality map[string]ConnectionQuality

	// Activity & Logs
	RecentLogs   []LogEntry
	ErrorCount   int
	WarningCount int
}

// LogEntry represents a log entry for display
type LogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Component string
}

// StreamInfo represents complete stream information from API
type StreamInfo struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	SourceAddr    string    `json:"source_addr"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	VideoCodec    string    `json:"video_codec"`
	Resolution    string    `json:"resolution"`
	Bitrate       int64     `json:"bitrate"`
	FrameRate     float64   `json:"frame_rate"`
	Stats         struct {
		BytesReceived    int64 `json:"bytes_received"`
		PacketsReceived  int64 `json:"packets_received"`
		PacketsLost      int64 `json:"packets_lost"`
		Bitrate          int64 `json:"bitrate"`
		FrameBufferStats struct {
			Capacity        int64   `json:"capacity"`
			Used            int64   `json:"used"`
			Available       int64   `json:"available"`
			FramesAssembled uint64  `json:"frames_assembled"`
			FramesDropped   uint64  `json:"frames_dropped"`
			QueuePressure   float64 `json:"queue_pressure"`
			Keyframes       uint64  `json:"keyframes"`
			PFrames         uint64  `json:"p_frames"`
			BFrames         uint64  `json:"b_frames"`
		} `json:"frame_buffer_stats"`
	} `json:"stats"`
}

// StreamDetailedStats holds detailed per-stream statistics
type StreamDetailedStats struct {
	PacketsReceived int64
	PacketsDropped  int64
	BytesReceived   int64
	FramesProcessed int64
	FramesDropped   int64
	BufferFill      float64
	Latency         float64
	Jitter          float64
	BitrateHistory  []float64
	LastUpdate      time.Time
}

// BufferInfo represents buffer status
type BufferInfo struct {
	Size       int64
	Used       int64
	Percent    float64
	RingBuffer float64
	GOPBuffer  int
}

// ConnectionQuality represents connection health
type ConnectionQuality struct {
	SignalStrength string // "Excellent", "Good", "Fair", "Poor"
	PacketLoss     float64
	RTT            float64
	Bandwidth      float64
	Stability      string
}

// NewDashboard creates a new dashboard instance
func NewDashboard(env *TestEnvironment, ffmpegMode bool, output *os.File) *Dashboard {
	ctx, cancel := context.WithCancel(context.Background())

	d := &Dashboard{
		env:        env,
		ctx:        ctx,
		cancel:     cancel,
		startTime:  time.Now(),
		ffmpegMode: ffmpegMode,
		output:     output,
		stats: DashboardStats{
			ServerStatus: "Starting",
			TestPhase:    "Initialization",
			RecentLogs:   make([]LogEntry, 0, 10), // Keep last 10 log entries
		},
	}

	// Initialize Rich dashboard (without alt screen so output stays visible)
	d.richModel = ui.NewRichDashboardModel(d, ffmpegMode)
	d.richProg = tea.NewProgram(d.richModel)

	return d
}

// GetHTTPClient implements ui.TestEnvironment interface
func (d *Dashboard) GetHTTPClient() *http.Client {
	return d.env.HTTPClient
}

// AddLogEntry adds a log entry to the dashboard
func (d *Dashboard) AddLogEntry(level, component, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Component: component,
	}

	// Add to recent logs (keep last 10)
	d.stats.RecentLogs = append(d.stats.RecentLogs, entry)
	if len(d.stats.RecentLogs) > 10 {
		d.stats.RecentLogs = d.stats.RecentLogs[1:]
	}

	// Forward to Rich dashboard
	if d.richModel != nil {
		d.richModel.AddLogEntry(level, component, message)
	}
}

// Start begins the dashboard display and stats collection
func (d *Dashboard) Start() {
	d.mu.Lock()
	d.isRunning = true

	// Reset state for clean start
	d.stats = DashboardStats{
		ServerStatus: "Starting",
		TestPhase:    "Initialization",
		RecentLogs:   make([]LogEntry, 0, 10),
	}
	d.startTime = time.Now()
	d.mu.Unlock()

	// Clear Redis state to prevent persistence between runs
	d.env.ClearRedisState()

	// Give the server a moment to detect Redis changes
	time.Sleep(250 * time.Millisecond)

	// Reset Rich dashboard
	if d.richModel != nil {
		d.richModel.Reset()
	}

	// Start stats collection
	go d.collectStats()

	// Start the Rich dashboard display updates
	go d.updateRichDisplay()
}

// collectStats fetches stats from the API
func (d *Dashboard) collectStats() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.fetchAndUpdateStats()
		}
	}
}

// fetchAndUpdateStats fetches comprehensive statistics from the server
func (d *Dashboard) fetchAndUpdateStats() {
	// Initialize maps if nil
	d.mu.Lock()
	if d.stats.StreamStats == nil {
		d.stats.StreamStats = make(map[string]StreamDetailedStats)
	}
	if d.stats.BufferStats == nil {
		d.stats.BufferStats = make(map[string]BufferInfo)
	}
	if d.stats.ConnectionQuality == nil {
		d.stats.ConnectionQuality = make(map[string]ConnectionQuality)
	}
	if d.stats.CodecStats == nil {
		d.stats.CodecStats = make(map[string]int)
	}
	if d.stats.BitrateHistory == nil {
		d.stats.BitrateHistory = make([]float64, 0, 30) // 30 data points for sparkline
	}
	d.mu.Unlock()

	// Try multiple endpoints since the server might be starting up
	// Try HTTPS first to avoid TLS handshake errors
	endpoints := []string{
		"https://127.0.0.1:8080/api/v1/stats",
		"https://localhost:8080/api/v1/stats",
		"http://127.0.0.1:8080/api/v1/stats",
		"http://localhost:8080/api/v1/stats",
	}

	var success bool
	for _, endpoint := range endpoints {
		if d.tryFetchStats(endpoint) {
			success = true
			break // Success, stop trying other endpoints
		}
	}

	if success {
		// Fetch detailed metrics
		d.fetchDetailedStreamStats()
		d.fetchBufferStatus()
		d.fetchMemoryMetrics()
		d.fetchConnectionQuality()
		d.updateBitrateHistory()
		d.generateActivityLogs()
	}
}

// tryFetchStats tries to fetch stats from a specific endpoint
func (d *Dashboard) tryFetchStats(endpoint string) bool {
	resp, err := d.env.HTTPClient.Get(endpoint)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	var apiStats struct {
		Started      bool `json:"started"`
		SRTSessions  int  `json:"srt_sessions"`
		RTPSessions  int  `json:"rtp_sessions"`
		TotalStreams int  `json:"total_streams"`
	}

	if err := json.Unmarshal(body, &apiStats); err != nil {
		return false
	}

	// Update dashboard stats
	d.mu.Lock()
	d.stats.SRTSessions = apiStats.SRTSessions
	d.stats.RTPSessions = apiStats.RTPSessions
	d.stats.TotalStreams = apiStats.TotalStreams
	if apiStats.Started {
		d.stats.ServerStatus = "Running"
	}
	d.mu.Unlock()

	// Try to fetch stream details
	d.tryFetchStreamDetails(strings.Replace(endpoint, "/stats", "/streams", 1))

	return true
}

// tryFetchStreamDetails tries to fetch stream details
func (d *Dashboard) tryFetchStreamDetails(endpoint string) {
	resp, err := d.env.HTTPClient.Get(endpoint)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var response struct {
		Streams []StreamInfo `json:"streams"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return
	}

	d.mu.Lock()
	d.stats.StreamsActive = response.Streams

	// Reset all aggregated stats
	d.stats.BitrateRTP = 0
	d.stats.BitrateSRT = 0
	d.stats.FramesProcessed = 0
	d.stats.FramesDropped = 0

	// Clear codec stats to prevent stale data
	d.stats.CodecStats = make(map[string]int)

	// Reset error/warning counts when no streams (indicates fresh start)
	if len(response.Streams) == 0 {
		d.stats.ErrorCount = 0
		d.stats.WarningCount = 0
		d.stats.StreamStats = make(map[string]StreamDetailedStats)
		d.stats.BufferStats = make(map[string]BufferInfo)
		d.stats.ConnectionQuality = make(map[string]ConnectionQuality)
	}

	// Aggregate stats from current streams
	for _, stream := range response.Streams {
		if stream.Type == "rtp" {
			d.stats.BitrateRTP += float64(stream.Bitrate)
		} else if stream.Type == "srt" {
			d.stats.BitrateSRT += float64(stream.Bitrate)
		}

		// Aggregate frame statistics from stream buffer stats
		d.stats.FramesProcessed += int64(stream.Stats.FrameBufferStats.FramesAssembled)
		d.stats.FramesDropped += int64(stream.Stats.FrameBufferStats.FramesDropped)

		// Track codec usage
		if stream.VideoCodec != "" {
			d.stats.CodecStats[stream.VideoCodec]++
		}
	}
	d.mu.Unlock()
}

// fetchDetailedStreamStats collects detailed statistics for each stream
func (d *Dashboard) fetchDetailedStreamStats() {
	d.mu.Lock()
	streams := d.stats.StreamsActive
	d.mu.Unlock()

	for _, stream := range streams {
		go func(streamID string) {
			endpoint := fmt.Sprintf("https://127.0.0.1:8080/api/v1/streams/%s/stats", streamID)
			resp, err := d.env.HTTPClient.Get(endpoint)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var stats struct {
				PacketsReceived int64   `json:"packets_received"`
				PacketsDropped  int64   `json:"packets_dropped"`
				BytesReceived   int64   `json:"bytes_received"`
				FramesProcessed int64   `json:"frames_processed"`
				FramesDropped   int64   `json:"frames_dropped"`
				BufferFill      float64 `json:"buffer_fill"`
				Latency         float64 `json:"latency"`
				Jitter          float64 `json:"jitter"`
				Bitrate         float64 `json:"bitrate"`
			}

			if json.NewDecoder(resp.Body).Decode(&stats) == nil {
				d.mu.Lock()
				detailed := StreamDetailedStats{
					PacketsReceived: stats.PacketsReceived,
					PacketsDropped:  stats.PacketsDropped,
					BytesReceived:   stats.BytesReceived,
					FramesProcessed: stats.FramesProcessed,
					FramesDropped:   stats.FramesDropped,
					BufferFill:      stats.BufferFill,
					Latency:         stats.Latency,
					Jitter:          stats.Jitter,
					LastUpdate:      time.Now(),
				}

				// Update bitrate history
				if len(detailed.BitrateHistory) >= 30 {
					detailed.BitrateHistory = detailed.BitrateHistory[1:]
				}
				detailed.BitrateHistory = append(detailed.BitrateHistory, stats.Bitrate)

				d.stats.StreamStats[streamID] = detailed
				d.mu.Unlock()
			}
		}(stream.ID)
	}
}

// fetchBufferStatus collects buffer information for each stream
func (d *Dashboard) fetchBufferStatus() {
	d.mu.Lock()
	streams := d.stats.StreamsActive
	d.mu.Unlock()

	for _, stream := range streams {
		go func(streamID string) {
			endpoint := fmt.Sprintf("https://127.0.0.1:8080/api/v1/streams/%s/buffer", streamID)
			resp, err := d.env.HTTPClient.Get(endpoint)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var buffer struct {
				Size       int64   `json:"size"`
				Used       int64   `json:"used"`
				Percent    float64 `json:"percent"`
				RingBuffer float64 `json:"ring_buffer"`
				GOPBuffer  int     `json:"gop_buffer"`
			}

			if json.NewDecoder(resp.Body).Decode(&buffer) == nil {
				d.mu.Lock()
				d.stats.BufferStats[streamID] = BufferInfo{
					Size:       buffer.Size,
					Used:       buffer.Used,
					Percent:    buffer.Percent,
					RingBuffer: buffer.RingBuffer,
					GOPBuffer:  buffer.GOPBuffer,
				}
				d.mu.Unlock()
			}
		}(stream.ID)
	}
}

// fetchMemoryMetrics collects system memory and resource metrics
func (d *Dashboard) fetchMemoryMetrics() {
	// Get memory stats from metrics endpoint
	resp, err := d.env.HTTPClientPlain.Get("http://127.0.0.1:9091/metrics")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	content := string(body)

	// Parse key metrics from Prometheus format
	if matches := regexp.MustCompile(`go_memstats_alloc_bytes (\d+)`).FindStringSubmatch(content); len(matches) > 1 {
		if val, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			d.stats.MemoryUsed = val
		}
	}

	if matches := regexp.MustCompile(`go_goroutines (\d+)`).FindStringSubmatch(content); len(matches) > 1 {
		if val, err := strconv.Atoi(matches[1]); err == nil {
			d.stats.Goroutines = val
		}
	}

	// Calculate memory percentage (assuming 8GB limit from config)
	d.stats.MemoryLimit = 8 * 1024 * 1024 * 1024 // 8GB
	if d.stats.MemoryLimit > 0 {
		d.stats.MemoryPercent = float64(d.stats.MemoryUsed) / float64(d.stats.MemoryLimit) * 100
	}
}

// fetchConnectionQuality analyzes connection quality for each stream
func (d *Dashboard) fetchConnectionQuality() {
	d.mu.Lock()
	streams := d.stats.StreamsActive
	d.mu.Unlock()

	for _, stream := range streams {
		go func(streamID string) {
			endpoint := fmt.Sprintf("https://127.0.0.1:8080/api/v1/streams/%s/sync", streamID)
			resp, err := d.env.HTTPClient.Get(endpoint)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var sync struct {
				PacketLoss float64 `json:"packet_loss"`
				RTT        float64 `json:"rtt"`
				Bandwidth  float64 `json:"bandwidth"`
				Jitter     float64 `json:"jitter"`
			}

			if json.NewDecoder(resp.Body).Decode(&sync) == nil {
				d.mu.Lock()

				// Determine signal strength based on packet loss and RTT
				var signalStrength string
				if sync.PacketLoss < 0.1 && sync.RTT < 50 {
					signalStrength = "Excellent"
				} else if sync.PacketLoss < 0.5 && sync.RTT < 100 {
					signalStrength = "Good"
				} else if sync.PacketLoss < 2.0 && sync.RTT < 200 {
					signalStrength = "Fair"
				} else {
					signalStrength = "Poor"
				}

				// Determine stability based on jitter
				var stability string
				if sync.Jitter < 10 {
					stability = "Stable"
				} else if sync.Jitter < 30 {
					stability = "Moderate"
				} else {
					stability = "Unstable"
				}

				d.stats.ConnectionQuality[streamID] = ConnectionQuality{
					SignalStrength: signalStrength,
					PacketLoss:     sync.PacketLoss,
					RTT:            sync.RTT,
					Bandwidth:      sync.Bandwidth,
					Stability:      stability,
				}
				d.mu.Unlock()
			}
		}(stream.ID)
	}
}

// updateBitrateHistory updates the global bitrate history for sparklines
func (d *Dashboard) updateBitrateHistory() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Calculate total bitrate from all streams
	totalBitrate := d.stats.BitrateRTP + d.stats.BitrateSRT

	// Update history
	if len(d.stats.BitrateHistory) >= 30 {
		d.stats.BitrateHistory = d.stats.BitrateHistory[1:]
	}
	d.stats.BitrateHistory = append(d.stats.BitrateHistory, totalBitrate)
}

// Stop stops the dashboard and restores terminal
func (d *Dashboard) Stop() {
	d.mu.Lock()
	d.isRunning = false

	// Clear all state to prevent persistence between runs
	d.stats = DashboardStats{
		ServerStatus: "Stopped",
		TestPhase:    "Cleanup",
		RecentLogs:   make([]LogEntry, 0, 10),
	}
	d.mu.Unlock()

	d.cancel()

	// Stop Rich dashboard
	if d.richModel != nil {
		d.richModel.Reset() // Add reset method
		d.richModel.Cleanup()
	}
	if d.richProg != nil {
		d.richProg.Quit()
	}
}

// SetPhase updates the current test phase
func (d *Dashboard) SetPhase(phase string) {
	d.mu.Lock()
	d.stats.TestPhase = phase
	d.mu.Unlock()

	// Forward to Rich dashboard
	if d.richModel != nil {
		d.richModel.SetPhase(phase)
	}
}

// SetProgress updates the progress percentage
func (d *Dashboard) SetProgress(progress int) {
	d.mu.Lock()
	d.stats.Progress = progress
	d.mu.Unlock()

	// Forward to Rich dashboard
	if d.richModel != nil {
		d.richModel.SetProgress(progress)
	}
}

// updateRichDisplay continuously updates the Rich dashboard display
func (d *Dashboard) updateRichDisplay() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.mu.RLock()
			if d.isRunning && d.richModel != nil {
				// Update the model with current dashboard stats
				d.richModel.UpdateStats(d.stats)

				// Clear screen and move cursor to top
				fmt.Fprint(d.output, "\033[2J\033[H")

				// Render the Rich dashboard view
				dashboardOutput := d.richModel.View()
				fmt.Fprint(d.output, dashboardOutput)
			}
			d.mu.RUnlock()
		}
	}
}

// generateActivityLogs creates realistic activity log entries for display
func (d *Dashboard) generateActivityLogs() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Only generate logs if we don't have many recent ones
	if len(d.stats.RecentLogs) > 20 {
		return
	}

	// Generate log entries based on current system activity

	// Stream-related logs
	for _, stream := range d.stats.StreamsActive {
		if len(d.stats.RecentLogs) >= 25 {
			break
		}

		// Skip if stream ID is empty (safety check)
		if stream.ID == "" {
			continue
		}

		// Connection events
		if !stream.LastHeartbeat.IsZero() && time.Since(stream.LastHeartbeat) < time.Minute {
			streamIDShort := stream.ID
			if len(streamIDShort) > 8 {
				streamIDShort = streamIDShort[:8]
			}
			d.addLog("info", "ingestion", fmt.Sprintf("Stream %s heartbeat received", streamIDShort))
		}

		// Frame processing events
		if stream.Stats.FrameBufferStats.FramesAssembled > 0 && stream.VideoCodec != "" {
			sourceAddr := stream.SourceAddr
			if sourceAddr == "" {
				sourceAddr = "unknown"
			}
			d.addLog("debug", "codec", fmt.Sprintf("%s frame assembled from %s", stream.VideoCodec, sourceAddr))
		}

		// Buffer status events
		if stream.Stats.FrameBufferStats.Capacity > 0 {
			bufferUsage := float64(stream.Stats.FrameBufferStats.Used) / float64(stream.Stats.FrameBufferStats.Capacity) * 100
			if bufferUsage > 80 {
				streamIDShort := stream.ID
				if len(streamIDShort) > 8 {
					streamIDShort = streamIDShort[:8]
				}
				d.addLog("warning", "buffer", fmt.Sprintf("High buffer usage %.1f%% on stream %s", bufferUsage, streamIDShort))
			}
		}

		// Quality events
		if stream.Stats.PacketsReceived > 0 {
			lossRate := float64(stream.Stats.PacketsLost) / float64(stream.Stats.PacketsReceived) * 100
			if lossRate > 2.0 {
				d.addLog("warning", "network", fmt.Sprintf("Packet loss %.2f%% on %s stream", lossRate, stream.Type))
			}
		}
	}

	// System-level logs
	if d.stats.MemoryPercent > 85 {
		d.addLog("warning", "system", fmt.Sprintf("High memory usage: %.1f%%", d.stats.MemoryPercent))
	}

	// Processing logs
	if d.stats.FramesProcessed > 0 {
		d.addLog("info", "processing", fmt.Sprintf("Total frames processed: %d", d.stats.FramesProcessed))
	}

	// Keep only the most recent 25 log entries
	if len(d.stats.RecentLogs) > 25 {
		d.stats.RecentLogs = d.stats.RecentLogs[len(d.stats.RecentLogs)-25:]
	}
}

// addLog is a helper to add log entries
func (d *Dashboard) addLog(level, component, message string) {
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Component: component,
	}
	d.stats.RecentLogs = append(d.stats.RecentLogs, entry)

	// Track error/warning counts
	if level == "error" {
		d.stats.ErrorCount++
	} else if level == "warning" {
		d.stats.WarningCount++
	}
}
