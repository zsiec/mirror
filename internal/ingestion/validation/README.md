# Validation Package

The validation package provides comprehensive input validation utilities for the video ingestion system. It ensures data integrity and prevents buffer overflows, malformed packets, and other security issues.

## Components

### PacketValidator
Validates various packet types including MPEG-TS, RTP, NAL units, and frames.

```go
validator := validation.NewPacketValidator()

// Validate MPEG-TS packet
err := validator.ValidateMPEGTSPacket(tsPacket)

// Validate RTP packet
err := validator.ValidateRTPPacket(rtpPacket)

// Validate NAL unit
err := validator.ValidateNALUnit(nalData)

// Validate complete frame
err := validator.ValidateFrame(frameData, nalUnitCount)
```

### BoundaryValidator
Provides boundary checking utilities to prevent buffer overreads and overflows.

```go
boundary := validation.NewBoundaryValidator()

// Check buffer access
err := boundary.ValidateBufferAccess(buffer, offset, length)

// Validate start code position
startCodeLen, err := boundary.ValidateStartCode(data, position)
```

### SizeValidator
Enforces size limits for various data types.

```go
size := validation.NewSizeValidator()

// Check against default limits
err := size.ValidateSize("packet", packetSize)

// Set custom limits
size.SetLimit("custom", 1024*1024) // 1MB
err = size.ValidateSize("custom", dataSize)
```

### TimestampValidator
Validates PTS/DTS timestamps and their relationships.

```go
ts := validation.NewTimestampValidator()

// Validate individual timestamps
err := ts.ValidatePTS(pts)
err := ts.ValidateDTS(dts)

// Validate PTS >= DTS
err := ts.ValidatePTSDTSOrder(pts, dts)
```

## Default Limits

- **Packet**: 64KB
- **Frame**: 50MB (from security.MaxFrameSize)
- **NAL Unit**: 10MB (from security.MaxNALUnitSize)
- **Buffer**: 100MB
- **GOP**: 200MB
- **PTS/DTS**: 33-bit range (0 to 2^33-1)

## Usage Examples

### Validating Incoming SRT Data
```go
func (c *SRTConnection) processMessage(data []byte) error {
    // Validate message size
    if err := sizeValidator.ValidateSize("packet", int64(len(data))); err != nil {
        return err
    }
    
    // Process MPEG-TS packets
    for i := 0; i+188 <= len(data); i += 188 {
        packet := data[i:i+188]
        if err := packetValidator.ValidateMPEGTSPacket(packet); err != nil {
            return fmt.Errorf("invalid TS packet at offset %d: %w", i, err)
        }
    }
    
    return nil
}
```

### Safe NAL Unit Parsing
```go
func findNALUnits(data []byte) ([][]byte, error) {
    boundary := validation.NewBoundaryValidator()
    packet := validation.NewPacketValidator()
    
    nalUnits := [][]byte{}
    
    for i := 0; i < len(data); {
        // Safely check for start code
        startLen, err := boundary.ValidateStartCode(data, i)
        if err != nil {
            i++
            continue
        }
        
        // Extract NAL unit...
        nalStart := i + startLen
        // ... find end ...
        
        // Validate NAL unit before adding
        if err := packet.ValidateNALUnit(nalData); err != nil {
            return nil, err
        }
        
        nalUnits = append(nalUnits, nalData)
    }
    
    return nalUnits, nil
}
```

### Timestamp Validation in Sync
```go
func (s *SyncManager) validateTimestamps(pts, dts int64) error {
    validator := validation.NewTimestampValidator()
    
    // Validate timestamps are in valid range
    if err := validator.ValidatePTSDTSOrder(pts, dts); err != nil {
        return fmt.Errorf("invalid timestamps: %w", err)
    }
    
    return nil
}
```

## Thread Safety

All validators are stateless and thread-safe. They can be shared across goroutines without synchronization.

## Performance Considerations

- Validators perform minimal allocations
- Validation functions are designed to fail fast
- Consider caching validator instances rather than creating new ones

## Testing

The package includes comprehensive tests covering:
- Valid and invalid packet formats
- Boundary conditions
- Edge cases (wraparound, maximum values)
- Error message validation

Run tests with:
```bash
go test ./internal/ingestion/validation -v
```
