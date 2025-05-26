package ui

import "github.com/charmbracelet/lipgloss"

// Broadcast-focused color palette with modern dark theme
var (
	// Primary broadcast colors
	Primary   = lipgloss.Color("#FF6B35") // Broadcast Orange/Red
	Secondary = lipgloss.Color("#1E88E5") // Broadcast Blue
	Success   = lipgloss.Color("#4CAF50") // Professional Green (readable)
	Warning   = lipgloss.Color("#FFB74D") // Amber warning
	Error     = lipgloss.Color("#F44336") // Alert Red
	
	// Text and background colors for dark broadcast theme
	Text      = lipgloss.Color("#E0E0E0") // Light text for dark background
	TextBright= lipgloss.Color("#FFFFFF") // Bright white text
	White     = lipgloss.Color("#FFFFFF") // White (compatibility)
	Muted     = lipgloss.Color("#90A4AE") // Muted gray for secondary info
	
	// Alternative success colors for better readability
	SuccessAlt = lipgloss.Color("#81C784") // Lighter readable green
	LiveGreen  = lipgloss.Color("#66BB6A") // Softer live indicator green
	
	// Background colors - dark broadcast studio aesthetic
	Background    = lipgloss.Color("#0D1117") // Deep dark background
	PanelBg       = lipgloss.Color("#161B26") // Panel background
	HeaderBg      = lipgloss.Color("#1C2128") // Header background
	BorderDark    = lipgloss.Color("#30363D") // Dark borders
	
	// Broadcast-specific accent colors
	OnAir         = lipgloss.Color("#FF1744") // On-air red
	Recording     = lipgloss.Color("#FF5722") // Recording orange
	Standby       = lipgloss.Color("#FFC107") // Standby yellow
	Offline       = lipgloss.Color("#424242") // Offline gray
	
	// Modern broadcast gradient colors
	GradientStart = lipgloss.Color("#1565C0") // Deep broadcast blue
	GradientEnd   = lipgloss.Color("#283593") // Dark purple-blue
	
	// Adaptive colors for modern broadcast look
	CardBg      = lipgloss.AdaptiveColor{Light: "#F5F5F5", Dark: "#161B26"}
	BorderColor = lipgloss.AdaptiveColor{Light: "#DDDDDD", Dark: "#30363D"}
	AccentBg    = lipgloss.AdaptiveColor{Light: "#E3F2FD", Dark: "#1A237E"}
)

// Broadcast-focused style definitions with dark theme
var (
	HeaderStyle = lipgloss.NewStyle().
		Foreground(TextBright).
		Background(HeaderBg).
		Padding(1, 2).
		Bold(true).
		Align(lipgloss.Center).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary)

	PanelStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(BorderDark).
		Background(PanelBg).
		Foreground(Text).
		Padding(1, 2).
		MarginBottom(1).
		Width(50)

	BroadcastPanelStyle = lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(Primary).
		Background(PanelBg).
		Foreground(Text).
		Padding(1, 2)

	PanelTitleStyle = lipgloss.NewStyle().
		Foreground(Primary).
		Background(PanelBg).
		Bold(true).
		Padding(0, 1)

	OnAirStyle = lipgloss.NewStyle().
		Foreground(OnAir).
		Bold(true).
		Background(PanelBg)

	SuccessStyle = lipgloss.NewStyle().
		Foreground(Success).
		Bold(true)

	ErrorStyle = lipgloss.NewStyle().
		Foreground(Error).
		Bold(true)

	WarningStyle = lipgloss.NewStyle().
		Foreground(Warning).
		Bold(true)

	InfoStyle = lipgloss.NewStyle().
		Foreground(Secondary).
		Bold(true)

	MutedStyle = lipgloss.NewStyle().
		Foreground(Muted)
		
	MetricStyle = lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		Background(PanelBg).
		Padding(0, 1)
		
	ValueStyle = lipgloss.NewStyle().
		Foreground(TextBright).
		Bold(true).
		Background(PanelBg).
		Padding(0, 1)
		
	ActiveStyle = lipgloss.NewStyle().
		Foreground(LiveGreen).
		Background(PanelBg).
		Bold(true).
		Padding(0, 1)
		
	InactiveStyle = lipgloss.NewStyle().
		Foreground(Offline).
		Background(PanelBg).
		Bold(true).
		Padding(0, 1)

	// New broadcast-specific styles
	LiveStyle = lipgloss.NewStyle().
		Foreground(OnAir).
		Background(PanelBg).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(OnAir)

	RecordingStyle = lipgloss.NewStyle().
		Foreground(Recording).
		Background(PanelBg).
		Bold(true).
		Padding(0, 1)

	StandbyStyle = lipgloss.NewStyle().
		Foreground(Standby).
		Background(PanelBg).
		Bold(true).
		Padding(0, 1)
)

// Terminal readability enhancements
var (
	// High contrast alternatives for poor terminal support
	HighContrastSuccess = lipgloss.Color("#00FF00") // Fallback bright green
	HighContrastError   = lipgloss.Color("#FF0000") // Fallback bright red
	HighContrastWarning = lipgloss.Color("#FFFF00") // Fallback bright yellow
)

// Helper functions for broadcast-style indicators
func StatusIcon(active bool) string {
	if active {
		return OnAirStyle.Render("🔴")
	}
	return InactiveStyle.Render("⚫")
}

func BroadcastStatusIcon(status string) string {
	switch status {
	case "live", "active", "on-air":
		return OnAirStyle.Render("🔴 LIVE")
	case "recording":
		return RecordingStyle.Render("⏺️ REC")
	case "standby":
		return StandbyStyle.Render("🟡 STBY")
	default:
		return InactiveStyle.Render("⚫ OFF")
	}
}

func LogLevelIcon(level string) string {
	switch level {
	case "error":
		return ErrorStyle.Render("❌")
	case "warn", "warning":
		return WarningStyle.Render("⚠️")
	case "info":
		return InfoStyle.Render("ℹ️")
	case "debug":
		return MutedStyle.Render("🐛")
	default:
		return MutedStyle.Render("•")
	}
}

func ProtocolIcon(protocol string) string {
	switch protocol {
	case "srt":
		return OnAirStyle.Render("📡")
	case "rtp":
		return InfoStyle.Render("📺")
	case "rtmp":
		return RecordingStyle.Render("📹")
	default:
		return MutedStyle.Render("❓")
	}
}

func StreamTypeIcon(streamType string) string {
	switch streamType {
	case "srt":
		return LiveStyle.Render("📡 SRT")
	case "rtp":
		return InfoStyle.Render("📺 RTP")
	case "rtmp":
		return RecordingStyle.Render("📹 RTMP")
	default:
		return MutedStyle.Render("📊 DATA")
	}
}
