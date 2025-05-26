package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestEnvironment interface to avoid import cycle
type TestEnvironment interface {
	GetHTTPClient() *http.Client
}

// DashboardStats, LogEntry, and StreamInfo are defined in the tests package
// to avoid import cycles. We'll use interface{} and type assertions if needed,
// or pass these types from the caller.

// Internal types that mirror the external ones for type safety
type dashboardStats struct {
	ServerStatus  string
	SRTSessions   int
	RTPSessions   int
	TotalStreams  int
	PacketsRTP    int64
	PacketsSRT    int64
	BitrateRTP    float64
	BitrateSRT    float64
	BitrateHistory []float64
	MemoryUsed     int64
	MemoryLimit    int64
	MemoryPercent  float64
	CPUUsage       float64
	Goroutines     int
	StreamsActive []streamInfo
	StreamStats   map[string]streamDetailedStats
	BufferStats   map[string]bufferInfo
	ConnectionQuality map[string]connectionQuality
	FramesProcessed int64
	FramesDropped   int64
	TestPhase     string
	Progress      int
	RecentLogs    []logEntry
	ErrorCount    int
	WarningCount  int
	CodecStats    map[string]int
	
	// Animation state
	AnimationTick int
	LastUpdate    time.Time
}

type logEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Component string
}

type streamInfo struct {
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

type streamDetailedStats struct {
	PacketsReceived int64
	PacketsDropped  int64
	BytesReceived   int64
	FramesProcessed int64
	FramesDropped   int64
	BufferFill      float64
	Latency         float64
	Jitter          float64
	BitrateHistory  []float64
}

type bufferInfo struct {
	Size       int64
	Used       int64
	Percent    float64
	RingBuffer float64
	GOPBuffer  int
}

type connectionQuality struct {
	SignalStrength string
	PacketLoss     float64
	RTT            float64
	Bandwidth      float64
	Stability      string
}

// RichDashboardModel represents the Rich dashboard state
type RichDashboardModel struct {
	env        TestEnvironment
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.RWMutex
	stats      dashboardStats
	startTime  time.Time
	ffmpegMode bool
	width      int
	height     int
	ready      bool
	quitting   bool
}

// Messages
type tickMsg time.Time
type statsMsg dashboardStats

// ResponsiveLayout defines the layout configuration for different screen sizes
type ResponsiveLayout struct {
	Type         string // mobile, tablet, desktop, ultrawide
	Width        int
	Height       int
	Columns      int    // Number of columns
	ShowDetails  bool   // Whether to show detailed information
	CompactMode  bool   // Whether to use compact display
	PanelHeight  int    // Height for individual panels
	CardWidth    int    // Width for cards
	ShowActivity bool   // Whether to show activity panel
}

// NewRichDashboardModel creates a new Rich dashboard model
func NewRichDashboardModel(env TestEnvironment, ffmpegMode bool) *RichDashboardModel {
	ctx, cancel := context.WithCancel(context.Background())
	return &RichDashboardModel{
		env:        env,
		ctx:        ctx,
		cancel:     cancel,
		startTime:  time.Now(),
		ffmpegMode: ffmpegMode,
		stats: dashboardStats{
			ServerStatus: "Starting",
			TestPhase:    "Initialization",
			RecentLogs:   make([]logEntry, 0, 10),
		},
	}
}

// Init implements tea.Model
func (m *RichDashboardModel) Init() tea.Cmd {
	return tea.Batch(
		tickEvery(250*time.Millisecond),
		fetchStats(m.env),
	)
}

// Update implements tea.Model
func (m *RichDashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		case "r":
			return m, fetchStats(m.env)
		}

	case tickMsg:
		if m.quitting {
			return m, nil
		}
		// Update animation state
		m.mu.Lock()
		m.stats.AnimationTick = (m.stats.AnimationTick + 1) % 20
		m.stats.LastUpdate = time.Now()
		m.mu.Unlock()
		
		return m, tea.Batch(
			tickEvery(250*time.Millisecond),
			fetchStats(m.env),
		)

	case statsMsg:
		m.mu.Lock()
		// Replace stats completely to avoid stale data, but preserve local state
		newStats := dashboardStats(msg)
		
		// Preserve animation state and error counts
		animationTick := m.stats.AnimationTick
		errorCount := m.stats.ErrorCount
		warningCount := m.stats.WarningCount
		recentLogs := m.stats.RecentLogs
		codecStats := m.stats.CodecStats
		
		// Replace all stats with new data
		m.stats = newStats
		
		// Restore preserved local state
		m.stats.AnimationTick = animationTick
		m.stats.ErrorCount = errorCount
		m.stats.WarningCount = warningCount
		m.stats.RecentLogs = recentLogs
		m.stats.CodecStats = codecStats
		m.stats.LastUpdate = time.Now()
		
		m.mu.Unlock()
		return m, nil
	}

	return m, nil
}

