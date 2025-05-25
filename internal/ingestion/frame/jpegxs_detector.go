package frame

import (
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// JPEG-XS markers
const (
	JPEGXSMarkerSOI  = 0xFF10 // Start of Image
	JPEGXSMarkerEOI  = 0xFF11 // End of Image
	JPEGXSMarkerSOT  = 0xFF12 // Start of Tile
	JPEGXSMarkerEOT  = 0xFF13 // End of Tile
	JPEGXSMarkerSLH  = 0xFF14 // Slice Header
	JPEGXSMarkerPIH  = 0xFF15 // Picture Header
	JPEGXSMarkerCDT  = 0xFF16 // Component Table
	JPEGXSMarkerWGT  = 0xFF17 // Weight Table
	JPEGXSMarkerCOM  = 0xFF18 // Comment
	JPEGXSMarkerNLT  = 0xFF19 // Nonlinearity Table
	JPEGXSMarkerCWD  = 0xFF1A // Codestream Wavelet Decomposition
	JPEGXSMarkerCTS  = 0xFF1B // Codestream Tile-part Size
)

// JPEGXSDetector detects JPEG-XS frame boundaries
// JPEG-XS is typically used for low-latency video with each frame independently coded
type JPEGXSDetector struct {
	inFrame      bool
	frameStarted bool
	lastMarker   uint16
}

// NewJPEGXSDetector creates a new JPEG-XS frame detector
func NewJPEGXSDetector() *JPEGXSDetector {
	return &JPEGXSDetector{}
}

// DetectBoundaries detects frame boundaries in JPEG-XS stream
func (d *JPEGXSDetector) DetectBoundaries(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	if len(pkt.Data) < 2 {
		return false, false
	}
	
	// JPEG-XS uses markers similar to JPEG
	markers := d.findMarkers(pkt.Data)
	
	for _, marker := range markers {
		switch marker {
		case JPEGXSMarkerSOI:
			// Start of Image - new frame begins
			if d.inFrame {
				// Previous frame didn't have proper EOI
				isEnd = true
			}
			isStart = true
			d.inFrame = true
			d.frameStarted = true
			
		case JPEGXSMarkerEOI:
			// End of Image - frame complete
			if d.inFrame {
				isEnd = true
				d.inFrame = false
				d.frameStarted = false
			}
			
		case JPEGXSMarkerPIH:
			// Picture Header - also indicates frame start
			if !d.inFrame {
				isStart = true
				d.inFrame = true
				d.frameStarted = true
			}
		}
		
		d.lastMarker = marker
	}
	
	// RTP mode might use marker bit for frame end
	if pkt.HasFlag(types.PacketFlagFrameEnd) && d.inFrame {
		isEnd = true
		d.inFrame = false
		d.frameStarted = false
	}
	
	return isStart, isEnd
}

// GetFrameType determines frame type from NAL units
// For JPEG-XS, all frames are intra-coded (I-frames)
func (d *JPEGXSDetector) GetFrameType(nalUnits []types.NALUnit) types.FrameType {
	// JPEG-XS only has intra frames
	return types.FrameTypeI
}

// IsKeyframe checks if data contains a keyframe
// For JPEG-XS, every frame is a keyframe (intra-coded)
func (d *JPEGXSDetector) IsKeyframe(data []byte) bool {
	// Check for SOI marker
	markers := d.findMarkers(data)
	for _, marker := range markers {
		if marker == JPEGXSMarkerSOI || marker == JPEGXSMarkerPIH {
			return true
		}
	}
	return len(markers) > 0 // Any JPEG-XS frame is a keyframe
}

// GetCodec returns the codec type
func (d *JPEGXSDetector) GetCodec() types.CodecType {
	return types.CodecJPEGXS
}

// findMarkers finds JPEG-XS markers in data
func (d *JPEGXSDetector) findMarkers(data []byte) []uint16 {
	markers := make([]uint16, 0)
	
	for i := 0; i < len(data)-1; i++ {
		// Look for marker prefix 0xFF
		if data[i] == 0xFF {
			// Check if next byte forms a valid marker
			markerByte := data[i+1]
			if markerByte >= 0x10 && markerByte <= 0x1F {
				marker := uint16(0xFF00) | uint16(markerByte)
				markers = append(markers, marker)
				
				// Skip marker data if present
				if i+3 < len(data) && markerByte != 0x10 && markerByte != 0x11 {
					// Most markers have length field
					length := (uint16(data[i+2]) << 8) | uint16(data[i+3])
					i += int(length) + 1
				} else {
					i++ // Skip marker byte
				}
			}
		}
	}
	
	return markers
}

// parseJPEGXSHeader extracts metadata from JPEG-XS picture header
// This is simplified - real implementation would parse the full header
func (d *JPEGXSDetector) parseJPEGXSHeader(data []byte) (width, height int, profile string) {
	// Find PIH marker
	for i := 0; i < len(data)-10; i++ {
		if data[i] == 0xFF && data[i+1] == 0x15 { // PIH marker
			// Skip marker and length
			if i+10 < len(data) {
				// Simplified extraction - real format is more complex
				// PIH contains: Lcod, Ppih, Plev, Wf, Hf, etc.
				width = int(data[i+6])<<8 | int(data[i+7])
				height = int(data[i+8])<<8 | int(data[i+9])
				
				// Profile based on Plev field
				plev := data[i+5]
				switch plev {
				case 0x00:
					profile = "Light"
				case 0x10:
					profile = "Main"
				case 0x20:
					profile = "High"
				default:
					profile = "Unknown"
				}
				
				return
			}
		}
	}
	
	return 0, 0, ""
}
