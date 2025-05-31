package mpegts

import (
	"errors"
	"fmt"
)

const (
	// MPEG-TS constants
	PacketSize = 188
	SyncByte   = 0x47
	MaxPID     = 8191

	// PIDs
	PIDProgramAssociation = 0x0000
	PIDConditionalAccess  = 0x0001
	PIDNull               = 0x1FFF
)

// Packet represents an MPEG-TS packet
type Packet struct {
	PID                   uint16
	PayloadStart          bool
	AdaptationFieldExists bool
	PayloadExists         bool
	ContinuityCounter     uint8
	Payload               []byte

	// PTS/DTS if present
	HasPTS bool
	HasDTS bool
	PTS    int64
	DTS    int64

	// PCR if present in adaptation field
	HasPCR bool
	PCR    int64
}

// Parser parses MPEG-TS packets
type Parser struct {
	pmtPID   uint16
	videoPID uint16
	audioPID uint16
	pcrPID   uint16

	// PES assembly
	pesBuffer  map[uint16][]byte
	pesStarted map[uint16]bool

	// PAT/PMT parsing state
	patParsed  bool
	pmtParsed  bool
	programNum uint16

	// Detected codec from PMT
	videoStreamType uint8
	audioStreamType uint8
}

// NewParser creates a new MPEG-TS parser
func NewParser() *Parser {
	return &Parser{
		pesBuffer:  make(map[uint16][]byte),
		pesStarted: make(map[uint16]bool),
	}
}

// Parse parses MPEG-TS data and returns packets
func (p *Parser) Parse(data []byte) ([]*Packet, error) {
	return p.ParseWithExtractor(data, nil)
}

// ParseWithExtractor parses MPEG-TS data with parameter set extraction
func (p *Parser) ParseWithExtractor(data []byte, extractor ParameterSetExtractor) ([]*Packet, error) {
	if len(data) < PacketSize {
		return nil, errors.New("data too small for MPEG-TS packet")
	}

	packets := make([]*Packet, 0)

	// Process all complete packets
	for i := 0; i+PacketSize <= len(data); i += PacketSize {
		pkt, err := p.parsePacket(data[i : i+PacketSize])
		if err != nil {
			// Skip invalid packets
			continue
		}

		// Handle PAT/PMT parsing for PID auto-detection
		if pkt.PID == PIDProgramAssociation && pkt.PayloadStart && pkt.PayloadExists {
			p.parsePAT(pkt.Payload)
		} else if pkt.PID == p.pmtPID && pkt.PayloadStart && pkt.PayloadExists {
			p.parsePMTWithExtractor(pkt.Payload, extractor)
		}

		// **NEW: Extract parameter sets from PES packets**
		if pkt.PayloadStart && pkt.PayloadExists {
			if err := p.parsePESHeader(pkt); err == nil {
				// Check if this PES packet contains parameter sets
				if extractor != nil {
					p.extractParameterSetsFromPES(pkt, extractor)
				}
				packets = append(packets, pkt)
			}
		} else if pkt.PayloadExists {
			// Continuation of PES packet - assemble complete PES
			if extractor != nil {
				p.assemblePESPacket(pkt, extractor)
			}
			packets = append(packets, pkt)
		}
	}

	return packets, nil
}

