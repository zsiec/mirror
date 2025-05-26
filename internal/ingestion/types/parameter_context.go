package types

import (
	"fmt"
	"sync"
	"time"
)

const (
	MaxParameterSetsPerSession  = 1000
	ParameterSetCleanupInterval = 1 * time.Hour
	MaxParameterSetAge          = 24 * time.Hour
	
	// Error code thresholds for CopyParameterSetsFrom return values
	// Negative return values indicate different error severities:
	ErrorCodeCriticalFailure = -1000 - MaxParameterSetsPerSession // -2000: Emergency cleanup failed completely
	ErrorCodeMemoryPressure  = -100 - MaxParameterSetsPerSession  // -1100: Emergency cleanup had no effect
	ErrorCodeTruncated       = -1                                 // -1 to -99: Copy truncated due to limits
)

// ParameterSetContext manages H.264/HEVC parameter sets with proper ID tracking
// This handles the complexity of live streams where parameter sets can change
type ParameterSetContext struct {
	mu    sync.RWMutex
	codec CodecType

	// H.264 parameter sets indexed by their actual IDs
	spsMap map[uint8]*ParameterSet // sps_id -> SPS
	ppsMap map[uint8]*PPSContext   // pps_id -> PPS with SPS reference

	// HEVC parameter sets
	vpsMap     map[uint8]*ParameterSet // vps_id -> VPS
	hevcSpsMap map[uint8]*ParameterSet // sps_id -> SPS
	hevcPpsMap map[uint8]*ParameterSet // pps_id -> PPS

	// Encoder session management for context tracking
	sessionManager *EncoderSessionManager

	// Tracking and observability
	lastUpdated time.Time
	totalSets   int
	streamID    string

	// Session lifecycle tracking for enhanced diagnostics
	sessionStartTime       time.Time
	totalFramesProcessed   uint64
	lastParameterSetUpdate time.Time

	// Cleanup management for long-running streams
	lastCleanup    time.Time
	cleanupEnabled bool
}

// ParameterSet represents a parsed parameter set with metadata
type ParameterSet struct {
	ID          uint8     `json:"id"`
	Data        []byte    `json:"-"` // Raw NAL unit data with header
	ParsedAt    time.Time `json:"parsed_at"`
	Size        int       `json:"size"`
	Valid       bool      `json:"valid"`
	ErrorReason string    `json:"error_reason,omitempty"`

	// H.264 SPS specific fields
	ProfileIDC *uint8 `json:"profile_idc,omitempty"`
	LevelIDC   *uint8 `json:"level_idc,omitempty"`
	Width      *int   `json:"width,omitempty"`
	Height     *int   `json:"height,omitempty"`
}

// PPSContext represents a PPS with its SPS dependency
type PPSContext struct {
	*ParameterSet
	ReferencedSPSID uint8 `json:"referenced_sps_id"`
}

// FrameDecodingRequirements represents what parameter sets a frame needs
type FrameDecodingRequirements struct {
	RequiredPPSID uint8 `json:"required_pps_id"`
	RequiredSPSID uint8 `json:"required_sps_id"`
	SliceType     uint8 `json:"slice_type"`
	IsIDR         bool  `json:"is_idr"`
}

// NewParameterSetContext creates a new parameter set context manager
func NewParameterSetContext(codec CodecType, streamID string) *ParameterSetContext {
	sessionConfig := CacheConfig{
		MaxParameterSets: 100,
		ParameterSetTTL:  5 * time.Minute,
		MaxSessions:      10,
	}

	now := time.Now()
	return &ParameterSetContext{
		codec:                  codec,
		streamID:               streamID,
		spsMap:                 make(map[uint8]*ParameterSet),
		ppsMap:                 make(map[uint8]*PPSContext),
		vpsMap:                 make(map[uint8]*ParameterSet),
		hevcSpsMap:             make(map[uint8]*ParameterSet),
		hevcPpsMap:             make(map[uint8]*ParameterSet),
		sessionManager:         NewEncoderSessionManager(streamID, sessionConfig),
		lastUpdated:            now,
		sessionStartTime:       now,
		totalFramesProcessed:   0,
		lastParameterSetUpdate: now,
		lastCleanup:            now,
		cleanupEnabled:         true,
	}
}

// AddSPS adds an H.264 SPS parameter set
func (ctx *ParameterSetContext) AddSPS(data []byte) error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if ctx.codec != CodecH264 {
		return fmt.Errorf("cannot add H.264 SPS to %s context", ctx.codec)
	}

	sps, err := ctx.parseSPS(data)
	if err != nil {
		return fmt.Errorf("invalid SPS: %w", err)
	}

	// Check for encoder session changes by detecting SPS changes
	existingSPS, exists := ctx.spsMap[sps.ID]
	sessionChanged := false

	if exists {
		// Compare critical SPS fields for session change detection
		if existingSPS.ProfileIDC != nil && sps.ProfileIDC != nil && *existingSPS.ProfileIDC != *sps.ProfileIDC {
			sessionChanged = true
		}
		if existingSPS.LevelIDC != nil && sps.LevelIDC != nil && *existingSPS.LevelIDC != *sps.LevelIDC {
			sessionChanged = true
		}
		if existingSPS.Width != nil && sps.Width != nil && *existingSPS.Width != *sps.Width {
			sessionChanged = true
		}
		if existingSPS.Height != nil && sps.Height != nil && *existingSPS.Height != *sps.Height {
			sessionChanged = true
		}
	} else {
		sessionChanged = true
	}

	ctx.spsMap[sps.ID] = sps
	now := time.Now()
	ctx.lastUpdated = now
	ctx.lastParameterSetUpdate = now
	ctx.totalSets++

	// Notify session manager of encoder context change
	if sessionChanged && ctx.sessionManager != nil {
		ctx.sessionManager.OnParameterSetChange("sps", sps.ID, data)
	}

	// Check if cleanup is needed for memory management
	ctx.checkAndPerformCleanup()

	return nil
}

