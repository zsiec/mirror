# Sampled Logger

High-frequency event logging with intelligent sampling to prevent log spam during video processing.

## Overview

The sampled logger is designed to handle high-frequency log events common in video streaming applications. Instead of logging every frame processing event, it samples logs based on categories and time windows.

## Features

- **Category-based Sampling**: Different sampling rates for different event types
- **Time Window Control**: Prevents log flooding within time periods  
- **Automatic Throttling**: Reduces logging under high load
- **Performance Optimized**: Minimal overhead for video processing paths

## Usage

```go
import "github.com/zsiec/mirror/internal/logger"

sampled := logger.NewSampledLogger(baseLogger)

// High-frequency events are automatically sampled
sampled.LogFrameProcessed(frameID, "frame processed successfully")
sampled.LogPacketReceived(packetID, "packet received")

// Critical events always logged
sampled.LogError("critical error", err)
```

## Sampling Categories

- **Frame Events**: 1 in 100 logged (1% sampling)
- **Packet Events**: 1 in 1000 logged (0.1% sampling)  
- **GOP Events**: 1 in 10 logged (10% sampling)
- **Error Events**: Always logged (100% sampling)
- **Performance Events**: 1 in 50 logged (2% sampling)

## Integration

Integrated throughout the video processing pipeline:
- Frame assembly logging
- Codec detection events
- Synchronization adjustments
- Backpressure events
- Memory allocation tracking

This prevents log files from growing too large while maintaining visibility into system operation.
