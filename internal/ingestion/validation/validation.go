package validation

import (
	"fmt"

	"github.com/zsiec/mirror/internal/ingestion/security"
)

// PacketValidator provides validation for various packet types
type PacketValidator struct {
	maxPacketSize   int
	maxPayloadSize  int
	syncByteEnabled bool
}

// NewPacketValidator creates a new packet validator with default settings
func NewPacketValidator() *PacketValidator {
	return &PacketValidator{
		maxPacketSize:   65536, // 64KB default
		maxPayloadSize:  65536,
		syncByteEnabled: true,
	}
}

// ValidateMPEGTSPacket validates an MPEG-TS packet
func (v *PacketValidator) ValidateMPEGTSPacket(data []byte) error {
	const tsPacketSize = 188

	if len(data) != tsPacketSize {
		return fmt.Errorf("invalid MPEG-TS packet size: got %d, expected %d", len(data), tsPacketSize)
	}

	if v.syncByteEnabled && data[0] != 0x47 {
		return fmt.Errorf("invalid MPEG-TS sync byte: got 0x%02X, expected 0x47", data[0])
	}

	return nil
}

// ValidateRTPPacket validates an RTP packet
func (v *PacketValidator) ValidateRTPPacket(data []byte) error {
	const minRTPHeaderSize = 12

	if len(data) < minRTPHeaderSize {
		return fmt.Errorf("RTP packet too small: got %d bytes, minimum %d", len(data), minRTPHeaderSize)
	}

	if len(data) > v.maxPacketSize {
		return fmt.Errorf("RTP packet too large: got %d bytes, maximum %d", len(data), v.maxPacketSize)
	}

	// Check RTP version (bits 6-7 of first byte should be 2)
	version := (data[0] >> 6) & 0x03
	if version != 2 {
		return fmt.Errorf("invalid RTP version: got %d, expected 2", version)
	}

	return nil
}

// ValidateNALUnit validates a NAL unit
func (v *PacketValidator) ValidateNALUnit(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty NAL unit")
	}

	if len(data) > security.MaxNALUnitSize {
		return fmt.Errorf("NAL unit too large: got %d bytes, maximum %d", len(data), security.MaxNALUnitSize)
	}

	// Check forbidden_zero_bit
	if (data[0] & 0x80) != 0 {
		return fmt.Errorf("NAL unit has forbidden_zero_bit set")
	}

	return nil
}

// ValidateFrame validates a complete frame
func (v *PacketValidator) ValidateFrame(data []byte, nalUnits int) error {
	if len(data) == 0 {
		return fmt.Errorf("empty frame")
	}

	if len(data) > security.MaxFrameSize {
		return fmt.Errorf("frame too large: got %d bytes, maximum %d", len(data), security.MaxFrameSize)
	}

	if nalUnits > security.MaxNALUnitsPerFrame {
		return fmt.Errorf("too many NAL units in frame: got %d, maximum %d", nalUnits, security.MaxNALUnitsPerFrame)
	}

	return nil
}

// BoundaryValidator provides boundary checking utilities
type BoundaryValidator struct{}

// NewBoundaryValidator creates a new boundary validator
func NewBoundaryValidator() *BoundaryValidator {
	return &BoundaryValidator{}
}

// ValidateBufferAccess checks if a buffer access is within bounds
func (b *BoundaryValidator) ValidateBufferAccess(buffer []byte, offset, length int) error {
	if offset < 0 {
		return fmt.Errorf("negative offset: %d", offset)
	}

	if length < 0 {
		return fmt.Errorf("negative length: %d", length)
	}

	if offset > len(buffer) {
		return fmt.Errorf("offset %d exceeds buffer size %d", offset, len(buffer))
	}

	if offset+length > len(buffer) {
		return fmt.Errorf("access [%d:%d] exceeds buffer size %d", offset, offset+length, len(buffer))
	}

	return nil
}

// ValidateStartCode checks if there's a valid start code at the given position
func (b *BoundaryValidator) ValidateStartCode(data []byte, pos int) (int, error) {
	// Need at least 3 bytes for start code
	if pos+3 > len(data) {
		return 0, fmt.Errorf("insufficient bytes for start code at position %d", pos)
	}

	// Check for 3-byte start code (0x00 0x00 0x01)
	if data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 1 {
		return 3, nil
	}

	// Check for 4-byte start code (0x00 0x00 0x00 0x01)
	if pos+4 <= len(data) && data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 0 && data[pos+3] == 1 {
		return 4, nil
	}

	return 0, fmt.Errorf("no start code found at position %d", pos)
}

// SizeValidator provides size validation utilities
type SizeValidator struct {
	limits map[string]int64
}

// NewSizeValidator creates a new size validator with default limits
func NewSizeValidator() *SizeValidator {
	return &SizeValidator{
		limits: map[string]int64{
			"packet":  65536,                   // 64KB
			"frame":   security.MaxFrameSize,   // 50MB
			"nalunit": security.MaxNALUnitSize, // 10MB
			"buffer":  104857600,               // 100MB
			"gop":     209715200,               // 200MB
		},
	}
}

// SetLimit sets a size limit for a given type
func (s *SizeValidator) SetLimit(limitType string, size int64) {
	s.limits[limitType] = size
}

// ValidateSize checks if a size is within limits for a given type
func (s *SizeValidator) ValidateSize(sizeType string, size int64) error {
	limit, ok := s.limits[sizeType]
	if !ok {
		return fmt.Errorf("unknown size type: %s", sizeType)
	}

	if size < 0 {
		return fmt.Errorf("negative size for %s: %d", sizeType, size)
	}

	if size > limit {
		return fmt.Errorf("%s size exceeds limit: got %d, maximum %d", sizeType, size, limit)
	}

	return nil
}

// TimestampValidator provides timestamp validation
type TimestampValidator struct {
	maxPTS int64
	maxDTS int64
}

// NewTimestampValidator creates a new timestamp validator
func NewTimestampValidator() *TimestampValidator {
	return &TimestampValidator{
		maxPTS: 1 << 33, // 33-bit PTS
		maxDTS: 1 << 33, // 33-bit DTS
	}
}

// ValidatePTS validates a presentation timestamp
func (t *TimestampValidator) ValidatePTS(pts int64) error {
	if pts < 0 {
		return fmt.Errorf("negative PTS: %d", pts)
	}

	if pts >= t.maxPTS {
		return fmt.Errorf("PTS exceeds 33-bit range: %d", pts)
	}

	return nil
}

// ValidateDTS validates a decoding timestamp
func (t *TimestampValidator) ValidateDTS(dts int64) error {
	if dts < 0 {
		return fmt.Errorf("negative DTS: %d", dts)
	}

	if dts >= t.maxDTS {
		return fmt.Errorf("DTS exceeds 33-bit range: %d", dts)
	}

	return nil
}

// ValidatePTSDTSOrder validates that PTS >= DTS
func (t *TimestampValidator) ValidatePTSDTSOrder(pts, dts int64) error {
	if err := t.ValidatePTS(pts); err != nil {
		return err
	}

	if err := t.ValidateDTS(dts); err != nil {
		return err
	}

	if pts < dts {
		return fmt.Errorf("PTS (%d) is less than DTS (%d)", pts, dts)
	}

	return nil
}