// parsePacket parses a single MPEG-TS packet
func (p *Parser) parsePacket(data []byte) (*Packet, error) {
	if len(data) != PacketSize {
		return nil, fmt.Errorf("invalid packet size: %d", len(data))
	}

	if data[0] != SyncByte {
		return nil, errors.New("missing sync byte")
	}

	pkt := &Packet{}

	// Extract header fields
	pkt.PID = uint16(data[1]&0x1F)<<8 | uint16(data[2])

	// Check for invalid PID
	if pkt.PID > MaxPID {
		return nil, fmt.Errorf("invalid PID: %d", pkt.PID)
	}

	// Transport error indicator
	if data[1]&0x80 != 0 {
		return nil, errors.New("transport error indicator set")
	}

	// Payload unit start indicator
	pkt.PayloadStart = data[1]&0x40 != 0

	// Adaptation field control
	adaptationFieldControl := (data[3] >> 4) & 0x03
	pkt.AdaptationFieldExists = adaptationFieldControl&0x02 != 0
	pkt.PayloadExists = adaptationFieldControl&0x01 != 0

	// Continuity counter
	pkt.ContinuityCounter = data[3] & 0x0F

	// Parse adaptation field if present
	offset := 4
	if pkt.AdaptationFieldExists {
		adaptationFieldLength := int(data[offset])
		offset++

		if adaptationFieldLength > 0 {
			// Check for PCR
			if data[offset]&0x10 != 0 && adaptationFieldLength >= 6 {
				// PCR is in next 6 bytes
				pcrBase := int64(data[offset+1])<<25 |
					int64(data[offset+2])<<17 |
					int64(data[offset+3])<<9 |
					int64(data[offset+4])<<1 |
					int64(data[offset+5]>>7)

				pcrExt := int64(data[offset+5]&0x01)<<8 |
					int64(data[offset+6])

				pkt.PCR = pcrBase*300 + pcrExt
				pkt.HasPCR = true
			}

			offset += adaptationFieldLength
		}
	}

	// Extract payload
	if pkt.PayloadExists && offset < PacketSize {
		pkt.Payload = data[offset:]
	}

	return pkt, nil
}

// parsePESHeader extracts PTS/DTS from PES header
func (p *Parser) parsePESHeader(pkt *Packet) error {
	if len(pkt.Payload) < 9 {
		return errors.New("PES header too short")
	}

	// Check PES start code prefix (0x000001)
	if pkt.Payload[0] != 0x00 || pkt.Payload[1] != 0x00 || pkt.Payload[2] != 0x01 {
		return errors.New("invalid PES start code")
	}

	// Stream ID
	streamID := pkt.Payload[3]

	// PES packet length (can be 0 for video)
	// pesLength := uint16(pkt.Payload[4])<<8 | uint16(pkt.Payload[5])

	// Check if this is a stream with PTS/DTS
	if streamID != 0xBC && streamID != 0xBE && streamID != 0xBF &&
		streamID != 0xF0 && streamID != 0xF1 && streamID != 0xFF {

		// PTS/DTS flags are in byte 7
		if len(pkt.Payload) < 9 {
			return errors.New("PES header too short for PTS/DTS")
		}

		ptsDtsFlags := (pkt.Payload[7] >> 6) & 0x03

		offset := 9

		// PTS present
		if ptsDtsFlags&0x02 != 0 {
			if len(pkt.Payload) < offset+5 {
				return errors.New("PES payload too short for PTS")
			}

			pts, err := p.extractTimestamp(pkt.Payload[offset:])
			if err != nil {
				return fmt.Errorf("failed to extract PTS: %w", err)
			}
			pkt.PTS = pts
			pkt.HasPTS = true
			offset += 5

			// DTS also present
			if ptsDtsFlags&0x01 != 0 {
				if len(pkt.Payload) < offset+5 {
					return errors.New("PES payload too short for DTS")
				}

				dts, err := p.extractTimestamp(pkt.Payload[offset:])
				if err != nil {
					return fmt.Errorf("failed to extract DTS: %w", err)
				}
				pkt.DTS = dts
				pkt.HasDTS = true
			}
		}
	}

	return nil
}

// extractTimestamp extracts a 33-bit timestamp from 5 bytes
func (p *Parser) extractTimestamp(data []byte) (int64, error) {
	// Add bounds checking to prevent panic
	if len(data) < 5 {
		return 0, fmt.Errorf("insufficient data for timestamp: need 5 bytes, got %d", len(data))
	}

	timestamp := int64(data[0]&0x0E)<<29 |
		int64(data[1])<<22 |
		int64(data[2]&0xFE)<<14 |
		int64(data[3])<<7 |
		int64(data[4])>>1

	return timestamp, nil
}

// SetVideoPID sets the video PID
func (p *Parser) SetVideoPID(pid uint16) {
	p.videoPID = pid
}

// SetAudioPID sets the audio PID
func (p *Parser) SetAudioPID(pid uint16) {
	p.audioPID = pid
}

// SetPCRPID sets the PCR PID
func (p *Parser) SetPCRPID(pid uint16) {
	p.pcrPID = pid
}

