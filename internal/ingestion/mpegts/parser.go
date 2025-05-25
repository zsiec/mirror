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

		// Extract PTS/DTS from PES packets
		if pkt.PayloadStart && pkt.PayloadExists {
			if err := p.parsePESHeader(pkt); err == nil {
				packets = append(packets, pkt)
			}
		} else if pkt.PayloadExists {
			// Continuation of PES packet
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