// AddPPS adds an H.264 PPS parameter set
func (ctx *ParameterSetContext) AddPPS(data []byte) error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if ctx.codec != CodecH264 {
		return fmt.Errorf("cannot add H.264 PPS to %s context", ctx.codec)
	}

	pps, err := ctx.parsePPS(data)
	if err != nil {
		return fmt.Errorf("invalid PPS: %w", err)
	}

	ctx.ppsMap[pps.ID] = pps
	now := time.Now()
	ctx.lastUpdated = now
	ctx.lastParameterSetUpdate = now
	ctx.totalSets++

	// Check if cleanup is needed for memory management
	ctx.checkAndPerformCleanup()

	return nil
}

// GetDecodingRequirements analyzes a frame to determine its parameter set needs
func (ctx *ParameterSetContext) GetDecodingRequirements(frame *VideoFrame) (*FrameDecodingRequirements, error) {
	if len(frame.NALUnits) == 0 {
		return nil, fmt.Errorf("frame has no NAL units")
	}

	// For H.264, analyze the first slice NAL unit
	for _, nalUnit := range frame.NALUnits {
		nalType := nalUnit.Type
		if nalType == 0 && len(nalUnit.Data) > 0 {
			nalType = nalUnit.Data[0] & 0x1F
		}

		// Check if this is a slice NAL unit
		if nalType >= 1 && nalType <= 5 {
			return ctx.parseSliceHeader(nalUnit.Data, nalType == 5)
		}
	}

	return nil, fmt.Errorf("no slice NAL units found in frame")
}

// CanDecodeFrame checks if we have all required parameter sets for a frame
func (ctx *ParameterSetContext) CanDecodeFrame(frame *VideoFrame) (bool, string) {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	return ctx.canDecodeFrameWithRequirements(frame, nil)
}

// canDecodeFrameWithRequirements is an internal helper that can reuse existing requirements
func (ctx *ParameterSetContext) canDecodeFrameWithRequirements(frame *VideoFrame, cachedRequirements *FrameDecodingRequirements) (bool, string) {
	var requirements *FrameDecodingRequirements
	var err error

	if cachedRequirements != nil {
		requirements = cachedRequirements
	} else {
		requirements, err = ctx.GetDecodingRequirements(frame)
		if err != nil {
			return false, fmt.Sprintf("cannot analyze frame requirements: %v", err)
		}
	}

	// Check if we have the required PPS
	pps, hasPPS := ctx.ppsMap[requirements.RequiredPPSID]
	if !hasPPS {
		return false, fmt.Sprintf("missing PPS %d", requirements.RequiredPPSID)
	}

	// Check if we have the SPS that the PPS references
	sps, hasSPS := ctx.spsMap[pps.ReferencedSPSID]
	if !hasSPS {
		return false, fmt.Sprintf("missing SPS %d (referenced by PPS %d)", pps.ReferencedSPSID, requirements.RequiredPPSID)
	}

	// Validate parameter sets are not corrupted
	if !pps.Valid {
		return false, fmt.Sprintf("PPS %d is invalid: %s", requirements.RequiredPPSID, pps.ErrorReason)
	}
	if !sps.Valid {
		return false, fmt.Sprintf("SPS %d is invalid: %s", pps.ReferencedSPSID, sps.ErrorReason)
	}

	return true, ""
}

// GetSessionManager returns the encoder session manager for external access
func (ctx *ParameterSetContext) GetSessionManager() *EncoderSessionManager {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	return ctx.sessionManager
}

// GenerateDecodableStream creates a properly formatted H.264 stream for a frame
func (ctx *ParameterSetContext) GenerateDecodableStream(frame *VideoFrame) ([]byte, error) {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	// Get requirements once to avoid duplicate parsing
	requirements, err := ctx.GetDecodingRequirements(frame)
	if err != nil {
		return nil, err
	}

	canDecode, reason := ctx.canDecodeFrameWithRequirements(frame, requirements)
	if !canDecode {
		return nil, fmt.Errorf("cannot decode frame: %s", reason)
	}

	// Build the stream: SPS + PPS + Frame NAL units
	var stream []byte
	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	// Add SPS
	pps := ctx.ppsMap[requirements.RequiredPPSID]
	sps := ctx.spsMap[pps.ReferencedSPSID]

	stream = append(stream, startCode...)
	stream = append(stream, sps.Data...)

	// Add PPS
	stream = append(stream, startCode...)
	stream = append(stream, pps.Data...)

	// Add frame NAL units
	for _, nalUnit := range frame.NALUnits {
		stream = append(stream, startCode...)

		// Construct NAL unit with proper header
		nalType := nalUnit.Type
		if nalType == 0 && len(nalUnit.Data) > 0 {
			nalType = nalUnit.Data[0] & 0x1F
		}

		// For slice NAL units, add proper header
		if nalType >= 1 && nalType <= 5 {
			nalHeader := byte(nalType) | 0x60 // F=0, NRI=3, Type=nalType
			stream = append(stream, nalHeader)
			stream = append(stream, nalUnit.Data...)
		} else {
			// For other NAL types, assume data includes header
			stream = append(stream, nalUnit.Data...)
		}
	}

	return stream, nil
}