// IsVideoPID returns true if this is the video PID
func (p *Parser) IsVideoPID(pid uint16) bool {
	return pid == p.videoPID
}

// IsAudioPID returns true if this is the audio PID
func (p *Parser) IsAudioPID(pid uint16) bool {
	return pid == p.audioPID
}

// IsPCRPID returns true if this is the PCR PID
func (p *Parser) IsPCRPID(pid uint16) bool {
	return pid == p.pcrPID
}

// GetVideoPID returns the current video PID
func (p *Parser) GetVideoPID() uint16 {
	return p.videoPID
}

// GetAudioPID returns the current audio PID
func (p *Parser) GetAudioPID() uint16 {
	return p.audioPID
}

// GetPMTPID returns the current PMT PID
func (p *Parser) GetPMTPID() uint16 {
	return p.pmtPID
}

// GetVideoStreamType returns the detected video stream type from PMT
func (p *Parser) GetVideoStreamType() uint8 {
	return p.videoStreamType
}

// GetAudioStreamType returns the detected audio stream type from PMT
func (p *Parser) GetAudioStreamType() uint8 {
	return p.audioStreamType
}

// parsePAT parses the Program Association Table to find PMT PID
func (p *Parser) parsePAT(payload []byte) {
	if p.patParsed || len(payload) < 8 {
		return
	}

	// Skip pointer field if present
	offset := 0
	if len(payload) > 0 {
		offset = int(payload[0]) + 1
	}

	if offset >= len(payload) || len(payload[offset:]) < 8 {
		return
	}

	data := payload[offset:]

	// Parse section header
	tableID := data[0]
	if tableID != 0x00 { // PAT table ID
		return
	}

	sectionLength := int((uint16(data[1]&0x0F) << 8) | uint16(data[2]))
	if sectionLength < 5 || len(data) < sectionLength+3 {
		return
	}

	// Skip to program list (after standard section header)
	programOffset := 8
	programListEnd := 3 + sectionLength - 4 // Exclude CRC

	// Parse programs
	for i := programOffset; i < programListEnd && i+3 < len(data); i += 4 {
		programNum := (uint16(data[i]) << 8) | uint16(data[i+1])
		pmtPID := ((uint16(data[i+2]) & 0x1F) << 8) | uint16(data[i+3])

		// Use first non-zero program
		if programNum != 0 {
			p.programNum = programNum
			p.pmtPID = pmtPID
			p.patParsed = true
			break
		}
	}
}

// ParameterSetExtractor is called when parameter sets are found in PMT
type ParameterSetExtractor func(parameterSets [][]byte, streamType uint8)

// parsePMT parses the Program Map Table to find video/audio PIDs and extract parameter sets
func (p *Parser) parsePMT(payload []byte) {
	p.parsePMTWithExtractor(payload, nil)
}

// ParsePMTWithExtractor parses PMT and calls extractor for parameter sets
func (p *Parser) ParsePMTWithExtractor(payload []byte, extractor ParameterSetExtractor) {
	p.parsePMTWithExtractor(payload, extractor)
}

