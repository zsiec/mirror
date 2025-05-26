package testdata

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"time"
)

// HEVCGenerator generates test HEVC/H.265 data
type HEVCGenerator struct {
	frameCount int
	width      int
	height     int
	fps        int
	random     *rand.Rand
}

// NewHEVCGenerator creates a new HEVC test data generator
func NewHEVCGenerator(width, height, fps int) *HEVCGenerator {
	return &HEVCGenerator{
		width:  width,
		height: height,
		fps:    fps,
		random: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NAL unit types for HEVC (H.265)
const (
	NALTrailN    = 0  // Trailing non-reference picture
	NALTrailR    = 1  // Trailing reference picture
	NALTSAN      = 2  // Temporal sub-layer access non-reference
	NALTSAR      = 3  // Temporal sub-layer access reference
	NALSTSAN     = 4  // Step-wise temporal sub-layer access non-reference
	NALSTSAR     = 5  // Step-wise temporal sub-layer access reference
	NALRADLN     = 6  // Random access decodable leading non-reference
	NALRADLR     = 7  // Random access decodable leading reference
	NALRASLN     = 8  // Random access skipped leading non-reference
	NALRASLR     = 9  // Random access skipped leading reference
	NALBLAN      = 16 // Broken link access non-reference
	NALBLAR      = 17 // Broken link access reference
	NALIDLN      = 18 // Instantaneous decoding refresh non-reference
	NALIDLR      = 19 // Instantaneous decoding refresh reference
	NALCRAN      = 20 // Clean random access non-reference
	NALCRAR      = 21 // Clean random access reference
	NALVPS       = 32 // Video parameter set
	NALSPS       = 33 // Sequence parameter set
	NALPPS       = 34 // Picture parameter set
	NALAUD       = 35 // Access unit delimiter
	NALEOS       = 36 // End of sequence
	NALEOB       = 37 // End of bitstream
	NALFILLER    = 38 // Filler data
	NALSEI       = 39 // Supplemental enhancement information
	NALSEISuffix = 40 // SEI suffix
)

// NALUnit represents an HEVC NAL unit
type NALUnit struct {
	Type    byte
	Payload []byte
}

// GenerateVPS generates a Video Parameter Set NAL unit
func (g *HEVCGenerator) GenerateVPS() []byte {
	var buf bytes.Buffer

	// Start code
	buf.Write([]byte{0x00, 0x00, 0x00, 0x01})

	// NAL unit header (2 bytes)
	// nal_unit_type = 32 (VPS), nuh_layer_id = 0, nuh_temporal_id_plus1 = 1
	buf.WriteByte((NALVPS << 1) | 0)
	buf.WriteByte(0x01)

	// VPS payload (simplified)
	vps := []byte{
		0x01,       // vps_video_parameter_set_id = 0
		0x00, 0x00, // vps_reserved_three_2bits, vps_max_layers_minus1
		0x00, 0x00, 0x00, // vps_max_sub_layers_minus1, vps_temporal_id_nesting_flag
		0xFF, 0xFF, // vps_reserved_0xffff_16bits
		// Profile tier level...
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	buf.Write(vps)

	return buf.Bytes()
}

// GenerateSPS generates a Sequence Parameter Set NAL unit
func (g *HEVCGenerator) GenerateSPS() []byte {
	var buf bytes.Buffer

	// Start code
	buf.Write([]byte{0x00, 0x00, 0x00, 0x01})

	// NAL unit header
	buf.WriteByte((NALSPS << 1) | 0)
	buf.WriteByte(0x01)

	// SPS payload (simplified)
	sps := []byte{
		0x01, // sps_video_parameter_set_id = 0
		0x00, // sps_max_sub_layers_minus1 = 0
		0x00, // sps_temporal_id_nesting_flag = 0
		// Profile tier level
		0x01,                   // general_profile_space = 0, general_tier_flag = 0, general_profile_idc = 1
		0x60,                   // general_profile_compatibility_flags
		0x00, 0x00, 0x00, 0x00, // more compatibility flags
		0x90,                         // general_constraint_indicator_flags
		0x00, 0x00, 0x00, 0x00, 0x00, // more constraint flags
		0x5D, // general_level_idc = 93 (level 3.1)
		0x00, // sps_seq_parameter_set_id = 0
		0x00, // chroma_format_idc = 1 (4:2:0)
	}

	// Add width and height (simplified encoding)
	buf.Write(sps)

	// Write width in golomb coding (simplified)
	buf.WriteByte(byte(g.width >> 8))
	buf.WriteByte(byte(g.width & 0xFF))

	// Write height in golomb coding (simplified)
	buf.WriteByte(byte(g.height >> 8))
	buf.WriteByte(byte(g.height & 0xFF))

	return buf.Bytes()
}

// GeneratePPS generates a Picture Parameter Set NAL unit
func (g *HEVCGenerator) GeneratePPS() []byte {
	var buf bytes.Buffer

	// Start code
	buf.Write([]byte{0x00, 0x00, 0x00, 0x01})

	// NAL unit header
	buf.WriteByte((NALPPS << 1) | 0)
	buf.WriteByte(0x01)

	// PPS payload (simplified)
	pps := []byte{
		0x01, // pps_pic_parameter_set_id = 0
		0x00, // pps_seq_parameter_set_id = 0
		0x00, // dependent_slice_segments_enabled_flag = 0, output_flag_present_flag = 0
		0x00, // num_extra_slice_header_bits = 0
		0x00, // sign_data_hiding_enabled_flag = 0, cabac_init_present_flag = 0
		0x00, // num_ref_idx_l0_default_active_minus1 = 0
		0x00, // num_ref_idx_l1_default_active_minus1 = 0
		0x00, // init_qp_minus26 = 0
		0x00, // more PPS data...
	}
	buf.Write(pps)

	return buf.Bytes()
}

// GenerateIDRFrame generates an IDR (Instantaneous Decoder Refresh) frame
func (g *HEVCGenerator) GenerateIDRFrame() []byte {
	var buf bytes.Buffer

	// Start code
	buf.Write([]byte{0x00, 0x00, 0x00, 0x01})

	// NAL unit header for IDR
	buf.WriteByte((NALIDLR << 1) | 0)
	buf.WriteByte(0x01)

	// Generate slice header (simplified)
	sliceHeader := []byte{
		0x00, // first_slice_segment_in_pic_flag = 1
		0x00, // slice_type = I
		// More slice header data...
	}
	buf.Write(sliceHeader)

	// Generate fake compressed data
	// In real HEVC, this would be CABAC-encoded macroblocks
	frameSize := (g.width * g.height * 3) / 2 / 100 // Rough estimate for compressed size
	compressedData := make([]byte, frameSize)
	g.random.Read(compressedData)

	// Ensure no start codes in compressed data
	for i := 0; i < len(compressedData)-3; i++ {
		if compressedData[i] == 0x00 && compressedData[i+1] == 0x00 {
			if compressedData[i+2] == 0x00 || compressedData[i+2] == 0x01 {
				compressedData[i+2] = 0x02 // Emulation prevention
			}
		}
	}

	buf.Write(compressedData)
	g.frameCount++

	return buf.Bytes()
}

// GeneratePFrame generates a P (Predicted) frame
func (g *HEVCGenerator) GeneratePFrame() []byte {
	var buf bytes.Buffer

	// Start code
	buf.Write([]byte{0x00, 0x00, 0x00, 0x01})

	// NAL unit header for trailing picture
	buf.WriteByte((NALTrailR << 1) | 0)
	buf.WriteByte(0x01)

	// Generate slice header for P slice
	sliceHeader := []byte{
		0x00, // first_slice_segment_in_pic_flag = 1
		0x01, // slice_type = P
		// More slice header data...
	}
	buf.Write(sliceHeader)

	// P frames are typically smaller than I frames
	frameSize := (g.width * g.height * 3) / 2 / 300 // Much smaller than I frame
	compressedData := make([]byte, frameSize)
	g.random.Read(compressedData)

	// Prevent start codes
	for i := 0; i < len(compressedData)-3; i++ {
		if compressedData[i] == 0x00 && compressedData[i+1] == 0x00 {
			if compressedData[i+2] == 0x00 || compressedData[i+2] == 0x01 {
				compressedData[i+2] = 0x02
			}
		}
	}

	buf.Write(compressedData)
	g.frameCount++

	return buf.Bytes()
}

// GenerateAUD generates an Access Unit Delimiter
func (g *HEVCGenerator) GenerateAUD() []byte {
	var buf bytes.Buffer

	// Start code
	buf.Write([]byte{0x00, 0x00, 0x00, 0x01})

	// NAL unit header
	buf.WriteByte((NALAUD << 1) | 0)
	buf.WriteByte(0x01)

	// AUD payload (pic_type for I slice)
	buf.WriteByte(0x00)

	return buf.Bytes()
}

// GenerateGOPSequence generates a Group of Pictures sequence
func (g *HEVCGenerator) GenerateGOPSequence(gopSize int) [][]byte {
	nalUnits := make([][]byte, 0)

	// Start with parameter sets if this is the first GOP
	if g.frameCount == 0 {
		nalUnits = append(nalUnits, g.GenerateVPS())
		nalUnits = append(nalUnits, g.GenerateSPS())
		nalUnits = append(nalUnits, g.GeneratePPS())
	}

	// Generate GOP: I P P P P P P P...
	for i := 0; i < gopSize; i++ {
		// Add AUD before each frame
		nalUnits = append(nalUnits, g.GenerateAUD())

		if i == 0 {
			// I frame
			nalUnits = append(nalUnits, g.GenerateIDRFrame())
		} else {
			// P frame
			nalUnits = append(nalUnits, g.GeneratePFrame())
		}
	}

	return nalUnits
}

// GenerateStreamSegment generates a segment of HEVC stream data
func (g *HEVCGenerator) GenerateStreamSegment(durationMs int) []byte {
	var buf bytes.Buffer

	// Calculate number of frames
	framesNeeded := (durationMs * g.fps) / 1000
	if framesNeeded == 0 {
		framesNeeded = 1
	}

	// Generate frames in GOPs of 30 frames
	gopSize := 30
	for frames := 0; frames < framesNeeded; {
		remainingFrames := framesNeeded - frames
		if remainingFrames > gopSize {
			remainingFrames = gopSize
		}

		nalUnits := g.GenerateGOPSequence(remainingFrames)
		for _, nal := range nalUnits {
			buf.Write(nal)
		}

		frames += remainingFrames
	}

	return buf.Bytes()
}

// RTPPacketize splits HEVC data into RTP packets
func RTPPacketize(nalUnit []byte, mtu int) [][]byte {
	if mtu == 0 {
		mtu = 1400 // Default MTU for RTP
	}

	// Remove start code if present
	data := nalUnit
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x00, 0x00, 0x00, 0x01}) {
		data = data[4:]
	} else if len(data) >= 3 && bytes.Equal(data[:3], []byte{0x00, 0x00, 0x01}) {
		data = data[3:]
	}

	packets := make([][]byte, 0)

	// If NAL unit fits in single RTP packet
	if len(data) <= mtu {
		packets = append(packets, data)
		return packets
	}

	// Fragment into FU (Fragmentation Unit) packets
	nalHeader := data[0:2]
	nalType := (nalHeader[0] >> 1) & 0x3F

	// Payload data (skip NAL header)
	payload := data[2:]

	// Create FU packets
	for offset := 0; offset < len(payload); {
		var buf bytes.Buffer

		// FU header (same layer_id and temporal_id as original NAL)
		fuHeader := []byte{
			(49 << 1) | (nalHeader[0] & 0x81), // FU type = 49
			nalHeader[1],                      // Same temporal_id
		}
		buf.Write(fuHeader)

		// FU payload header
		fuPayloadHeader := nalType
		if offset == 0 {
			fuPayloadHeader |= 0x80 // Start bit
		}
		if offset+mtu-3 >= len(payload) {
			fuPayloadHeader |= 0x40 // End bit
		}
		buf.WriteByte(fuPayloadHeader)

		// Calculate how much data fits in this packet
		remainingSpace := mtu - 3 // 2 bytes FU header + 1 byte FU payload header
		end := offset + remainingSpace
		if end > len(payload) {
			end = len(payload)
		}

		buf.Write(payload[offset:end])
		packets = append(packets, buf.Bytes())

		offset = end
	}

	return packets
}

// GenerateRTPStream generates RTP packets for a duration
func (g *HEVCGenerator) GenerateRTPStream(durationMs int, ssrc uint32) []*RTPPacket {
	// Generate HEVC data
	streamData := g.GenerateStreamSegment(durationMs)

	// Split into NAL units
	nalUnits := SplitNALUnits(streamData)

	packets := make([]*RTPPacket, 0)
	sequenceNumber := uint16(g.random.Intn(65536))
	timestamp := uint32(g.random.Intn(4294967296))
	timestampIncrement := uint32(90000 / g.fps) // 90kHz clock

	for _, nalUnit := range nalUnits {
		// Check if this NAL unit starts a new access unit
		nalType := (nalUnit[4] >> 1) & 0x3F // Skip start code, get NAL type
		isNewAccessUnit := nalType == NALAUD || nalType == NALIDLR ||
			nalType == NALTrailR || nalType == NALTrailN

		if isNewAccessUnit && len(packets) > 0 {
			timestamp += timestampIncrement
		}

		// Packetize NAL unit
		rtpPayloads := RTPPacketize(nalUnit, 1400)

		for i, payload := range rtpPayloads {
			pkt := &RTPPacket{
				Version:        2,
				PayloadType:    96, // Dynamic payload type for H.265
				SequenceNumber: sequenceNumber,
				Timestamp:      timestamp,
				SSRC:           ssrc,
				Marker:         i == len(rtpPayloads)-1, // Mark last packet of NAL unit
				Payload:        payload,
			}
			packets = append(packets, pkt)
			sequenceNumber++
		}
	}

	return packets
}

// RTPPacket represents an RTP packet
type RTPPacket struct {
	Version        uint8
	Padding        bool
	Extension      bool
	CSRCCount      uint8
	Marker         bool
	PayloadType    uint8
	SequenceNumber uint16
	Timestamp      uint32
	SSRC           uint32
	CSRC           []uint32
	Payload        []byte
}

// Marshal serializes the RTP packet to bytes
func (p *RTPPacket) Marshal() []byte {
	buf := make([]byte, 12+len(p.Payload))

	// Byte 0: V(2), P(1), X(1), CC(4)
	buf[0] = (p.Version << 6) | (p.CSRCCount & 0x0F)
	if p.Padding {
		buf[0] |= 0x20
	}
	if p.Extension {
		buf[0] |= 0x10
	}

	// Byte 1: M(1), PT(7)
	buf[1] = p.PayloadType & 0x7F
	if p.Marker {
		buf[1] |= 0x80
	}

	// Sequence number
	binary.BigEndian.PutUint16(buf[2:4], p.SequenceNumber)

	// Timestamp
	binary.BigEndian.PutUint32(buf[4:8], p.Timestamp)

	// SSRC
	binary.BigEndian.PutUint32(buf[8:12], p.SSRC)

	// Payload
	copy(buf[12:], p.Payload)

	return buf
}

// SplitNALUnits splits a byte stream into individual NAL units
func SplitNALUnits(data []byte) [][]byte {
	nalUnits := make([][]byte, 0)

	for i := 0; i < len(data); {
		// Find start code
		startPos := -1
		startCodeLen := 0

		for j := i; j < len(data)-2; j++ {
			if data[j] == 0x00 && data[j+1] == 0x00 {
				if j < len(data)-3 && data[j+2] == 0x00 && data[j+3] == 0x01 {
					startPos = j
					startCodeLen = 4
					break
				} else if data[j+2] == 0x01 {
					startPos = j
					startCodeLen = 3
					break
				}
			}
		}

		if startPos == -1 {
			// No more NAL units
			break
		}

		// Find next start code
		nextPos := len(data)
		for j := startPos + startCodeLen; j < len(data)-2; j++ {
			if data[j] == 0x00 && data[j+1] == 0x00 {
				if j < len(data)-3 && data[j+2] == 0x00 && data[j+3] == 0x01 {
					nextPos = j
					break
				} else if data[j+2] == 0x01 {
					nextPos = j
					break
				}
			}
		}

		// Extract NAL unit (including start code)
		nalUnit := data[startPos:nextPos]
		nalUnits = append(nalUnits, nalUnit)

		i = nextPos
	}

	return nalUnits
}