// GenerateBestEffortStream generates a decodable stream using the best available parameter sets
// This is a fallback method that tries to create a usable stream even with imperfect parameter matching
func (ctx *ParameterSetContext) GenerateBestEffortStream(frame *VideoFrame) ([]byte, error) {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	if len(frame.NALUnits) == 0 {
		return nil, fmt.Errorf("frame has no NAL units")
	}

	// Use the most recent/compatible parameter sets we have
	var bestSPS *ParameterSet
	var bestPPS *PPSContext

	// Find the best available SPS (prefer the most recent one)
	for _, sps := range ctx.spsMap {
		if sps.Valid {
			if bestSPS == nil || sps.ParsedAt.After(bestSPS.ParsedAt) {
				bestSPS = sps
			}
		}
	}

	// Find the best available PPS that references our chosen SPS
	if bestSPS != nil {
		for _, pps := range ctx.ppsMap {
			if pps.Valid && pps.ReferencedSPSID == bestSPS.ID {
				if bestPPS == nil || pps.ParsedAt.After(bestPPS.ParsedAt) {
					bestPPS = pps
				}
			}
		}
	}

	// If we don't have a matching PPS for our SPS, find the best compatible pair
	if bestPPS == nil {
		// Strategy: Find any valid SPS-PPS pair that work together
		for _, sps := range ctx.spsMap {
			if !sps.Valid {
				continue
			}
			for _, pps := range ctx.ppsMap {
				if pps.Valid && pps.ReferencedSPSID == sps.ID {
					// Found a compatible pair - prefer more recent ones
					if bestSPS == nil || sps.ParsedAt.After(bestSPS.ParsedAt) {
						bestSPS = sps
						bestPPS = pps
					}
					break
				}
			}
		}

		// If still no compatible pair, use the newest SPS and any valid PPS
		if bestPPS == nil {
			for _, pps := range ctx.ppsMap {
				if pps.Valid {
					if bestPPS == nil || pps.ParsedAt.After(bestPPS.ParsedAt) {
						bestPPS = pps
					}
				}
			}
		}
	}

	if bestSPS == nil || bestPPS == nil {
		return nil, fmt.Errorf("no valid parameter sets available for best-effort stream")
	}

	// Build the stream: SPS + PPS + Frame NAL units
	var stream []byte
	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	// **Strategy: Use the most recent parameter sets and hope they're compatible**
	// This is a best-effort approach - the parameter sets may not perfectly match
	// but should give FFmpeg enough information to decode the frame

	// Add SPS
	stream = append(stream, startCode...)
	stream = append(stream, bestSPS.Data...)

	// Add PPS
	stream = append(stream, startCode...)
	stream = append(stream, bestPPS.Data...)

	// **H.264 EXPERT FIX: Proper parameter set remapping strategy**
	// Get requirements once to avoid duplicate parsing
	requirements, err := ctx.GetDecodingRequirements(frame)
	if err != nil {
		// Fallback: send all available parameter sets to give FFmpeg the best chance
		var stream []byte
		startCode := []byte{0x00, 0x00, 0x00, 0x01}

		// Add all available SPS
		for _, sps := range ctx.spsMap {
			if sps.Valid {
				stream = append(stream, startCode...)
				stream = append(stream, sps.Data...)
			}
		}

		// Add all available PPS
		for _, pps := range ctx.ppsMap {
			if pps.Valid {
				stream = append(stream, startCode...)
				stream = append(stream, pps.Data...)
			}
		}

		// Add frame NAL units
		for _, nalUnit := range frame.NALUnits {
			stream = append(stream, startCode...)
			stream = append(stream, nalUnit.Data...)
		}

		return stream, nil
	}

	// Strategy 1: Try to find compatible parameter sets first
	compatibleSPS, compatiblePPS := ctx.findCompatibleParameterSetsWithRequirements(requirements)
	if compatibleSPS != nil && compatiblePPS != nil {
		// Use compatible parameter sets
		stream = append(stream, startCode...)
		stream = append(stream, compatibleSPS.Data...)
		stream = append(stream, startCode...)
		stream = append(stream, compatiblePPS.Data...)

		// Add frame NAL units unchanged (they already reference correct IDs)
		for _, nalUnit := range frame.NALUnits {
			stream = append(stream, startCode...)
			stream = append(stream, nalUnit.Data...)
		}
		return stream, nil
	}

	// Strategy 2: Use best available parameter sets with ID remapping
	// Create remapped parameter sets with consistent IDs
	remappedSPS := ctx.createRemappedSPS(bestSPS, 0)    // Force SPS ID = 0
	remappedPPS := ctx.createRemappedPPS(bestPPS, 0, 0) // Force PPS ID = 0, references SPS ID = 0

	if remappedSPS != nil && remappedPPS != nil {
		stream = append(stream, startCode...)
		stream = append(stream, remappedSPS...)
		stream = append(stream, startCode...)
		stream = append(stream, remappedPPS...)

		// Remap slice headers to reference PPS ID 0
		for _, nalUnit := range frame.NALUnits {
			nalType := nalUnit.Type
			if nalType == 0 && len(nalUnit.Data) > 0 {
				nalType = nalUnit.Data[0] & 0x1F
			}

			if nalType >= 1 && nalType <= 5 { // Slice NAL units
				remappedSlice := ctx.remapSliceHeaderPPSID(nalUnit.Data, 0)
				stream = append(stream, startCode...)
				stream = append(stream, remappedSlice...)
			} else {
				// Non-slice NAL units (AUD, SEI, etc.) - copy unchanged
				stream = append(stream, startCode...)
				stream = append(stream, nalUnit.Data...)
			}
		}
		return stream, nil
	}

	// Strategy 3: Original fallback (send everything and hope)
	stream = append(stream, startCode...)
	stream = append(stream, bestSPS.Data...)
	stream = append(stream, startCode...)
	stream = append(stream, bestPPS.Data...)

	// Add frame NAL units
	for _, nalUnit := range frame.NALUnits {
		stream = append(stream, startCode...)
		stream = append(stream, nalUnit.Data...)
	}

	return stream, nil
}

// findCompatibleParameterSets finds parameter sets that match the frame's requirements exactly
func (ctx *ParameterSetContext) findCompatibleParameterSets(frame *VideoFrame) (*ParameterSet, *PPSContext) {
	requirements, err := ctx.GetDecodingRequirements(frame)
	if err != nil {
		return nil, nil
	}

	return ctx.findCompatibleParameterSetsWithRequirements(requirements)
}