// parsePMTWithExtractor parses the Program Map Table with parameter set extraction
func (p *Parser) parsePMTWithExtractor(payload []byte, extractor ParameterSetExtractor) {
	if p.pmtParsed || len(payload) < 12 {
		return
	}

	// Skip pointer field if present
	offset := 0
	if len(payload) > 0 {
		offset = int(payload[0]) + 1
	}

	if offset >= len(payload) || len(payload[offset:]) < 12 {
		return
	}

	data := payload[offset:]

	// Parse section header
	tableID := data[0]
	if tableID != 0x02 { // PMT table ID
		return
	}

	sectionLength := int((uint16(data[1]&0x0F) << 8) | uint16(data[2]))
	if sectionLength < 9 || len(data) < sectionLength+3 {
		return
	}

	// Extract PCR PID
	p.pcrPID = ((uint16(data[8]) & 0x1F) << 8) | uint16(data[9])

	// Program info length
	programInfoLength := int((uint16(data[10]&0x0F) << 8) | uint16(data[11]))

	// **NEW: Parse program-level descriptors for parameter sets**
	if programInfoLength > 0 && extractor != nil {
		p.extractParameterSetsFromDescriptors(data[12:12+programInfoLength], 0, extractor)
	}

	// Start of elementary streams
	streamOffset := 12 + programInfoLength
	streamListEnd := 3 + sectionLength - 4 // Exclude CRC

	// Parse elementary streams
	for i := streamOffset; i < streamListEnd && i+4 < len(data); {
		streamType := data[i]
		elementaryPID := ((uint16(data[i+1]) & 0x1F) << 8) | uint16(data[i+2])
		esInfoLength := int((uint16(data[i+3]&0x0F) << 8) | uint16(data[i+4]))

		// **NEW: Parse ES-level descriptors for parameter sets**
		if esInfoLength > 0 && extractor != nil && i+5+esInfoLength <= len(data) {
			p.extractParameterSetsFromDescriptors(data[i+5:i+5+esInfoLength], streamType, extractor)
		}

		// Identify stream types
		switch streamType {
		case 0x01, 0x02: // MPEG-1/2 Video
			fallthrough
		case 0x1B: // H.264 Video
			fallthrough
		case 0x24: // HEVC Video
			fallthrough
		case 0x51: // AV1 Video
			if p.videoPID == 0 {
				p.videoPID = elementaryPID
				p.videoStreamType = streamType
			}
		case 0x03, 0x04: // MPEG-1/2 Audio
			fallthrough
		case 0x0F: // AAC Audio
			fallthrough
		case 0x11: // AAC Audio
			fallthrough
		case 0x81: // AC-3 Audio
			if p.audioPID == 0 {
				p.audioPID = elementaryPID
				p.audioStreamType = streamType
			}
		}

		// Move to next stream
		i += 5 + esInfoLength
	}

	p.pmtParsed = true
}

// extractParameterSetsFromDescriptors extracts parameter sets from MPEG-TS descriptors
func (p *Parser) extractParameterSetsFromDescriptors(descriptors []byte, streamType uint8, extractor ParameterSetExtractor) {
	offset := 0
	var parameterSets [][]byte

	for offset < len(descriptors) {
		if offset+2 > len(descriptors) {
			break
		}

		descriptorTag := descriptors[offset]
		descriptorLength := int(descriptors[offset+1])

		if offset+2+descriptorLength > len(descriptors) {
			break
		}

		descriptorData := descriptors[offset+2 : offset+2+descriptorLength]

		// Look for parameter set descriptors
		switch descriptorTag {
		case 0x28: // AVC video descriptor (H.264)
			if streamType == 0x1B {
				paramSets := p.extractH264ParameterSetsFromDescriptor(descriptorData)
				parameterSets = append(parameterSets, paramSets...)
			}
		case 0x38: // HEVC video descriptor
			if streamType == 0x24 {
				paramSets := p.extractHEVCParameterSetsFromDescriptor(descriptorData)
				parameterSets = append(parameterSets, paramSets...)
			}
		case 0x42: // AV1 video descriptor
			if streamType == 0x51 {
				paramSets := p.extractAV1ParameterSetsFromDescriptor(descriptorData)
				parameterSets = append(parameterSets, paramSets...)
			}
		}

		offset += 2 + descriptorLength
	}

	// Call extractor if we found parameter sets
	if len(parameterSets) > 0 && extractor != nil {
		extractor(parameterSets, streamType)
	}
}