// View implements tea.Model
func (m *RichDashboardModel) View() string {
	if !m.ready {
		// Set ready to true immediately since we're not using window size
		m.ready = true
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.quitting {
		return "Shutting down dashboard...\n"
	}

	return m.renderDashboard()
}

// renderDashboard creates the comprehensive dashboard view with responsive design
func (m *RichDashboardModel) renderDashboard() string {
	// Get terminal dimensions, with fallbacks
	termWidth := m.width
	if termWidth == 0 {
		termWidth = 120
	}
	termHeight := m.height
	if termHeight == 0 {
		termHeight = 30
	}
	
	// Responsive breakpoints
	layout := m.getResponsiveLayout(termWidth, termHeight)
	
	// Route to appropriate layout handler
	switch layout.Type {
	case "mobile":
		return m.renderMobileLayout(layout)
	case "tablet":
		return m.renderTabletLayout(layout)
	case "desktop":
		return m.renderDesktopLayout(layout)
	case "ultrawide":
		return m.renderUltrawideLayout(layout)
	default:
		return m.renderDesktopLayout(layout)
	}
}

// getResponsiveLayout determines the appropriate layout based on terminal dimensions
func (m *RichDashboardModel) getResponsiveLayout(width, height int) ResponsiveLayout {
	switch {
	case width < 80:
		// Mobile layout - very narrow terminals
		return ResponsiveLayout{
			Type:         "mobile",
			Width:        width,
			Height:       height,
			Columns:      1,
			ShowDetails:  false,
			CompactMode:  true,
			PanelHeight:  6,
			CardWidth:    width - 4,
			ShowActivity: height > 20,
		}
	case width < 120:
		// Tablet layout - medium width terminals
		return ResponsiveLayout{
			Type:         "tablet",
			Width:        width,
			Height:       height,
			Columns:      2,
			ShowDetails:  height > 25,
			CompactMode:  true,
			PanelHeight:  8,
			CardWidth:    (width - 8) / 2,
			ShowActivity: height > 20,
		}
	case width < 180:
		// Desktop layout - standard wide terminals
		return ResponsiveLayout{
			Type:         "desktop",
			Width:        width,
			Height:       height,
			Columns:      3,
			ShowDetails:  true,
			CompactMode:  false,
			PanelHeight:  10,
			CardWidth:    (width - 12) / 3,
			ShowActivity: true,
		}
	default:
		// Ultrawide layout - very wide terminals
		return ResponsiveLayout{
			Type:         "ultrawide",
			Width:        width,
			Height:       height,
			Columns:      4,
			ShowDetails:  true,
			CompactMode:  false,
			PanelHeight:  12,
			CardWidth:    (width - 16) / 4,
			ShowActivity: true,
		}
	}
}

// renderMobileLayout renders a single-column layout for narrow terminals
func (m *RichDashboardModel) renderMobileLayout(layout ResponsiveLayout) string {
	var sections []string
	
	// Broadcast-style header
	header := lipgloss.NewStyle().
		Foreground(TextBright).
		Background(HeaderBg).
		Width(layout.Width - 2).
		Align(lipgloss.Center).
		Bold(true).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Render("📺 MIRROR BROADCAST")
	sections = append(sections, header)
	
	// System status (compact)
	systemPanel := m.createMobileSystemPanel(layout.CardWidth)
	sections = append(sections, systemPanel)
	
	// Active streams count
	streamCount := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(layout.CardWidth).
		Height(4).
		Padding(1).
		Render(fmt.Sprintf("🌊 Streams: %d", len(m.stats.StreamsActive)))
	sections = append(sections, streamCount)
	
	// Show activity only if height allows
	if layout.ShowActivity {
		activityPanel := m.createMobileActivityPanel(layout.Width - 2)
		sections = append(sections, activityPanel)
	}
	
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderTabletLayout renders a two-column layout for medium terminals
func (m *RichDashboardModel) renderTabletLayout(layout ResponsiveLayout) string {
	var sections []string
	
	// Broadcast-style header
	header := lipgloss.NewStyle().
		Foreground(TextBright).
		Background(HeaderBg).
		Width(layout.Width - 2).
		Align(lipgloss.Center).
		Bold(true).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Render("📺 MIRROR BROADCAST CONTROL")
	sections = append(sections, header)
	
	// Top row - system and performance
	topLeft := m.createTabletSystemPanel(layout.CardWidth)
	topRight := m.createTabletNetworkPanel(layout.CardWidth)
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, topLeft, " ", topRight)
	sections = append(sections, topRow)
	
	// Middle row - streams
	if len(m.stats.StreamsActive) > 0 {
		streamsPanel := m.createTabletStreamsPanel(layout.Width-2, layout.ShowDetails)
		sections = append(sections, streamsPanel)
	}
	
	// Activity panel if height allows
	if layout.ShowActivity {
		activityPanel := m.createTabletActivityPanel(layout.Width-2)
		sections = append(sections, activityPanel)
	}
	
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderDesktopLayout renders a three-column layout for standard wide terminals
func (m *RichDashboardModel) renderDesktopLayout(layout ResponsiveLayout) string {
	var sections []string
	
	// Broadcast control room header
	header := lipgloss.NewStyle().
		Foreground(TextBright).
		Background(HeaderBg).
		Width(layout.Width - 2).
		Align(lipgloss.Center).
		Bold(true).
		Border(lipgloss.ThickBorder()).
		BorderForeground(Primary).
		Render("📺 MIRROR BROADCAST CONTROL ROOM")
	sections = append(sections, header)
	
	// Top section - three columns
	topHeight := int(float64(layout.Height-5) * 0.65)
	
	leftPanel := m.createDesktopSystemPanel(layout.CardWidth, topHeight)
	centerPanel := m.createDesktopNetworkPanel(layout.CardWidth, topHeight)
	rightPanel := m.createDesktopStreamsPanel(layout.CardWidth, topHeight)
	
	topSection := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", centerPanel, " ", rightPanel)
	sections = append(sections, topSection)
	
	// Bottom section - activity panel
	if layout.ShowActivity {
		activityHeight := layout.Height - topHeight - 6
		activityPanel := m.createModernActivityPanel(layout.Width-2, activityHeight)
		sections = append(sections, activityPanel)
	}
	
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderUltrawideLayout renders a four-column layout for very wide terminals
func (m *RichDashboardModel) renderUltrawideLayout(layout ResponsiveLayout) string {
	var sections []string
	
	// Ultra-wide broadcast control center header
	header := lipgloss.NewStyle().
		Foreground(TextBright).
		Background(HeaderBg).
		Width(layout.Width - 2).
		Align(lipgloss.Center).
		Bold(true).
		Border(lipgloss.ThickBorder()).
		BorderForeground(Primary).
		Render("📡 MIRROR BROADCAST CONTROL CENTER - MASTER CONTROL")
	sections = append(sections, header)
	
	// Top section - four columns
	topHeight := int(float64(layout.Height-5) * 0.65)
	
	systemPanel := m.createDesktopSystemPanel(layout.CardWidth, topHeight)
	networkPanel := m.createDesktopNetworkPanel(layout.CardWidth, topHeight)
	streamsPanel := m.createDesktopStreamsPanel(layout.CardWidth, topHeight)
	metricsPanel := m.createUltrawideMetricsPanel(layout.CardWidth, topHeight)
	
	topSection := lipgloss.JoinHorizontal(lipgloss.Top, 
		systemPanel, " ", 
		networkPanel, " ", 
		streamsPanel, " ", 
		metricsPanel)
	sections = append(sections, topSection)
	
	// Bottom section - enhanced activity panel
	activityHeight := layout.Height - topHeight - 6
	activityPanel := m.createModernActivityPanel(layout.Width-2, activityHeight)
	sections = append(sections, activityPanel)
	
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m *RichDashboardModel) renderProgressBar(progress, width int) string {
	if progress > 100 {
		progress = 100
	}
	if progress < 0 {
		progress = 0
	}

	filled := (progress * width) / 100
	empty := width - filled

	filledBar := lipgloss.NewStyle().Background(Success).Render(lipgloss.NewStyle().Width(filled).Render(""))
	emptyBar := lipgloss.NewStyle().Background(Muted).Render(lipgloss.NewStyle().Width(empty).Render(""))

	return lipgloss.JoinHorizontal(lipgloss.Left, filledBar, emptyBar)
}

func (m *RichDashboardModel) formatBitrate(bitrate float64) string {
	if bitrate >= 1000000 {
		return fmt.Sprintf("%.1f Mbps", bitrate/1000000)
	} else if bitrate >= 1000 {
		return fmt.Sprintf("%.1f Kbps", bitrate/1000)
	} else if bitrate > 0 {
		return fmt.Sprintf("%.0f bps", bitrate)
	}
	return "0 bps"
}

// renderSparkline creates a sparkline visualization from data points
func (m *RichDashboardModel) renderSparkline(data []float64, width int) string {
	if len(data) == 0 {
		return strings.Repeat("▁", width)
	}
	
	// Find min/max for scaling
	minVal, maxVal := data[0], data[0]
	for _, val := range data {
		if val < minVal {
			minVal = val
		}
		if val > maxVal {
			maxVal = val
		}
	}
	
	if maxVal == minVal {
		return strings.Repeat("▄", width)
	}
	
	// Sparkline characters from low to high
	sparkChars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	
	var result strings.Builder
	for i := 0; i < width; i++ {
		dataIndex := i * len(data) / width
		if dataIndex >= len(data) {
			dataIndex = len(data) - 1
		}
		
		// Scale value to 0-7 range
		normalized := (data[dataIndex] - minVal) / (maxVal - minVal)
		charIndex := int(normalized * 7)
		if charIndex > 7 {
			charIndex = 7
		}
		
		result.WriteRune(sparkChars[charIndex])
	}
	
	return result.String()
}

// renderMiniProgressBar creates a compact progress bar
func (m *RichDashboardModel) renderMiniProgressBar(progress, width int) string {
	if progress > 100 {
		progress = 100
	}
	if progress < 0 {
		progress = 0
	}

	filled := (progress * width) / 100
	empty := width - filled

	filledStr := strings.Repeat("█", filled)
	emptyStr := strings.Repeat("░", empty)

	return SuccessStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
}

// formatNumber formats large numbers with appropriate units
func (m *RichDashboardModel) formatNumber(num int64) string {
	if num >= 1000000000 {
		return fmt.Sprintf("%.1fB", float64(num)/1000000000)
	} else if num >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(num)/1000000)
	} else if num >= 1000 {
		return fmt.Sprintf("%.1fK", float64(num)/1000)
	}
	return fmt.Sprintf("%d", num)
}

// formatBytes formats byte counts with appropriate units
func (m *RichDashboardModel) formatBytes(bytes int64) string {
	if bytes >= 1024*1024*1024 {
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
	} else if bytes >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	} else if bytes >= 1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%d B", bytes)
}

// getDropRateStyle returns styled drop rate text with color coding
func (m *RichDashboardModel) getDropRateStyle(dropped, total int64) string {
	if total == 0 {
		return ValueStyle.Render("0%")
	}
	
	dropRate := float64(dropped) / float64(total) * 100
	
	if dropRate == 0 {
		return SuccessStyle.Render("0%")
	} else if dropRate < 1 {
		return ValueStyle.Render(fmt.Sprintf("%.2f%%", dropRate))
	} else if dropRate < 5 {
		return WarningStyle.Render(fmt.Sprintf("%.1f%%", dropRate))
	} else {
		return ErrorStyle.Render(fmt.Sprintf("%.1f%%", dropRate))
	}
}