// findCompatibleParameterSetsWithRequirements is an internal helper that reuses existing requirements
func (ctx *ParameterSetContext) findCompatibleParameterSetsWithRequirements(requirements *FrameDecodingRequirements) (*ParameterSet, *PPSContext) {
	// Check if we have the exact PPS the frame needs
	pps, hasPPS := ctx.ppsMap[requirements.RequiredPPSID]
	if !hasPPS || !pps.Valid {
		return nil, nil
	}

	// Check if we have the SPS that this PPS references
	sps, hasSPS := ctx.spsMap[pps.ReferencedSPSID]
	if !hasSPS || !sps.Valid {
		return nil, nil
	}

	return sps, pps
}

// createRemappedSPS creates a new SPS with the specified ID
func (ctx *ParameterSetContext) createRemappedSPS(originalSPS *ParameterSet, newID uint8) []byte {
	if originalSPS == nil || len(originalSPS.Data) < 5 {
		return nil
	}

	// Clone the original SPS data
	newSPS := make([]byte, len(originalSPS.Data))
	copy(newSPS, originalSPS.Data)

	// Skip NAL header (0x67), parse and rewrite SPS ID
	if newSPS[0] != 0x67 {
		return nil // Invalid SPS NAL header
	}

	// Parse SPS payload to locate and modify seq_parameter_set_id
	payload := newSPS[1:] // Skip NAL header
	if len(payload) < 4 {
		return nil
	}

	br := NewBitReader(payload)

	// Skip profile_idc (8 bits)
	if _, err := br.ReadBits(8); err != nil {
		return originalSPS.Data // Fallback to original
	}

	// Skip constraint flags (8 bits)
	if _, err := br.ReadBits(8); err != nil {
		return originalSPS.Data
	}

	// Skip level_idc (8 bits)
	if _, err := br.ReadBits(8); err != nil {
		return originalSPS.Data
	}

	// Now we're at seq_parameter_set_id position
	startBitPos := br.GetBitPosition()

	// Read original SPS ID to know how many bits to replace
	originalID, err := br.ReadUE()
	if err != nil || originalID > 31 {
		return originalSPS.Data // Fallback
	}

	endBitPos := br.GetBitPosition()

	// Create new bitstream with remapped ID
	bw := NewBitWriter()

	// Copy everything before SPS ID
	originalBr := NewBitReader(payload)
	for i := 0; i < startBitPos; i++ {
		bit, err := originalBr.ReadBit()
		if err != nil {
			return originalSPS.Data
		}
		bw.WriteBit(bit)
	}

	// Write new SPS ID
	bw.WriteUE(uint32(newID))

	// Copy everything after SPS ID
	originalBr.SeekToBit(endBitPos)
	for originalBr.HasMoreBits() {
		bit, err := originalBr.ReadBit()
		if err != nil {
			break
		}
		bw.WriteBit(bit)
	}

	// Reconstruct full NAL unit
	newPayload := bw.GetBytes()
	result := make([]byte, 1+len(newPayload))
	result[0] = 0x67 // SPS NAL header
	copy(result[1:], newPayload)

	return result
}

// createRemappedPPS creates a new PPS with the specified ID and SPS reference
func (ctx *ParameterSetContext) createRemappedPPS(originalPPS *PPSContext, newID uint8, referencedSPSID uint8) []byte {
	if originalPPS == nil || len(originalPPS.Data) < 3 {
		return nil
	}

	// Clone the original PPS data
	newPPS := make([]byte, len(originalPPS.Data))
	copy(newPPS, originalPPS.Data)

	// Skip NAL header (0x68), parse and rewrite PPS ID and SPS reference
	if newPPS[0] != 0x68 {
		return nil // Invalid PPS NAL header
	}

	// Parse PPS payload to locate and modify pic_parameter_set_id and seq_parameter_set_id
	payload := newPPS[1:] // Skip NAL header
	if len(payload) < 2 {
		return nil
	}

	br := NewBitReader(payload)

	// Read original PPS ID position
	originalPPSID, err := br.ReadUE()
	if err != nil || originalPPSID > 255 {
		return originalPPS.Data // Fallback
	}

	// Read original SPS ID position
	originalSPSID, err := br.ReadUE()
	if err != nil || originalSPSID > 31 {
		return originalPPS.Data // Fallback
	}
	spsEndBit := br.GetBitPosition()

	// Create new bitstream with remapped IDs
	bw := NewBitWriter()

	// Copy everything before PPS ID (none in this case)

	// Write new PPS ID
	bw.WriteUE(uint32(newID))

	// Write new SPS ID reference
	bw.WriteUE(uint32(referencedSPSID))

	// Copy everything after SPS ID reference
	originalBr := NewBitReader(payload)
	originalBr.SeekToBit(spsEndBit)
	for originalBr.HasMoreBits() {
		bit, err := originalBr.ReadBit()
		if err != nil {
			break
		}
		bw.WriteBit(bit)
	}

	// Reconstruct full NAL unit
	newPayload := bw.GetBytes()
	result := make([]byte, 1+len(newPayload))
	result[0] = 0x68 // PPS NAL header
	copy(result[1:], newPayload)

	return result
}

