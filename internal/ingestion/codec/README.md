# Codec Package

The `codec` package provides codec detection and depacketization for various video formats in the Mirror platform. It supports H.264, HEVC/H.265, AV1, and JPEG-XS with automatic detection and efficient processing.

## Overview

Key components:
- **Codec Detector**: Automatic codec identification from packet data
- **Depacketizers**: Format-specific packet handling
- **Frame Assembly**: Codec-aware frame reconstruction

## Supported Codecs

### H.264/AVC
```go
depacketizer := codec.NewH264Depacketizer()

// Process NAL units
nalUnit, err := depacketizer.Depacketize(packet)

// Access SPS/PPS
sps := depacketizer.GetSPS()
pps := depacketizer.GetPPS()
```

### HEVC/H.265
```go
depacketizer := codec.NewHEVCDepacketizer()

// Supports VPS/SPS/PPS
vps := depacketizer.GetVPS()

// Handle temporal layers
layer := depacketizer.GetTemporalID()
```

### AV1
```go
depacketizer := codec.NewAV1Depacketizer()

// OBU parsing
obu, err := depacketizer.ParseOBU(data)

// Sequence header access
seqHeader := depacketizer.GetSequenceHeader()
```

### JPEG-XS
```go
depacketizer := codec.NewJPEGXSDepacketizer()

// Low-latency image handling
image, err := depacketizer.AssembleImage(packets)
```

## Codec Detection

Automatic codec identification:

```go
detector := codec.NewDetector()

// Detect from packet
codecType := detector.Detect(packet)

switch codecType {
case codec.H264:
    // Use H.264 depacketizer
case codec.HEVC:
    // Use HEVC depacketizer
case codec.AV1:
    // Use AV1 depacketizer
}
```

## Features

- **Automatic detection** from packet headers
- **Efficient NAL/OBU parsing** with zero-copy
- **Parameter set caching** (SPS/PPS/VPS)
- **Error resilience** for packet loss
- **Temporal layer support** for scalable codecs

## Related Documentation

- [Ingestion Overview](../README.md)
- [Frame Processing](../frame/README.md)
- [Main Documentation](../../../README.md)
