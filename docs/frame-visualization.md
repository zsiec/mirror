# Frame Visualization System

The Mirror Frame Visualization System provides real-time analysis and visualization of digital video streaming, packetization, and frame processing. This educational tool helps developers understand how live video streams work at the frame level.

## Features

### 🎬 Real-time Frame Analysis
- Live frame-by-frame visualization as streams are processed
- Frame type identification (I, P, B, IDR frames)
- NAL unit analysis and breakdown
- GOP (Group of Pictures) structure visualization
- Timing and synchronization analysis

### 📊 Interactive Dashboard
- Real-time statistics (frame rate, bitrate, frame counts)
- Frame type distribution charts
- Bitrate visualization over time
- Timeline view of last 60 frames
- Capture and detailed frame inspection

### 🔍 Educational Insights
- Visual representation of video packetization
- Frame dependency relationships
- Codec-specific parameter analysis
- Performance metrics and latency tracking

## Architecture

### Backend Components

#### Frame Visualization Manager (`internal/ingestion/frame_visualization.go`)
- Manages WebSocket connections for real-time data streaming
- Handles frame capture for detailed analysis
- Provides REST API endpoints for frame data access

#### API Endpoints
- `GET /api/v1/streams/{id}/frames/live` - WebSocket for real-time frame streaming
- `POST /api/v1/frames/capture/start` - Start frame capture
- `POST /api/v1/frames/capture/stop` - Stop frame capture
- `GET /api/v1/frames/captured` - List captured frames
- `GET /api/v1/frames/captured/{index}` - Get specific captured frame
- `GET /api/v1/streams/{id}/frames/analysis` - Frame analysis summary
- `GET /api/v1/streams/{id}/frames/types` - Frame type distribution

### Frontend Components

#### Web UI (`web/frame-visualization.html`)
- Modern, responsive single-page application
- Chart.js for real-time data visualization
- WebSocket integration for live updates
- Modal dialogs for detailed frame inspection

## Getting Started

### Prerequisites
- Go 1.23+
- FFmpeg with SRT support
- Mirror server running

### 1. Start the Mirror Server
```bash
cd /path/to/mirror
go run cmd/mirror/main.go
```

### 2. Access the Visualization UI
Open your browser and navigate to:
```
https://localhost:8081
```

The browser will automatically redirect to the frame visualization dashboard.

### 3. Start a Test Stream
Use the provided demo script to start streaming:
```bash
# Basic test pattern stream
./scripts/stream-demo.sh

# Mandelbrot fractal with high profile
./scripts/stream-demo.sh stream mandelbrot high

# Check dependencies
./scripts/stream-demo.sh check
```

### 4. Connect and Visualize
1. Select your stream from the dropdown
2. Click "Connect" to start real-time visualization
3. Optionally start "Capture" for detailed frame analysis
4. Explore the various visualization panels

## Stream Demo Script

The `scripts/stream-demo.sh` script provides several test content types:

### Content Types
- **testsrc** - Colorful test pattern with frame counters (default)
- **mandelbrot** - Animated Mandelbrot set fractal
- **noise** - RGB noise pattern  
- **life** - Conway's Game of Life simulation

### H.264 Profiles
- **baseline** - Maximum compatibility, simple encoding
- **main** - Better compression, moderate complexity
- **high** - Best compression, complex encoding (default)

### Examples
```bash
# Default: test pattern with high profile
./scripts/stream-demo.sh

# Mandelbrot fractal with main profile
./scripts/stream-demo.sh stream mandelbrot main

# Conway's Game of Life with baseline profile
./scripts/stream-demo.sh stream life baseline
```

## Understanding the Visualizations

### Frame Timeline
- Visual representation of the last 60 frames
- Color-coded by frame type:
  - 🟢 Green: I-frames (keyframes)
  - 🔵 Blue: P-frames (predictive)
  - 🟠 Orange: B-frames (bidirectional)
  - 🟣 Purple: IDR-frames (instant decoder refresh)

### Frame Type Distribution
- Pie chart showing proportion of different frame types
- Helps understand GOP structure and encoding efficiency
- Updates in real-time as frames are processed

### Bitrate Analysis
- Line chart showing bitrate variation over time
- Reveals encoding complexity and scene changes
- Useful for identifying quality fluctuations

### Real-time Statistics
- Current frame rate (frames per second)
- Instantaneous bitrate (Mbps)
- Total frames processed
- Keyframe count