// remapSliceHeaderPPSID creates a new slice header with the specified PPS ID
func (ctx *ParameterSetContext) remapSliceHeaderPPSID(originalSlice []byte, newPPSID uint8) []byte {
	if len(originalSlice) < 3 {
		return originalSlice
	}

	// Clone the original slice data
	newSlice := make([]byte, len(originalSlice))
	copy(newSlice, originalSlice)

	// Parse slice header to locate and modify pic_parameter_set_id
	nalType := newSlice[0] & 0x1F
	if nalType < 1 || nalType > 5 {
		return originalSlice // Not a slice NAL unit
	}

	// Parse slice header payload (skip NAL header)
	payload := newSlice[1:]
	if len(payload) < 2 {
		return originalSlice
	}

	br := NewBitReader(payload)

	// Read first_mb_in_slice
	_, err := br.ReadUE()
	if err != nil {
		return originalSlice // Fallback
	}

	// Read slice_type
	_, err = br.ReadUE()
	if err != nil {
		return originalSlice // Fallback
	}

	// Read pic_parameter_set_id (this is what we want to remap)
	ppsIDStartBit := br.GetBitPosition()
	originalPPSID, err := br.ReadUE()
	if err != nil || originalPPSID > 255 {
		return originalSlice // Fallback
	}
	ppsIDEndBit := br.GetBitPosition()

	// Create new bitstream with remapped PPS ID
	bw := NewBitWriter()

	// Copy everything before PPS ID
	originalBr := NewBitReader(payload)
	for i := 0; i < ppsIDStartBit; i++ {
		bit, err := originalBr.ReadBit()
		if err != nil {
			return originalSlice
		}
		bw.WriteBit(bit)
	}

	// Write new PPS ID
	bw.WriteUE(uint32(newPPSID))

	// Copy everything after PPS ID
	originalBr.SeekToBit(ppsIDEndBit)
	for originalBr.HasMoreBits() {
		bit, err := originalBr.ReadBit()
		if err != nil {
			break
		}
		bw.WriteBit(bit)
	}

	// Reconstruct full NAL unit
	newPayload := bw.GetBytes()
	result := make([]byte, 1+len(newPayload))
	result[0] = newSlice[0] // Preserve original NAL header
	copy(result[1:], newPayload)

	return result
}

// GetStatistics returns context statistics for monitoring
func (ctx *ParameterSetContext) GetStatistics() map[string]interface{} {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	stats := map[string]interface{}{
		"stream_id":    ctx.streamID,
		"codec":        ctx.codec.String(),
		"last_updated": ctx.lastUpdated,
		"total_sets":   ctx.totalSets,
		"sps_count":    len(ctx.spsMap),
		"pps_count":    len(ctx.ppsMap),
	}

	if ctx.codec == CodecH264 {
		// Add H.264 specific stats
		validSPS := 0
		validPPS := 0

		for _, sps := range ctx.spsMap {
			if sps.Valid {
				validSPS++
			}
		}

		for _, pps := range ctx.ppsMap {
			if pps.Valid {
				validPPS++
			}
		}

		stats["valid_sps_count"] = validSPS
		stats["valid_pps_count"] = validPPS
	}

	return stats
}

// GetSessionStatistics returns comprehensive session statistics for production monitoring
func (ctx *ParameterSetContext) GetSessionStatistics() map[string]interface{} {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	return map[string]interface{}{
		"session_duration_ms":    time.Since(ctx.sessionStartTime).Milliseconds(),
		"total_frames_processed": ctx.totalFramesProcessed,
		"sps_count":              len(ctx.spsMap),
		"pps_count":              len(ctx.ppsMap),
		"sps_ids":                ctx.getSPSIDs(),
		"pps_ids":                ctx.getPPSIDs(),
		"last_parameter_update":  ctx.lastParameterSetUpdate,
		"parameter_set_coverage": ctx.calculateCoverage(),
	}
}

// getSPSIDs returns all available SPS IDs
func (ctx *ParameterSetContext) getSPSIDs() []uint8 {
	ids := make([]uint8, 0, len(ctx.spsMap))
	for id := range ctx.spsMap {
		ids = append(ids, id)
	}
	return ids
}

// getPPSIDs returns all available PPS IDs
func (ctx *ParameterSetContext) getPPSIDs() []uint8 {
	ids := make([]uint8, 0, len(ctx.ppsMap))
	for id := range ctx.ppsMap {
		ids = append(ids, id)
	}
	return ids
}

// calculateCoverage determines parameter set coverage quality
func (ctx *ParameterSetContext) calculateCoverage() float64 {
	if len(ctx.spsMap) == 0 {
		return 0.0
	}

	// Calculate percentage of PPS that have valid SPS references
	validPairings := 0
	for _, pps := range ctx.ppsMap {
		if sps, exists := ctx.spsMap[pps.ReferencedSPSID]; exists && sps.Valid && pps.Valid {
			validPairings++
		}
	}

	if len(ctx.ppsMap) == 0 {
		return 0.5 // Have SPS but no PPS
	}

	return float64(validPairings) / float64(len(ctx.ppsMap))
}

// IncrementFrameCount increments the total frames processed counter
func (ctx *ParameterSetContext) IncrementFrameCount() {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.totalFramesProcessed++
}

// checkAndPerformCleanup checks if cleanup is needed and performs it (called with lock held)
func (ctx *ParameterSetContext) checkAndPerformCleanup() {
	if !ctx.cleanupEnabled {
		return
	}

	now := time.Now()

	// Check if it's time for cleanup
	if now.Sub(ctx.lastCleanup) < ParameterSetCleanupInterval {
		return
	}

	// Check if we have too many parameter sets
	totalSets := len(ctx.spsMap) + len(ctx.ppsMap) + len(ctx.vpsMap) + len(ctx.hevcSpsMap) + len(ctx.hevcPpsMap)

	if totalSets > MaxParameterSetsPerSession {
		ctx.performCleanup(now)
	} else {
		// Update last cleanup time even if no cleanup was needed
		ctx.lastCleanup = now
	}
}

// performCleanup removes old parameter sets for extremely long-running streams
func (ctx *ParameterSetContext) performCleanup(now time.Time) {
	cleaned := 0

	// Clean old SPS sets - keep only the most recent version of each ID
	for id, sps := range ctx.spsMap {
		if now.Sub(sps.ParsedAt) > MaxParameterSetAge {
			delete(ctx.spsMap, id)
			cleaned++
		}
	}

	// Clean old PPS sets - keep only the most recent version of each ID
	for id, pps := range ctx.ppsMap {
		if now.Sub(pps.ParsedAt) > MaxParameterSetAge {
			delete(ctx.ppsMap, id)
			cleaned++
		}
	}

	// Clean HEVC parameter sets if applicable
	if ctx.codec == CodecHEVC {
		for id, vps := range ctx.vpsMap {
			if now.Sub(vps.ParsedAt) > MaxParameterSetAge {
				delete(ctx.vpsMap, id)
				cleaned++
			}
		}

		for id, sps := range ctx.hevcSpsMap {
			if now.Sub(sps.ParsedAt) > MaxParameterSetAge {
				delete(ctx.hevcSpsMap, id)
				cleaned++
			}
		}

		for id, pps := range ctx.hevcPpsMap {
			if now.Sub(pps.ParsedAt) > MaxParameterSetAge {
				delete(ctx.hevcPpsMap, id)
				cleaned++
			}
		}
	}

	ctx.lastCleanup = now

	// Log cleanup activity
	if cleaned > 0 {
		// Note: We can't use logger here as we don't have access to it
		// This would be logged by the calling component
		ctx.totalSets -= cleaned
	}
}