// extractH264ParameterSetsFromDescriptor extracts H.264 SPS/PPS from AVC descriptor
func (p *Parser) extractH264ParameterSetsFromDescriptor(data []byte) [][]byte {
	var parameterSets [][]byte

	if len(data) < 6 {
		return parameterSets
	}

	// Parse AVC configuration record
	offset := 0

	// Skip profile, constraints, level
	offset += 3

	if offset >= len(data) {
		return parameterSets
	}

	// Length size minus 1 (usually 3, meaning 4-byte length)
	_ = data[offset] & 0x03 // Skip length size field
	offset++

	if offset >= len(data) {
		return parameterSets
	}

	// Number of SPS
	numSPS := data[offset] & 0x1F
	offset++

	// Extract SPS
	for i := 0; i < int(numSPS) && offset+2 <= len(data); i++ {
		spsLength := int(data[offset])<<8 | int(data[offset+1])
		offset += 2

		if offset+spsLength <= len(data) {
			// Add NAL header (0x00 0x00 0x00 0x01 0x67) for H.264 SPS
			spsWithHeader := make([]byte, 5+spsLength)
			spsWithHeader[0] = 0x00
			spsWithHeader[1] = 0x00
			spsWithHeader[2] = 0x00
			spsWithHeader[3] = 0x01
			spsWithHeader[4] = 0x67 // H.264 SPS NAL header
			copy(spsWithHeader[5:], data[offset:offset+spsLength])
			parameterSets = append(parameterSets, spsWithHeader)
			offset += spsLength
		}
	}

	if offset >= len(data) {
		return parameterSets
	}

	// Number of PPS
	numPPS := data[offset]
	offset++

	// Extract PPS
	for i := 0; i < int(numPPS) && offset+2 <= len(data); i++ {
		ppsLength := int(data[offset])<<8 | int(data[offset+1])
		offset += 2

		if offset+ppsLength <= len(data) {
			// Add NAL header (0x00 0x00 0x00 0x01 0x68) for H.264 PPS
			ppsWithHeader := make([]byte, 5+ppsLength)
			ppsWithHeader[0] = 0x00
			ppsWithHeader[1] = 0x00
			ppsWithHeader[2] = 0x00
			ppsWithHeader[3] = 0x01
			ppsWithHeader[4] = 0x68 // H.264 PPS NAL header
			copy(ppsWithHeader[5:], data[offset:offset+ppsLength])
			parameterSets = append(parameterSets, ppsWithHeader)
			offset += ppsLength
		}
	}

	return parameterSets
}

// extractHEVCParameterSetsFromDescriptor extracts HEVC VPS/SPS/PPS from HEVC descriptor
func (p *Parser) extractHEVCParameterSetsFromDescriptor(data []byte) [][]byte {
	var parameterSets [][]byte

	if len(data) < 22 {
		return parameterSets
	}

	// Parse HEVC configuration record
	offset := 22 // Skip fixed fields

	if offset >= len(data) {
		return parameterSets
	}

	numArrays := data[offset]
	offset++

	// Process parameter set arrays (VPS, SPS, PPS)
	for i := 0; i < int(numArrays) && offset+3 <= len(data); i++ {
		_ = data[offset] & 0x3F // Skip NAL unit type
		offset++

		numNalUnits := int(data[offset])<<8 | int(data[offset+1])
		offset += 2

		for j := 0; j < numNalUnits && offset+2 <= len(data); j++ {
			nalLength := int(data[offset])<<8 | int(data[offset+1])
			offset += 2

			if offset+nalLength <= len(data) {
				// Add start code for HEVC NAL units
				nalWithHeader := make([]byte, 4+nalLength)
				nalWithHeader[0] = 0x00
				nalWithHeader[1] = 0x00
				nalWithHeader[2] = 0x00
				nalWithHeader[3] = 0x01
				copy(nalWithHeader[4:], data[offset:offset+nalLength])
				parameterSets = append(parameterSets, nalWithHeader)
				offset += nalLength
			}
		}
	}

	return parameterSets
}

// extractAV1ParameterSetsFromDescriptor extracts AV1 sequence header from AV1 descriptor
func (p *Parser) extractAV1ParameterSetsFromDescriptor(data []byte) [][]byte {
	var parameterSets [][]byte

	if len(data) < 4 {
		return parameterSets
	}

	// AV1 configuration record parsing would go here
	// This is more complex as AV1 doesn't use traditional parameter sets
	// Instead it uses sequence headers in the bitstream

	return parameterSets
}

// extractParameterSetsFromPES extracts parameter sets from PES packet payload
func (p *Parser) extractParameterSetsFromPES(pkt *Packet, extractor ParameterSetExtractor) {
	if len(pkt.Payload) < 9 {
		return
	}

	// Check if this is a video PES packet
	if pkt.PID != p.videoPID || p.videoPID == 0 {
		return
	}

	// Find PES payload start (after PES header)
	pesHeaderLength := int(pkt.Payload[8])
	pesPayloadStart := 9 + pesHeaderLength

	if pesPayloadStart >= len(pkt.Payload) {
		return
	}

	pesPayload := pkt.Payload[pesPayloadStart:]

	parameterSets := p.extractParameterSetsFromBitstream(pesPayload, p.videoStreamType)

	if len(parameterSets) > 0 {
		extractor(parameterSets, p.videoStreamType)
	}
}