## Frame Data Structure

Each frame contains comprehensive metadata:

```javascript
{
  "stream_id": "demo-stream-001",
  "timestamp": "2024-01-15T10:30:45Z",
  "frame_type": "I",
  "frame_size": 8192,
  "pts": 90000,
  "dts": 90000,
  "duration": 3000,
  "is_keyframe": true,
  "nal_units": [
    {
      "type": 7,
      "type_name": "SPS",
      "size": 32,
      "importance": 5
    }
  ],
  "bitrate": 4000000,
  "codec": "H.264",
  "resolution": {
    "width": 1920,
    "height": 1080,
    "profile": "High",
    "level": "4.0"
  },
  "gop": {
    "gop_id": 123,
    "position": 0,
    "gop_size": 30,
    "is_last_in_gop": false
  },
  "timing": {
    "capture_time": "2024-01-15T10:30:45.123Z",
    "processing_latency": "5ms"
  }
}
```

## Educational Use Cases

### Learning Video Compression
- Observe how different frame types contribute to compression
- Understand GOP structure and its impact on quality
- See the relationship between bitrate and visual complexity

### Debugging Stream Issues
- Identify frame drops or corruption
- Analyze timing and synchronization problems
- Monitor bitrate variations and quality fluctuations

### Performance Analysis
- Measure processing latency
- Track memory usage and queue depths
- Evaluate codec efficiency

### Network Analysis
- Observe packetization strategies
- Understand SRT protocol behavior
- Monitor connection stability

## Customization

### Adding New Content Types
Modify `scripts/stream-demo.sh` to add new FFmpeg test sources:

```bash
"custom")
    print_status "Generating custom content..."
    output_args="-f lavfi -i 'your_custom_filter'"
    ;;
```

### Extending Frame Analysis
Add new metrics to the frame visualization manager:

```go
type FrameVisualizationData struct {
    // Existing fields...
    CustomMetric float64 `json:"custom_metric"`
}
```

### UI Modifications
The web UI uses vanilla JavaScript and Chart.js. Customize charts in the `setupCharts()` function or add new visualization panels.

## Troubleshooting

### Common Issues

#### WebSocket Connection Failed
- Ensure the Mirror server is running on port 8081
- Check if the selected stream exists and is active
- Verify no firewall is blocking the connection

#### No Frames Received
- Confirm the SRT stream is actually sending data
- Check the stream ID matches between sender and receiver
- Verify codec compatibility (H.264, HEVC, AV1, JPEGXS)

#### FFmpeg Streaming Errors
- Ensure FFmpeg has SRT support: `ffmpeg -protocols | grep srt`
- Check if the SRT port (30000) is available
- Verify network connectivity to the Mirror server

#### Performance Issues
- Reduce the visualization update rate if the UI becomes sluggish
- Limit the number of captured frames
- Use a lower bitrate or resolution for test streams

### Debug Mode
Enable debug endpoints in the Mirror server configuration to access additional debugging information:

```yaml
server:
  debug_endpoints: true
```

Then access:
- `/debug/pprof/` - Go profiling data
- `/debug/vars` - Runtime variables
- `/api/v1/stats` - Detailed system statistics

## API Reference

### WebSocket Protocol
The frame streaming WebSocket sends JSON messages with frame data. Connect to:
```
wss://localhost:8081/api/v1/streams/{stream_id}/frames/live
```

### Capture Control
```bash
# Start capture with 500 frame limit
curl -X POST https://localhost:8081/api/v1/frames/capture/start \
  -H "Content-Type: application/json" \
  -d '{"enabled": true, "max_frames": 500}' \
  -k

# Stop capture
curl -X POST https://localhost:8081/api/v1/frames/capture/stop -k

# Get capture status
curl https://localhost:8081/api/v1/frames/capture/status -k
```

## Performance Considerations

- WebSocket connections can handle ~30-60 FPS without issues
- Frame capture is limited to prevent memory exhaustion
- Charts are optimized for real-time updates with minimal performance impact
- Large GOP sizes may cause UI lag during keyframe processing

## Future Enhancements

- Support for additional codecs (VP9, AV01)
- Advanced analytics (quality metrics, compression efficiency)
- Multi-stream comparison views
- Export capabilities for captured data
- Machine learning-based anomaly detection
- Integration with popular streaming tools (OBS, etc.)

---

For questions or issues, please refer to the main Mirror documentation or create an issue in the project repository.