// GetCleanupStats returns cleanup statistics for monitoring
func (ctx *ParameterSetContext) GetCleanupStats() map[string]interface{} {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	return map[string]interface{}{
		"cleanup_enabled":         ctx.cleanupEnabled,
		"last_cleanup":            ctx.lastCleanup,
		"next_cleanup_due":        ctx.lastCleanup.Add(ParameterSetCleanupInterval),
		"total_parameter_sets":    len(ctx.spsMap) + len(ctx.ppsMap) + len(ctx.vpsMap) + len(ctx.hevcSpsMap) + len(ctx.hevcPpsMap),
		"max_parameter_sets":      MaxParameterSetsPerSession,
		"cleanup_interval_hours":  ParameterSetCleanupInterval.Hours(),
		"max_parameter_age_hours": MaxParameterSetAge.Hours(),
	}
}

// CopyParameterSetsFrom copies all parameter sets from another context with bounds checking
// Returns negative value on critical errors that should be logged/alerted
func (ctx *ParameterSetContext) CopyParameterSetsFrom(sourceCtx *ParameterSetContext) int {
	if sourceCtx == nil {
		return 0
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	sourceCtx.mu.RLock()
	defer sourceCtx.mu.RUnlock()

	// Check current size and enforce hard limits
	currentTotal := len(ctx.spsMap) + len(ctx.ppsMap) + len(ctx.vpsMap) + len(ctx.hevcSpsMap) + len(ctx.hevcPpsMap)
	
	// CRITICAL: If we're over the limit, this is a serious memory leak issue
	if currentTotal >= MaxParameterSetsPerSession {
		// Try emergency cleanup once
		cleanedCount := ctx.performEmergencyCleanupUnsafe()
		currentTotal = len(ctx.spsMap) + len(ctx.ppsMap) + len(ctx.vpsMap) + len(ctx.hevcSpsMap) + len(ctx.hevcPpsMap)
		
		if currentTotal >= MaxParameterSetsPerSession {
			// CRITICAL ERROR: Emergency cleanup failed, system is in dangerous state
			return ErrorCodeCriticalFailure - currentTotal // Shows actual count beyond limit
		}
		
		// Partial recovery - log warning but continue with reduced capacity
		if cleanedCount == 0 {
			// Cleanup didn't free anything - this indicates a serious problem
			return ErrorCodeMemoryPressure - currentTotal // Shows we're still under pressure
		}
	}

	copiedCount := 0
	maxToCopy := MaxParameterSetsPerSession - currentTotal

	// Copy H.264 SPS with bounds checking
	for id, sps := range sourceCtx.spsMap {
		if copiedCount >= maxToCopy {
			// Hit our safety limit - return negative to signal truncation
			return -(copiedCount + 1) // Negative indicates partial copy due to limits
		}
		
		if sps.Valid {
			// Create a deep copy of the SPS
			spsCopy := &ParameterSet{
				ID:          sps.ID,
				Data:        make([]byte, len(sps.Data)),
				ParsedAt:    sps.ParsedAt,
				Size:        sps.Size,
				Valid:       sps.Valid,
				ErrorReason: sps.ErrorReason,
			}
			copy(spsCopy.Data, sps.Data)

			// Copy optional fields
			if sps.ProfileIDC != nil {
				profileCopy := *sps.ProfileIDC
				spsCopy.ProfileIDC = &profileCopy
			}
			if sps.LevelIDC != nil {
				levelCopy := *sps.LevelIDC
				spsCopy.LevelIDC = &levelCopy
			}
			if sps.Width != nil {
				widthCopy := *sps.Width
				spsCopy.Width = &widthCopy
			}
			if sps.Height != nil {
				heightCopy := *sps.Height
				spsCopy.Height = &heightCopy
			}

			ctx.spsMap[id] = spsCopy
			copiedCount++
		}
	}

	// Copy H.264 PPS with bounds checking
	for id, pps := range sourceCtx.ppsMap {
		if copiedCount >= maxToCopy {
			// Hit our safety limit - return negative to signal truncation
			return -(copiedCount + 1) // Negative indicates partial copy due to limits
		}
		
		if pps.Valid {
			// Create a deep copy of the PPS
			ppsCopy := &PPSContext{
				ParameterSet: &ParameterSet{
					ID:          pps.ID,
					Data:        make([]byte, len(pps.Data)),
					ParsedAt:    pps.ParsedAt,
					Size:        pps.Size,
					Valid:       pps.Valid,
					ErrorReason: pps.ErrorReason,
				},
				ReferencedSPSID: pps.ReferencedSPSID,
			}
			copy(ppsCopy.Data, pps.Data)

			ctx.ppsMap[id] = ppsCopy
			copiedCount++
		}
	}

	// Copy HEVC parameter sets if applicable
	if ctx.codec == CodecHEVC {
		// Copy VPS with bounds checking
		for id, vps := range sourceCtx.vpsMap {
			if copiedCount >= maxToCopy {
				return -(copiedCount + 1) // Truncated due to limits
			}
			
			if vps.Valid {
				vpsCopy := &ParameterSet{
					ID:          vps.ID,
					Data:        make([]byte, len(vps.Data)),
					ParsedAt:    vps.ParsedAt,
					Size:        vps.Size,
					Valid:       vps.Valid,
					ErrorReason: vps.ErrorReason,
				}
				copy(vpsCopy.Data, vps.Data)
				ctx.vpsMap[id] = vpsCopy
				copiedCount++
			}
		}

		// Copy HEVC SPS with bounds checking
		for id, sps := range sourceCtx.hevcSpsMap {
			if copiedCount >= maxToCopy {
				return -(copiedCount + 1) // Truncated due to limits
			}
			
			if sps.Valid {
				spsCopy := &ParameterSet{
					ID:          sps.ID,
					Data:        make([]byte, len(sps.Data)),
					ParsedAt:    sps.ParsedAt,
					Size:        sps.Size,
					Valid:       sps.Valid,
					ErrorReason: sps.ErrorReason,
				}
				copy(spsCopy.Data, sps.Data)
				ctx.hevcSpsMap[id] = spsCopy
				copiedCount++
			}
		}

		// Copy HEVC PPS with bounds checking
		for id, pps := range sourceCtx.hevcPpsMap {
			if copiedCount >= maxToCopy {
				return -(copiedCount + 1) // Truncated due to limits
			}
			
			if pps.Valid {
				ppsCopy := &ParameterSet{
					ID:          pps.ID,
					Data:        make([]byte, len(pps.Data)),
					ParsedAt:    pps.ParsedAt,
					Size:        pps.Size,
					Valid:       pps.Valid,
					ErrorReason: pps.ErrorReason,
				}
				copy(ppsCopy.Data, pps.Data)
				ctx.hevcPpsMap[id] = ppsCopy
				copiedCount++
			}
		}
	}

	// Update metadata
	if copiedCount > 0 {
		ctx.lastUpdated = time.Now()
		ctx.totalSets += copiedCount
	}

	return copiedCount
}

// GetParameterSetData returns raw parameter set data for a specific SPS/PPS ID
func (ctx *ParameterSetContext) GetParameterSetData(paramType string, id uint8) ([]byte, bool) {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	switch paramType {
	case "sps":
		if sps, exists := ctx.spsMap[id]; exists && sps.Valid {
			dataCopy := make([]byte, len(sps.Data))
			copy(dataCopy, sps.Data)
			return dataCopy, true
		}
	case "pps":
		if pps, exists := ctx.ppsMap[id]; exists && pps.Valid {
			dataCopy := make([]byte, len(pps.Data))
			copy(dataCopy, pps.Data)
			return dataCopy, true
		}
	case "vps":
		if vps, exists := ctx.vpsMap[id]; exists && vps.Valid {
			dataCopy := make([]byte, len(vps.Data))
			copy(dataCopy, vps.Data)
			return dataCopy, true
		}
	}

	return nil, false
}

// GetAllParameterSets returns all parameter sets in a format suitable for copying
func (ctx *ParameterSetContext) GetAllParameterSets() map[string]map[uint8][]byte {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	result := make(map[string]map[uint8][]byte)

	// H.264 SPS
	if len(ctx.spsMap) > 0 {
		result["sps"] = make(map[uint8][]byte)
		for id, sps := range ctx.spsMap {
			if sps.Valid {
				dataCopy := make([]byte, len(sps.Data))
				copy(dataCopy, sps.Data)
				result["sps"][id] = dataCopy
			}
		}
	}

	// H.264 PPS
	if len(ctx.ppsMap) > 0 {
		result["pps"] = make(map[uint8][]byte)
		for id, pps := range ctx.ppsMap {
			if pps.Valid {
				dataCopy := make([]byte, len(pps.Data))
				copy(dataCopy, pps.Data)
				result["pps"][id] = dataCopy
			}
		}
	}

	// HEVC parameter sets
	if ctx.codec == CodecHEVC {
		if len(ctx.vpsMap) > 0 {
			result["vps"] = make(map[uint8][]byte)
			for id, vps := range ctx.vpsMap {
				if vps.Valid {
					dataCopy := make([]byte, len(vps.Data))
					copy(dataCopy, vps.Data)
					result["vps"][id] = dataCopy
				}
			}
		}

		if len(ctx.hevcSpsMap) > 0 {
			result["hevc_sps"] = make(map[uint8][]byte)
			for id, sps := range ctx.hevcSpsMap {
				if sps.Valid {
					dataCopy := make([]byte, len(sps.Data))
					copy(dataCopy, sps.Data)
					result["hevc_sps"][id] = dataCopy
				}
			}
		}

		if len(ctx.hevcPpsMap) > 0 {
			result["hevc_pps"] = make(map[uint8][]byte)
			for id, pps := range ctx.hevcPpsMap {
				if pps.Valid {
					dataCopy := make([]byte, len(pps.Data))
					copy(dataCopy, pps.Data)
					result["hevc_pps"][id] = dataCopy
				}
			}
		}
	}

	return result
}

// Clear removes all parameter sets from the context (for cleanup during shutdown)
func (ctx *ParameterSetContext) Clear() {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Clear H.264 parameter sets
	ctx.spsMap = make(map[uint8]*ParameterSet)
	ctx.ppsMap = make(map[uint8]*PPSContext)

	// Clear HEVC parameter sets
	ctx.vpsMap = make(map[uint8]*ParameterSet)
	ctx.hevcSpsMap = make(map[uint8]*ParameterSet)
	ctx.hevcPpsMap = make(map[uint8]*ParameterSet)

	// Reset counters
	ctx.totalSets = 0
	ctx.lastUpdated = time.Now()
	ctx.lastCleanup = time.Now()
}

// ClearOldest removes the oldest parameter sets to free memory
func (ctx *ParameterSetContext) ClearOldest(count int) int {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if count <= 0 {
		return 0
	}

	type paramSetWithAge struct {
		paramType string
		id        uint8
		age       time.Time
	}

	// Collect all parameter sets with their ages
	var allSets []paramSetWithAge

	// H.264 SPS
	for id, sps := range ctx.spsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "sps",
			id:        id,
			age:       sps.ParsedAt,
		})
	}

	// H.264 PPS
	for id, pps := range ctx.ppsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "pps",
			id:        id,
			age:       pps.ParsedAt,
		})
	}

	// HEVC VPS
	for id, vps := range ctx.vpsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "vps",
			id:        id,
			age:       vps.ParsedAt,
		})
	}

	// HEVC SPS
	for id, sps := range ctx.hevcSpsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "hevc_sps",
			id:        id,
			age:       sps.ParsedAt,
		})
	}

	// HEVC PPS
	for id, pps := range ctx.hevcPpsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "hevc_pps",
			id:        id,
			age:       pps.ParsedAt,
		})
	}

	// Sort by age (oldest first)
	for i := 0; i < len(allSets)-1; i++ {
		for j := 0; j < len(allSets)-i-1; j++ {
			if allSets[j].age.After(allSets[j+1].age) {
				allSets[j], allSets[j+1] = allSets[j+1], allSets[j]
			}
		}
	}

	// Remove the oldest sets
	removed := 0
	for i := 0; i < len(allSets) && removed < count; i++ {
		set := allSets[i]
		switch set.paramType {
		case "sps":
			delete(ctx.spsMap, set.id)
			removed++
		case "pps":
			delete(ctx.ppsMap, set.id)
			removed++
		case "vps":
			delete(ctx.vpsMap, set.id)
			removed++
		case "hevc_sps":
			delete(ctx.hevcSpsMap, set.id)
			removed++
		case "hevc_pps":
			delete(ctx.hevcPpsMap, set.id)
			removed++
		}
	}

	// Update counters
	ctx.totalSets -= removed
	ctx.lastCleanup = time.Now()

	return removed
}