// assemblePESPacket handles PES packet assembly for parameter set extraction
func (p *Parser) assemblePESPacket(pkt *Packet, extractor ParameterSetExtractor) {
	if pkt.PID != p.videoPID || p.videoPID == 0 {
		return
	}

	// Initialize buffer for this PID if needed
	if !p.pesStarted[pkt.PID] {
		return // Only process if we've seen the start
	}

	// Append payload to buffer
	if existingBuffer, exists := p.pesBuffer[pkt.PID]; exists {
		p.pesBuffer[pkt.PID] = append(existingBuffer, pkt.Payload...)
	}

	// For video streams, we can extract parameter sets from partial data
	// as they typically appear early in the PES packet
	if len(p.pesBuffer[pkt.PID]) > 1024 { // Process if we have enough data
		parameterSets := p.extractParameterSetsFromBitstream(p.pesBuffer[pkt.PID], p.videoStreamType)
		if len(parameterSets) > 0 {
			extractor(parameterSets, p.videoStreamType)
		}
	}
}

// extractParameterSetsFromBitstream extracts parameter sets from raw bitstream data
func (p *Parser) extractParameterSetsFromBitstream(data []byte, streamType uint8) [][]byte {
	var parameterSets [][]byte

	// Look for NAL unit start codes (0x00 0x00 0x01 or 0x00 0x00 0x00 0x01)
	for i := 0; i < len(data)-4; i++ {
		// Check for start code
		var nalStart int
		if data[i] == 0x00 && data[i+1] == 0x00 && data[i+2] == 0x01 {
			nalStart = i + 3
		} else if data[i] == 0x00 && data[i+1] == 0x00 && data[i+2] == 0x00 && data[i+3] == 0x01 {
			nalStart = i + 4
			i++ // Skip extra byte
		} else {
			continue
		}

		if nalStart >= len(data) {
			break
		}

		// Extract NAL unit type
		var nalType uint8
		switch streamType {
		case 0x1B: // H.264
			nalType = data[nalStart] & 0x1F
		case 0x24: // HEVC
			nalType = (data[nalStart] >> 1) & 0x3F
		default:
			continue
		}

		// Check if this is a parameter set NAL unit
		isParameterSet := false
		switch streamType {
		case 0x1B: // H.264
			isParameterSet = (nalType == 7 || nalType == 8) // SPS or PPS
		case 0x24: // HEVC
			isParameterSet = (nalType == 32 || nalType == 33 || nalType == 34) // VPS, SPS, or PPS
		}

		if !isParameterSet {
			continue
		}

		// Find the end of this NAL unit (next start code or end of data)
		nalEnd := len(data)
		for j := nalStart + 1; j < len(data)-3; j++ {
			if data[j] == 0x00 && data[j+1] == 0x00 && data[j+2] == 0x01 {
				nalEnd = j
				break
			}
			if j < len(data)-4 && data[j] == 0x00 && data[j+1] == 0x00 && data[j+2] == 0x00 && data[j+3] == 0x01 {
				nalEnd = j
				break
			}
		}

		// Extract NAL unit with start code
		nalStartPos := nalStart - 4
		if nalStartPos < 0 || (nalStartPos+2 < len(data) && data[nalStartPos+2] == 0x01) {
			nalStartPos = nalStart - 3
		}

		// Ensure we don't go negative
		if nalStartPos < 0 {
			nalStartPos = 0
		}

		nalUnit := make([]byte, nalEnd-nalStartPos)
		copy(nalUnit, data[nalStartPos:nalEnd])
		parameterSets = append(parameterSets, nalUnit)

		// Jump to end of this NAL unit
		i = nalEnd - 1
	}

	return parameterSets
}

// AddParameterSetsFromPMT adds parameter sets extracted from PMT
func (p *Parser) AddParameterSetsFromPMT(extractor ParameterSetExtractor) {
	// This method can be called to re-process the PMT with the extractor
	// if PMT was parsed before extractor was available
	if p.pmtParsed {
		p.pmtParsed = false // Reset to allow re-parsing
	}
}