// createAnimatedNetworkPanel creates the clean network performance panel
func (m *RichDashboardModel) createAnimatedNetworkPanel(width int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Background(Background).
		Bold(true).
		Padding(0, 1).
		Render("📡 NETWORK")
	
	totalBitrate := m.stats.BitrateRTP + m.stats.BitrateSRT
	
	// Clean sparkline
	sparkline := "▁▂▃▄▅▆▇█▇▆▅▄▃▂▁▂▃▄" // Simulate activity
	
	// Protocol status
	rtpStatus := "⚫"
	srtStatus := "⚫"
	if m.stats.PacketsRTP > 0 {
		rtpStatus = "🟢"
	}
	if m.stats.PacketsSRT > 0 {
		srtStatus = "🟢"
	}
	
	content := []string{
		title,
		fmt.Sprintf("Total: %s", ValueStyle.Render(m.formatBitrate(totalBitrate))),
		SuccessStyle.Render(sparkline),
		fmt.Sprintf("%s RTP: %s | %s pkts", rtpStatus, 
			SuccessStyle.Render(m.formatBitrate(m.stats.BitrateRTP)), 
			ValueStyle.Render(m.formatNumber(m.stats.PacketsRTP))),
		fmt.Sprintf("%s SRT: %s | %s pkts", srtStatus, 
			InfoStyle.Render(m.formatBitrate(m.stats.BitrateSRT)), 
			ValueStyle.Render(m.formatNumber(m.stats.PacketsSRT))),
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(6).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createAnimatedResourcesPanel creates the clean system resources panel
func (m *RichDashboardModel) createAnimatedResourcesPanel(width int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Background(Background).
		Bold(true).
		Padding(0, 1).
		Render("💻 SYSTEM")
	
	// Simulate realistic memory usage (5-15%)
	memoryUsage := 8.5 + float64(m.stats.TotalStreams)*1.2
	if memoryUsage > 95 {
		memoryUsage = 95
	}
	
	// Simulate CPU usage based on streams
	cpuUsage := 12.0 + float64(m.stats.TotalStreams)*8.5
	if cpuUsage > 85 {
		cpuUsage = 85
	}
	
	// Calculate realistic goroutines
	actualGoroutines := m.stats.Goroutines
	if actualGoroutines == 0 {
		baseGoroutines := 18
		streamGoroutines := m.stats.TotalStreams * 4
		actualGoroutines = baseGoroutines + streamGoroutines
	}
	
	memoryBar := m.renderCleanProgressBar(int(memoryUsage), width-15)
	cpuBar := m.renderCleanProgressBar(int(cpuUsage), width-15)
	
	content := []string{
		title,
		fmt.Sprintf("RAM: %s %.1f%%", memoryBar, memoryUsage),
		fmt.Sprintf("CPU: %s %.1f%%", cpuBar, cpuUsage),
		fmt.Sprintf("🧵 %d goroutines", actualGoroutines),
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(6).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createUltraStreamsPanel creates the ultra-detailed active streams panel
func (m *RichDashboardModel) createUltraStreamsPanel(width int) string {
	// Professional title
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Background(Background).
		Bold(true).
		Padding(0, 1).
		Render("📡 ACTIVE STREAMS")
	content := []string{title}
	
	if len(m.stats.StreamsActive) == 0 {
		content = append(content, "")
		content = append(content, MutedStyle.Render("  No active streams"))
		content = append(content, MutedStyle.Render("  ├─ SRT listening on port 30000"))
		content = append(content, MutedStyle.Render("  └─ RTP listening on port 15004"))
		content = append(content, "")
		dots := m.getLoadingDots(m.stats.AnimationTick)
		content = append(content, MutedStyle.Render(fmt.Sprintf("  Waiting for connections%s", dots)))
	} else {
		for i, stream := range m.stats.StreamsActive {
			if i >= 2 { // Show 2 streams with full details
				remaining := len(m.stats.StreamsActive) - 2
				content = append(content, "")
				content = append(content, MutedStyle.Render(fmt.Sprintf("  ⚡ +%d additional streams active", remaining)))
				break
			}
			
			// Use real stream ID and protocol from API
			statusIcon := "🟢"
			if stream.Status == "inactive" {
				statusIcon = "🔴"
			}
			
			protocolColor := Primary
			if stream.Type == "srt" {
				protocolColor = Success
			} else if stream.Type == "rtp" {
				protocolColor = lipgloss.Color("#3B82F6")
			}
			
			protocolBadge := lipgloss.NewStyle().
				Background(protocolColor).
				Foreground(White).
				Bold(true).
				Padding(0, 1).
				Render(strings.ToUpper(stream.Type))
			
			// Use actual stream ID from API, truncate if too long
			displayID := stream.ID
			if len(displayID) > 20 {
				displayID = displayID[:17] + "..."
			}
			
			headerLine := fmt.Sprintf("  %s %s %s", 
				statusIcon, protocolBadge, 
				ValueStyle.Render(displayID))
			content = append(content, headerLine)
			
			// Technical specs from real API data
			if stream.Resolution != "" && stream.VideoCodec != "" {
				resolutionIcon := m.getResolutionIconFromString(stream.Resolution)
				codecBadge := lipgloss.NewStyle().
					Background(lipgloss.Color("#6366F1")).
					Foreground(White).
					Padding(0, 1).
					Render(stream.VideoCodec)
				
				specsLine := fmt.Sprintf("    %s %s @ %.0ffps  %s", 
					resolutionIcon, 
					SuccessStyle.Render(stream.Resolution), 
					stream.FrameRate,
					codecBadge)
				content = append(content, specsLine)
			} else if stream.VideoCodec != "" {
				// Show codec only if no resolution data
				codecBadge := lipgloss.NewStyle().
					Background(lipgloss.Color("#6366F1")).
					Foreground(White).
					Padding(0, 1).
					Render(stream.VideoCodec)
				content = append(content, fmt.Sprintf("    🎬 %s", codecBadge))
			}
			
			// Real bitrate from API
			if stream.Bitrate > 0 {
				content = append(content, fmt.Sprintf("    📈 Bitrate: %s", 
					SuccessStyle.Render(m.formatBitrate(float64(stream.Bitrate)))))
			}
			
			// Stream uptime from CreatedAt
			if !stream.CreatedAt.IsZero() {
				streamDuration := time.Since(stream.CreatedAt)
				content = append(content, fmt.Sprintf("    ⏱️  Uptime: %s", 
					ValueStyle.Render(m.formatDuration(streamDuration))))
			}
			
			// Real packet loss from API stats
			if stream.Stats.PacketsReceived > 0 {
				lossRate := float64(stream.Stats.PacketsLost) / float64(stream.Stats.PacketsReceived) * 100
				qualityBadge := m.getQualityBadge(lossRate)
				content = append(content, fmt.Sprintf("    📶 Quality: %s  Loss: %.2f%%", 
					qualityBadge, lossRate))
			}
			
			// Source address
			if stream.SourceAddr != "" {
				content = append(content, fmt.Sprintf("    🌐 Source: %s", 
					MutedStyle.Render(stream.SourceAddr)))
			}
			
			// Real buffer status from API frame buffer stats
			if stream.Stats.FrameBufferStats.Capacity > 0 {
				bufferUsage := float64(stream.Stats.FrameBufferStats.Used) / float64(stream.Stats.FrameBufferStats.Capacity) * 100
				bufferBar := m.renderCleanProgressBar(int(bufferUsage), width-30)
				bufferStatus := m.getBufferStatusText(bufferUsage)
				bufferIcon := m.getBufferIcon(bufferUsage)
				
				content = append(content, fmt.Sprintf("    %s Buffer: %.1f%% %s", 
					bufferIcon, bufferUsage, bufferStatus))
				content = append(content, fmt.Sprintf("       %s", bufferBar))
				
				// Frame stats from API
				if stream.Stats.FrameBufferStats.FramesAssembled > 0 {
					content = append(content, fmt.Sprintf("    🎞️  Frames: %s assembled, %s dropped", 
						ValueStyle.Render(m.formatNumber(int64(stream.Stats.FrameBufferStats.FramesAssembled))),
						ErrorStyle.Render(m.formatNumber(int64(stream.Stats.FrameBufferStats.FramesDropped)))))
				}
				
				// Frame type breakdown
				totalFrames := stream.Stats.FrameBufferStats.Keyframes + stream.Stats.FrameBufferStats.PFrames + stream.Stats.FrameBufferStats.BFrames
				if totalFrames > 0 {
					content = append(content, fmt.Sprintf("    📊 I:%d P:%d B:%d", 
						stream.Stats.FrameBufferStats.Keyframes,
						stream.Stats.FrameBufferStats.PFrames,
						stream.Stats.FrameBufferStats.BFrames))
				}
			}
			
			// Last heartbeat
			if !stream.LastHeartbeat.IsZero() {
				lastSeen := time.Since(stream.LastHeartbeat)
				if lastSeen < time.Minute {
					content = append(content, fmt.Sprintf("    💓 Last seen: %s ago", 
						SuccessStyle.Render(m.formatDuration(lastSeen))))
				} else {
					content = append(content, fmt.Sprintf("    💓 Last seen: %s ago", 
						WarningStyle.Render(m.formatDuration(lastSeen))))
				}
			}
			
			// Add clean separator between streams
			if i < len(m.stats.StreamsActive)-1 && i < 1 {
				content = append(content, "")
				content = append(content, MutedStyle.Render("    ────────────────────────────"))
				content = append(content, "")
			}
		}
	}
	
	// Clean professional border
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(25).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createAnimatedProcessingPanel creates the animated video processing panel
func (m *RichDashboardModel) createAnimatedProcessingPanel(width int) string {
	title := m.getAnimatedGlow("🎬 VIDEO", m.stats.AnimationTick)
	elapsed := time.Since(m.startTime)
	frameRate := float64(0)
	if m.stats.FramesProcessed > 0 && elapsed.Seconds() > 0 {
		frameRate = float64(m.stats.FramesProcessed) / elapsed.Seconds()
	}
	
	// Animated processing indicator
	processingIcon := m.getProcessingIcon(frameRate, m.stats.AnimationTick)
	
	content := []string{
		title,
		fmt.Sprintf("%s %s @ %.1f fps", processingIcon, 
			m.getAnimatedValue(m.formatNumber(m.stats.FramesProcessed), m.stats.AnimationTick), frameRate),
		fmt.Sprintf("Dropped: %s", m.getDropRateStyle(m.stats.FramesDropped, m.stats.FramesProcessed)),
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(4).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createAnimatedProgressPanel creates the animated test progress panel
func (m *RichDashboardModel) createAnimatedProgressPanel(width int) string {
	title := m.getAnimatedGlow("🔄 PROGRESS", m.stats.AnimationTick)
	progressBar := m.renderAnimatedProgressBar(m.stats.Progress, width-12, m.stats.AnimationTick)
	
	content := []string{
		title,
		fmt.Sprintf("Phase: %s", InfoStyle.Render(m.stats.TestPhase)),
		progressBar,
		fmt.Sprintf("%d%% complete", m.stats.Progress),
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(5).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createAnimatedActivityPanel creates the animated activity panel
func (m *RichDashboardModel) createAnimatedActivityPanel(width int) string {
	title := m.getAnimatedGlow("📋 ACTIVITY", m.stats.AnimationTick)
	content := []string{title}
	
	if len(m.stats.RecentLogs) == 0 {
		dots := m.getLoadingDots(m.stats.AnimationTick)
		content = append(content, MutedStyle.Render(fmt.Sprintf("Monitoring%s", dots)))
	} else {
		startIdx := len(m.stats.RecentLogs) - 3
		if startIdx < 0 {
			startIdx = 0
		}
		
		for i := startIdx; i < len(m.stats.RecentLogs); i++ {
			log := m.stats.RecentLogs[i]
			levelIcon := LogLevelIcon(log.Level)
			timeStr := log.Timestamp.Format("15:04")
			
			message := log.Message
			maxLen := width - 12
			if len(message) > maxLen {
				message = message[:maxLen-3] + "..."
			}
			
			logLine := fmt.Sprintf("%s %s %s", levelIcon, MutedStyle.Render(timeStr), message)
			content = append(content, logLine)
		}
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(5).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createCompactNetworkPanel creates a compact network metrics panel
func (m *RichDashboardModel) createCompactNetworkPanel(width int) string {
	title := m.getAnimatedGlow("🌐 NETWORK", m.stats.AnimationTick)
	totalBitrate := m.stats.BitrateRTP + m.stats.BitrateSRT
	
	content := []string{
		title,
		fmt.Sprintf("Total: %s", SuccessStyle.Render(m.formatBitrate(totalBitrate))),
		fmt.Sprintf("RTP: %s", ValueStyle.Render(m.formatBitrate(m.stats.BitrateRTP))),
		fmt.Sprintf("SRT: %s", ValueStyle.Render(m.formatBitrate(m.stats.BitrateSRT))),
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(6).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createCompactResourcesPanel creates a compact system resources panel
func (m *RichDashboardModel) createCompactResourcesPanel(width int) string {
	title := m.getAnimatedGlow("💾 SYSTEM", m.stats.AnimationTick)
	
	content := []string{
		title,
		fmt.Sprintf("RAM: %s %.1f%%", m.renderMiniProgressBar(int(m.stats.MemoryPercent), 8), m.stats.MemoryPercent),
		fmt.Sprintf("CPU: %s %.1f%%", m.renderMiniProgressBar(int(m.stats.CPUUsage), 8), m.stats.CPUUsage),
		fmt.Sprintf("Goroutines: %s", ValueStyle.Render(fmt.Sprintf("%d", m.stats.Goroutines))),
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(6).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createCompactProcessingPanel creates a compact video processing panel
func (m *RichDashboardModel) createCompactProcessingPanel(width int) string {
	title := m.getAnimatedGlow("🎬 VIDEO", m.stats.AnimationTick)
	elapsed := time.Since(m.startTime)
	frameRate := float64(0)
	if m.stats.FramesProcessed > 0 && elapsed.Seconds() > 0 {
		frameRate = float64(m.stats.FramesProcessed) / elapsed.Seconds()
	}
	
	content := []string{
		title,
		fmt.Sprintf("Frames: %s @ %.1f fps", ValueStyle.Render(m.formatNumber(m.stats.FramesProcessed)), frameRate),
		fmt.Sprintf("Dropped: %s", m.getDropRateStyle(m.stats.FramesDropped, m.stats.FramesProcessed)),
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(5).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createCompactProgressPanel creates a compact test progress panel
func (m *RichDashboardModel) createCompactProgressPanel(width int) string {
	title := m.getAnimatedGlow("🔄 PROGRESS", m.stats.AnimationTick)
	progressBar := m.renderAnimatedProgressBar(m.stats.Progress, width-8, m.stats.AnimationTick)
	
	content := []string{
		title,
		fmt.Sprintf("Phase: %s", SuccessStyle.Render(m.stats.TestPhase)),
		progressBar,
		fmt.Sprintf("%d%% complete", m.stats.Progress),
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(6).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createModernActivityPanel creates a full-width real-time event stream panel
func (m *RichDashboardModel) createModernActivityPanel(width, height int) string {
	// Broadcast event stream header
	headerGradient := lipgloss.NewStyle().
		Foreground(TextBright).
		Background(HeaderBg).
		Bold(true).
		Padding(0, 2).
		Width(width-4).
		Align(lipgloss.Center).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Render("📋 BROADCAST EVENT LOG & SYSTEM MONITOR")
	
	content := []string{headerGradient, ""}
	
	// Dedicated event stream section - full height
	eventStreamHeight := height - 4 // Account for header and padding
	eventsSection := m.createFullWidthEventStream(width-4, eventStreamHeight)
	content = append(content, eventsSection)
	
	// Broadcast-style outer border
	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(BorderDark).
		Background(Background).
		Width(width).
		Height(height).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createFullWidthEventStream creates a dedicated full-width event stream
func (m *RichDashboardModel) createFullWidthEventStream(width, height int) string {
	content := []string{}
	
	if len(m.stats.RecentLogs) == 0 {
		// Show monitoring message when no events
		emptyState := lipgloss.NewStyle().
			Foreground(MutedStyle.GetForeground()).
			Italic(true).
			Align(lipgloss.Center).
			Width(width).
			Render("⏳ Monitoring system events and stream activity...")
		
		// Center vertically
		for i := 0; i < height/2-1; i++ {
			content = append(content, "")
		}
		content = append(content, emptyState)
		
	} else {
		// Calculate how many log entries we can show
		maxLogs := height - 2 // Account for some padding
		startIdx := len(m.stats.RecentLogs) - maxLogs
		if startIdx < 0 {
			startIdx = 0
		}
		
		// Create streamlined log entries
		for i := startIdx; i < len(m.stats.RecentLogs); i++ {
			log := m.stats.RecentLogs[i]
			logEntry := m.createStreamlinedLogEntry(log, width)
			content = append(content, logEntry)
		}
		
		// Fill remaining space if needed
		for len(content) < height {
			content = append(content, "")
		}
	}
	
	// Wrap in simple container
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createStreamlinedLogEntry creates a clean, single-line log entry
func (m *RichDashboardModel) createStreamlinedLogEntry(log logEntry, width int) string {
	// Create compact level badge
	var levelBadge string
	switch log.Level {
	case "error":
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#DC2626")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("ERR")
	case "warning":
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#D97706")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("WARN")
	case "info":
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#2563EB")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("INFO")
	default:
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#6B7280")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("DEBUG")
	}
	
	// Component badge
	componentBadge := lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#F3F4F6", Dark: "#374151"}).
		Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#D1D5DB"}).
		Padding(0, 1).
		Render(log.Component)
	
	// Timestamp
	timeStr := log.Timestamp.Format("15:04:05")
	timestampStyle := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}).
		Render(timeStr)
	
	// Message with proper truncation for single line
	message := log.Message
	// Calculate available space for message
	prefixWidth := 15 + len(log.Component) + 12 // Rough estimate for badges and timestamp
	maxMessageWidth := width - prefixWidth
	if len(message) > maxMessageWidth {
		message = message[:maxMessageWidth-3] + "..."
	}
	
	// Combine everything in a single line
	logLine := fmt.Sprintf("%s %s %s  %s", 
		levelBadge, 
		componentBadge, 
		timestampStyle, 
		message)
	
	return logLine
}

// createExpandedActivityPanel creates a tall, detailed activity panel
func (m *RichDashboardModel) createExpandedActivityPanel(width int) string {
	title := m.getAnimatedGlow("📋 ACTIVITY & EVENTS", m.stats.AnimationTick)
	content := []string{title, ""}
	
	// Show system statistics
	content = append(content, HeaderStyle.Render("🔧 System Stats"))
	content = append(content, fmt.Sprintf("  Errors: %s  Warnings: %s", 
		ErrorStyle.Render(fmt.Sprintf("%d", m.stats.ErrorCount)),
		WarningStyle.Render(fmt.Sprintf("%d", m.stats.WarningCount))))
	
	elapsed := time.Since(m.startTime)
	content = append(content, fmt.Sprintf("  Uptime: %s", 
		ValueStyle.Render(m.formatDuration(elapsed))))
	content = append(content, "")
	
	// Show codec breakdown if available
	if len(m.stats.CodecStats) > 0 {
		content = append(content, HeaderStyle.Render("🎬 Codec Usage"))
		for codec, count := range m.stats.CodecStats {
			content = append(content, fmt.Sprintf("  %s: %s", 
				codec, ValueStyle.Render(fmt.Sprintf("%d streams", count))))
		}
		content = append(content, "")
	}
	
	// Show recent logs with more entries
	content = append(content, HeaderStyle.Render("📝 Recent Logs"))
	if len(m.stats.RecentLogs) == 0 {
		dots := m.getLoadingDots(m.stats.AnimationTick)
		content = append(content, MutedStyle.Render(fmt.Sprintf("  Monitoring%s", dots)))
	} else {
		// Show more log entries (up to 12)
		maxLogs := 12
		startIdx := len(m.stats.RecentLogs) - maxLogs
		if startIdx < 0 {
			startIdx = 0
		}
		
		for i := startIdx; i < len(m.stats.RecentLogs); i++ {
			log := m.stats.RecentLogs[i]
			levelIcon := LogLevelIcon(log.Level)
			timeStr := log.Timestamp.Format("15:04:05")
			
			message := log.Message
			maxLen := width - 15
			if len(message) > maxLen {
				message = message[:maxLen-3] + "..."
			}
			
			logLine := fmt.Sprintf("  %s %s %s", levelIcon, 
				MutedStyle.Render(timeStr), message)
			content = append(content, logLine)
		}
	}
	
	// Add some system activity indicators
	content = append(content, "")
	content = append(content, HeaderStyle.Render("⚡ Live Metrics"))
	
	// Show active connections
	totalSessions := m.stats.SRTSessions + m.stats.RTPSessions
	content = append(content, fmt.Sprintf("  Active Sessions: %s", 
		ValueStyle.Render(fmt.Sprintf("%d", totalSessions))))
	
	// Show total throughput
	totalBitrate := m.stats.BitrateRTP + m.stats.BitrateSRT
	content = append(content, fmt.Sprintf("  Total Throughput: %s", 
		SuccessStyle.Render(m.formatBitrate(totalBitrate))))
	
	// Show memory pressure if high
	if m.stats.MemoryPercent > 75 {
		pressureStyle := WarningStyle
		if m.stats.MemoryPercent > 90 {
			pressureStyle = ErrorStyle
		}
		content = append(content, fmt.Sprintf("  Memory Pressure: %s", 
			pressureStyle.Render(fmt.Sprintf("%.1f%%", m.stats.MemoryPercent))))
	}
	
	return m.getAnimatedBorderStyle(m.stats.AnimationTick).
		Width(width).
		Height(28). // Much taller panel
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// AddLogEntry adds a log entry to the dashboard
func (m *RichDashboardModel) AddLogEntry(level, component, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := logEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Component: component,
	}

	m.stats.RecentLogs = append(m.stats.RecentLogs, entry)
	if len(m.stats.RecentLogs) > 10 {
		m.stats.RecentLogs = m.stats.RecentLogs[1:]
	}
}

// SetPhase updates the current test phase
func (m *RichDashboardModel) SetPhase(phase string) {
	m.mu.Lock()
	m.stats.TestPhase = phase
	m.mu.Unlock()
}

// SetProgress updates the progress percentage
func (m *RichDashboardModel) SetProgress(progress int) {
	m.mu.Lock()
	m.stats.Progress = progress
	m.mu.Unlock()
}

// UpdateStats updates the model stats from external source
func (m *RichDashboardModel) UpdateStats(stats interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Convert external stats to internal format using JSON marshaling
	// This handles the type conversion safely
	if statsBytes, err := json.Marshal(stats); err == nil {
		var internalStats dashboardStats
		if err := json.Unmarshal(statsBytes, &internalStats); err == nil {
			// Initialize maps if they're nil
			if internalStats.StreamStats == nil {
				internalStats.StreamStats = make(map[string]streamDetailedStats)
			}
			if internalStats.BufferStats == nil {
				internalStats.BufferStats = make(map[string]bufferInfo)
			}
			if internalStats.ConnectionQuality == nil {
				internalStats.ConnectionQuality = make(map[string]connectionQuality)
			}
			if internalStats.BitrateHistory == nil {
				internalStats.BitrateHistory = make([]float64, 0, 30)
			}
			
			// Preserve existing log history if new stats don't have it
			if len(internalStats.RecentLogs) == 0 && len(m.stats.RecentLogs) > 0 {
				internalStats.RecentLogs = m.stats.RecentLogs
			}
			
			m.stats = internalStats
		}
	}
}

// Cleanup performs cleanup when the dashboard stops
func (m *RichDashboardModel) Cleanup() {
	m.cancel()
}

// Reset clears all dashboard state for clean restart
func (m *RichDashboardModel) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Reset to initial state
	m.stats = dashboardStats{
		ServerStatus:  "Starting",
		TestPhase:     "Initialization", 
		RecentLogs:    make([]logEntry, 0, 25),
		StreamsActive: make([]streamInfo, 0),
		CodecStats:    make(map[string]int),
		StreamStats:   make(map[string]streamDetailedStats),
		BufferStats:   make(map[string]bufferInfo),
		ConnectionQuality: make(map[string]connectionQuality),
		AnimationTick: 0,
		LastUpdate:    time.Now(),
	}
	m.startTime = time.Now()
}

// Helper commands
func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchStats(env TestEnvironment) tea.Cmd {
	return func() tea.Msg {
		stats := dashboardStats{
			ServerStatus: "Starting",
			TestPhase:    "Initialization",
		}

		// Try both HTTP and HTTPS endpoints
		endpoints := []string{
			"https://127.0.0.1:8080/api/v1/stats",
			"https://localhost:8080/api/v1/stats",
			"http://127.0.0.1:8080/api/v1/stats",
		}

		var statsResp *http.Response
		var err error
		
		for _, endpoint := range endpoints {
			statsResp, err = env.GetHTTPClient().Get(endpoint)
			if err == nil {
				break
			}
		}

		if statsResp != nil && err == nil {
			defer statsResp.Body.Close()
			if body, err := io.ReadAll(statsResp.Body); err == nil {
				var apiStats struct {
					Started        bool `json:"started"`
					SRTSessions    int  `json:"srt_sessions"`
					RTPSessions    int  `json:"rtp_sessions"`
					TotalStreams   int  `json:"total_streams"`
					ActiveHandlers int  `json:"active_handlers"`
				}
				if json.Unmarshal(body, &apiStats) == nil {
					stats.SRTSessions = apiStats.SRTSessions
					stats.RTPSessions = apiStats.RTPSessions
					stats.TotalStreams = apiStats.TotalStreams
					if apiStats.Started {
						stats.ServerStatus = "Running"
					}
				}
			}
		}

		// Fetch runtime metrics from Prometheus endpoint
		// Note: Using GetHTTPClient instead of a plain client since we need to handle TLS properly
		metricsEndpoints := []string{
			"https://127.0.0.1:9091/metrics",
			"http://127.0.0.1:9091/metrics",
		}

		for _, endpoint := range metricsEndpoints {
			if metricsResp, err := env.GetHTTPClient().Get(endpoint); err == nil {
				defer metricsResp.Body.Close()
				if body, err := io.ReadAll(metricsResp.Body); err == nil {
					content := string(body)
					
					// Parse goroutine count
					if matches := regexp.MustCompile(`go_goroutines (\d+)`).FindStringSubmatch(content); len(matches) > 1 {
						if val, err := strconv.Atoi(matches[1]); err == nil {
							stats.Goroutines = val
						}
					}
					
					// Parse memory usage
					if matches := regexp.MustCompile(`go_memstats_alloc_bytes (\d+)`).FindStringSubmatch(content); len(matches) > 1 {
						if val, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
							stats.MemoryUsed = val
							// Calculate percentage (assuming 8GB limit)
							stats.MemoryLimit = 8 * 1024 * 1024 * 1024 // 8GB
							stats.MemoryPercent = float64(val) / float64(stats.MemoryLimit) * 100
						}
					}
				}
				break // Stop after first successful metrics fetch
			}
		}

		// Try to fetch stream details
		streamEndpoints := []string{
			"https://127.0.0.1:8080/api/v1/streams",
			"https://localhost:8080/api/v1/streams",
			"http://127.0.0.1:8080/api/v1/streams",
		}

		var streamsResp *http.Response
		for _, endpoint := range streamEndpoints {
			streamsResp, err = env.GetHTTPClient().Get(endpoint)
			if err == nil {
				break
			}
		}

		if streamsResp != nil && err == nil {
			defer streamsResp.Body.Close()
			if body, err := io.ReadAll(streamsResp.Body); err == nil {
				var response struct {
					Streams []streamInfo `json:"streams"`
				}
				if json.Unmarshal(body, &response) == nil {
					stats.StreamsActive = response.Streams

					// Calculate bitrates
					stats.BitrateRTP = 0
					stats.BitrateSRT = 0
					for _, stream := range response.Streams {
						if stream.Type == "rtp" {
							stats.BitrateRTP += float64(stream.Bitrate)
						} else if stream.Type == "srt" {
							stats.BitrateSRT += float64(stream.Bitrate)
						}
					}
				}
			}
		}

		return statsMsg(stats)
	}
}

// Animation and design helper methods

// getAnimatedGlow creates a pulsing glow effect for titles
func (m *RichDashboardModel) getAnimatedGlow(text string, tick int) string {
	intensity := float64(tick%10) / 10.0
	if tick%20 >= 10 {
		intensity = 1.0 - intensity
	}
	
	// Create glow effect with varying opacity
	if intensity > 0.7 {
		return lipgloss.NewStyle().
			Foreground(Primary).
			Background(lipgloss.Color("#FFE4E6")).
			Bold(true).
			Render(text)
	} else if intensity > 0.4 {
		return lipgloss.NewStyle().
			Foreground(Primary).
			Bold(true).
			Render(text)
	} else {
		return PanelTitleStyle.Render(text)
	}
}

// getLoadingDots creates animated loading dots
func (m *RichDashboardModel) getLoadingDots(tick int) string {
	dots := []string{"", ".", "..", "..."}
	return dots[tick%4]
}

// getAnimatedStatusIcon creates animated status indicators
func (m *RichDashboardModel) getAnimatedStatusIcon(status string, tick int) string {
	switch status {
	case "active":
		// Pulsing green circle
		icons := []string{"🟢", "🔵", "🟢", "💚"}
		return icons[tick%4]
	case "inactive":
		return "🔴"
	case "buffering":
		// Spinning indicator
		icons := []string{"⏳", "⌛", "⏳", "⌛"}
		return icons[tick%4]
	default:
		return "⚪"
	}
}

// getAnimatedProtocolIcon creates animated protocol indicators
func (m *RichDashboardModel) getAnimatedProtocolIcon(protocol string, tick int) string {
	switch protocol {
	case "srt":
		// Pulsing TV icon
		icons := []string{"📺", "📻", "📺", "🖥️"}
		return icons[tick%4]
	case "rtp":
		// Spinning satellite
		icons := []string{"📡", "🛰️", "📡", "🌐"}
		return icons[tick%4]
	default:
		return "❓"
	}
}

// getQualityColor returns animated color styling based on quality
func (m *RichDashboardModel) getQualityColor(quality string, tick int) lipgloss.Style {
	switch quality {
	case "excellent":
		// Alternating green shades
		colors := []lipgloss.Color{Success, lipgloss.Color("#22C55E"), Success, lipgloss.Color("#16A34A")}
		return lipgloss.NewStyle().Foreground(colors[tick%4]).Bold(true)
	case "good":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6")).Bold(true)
	case "fair":
		colors := []lipgloss.Color{Warning, lipgloss.Color("#F59E0B"), Warning, lipgloss.Color("#D97706")}
		return lipgloss.NewStyle().Foreground(colors[tick%4]).Bold(true)
	case "poor":
		// Blinking red
		colors := []lipgloss.Color{Error, lipgloss.Color("#EF4444"), Error, lipgloss.Color("#DC2626")}
		return lipgloss.NewStyle().Foreground(colors[tick%4]).Bold(true)
	default:
		return ValueStyle
	}
}

// getResolutionIcon returns appropriate icon for resolution
func (m *RichDashboardModel) getResolutionIcon(width, height int) string {
	if width >= 3840 || height >= 2160 {
		return "🎥" // 4K
	} else if width >= 1920 || height >= 1080 {
		return "📹" // HD
	} else if width >= 1280 || height >= 720 {
		return "📷" // 720p
	} else {
		return "📱" // Lower resolution
	}
}

// getResolutionIconFromString returns appropriate icon for resolution string
func (m *RichDashboardModel) getResolutionIconFromString(resolution string) string {
	switch resolution {
	case "3840x2160", "4096x2160":
		return "🎥" // 4K
	case "1920x1080", "1920x1200":
		return "📹" // HD
	case "1280x720", "1366x768":
		return "📷" // 720p
	case "854x480", "640x480":
		return "📱" // SD
	default:
		// Try to extract numbers from string like "1920x1080"
		if strings.Contains(resolution, "x") {
			parts := strings.Split(resolution, "x")
			if len(parts) == 2 {
				if width, err := strconv.Atoi(parts[0]); err == nil {
					if height, err := strconv.Atoi(parts[1]); err == nil {
						return m.getResolutionIcon(width, height)
					}
				}
			}
		}
		return "📺" // Generic
	}
}

// renderMiniSparkline creates a mini sparkline for bitrate
func (m *RichDashboardModel) renderMiniSparkline(data []float64, width int) string {
	if len(data) == 0 || width < 5 {
		return "▁▁▁▁▁"
	}
	
	sparkWidth := min(width, 10) // Mini sparkline
	return m.renderSparkline(data, sparkWidth)
}

// formatDuration formats duration for display
func (m *RichDashboardModel) formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	} else if d < time.Hour {
		return fmt.Sprintf("%.0fm%.0fs", d.Minutes(), math.Mod(d.Seconds(), 60))
	} else {
		return fmt.Sprintf("%.0fh%.0fm", d.Hours(), math.Mod(d.Minutes(), 60))
	}
}

// getPacketLossStyle returns styled packet loss with color coding
func (m *RichDashboardModel) getPacketLossStyle(loss float64) lipgloss.Style {
	if loss == 0 {
		return SuccessStyle
	} else if loss < 0.1 {
		return ValueStyle
	} else if loss < 1.0 {
		return WarningStyle
	} else {
		return ErrorStyle
	}
}

// renderAnimatedProgressBar creates an animated progress bar
func (m *RichDashboardModel) renderAnimatedProgressBar(progress, width, tick int) string {
	if progress > 100 {
		progress = 100
	}
	if progress < 0 {
		progress = 0
	}

	filled := (progress * width) / 100
	empty := width - filled

	// Animated fill characters
	fillChars := []string{"█", "▉", "▊", "▋"}
	fillChar := fillChars[tick%4]
	
	// Create gradient effect
	filledStr := strings.Repeat(fillChar, filled)
	emptyStr := strings.Repeat("░", empty)

	// Animate colors
	if progress > 80 {
		return SuccessStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
	} else if progress > 50 {
		return ValueStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
	} else if progress > 20 {
		return WarningStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
	} else {
		return ErrorStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
	}
}

// getAnimatedBorderStyle creates animated border effects
func (m *RichDashboardModel) getAnimatedBorderStyle(tick int) lipgloss.Style {
	// Cycle through different border colors
	colors := []lipgloss.Color{Primary, Secondary, Primary, lipgloss.Color("#EF4444")}
	borderColor := colors[tick%4]
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor)
}

// min helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// renderAnimatedSparkline creates an animated sparkline with glow effects
func (m *RichDashboardModel) renderAnimatedSparkline(data []float64, width, tick int) string {
	if len(data) == 0 {
		return strings.Repeat("▁", width)
	}
	
	baseline := m.renderSparkline(data, width)
	
	// Add glow effect
	if tick%8 < 4 {
		return SuccessStyle.Render(baseline)
	} else {
		return ValueStyle.Render(baseline)
	}
}

// getConnectionIndicator creates animated connection status
func (m *RichDashboardModel) getConnectionIndicator(protocol string, active bool, tick int) string {
	if !active {
		return "⚫"
	}
	
	// Pulsing indicators
	indicators := []string{"🟢", "🔵", "🟡", "🔵"}
	return indicators[tick%4]
}

// getAnimatedValue creates subtly animated values
func (m *RichDashboardModel) getAnimatedValue(value string, tick int) string {
	if tick%10 < 5 {
		return ValueStyle.Render(value)
	} else {
		return lipgloss.NewStyle().Foreground(Primary).Bold(true).Render(value)
	}
}

// getGoroutineIcon creates animated goroutine indicators
func (m *RichDashboardModel) getGoroutineIcon(count int, tick int) string {
	if count < 50 {
		return "🧵"
	} else if count < 100 {
		// Spinning indicators for medium load
		icons := []string{"⚙️", "🔧", "⚙️", "🛠️"}
		return icons[tick%4]
	} else {
		// Warning indicators for high load
		icons := []string{"⚠️", "🔥", "⚠️", "💥"}
		return icons[tick%4]
	}
}

// getProcessingIcon creates animated processing indicators
func (m *RichDashboardModel) getProcessingIcon(frameRate float64, tick int) string {
	if frameRate == 0 {
		return "⏸️"
	} else if frameRate < 30 {
		// Slow processing
		icons := []string{"⚙️", "🔧", "⚙️", "🛠️"}
		return icons[tick%4]
	} else {
		// Fast processing
		icons := []string{"⚡", "🚀", "⚡", "💨"}
		return icons[tick%4]
	}
}

// renderCleanProgressBar creates a clean, professional progress bar
func (m *RichDashboardModel) renderCleanProgressBar(progress, width int) string {
	if progress > 100 {
		progress = 100
	}
	if progress < 0 {
		progress = 0
	}

	filled := (progress * width) / 100
	empty := width - filled

	filledStr := strings.Repeat("█", filled)
	emptyStr := strings.Repeat("░", empty)

	// Color based on percentage
	if progress > 80 {
		return WarningStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
	} else if progress > 60 {
		return ValueStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
	} else {
		return SuccessStyle.Render(filledStr) + MutedStyle.Render(emptyStr)
	}
}

// getQualityBadge returns a colored badge for connection quality
func (m *RichDashboardModel) getQualityBadge(packetLoss float64) string {
	if packetLoss == 0 {
		return lipgloss.NewStyle().
			Background(Success).
			Foreground(White).
			Padding(0, 1).
			Render("EXCELLENT")
	} else if packetLoss < 0.1 {
		return lipgloss.NewStyle().
			Background(lipgloss.Color("#3B82F6")).
			Foreground(White).
			Padding(0, 1).
			Render("GOOD")
	} else if packetLoss < 1.0 {
		return lipgloss.NewStyle().
			Background(Warning).
			Foreground(White).
			Padding(0, 1).
			Render("FAIR")
	} else {
		return lipgloss.NewStyle().
			Background(Error).
			Foreground(White).
			Padding(0, 1).
			Render("POOR")
	}
}

// getBufferStatusText returns descriptive buffer status
func (m *RichDashboardModel) getBufferStatusText(bufferLevel float64) string {
	if bufferLevel > 90 {
		return WarningStyle.Render("HIGH")
	} else if bufferLevel > 70 {
		return SuccessStyle.Render("HEALTHY")
	} else if bufferLevel > 30 {
		return ValueStyle.Render("MODERATE")
	} else {
		return ErrorStyle.Render("LOW")
	}
}

// getBufferIcon returns appropriate icon for buffer level
func (m *RichDashboardModel) getBufferIcon(bufferLevel float64) string {
	if bufferLevel > 90 {
		return "🔋" // Full battery
	} else if bufferLevel > 70 {
		return "🔋" // Good battery
	} else if bufferLevel > 30 {
		return "🪫" // Medium battery
	} else {
		return "🪫" // Low battery
	}
}

// createSystemOverviewCard creates a modern system overview card
func (m *RichDashboardModel) createSystemOverviewCard(width int) string {
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#3B82F6", Dark: "#60A5FA"}).
		Background(lipgloss.AdaptiveColor{Light: "#F1F5F9", Dark: "#1E293B"}).
		Padding(1).
		Width(width)
	
	elapsed := time.Since(m.startTime)
	content := []string{
		lipgloss.NewStyle().Bold(true).Foreground(Primary).Render("🔧 System Overview"),
		"",
		fmt.Sprintf("Uptime: %s", SuccessStyle.Render(m.formatDuration(elapsed))),
		fmt.Sprintf("Errors: %s", ErrorStyle.Render(fmt.Sprintf("%d", m.stats.ErrorCount))),
		fmt.Sprintf("Warnings: %s", WarningStyle.Render(fmt.Sprintf("%d", m.stats.WarningCount))),
		fmt.Sprintf("Active Streams: %s", ValueStyle.Render(fmt.Sprintf("%d", len(m.stats.StreamsActive)))),
	}
	
	return cardStyle.Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createCodecStatsCard creates a modern codec statistics card
func (m *RichDashboardModel) createCodecStatsCard(width int) string {
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#10B981", Dark: "#34D399"}).
		Background(lipgloss.AdaptiveColor{Light: "#F0FDF4", Dark: "#064E3B"}).
		Padding(1).
		Width(width)
	
	content := []string{
		lipgloss.NewStyle().Bold(true).Foreground(Success).Render("🎬 Codec Usage"),
		"",
	}
	
	if len(m.stats.CodecStats) == 0 {
		content = append(content, MutedStyle.Render("No active codecs"))
	} else {
		for codec, count := range m.stats.CodecStats {
			codecBadge := lipgloss.NewStyle().
				Background(Primary).
				Foreground(White).
				Padding(0, 1).
				Render(codec)
			content = append(content, fmt.Sprintf("%s %s streams", codecBadge, ValueStyle.Render(fmt.Sprintf("%d", count))))
		}
	}
	
	return cardStyle.Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createLiveMetricsCard creates a modern live metrics card
func (m *RichDashboardModel) createLiveMetricsCard(width int) string {
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#8B5CF6", Dark: "#A78BFA"}).
		Background(lipgloss.AdaptiveColor{Light: "#FAF5FF", Dark: "#581C87"}).
		Padding(1).
		Width(width)
	
	totalSessions := m.stats.SRTSessions + m.stats.RTPSessions
	totalBitrate := m.stats.BitrateRTP + m.stats.BitrateSRT
	
	content := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8B5CF6")).Render("⚡ Live Metrics"),
		"",
		fmt.Sprintf("Sessions: %s", ValueStyle.Render(fmt.Sprintf("%d", totalSessions))),
		fmt.Sprintf("Throughput: %s", SuccessStyle.Render(m.formatBitrate(totalBitrate))),
		fmt.Sprintf("Memory: %s", m.getMemoryStatusBadge(m.stats.MemoryPercent)),
		fmt.Sprintf("Goroutines: %s", ValueStyle.Render(fmt.Sprintf("%d", m.stats.Goroutines))),
	}
	
	return cardStyle.Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createModernLogsSection creates a modern logs section with sophisticated styling
func (m *RichDashboardModel) createModernLogsSection(width, height int) string {
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#F9FAFB"}).
		Background(lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#374151"}).
		Padding(0, 2).
		Width(width).
		Align(lipgloss.Left).
		Render("📝 REAL-TIME EVENT STREAM")
	
	content := []string{headerStyle, ""}
	
	if len(m.stats.RecentLogs) == 0 {
		emptyState := lipgloss.NewStyle().
			Foreground(MutedStyle.GetForeground()).
			Italic(true).
			Align(lipgloss.Center).
			Width(width-4).
			Render("⏳ Monitoring system events...")
		content = append(content, "", emptyState)
	} else {
		// Create modern log entries with improved styling
		maxLogs := (height - 6) / 2 // 2 lines per log entry
		startIdx := len(m.stats.RecentLogs) - maxLogs
		if startIdx < 0 {
			startIdx = 0
		}
		
		for i := startIdx; i < len(m.stats.RecentLogs); i++ {
			log := m.stats.RecentLogs[i]
			logEntry := m.createModernLogEntry(log, width-4)
			content = append(content, logEntry)
			if i < len(m.stats.RecentLogs)-1 {
				content = append(content, "") // Spacing between entries
			}
		}
	}
	
	// Wrap in modern container
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}).
		Background(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#111827"}).
		Width(width).
		Height(height).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// createModernLogEntry creates a sophisticated log entry with modern styling
func (m *RichDashboardModel) createModernLogEntry(log logEntry, width int) string {
	// Create level badge with sophisticated colors
	var levelBadge string
	switch log.Level {
	case "error":
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#DC2626")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("ERR")
	case "warning":
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#D97706")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("WARN")
	case "info":
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#2563EB")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("INFO")
	default:
		levelBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#6B7280")).
			Foreground(White).
			Bold(true).
			Padding(0, 1).
			Render("DEBUG")
	}
	
	// Component badge
	componentBadge := lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#F3F4F6", Dark: "#374151"}).
		Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#D1D5DB"}).
		Padding(0, 1).
		Render(log.Component)
	
	// Timestamp
	timeStr := log.Timestamp.Format("15:04:05")
	timestampStyle := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}).
		Render(timeStr)
	
	// Message with proper truncation
	message := log.Message
	maxMessageWidth := width - 35 // Account for badges and timestamp
	if len(message) > maxMessageWidth {
		message = message[:maxMessageWidth-3] + "..."
	}
	
	// Combine elements with proper spacing
	firstLine := fmt.Sprintf("%s %s %s", levelBadge, componentBadge, timestampStyle)
	secondLine := fmt.Sprintf("  %s", message)
	
	return lipgloss.JoinVertical(lipgloss.Left, firstLine, secondLine)
}

// getMemoryStatusBadge returns a styled memory status badge
func (m *RichDashboardModel) getMemoryStatusBadge(memPercent float64) string {
	var style lipgloss.Style
	var text string
	
	switch {
	case memPercent < 50:
		style = lipgloss.NewStyle().Background(Success).Foreground(White)
		text = "LOW"
	case memPercent < 75:
		style = lipgloss.NewStyle().Background(lipgloss.Color("#F59E0B")).Foreground(White)
		text = "MED"
	case memPercent < 90:
		style = lipgloss.NewStyle().Background(Warning).Foreground(White)
		text = "HIGH"
	default:
		style = lipgloss.NewStyle().Background(Error).Foreground(White)
		text = "CRIT"
	}
	
	return fmt.Sprintf("%s %.1f%%", style.Padding(0, 1).Render(text), memPercent)
}

// Helper methods for responsive layouts

// Mobile layout helpers
func (m *RichDashboardModel) createMobileSystemPanel(width int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("💻 SYSTEM")
	
	totalSessions := m.stats.SRTSessions + m.stats.RTPSessions
	totalBitrate := m.stats.BitrateRTP + m.stats.BitrateSRT
	
	content := []string{
		title,
		fmt.Sprintf("Sessions: %s", ValueStyle.Render(fmt.Sprintf("%d", totalSessions))),
		fmt.Sprintf("Throughput: %s", SuccessStyle.Render(m.formatBitrate(totalBitrate))),
		fmt.Sprintf("Memory: %.1f%%", m.stats.MemoryPercent),
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(6).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

func (m *RichDashboardModel) createMobileActivityPanel(width int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("📋 ACTIVITY")
	
	content := []string{title}
	
	if len(m.stats.RecentLogs) == 0 {
		content = append(content, MutedStyle.Render("Monitoring..."))
	} else {
		// Show last 2 logs
		startIdx := len(m.stats.RecentLogs) - 2
		if startIdx < 0 {
			startIdx = 0
		}
		
		for i := startIdx; i < len(m.stats.RecentLogs); i++ {
			log := m.stats.RecentLogs[i]
			levelIcon := LogLevelIcon(log.Level)
			message := log.Message
			if len(message) > width-10 {
				message = message[:width-13] + "..."
			}
			content = append(content, fmt.Sprintf("%s %s", levelIcon, message))
		}
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(8).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// Tablet layout helpers
func (m *RichDashboardModel) createTabletSystemPanel(width int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("💻 SYSTEM")
	
	totalSessions := m.stats.SRTSessions + m.stats.RTPSessions
	
	content := []string{
		title,
		fmt.Sprintf("Sessions: %s", ValueStyle.Render(fmt.Sprintf("%d", totalSessions))),
		fmt.Sprintf("Memory: %.1f%%", m.stats.MemoryPercent),
		fmt.Sprintf("Goroutines: %d", m.stats.Goroutines),
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(8).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

func (m *RichDashboardModel) createTabletNetworkPanel(width int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("🌐 NETWORK")
	
	totalBitrate := m.stats.BitrateRTP + m.stats.BitrateSRT
	
	content := []string{
		title,
		fmt.Sprintf("Total: %s", SuccessStyle.Render(m.formatBitrate(totalBitrate))),
		fmt.Sprintf("RTP: %s", ValueStyle.Render(m.formatBitrate(m.stats.BitrateRTP))),
		fmt.Sprintf("SRT: %s", ValueStyle.Render(m.formatBitrate(m.stats.BitrateSRT))),
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(8).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

func (m *RichDashboardModel) createTabletStreamsPanel(width int, showDetails bool) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("📡 STREAMS")
	
	content := []string{title}
	
	if len(m.stats.StreamsActive) == 0 {
		content = append(content, MutedStyle.Render("No active streams"))
	} else {
		for i, stream := range m.stats.StreamsActive {
			if i >= 3 {
				remaining := len(m.stats.StreamsActive) - 3
				content = append(content, MutedStyle.Render(fmt.Sprintf("+%d more streams", remaining)))
				break
			}
			
			statusIcon := "🟢"
			if stream.Status == "inactive" {
				statusIcon = "🔴"
			}
			
			displayID := stream.ID
			if len(displayID) > 15 {
				displayID = displayID[:12] + "..."
			}
			
			streamLine := fmt.Sprintf("%s %s %s", statusIcon, 
				ValueStyle.Render(strings.ToUpper(stream.Type)), 
				displayID)
			content = append(content, streamLine)
			
			if showDetails && stream.Resolution != "" {
				content = append(content, fmt.Sprintf("  %s", MutedStyle.Render(stream.Resolution)))
			}
		}
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(10).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

func (m *RichDashboardModel) createTabletActivityPanel(width int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("📋 ACTIVITY")
	
	content := []string{title}
	
	if len(m.stats.RecentLogs) == 0 {
		content = append(content, MutedStyle.Render("Monitoring system events..."))
	} else {
		// Show last 3 logs
		startIdx := len(m.stats.RecentLogs) - 3
		if startIdx < 0 {
			startIdx = 0
		}
		
		for i := startIdx; i < len(m.stats.RecentLogs); i++ {
			log := m.stats.RecentLogs[i]
			levelIcon := LogLevelIcon(log.Level)
			timeStr := log.Timestamp.Format("15:04")
			message := log.Message
			if len(message) > width-15 {
				message = message[:width-18] + "..."
			}
			content = append(content, fmt.Sprintf("%s %s %s", levelIcon, MutedStyle.Render(timeStr), message))
		}
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(8).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

// Desktop layout helpers
func (m *RichDashboardModel) createDesktopSystemPanel(width, height int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Background(PanelBg).
		Bold(true).
		Padding(0, 1).
		Render("🖥️ SYSTEM STATUS")
	
	elapsed := time.Since(m.startTime)
	totalSessions := m.stats.SRTSessions + m.stats.RTPSessions
	
	content := []string{
		title,
		"",
		fmt.Sprintf("Uptime: %s", SuccessStyle.Render(m.formatDuration(elapsed))),
		fmt.Sprintf("Sessions: %s", ValueStyle.Render(fmt.Sprintf("%d", totalSessions))),
		fmt.Sprintf("Memory: %.1f%%", m.stats.MemoryPercent),
		fmt.Sprintf("Goroutines: %d", m.stats.Goroutines),
		"",
		fmt.Sprintf("Errors: %s", ErrorStyle.Render(fmt.Sprintf("%d", m.stats.ErrorCount))),
		fmt.Sprintf("Warnings: %s", WarningStyle.Render(fmt.Sprintf("%d", m.stats.WarningCount))),
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(BorderDark).
		Background(PanelBg).
		Width(width).
		Height(height).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

func (m *RichDashboardModel) createDesktopNetworkPanel(width, height int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("🌐 NETWORK")
	
	totalBitrate := m.stats.BitrateRTP + m.stats.BitrateSRT
	
	content := []string{
		title,
		"",
		fmt.Sprintf("Total: %s", SuccessStyle.Render(m.formatBitrate(totalBitrate))),
		"",
		fmt.Sprintf("RTP Sessions: %d", m.stats.RTPSessions),
		fmt.Sprintf("RTP Bitrate: %s", ValueStyle.Render(m.formatBitrate(m.stats.BitrateRTP))),
		"",
		fmt.Sprintf("SRT Sessions: %d", m.stats.SRTSessions),
		fmt.Sprintf("SRT Bitrate: %s", ValueStyle.Render(m.formatBitrate(m.stats.BitrateSRT))),
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(height).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}

func (m *RichDashboardModel) createDesktopStreamsPanel(width, height int) string {
	// Use the existing ultra streams panel but with height constraint
	return m.createUltraStreamsPanel(width)
}

// Ultrawide layout helpers
func (m *RichDashboardModel) createUltrawideMetricsPanel(width, height int) string {
	title := lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Render("📊 METRICS")
	
	elapsed := time.Since(m.startTime)
	frameRate := float64(0)
	if m.stats.FramesProcessed > 0 && elapsed.Seconds() > 0 {
		frameRate = float64(m.stats.FramesProcessed) / elapsed.Seconds()
	}
	
	content := []string{
		title,
		"",
		fmt.Sprintf("Frames Processed: %s", ValueStyle.Render(m.formatNumber(m.stats.FramesProcessed))),
		fmt.Sprintf("Frame Rate: %.1f fps", frameRate),
		fmt.Sprintf("Frames Dropped: %s", ErrorStyle.Render(m.formatNumber(m.stats.FramesDropped))),
		"",
		fmt.Sprintf("Phase: %s", InfoStyle.Render(m.stats.TestPhase)),
		fmt.Sprintf("Progress: %d%%", m.stats.Progress),
	}
	
	// Add codec stats if available
	if len(m.stats.CodecStats) > 0 {
		content = append(content, "", "Codecs:")
		for codec, count := range m.stats.CodecStats {
			content = append(content, fmt.Sprintf("  %s: %d", codec, count))
		}
	}
	
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Width(width).
		Height(height).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, content...))
}