// clearOldestUnsafe removes the oldest parameter sets without acquiring mutex
// Must be called with mutex already held
func (ctx *ParameterSetContext) clearOldestUnsafe(count int) int {
	if count <= 0 {
		return 0
	}

	type paramSetWithAge struct {
		paramType string
		id        uint8
		age       time.Time
	}

	// Collect all parameter sets with their ages
	var allSets []paramSetWithAge

	// H.264 SPS
	for id, sps := range ctx.spsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "sps",
			id:        id,
			age:       sps.ParsedAt,
		})
	}

	// H.264 PPS
	for id, pps := range ctx.ppsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "pps",
			id:        id,
			age:       pps.ParsedAt,
		})
	}

	// HEVC VPS
	for id, vps := range ctx.vpsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "vps",
			id:        id,
			age:       vps.ParsedAt,
		})
	}

	// HEVC SPS
	for id, sps := range ctx.hevcSpsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "hevc_sps",
			id:        id,
			age:       sps.ParsedAt,
		})
	}

	// HEVC PPS
	for id, pps := range ctx.hevcPpsMap {
		allSets = append(allSets, paramSetWithAge{
			paramType: "hevc_pps",
			id:        id,
			age:       pps.ParsedAt,
		})
	}

	// Sort by age (oldest first) - use bubble sort to avoid import
	for i := 0; i < len(allSets)-1; i++ {
		for j := 0; j < len(allSets)-i-1; j++ {
			if allSets[j].age.After(allSets[j+1].age) {
				allSets[j], allSets[j+1] = allSets[j+1], allSets[j]
			}
		}
	}

	// Remove the oldest sets
	removed := 0
	for i := 0; i < len(allSets) && removed < count; i++ {
		set := allSets[i]
		switch set.paramType {
		case "sps":
			delete(ctx.spsMap, set.id)
			removed++
		case "pps":
			delete(ctx.ppsMap, set.id)
			removed++
		case "vps":
			delete(ctx.vpsMap, set.id)
			removed++
		case "hevc_sps":
			delete(ctx.hevcSpsMap, set.id)
			removed++
		case "hevc_pps":
			delete(ctx.hevcPpsMap, set.id)
			removed++
		}
	}

	// Update counters (don't update totalSets here - caller will do it)
	return removed
}

// performEmergencyCleanupUnsafe performs aggressive cleanup when at memory limits
// Must be called with mutex already held
func (ctx *ParameterSetContext) performEmergencyCleanupUnsafe() int {
	now := time.Now()
	removed := 0
	
	// Emergency cleanup is more aggressive - remove anything older than 1 hour
	emergencyAge := 1 * time.Hour
	
	// Clean H.264 parameter sets
	for id, sps := range ctx.spsMap {
		if now.Sub(sps.ParsedAt) > emergencyAge {
			delete(ctx.spsMap, id)
			removed++
		}
	}
	
	for id, pps := range ctx.ppsMap {
		if now.Sub(pps.ParsedAt) > emergencyAge {
			delete(ctx.ppsMap, id)
			removed++
		}
	}
	
	// Clean HEVC parameter sets
	for id, vps := range ctx.vpsMap {
		if now.Sub(vps.ParsedAt) > emergencyAge {
			delete(ctx.vpsMap, id)
			removed++
		}
	}
	
	for id, sps := range ctx.hevcSpsMap {
		if now.Sub(sps.ParsedAt) > emergencyAge {
			delete(ctx.hevcSpsMap, id)
			removed++
		}
	}
	
	for id, pps := range ctx.hevcPpsMap {
		if now.Sub(pps.ParsedAt) > emergencyAge {
			delete(ctx.hevcPpsMap, id)
			removed++
		}
	}
	
	// If still not enough, remove oldest 50% regardless of age
	currentTotal := len(ctx.spsMap) + len(ctx.ppsMap) + len(ctx.vpsMap) + len(ctx.hevcSpsMap) + len(ctx.hevcPpsMap)
	if currentTotal >= MaxParameterSetsPerSession {
		additionalRemoved := ctx.clearOldestUnsafe(currentTotal / 2)
		removed += additionalRemoved
	}
	
	ctx.totalSets -= removed
	ctx.lastCleanup = now
	
	return removed
}
