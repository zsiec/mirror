# Mirror Streaming Platform - Part 4: HLS Packaging and Delivery

## Table of Contents
1. [Packaging Service Overview](#packaging-service-overview)
2. [Transcoding Integration](#transcoding-integration)
3. [CMAF Segmentation](#cmaf-segmentation)
4. [LL-HLS Playlist Generation](#ll-hls-playlist-generation)
5. [HTTP/3 Server and Push](#http3-server-and-push)
6. [DVR Recording](#dvr-recording)

## Packaging Service Overview

### internal/packaging/service.go
```go
package packaging

import (
    "context"
    "fmt"
    "net/http"
    "sync"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
    "github.com/quic-go/quic-go"
    "github.com/quic-go/quic-go/http3"
    "github.com/redis/go-redis/v9"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/internal/storage"
    "github.com/zsiec/mirror/internal/transcoding"
    "github.com/zsiec/mirror/pkg/models"
)

var (
    // Metrics
    segmentsGeneratedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "mirror_segments_generated_total",
        Help: "Total number of HLS segments generated",
    }, []string{"stream_id"})
    
    playlistRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "mirror_playlist_requests_total",
        Help: "Total number of playlist requests",
    }, []string{"stream_id", "type"})
    
    http3PushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "mirror_http3_push_total",
        Help: "Total number of HTTP/3 server pushes",
    }, []string{"stream_id", "status"})
)

// Service handles HLS packaging and delivery
type Service struct {
    ctx    context.Context
    cancel context.CancelFunc
    cfg    *config.Config

    // Dependencies
    transcoding *transcoding.Service
    storage     *storage.Service
    redis       *redis.Client

    // Active packagers
    packagers sync.Map // map[string]*StreamPackager

    // HTTP/3 push management
    pushManager *PushManager

    // Control
    wg      sync.WaitGroup
    errChan chan error
}

// New creates a new packaging service
func New(ctx context.Context, cfg *config.Config, transcoding *transcoding.Service, storage *storage.Service, redis *redis.Client) (*Service, error) {
    ctx, cancel := context.WithCancel(ctx)

    s := &Service{
        ctx:         ctx,
        cancel:      cancel,
        cfg:         cfg,
        transcoding: transcoding,
        storage:     storage,
        redis:       redis,
        errChan:     make(chan error, 10),
    }

    // Initialize push manager
    s.pushManager = NewPushManager(s)

    return s, nil
}

// Start begins the packaging service
func (s *Service) Start() error {
    log.Info().Msg("Starting packaging service")

    // Subscribe to transcoded streams
    outputs := s.transcoding.GetOutputChannels()
    
    for streamID, ch := range outputs {
        s.wg.Add(1)
        go s.handleTranscodedStream(streamID, ch)
    }

    // Monitor for new streams
    s.wg.Add(1)
    go s.monitorNewStreams()

    select {
    case err := <-s.errChan:
        return err
    case <-s.ctx.Done():
        return s.ctx.Err()
    }
}

// Stop gracefully shuts down the service
func (s *Service) Stop(ctx context.Context) error {
    log.Info().Msg("Stopping packaging service")

    s.cancel()

    // Stop all packagers
    s.packagers.Range(func(key, value interface{}) bool {
        packager := value.(*StreamPackager)
        packager.Stop()
        return true
    })

    // Wait for completion
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

func (s *Service) handleTranscodedStream(streamID string, ch <-chan []byte) {
    defer s.wg.Done()

    // Create packager for stream
    packager := NewStreamPackager(s, streamID, s.cfg)
    s.packagers.Store(streamID, packager)
    defer s.packagers.Delete(streamID)

    // Start packager
    if err := packager.Start(); err != nil {
        log.Error().Err(err).Str("stream_id", streamID).Msg("Failed to start packager")
        return
    }
    defer packager.Stop()

    // Process transcoded data
    for {
        select {
        case data, ok := <-ch:
            if !ok {
                log.Info().Str("stream_id", streamID).Msg("Transcoded stream ended")
                return
            }

            if err := packager.ProcessData(data); err != nil {
                log.Error().Err(err).Str("stream_id", streamID).Msg("Packaging error")
                s.errChan <- fmt.Errorf("packaging error for stream %s: %w", streamID, err)
                return
            }

        case <-s.ctx.Done():
            return
        }
    }
}

func (s *Service) monitorNewStreams() {
    defer s.wg.Done()

    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            // Check for new transcoded streams
            outputs := s.transcoding.GetOutputChannels()
            
            for streamID, ch := range outputs {
                if _, exists := s.packagers.Load(streamID); !exists {
                    s.wg.Add(1)
                    go s.handleTranscodedStream(streamID, ch)
                }
            }

        case <-s.ctx.Done():
            return
        }
    }
}

// HandleStreamRequest serves HLS content via HTTP/3
func (s *Service) HandleStreamRequest(w http.ResponseWriter, r *http.Request) {
    // Extract stream ID and request type from path
    // Expected format: /streams/{stream_id}/{playlist.m3u8|segment_N.m4s}
    
    path := r.URL.Path
    streamID, resourceType, resourceName := parseStreamPath(path)
    
    if streamID == "" {
        http.Error(w, "Invalid stream path", http.StatusBadRequest)
        return
    }

    // Get packager for stream
    packagerI, exists := s.packagers.Load(streamID)
    if !exists {
        http.Error(w, "Stream not found", http.StatusNotFound)
        return
    }
    packager := packagerI.(*StreamPackager)

    switch resourceType {
    case "playlist":
        s.handlePlaylistRequest(w, r, packager)
    case "segment":
        s.handleSegmentRequest(w, r, packager, resourceName)
    case "init":
        s.handleInitRequest(w, r, packager)
    default:
        http.Error(w, "Invalid resource type", http.StatusBadRequest)
    }
}

func (s *Service) handlePlaylistRequest(w http.ResponseWriter, r *http.Request, packager *StreamPackager) {
    streamID := packager.streamID
    playlistRequestsTotal.WithLabelValues(streamID, "playlist").Inc()

    // Check for LL-HLS query parameters
    msn := r.URL.Query().Get("_HLS_msn")
    part := r.URL.Query().Get("_HLS_part")

    // Handle blocking playlist reload
    if msn != "" {
        packager.WaitForSegment(msn)
    }

    // Generate playlist
    playlist, err := packager.GeneratePlaylist()
    if err != nil {
        log.Error().Err(err).Str("stream_id", streamID).Msg("Failed to generate playlist")
        http.Error(w, "Internal server error", http.StatusInternalServerError)
        return
    }

    // HTTP/3 Push support
    if pusher, ok := w.(http.Pusher); ok && r.ProtoMajor >= 3 {
        s.pushManager.PushPredictiveSegments(pusher, packager, r)
    }

    // Set headers
    w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.Header().Set("X-Accel-Buffering", "no")

    // Handle partial segment request
    if part != "" {
        // Serve partial segment info
        w.Write([]byte(playlist))
        return
    }

    w.Write([]byte(playlist))
}

func (s *Service) handleSegmentRequest(w http.ResponseWriter, r *http.Request, packager *StreamPackager, segmentName string) {
    streamID := packager.streamID
    playlistRequestsTotal.WithLabelValues(streamID, "segment").Inc()

    // Check for partial segment request
    partNum := r.URL.Query().Get("_HLS_part")
    if partNum != "" {
        s.handlePartialSegmentRequest(w, r, packager, segmentName, partNum)
        return
    }

    // Get segment data
    segment, err := packager.GetSegment(segmentName)
    if err != nil {
        http.Error(w, "Segment not found", http.StatusNotFound)
        return
    }

    // Set headers
    w.Header().Set("Content-Type", "video/mp4")
    w.Header().Set("Content-Length", fmt.Sprintf("%d", len(segment.Data)))
    w.Header().Set("Cache-Control", "public, max-age=3600")
    w.Header().Set("Access-Control-Allow-Origin", "*")

    w.Write(segment.Data)
}

func (s *Service) handlePartialSegmentRequest(w http.ResponseWriter, r *http.Request, packager *StreamPackager, segmentName string, partNum string) {
    streamID := packager.streamID
    playlistRequestsTotal.WithLabelValues(streamID, "partial").Inc()

    // Get partial segment
    part, err := packager.GetPartialSegment(segmentName, partNum)
    if err != nil {
        http.Error(w, "Partial segment not found", http.StatusNotFound)
        return
    }

    // Set headers for CMAF chunk
    w.Header().Set("Content-Type", "video/mp4")
    w.Header().Set("Content-Length", fmt.Sprintf("%d", len(part.Data)))
    w.Header().Set("Cache-Control", "max-age=1")
    w.Header().Set("X-Part-Duration", fmt.Sprintf("%.3f", part.Duration))

    w.Write(part.Data)
}

func (s *Service) handleInitRequest(w http.ResponseWriter, r *http.Request, packager *StreamPackager) {
    streamID := packager.streamID
    playlistRequestsTotal.WithLabelValues(streamID, "init").Inc()

    // Get init segment
    initData, err := packager.GetInitSegment()
    if err != nil {
        http.Error(w, "Init segment not found", http.StatusNotFound)
        return
    }

    // Set headers
    w.Header().Set("Content-Type", "video/mp4")
    w.Header().Set("Content-Length", fmt.Sprintf("%d", len(initData)))
    w.Header().Set("Cache-Control", "public, max-age=3600")
    w.Header().Set("Access-Control-Allow-Origin", "*")

    w.Write(initData)
}

// Helper function to parse stream path
func parseStreamPath(path string) (streamID, resourceType, resourceName string) {
    // Remove leading slash and split
    parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
    
    if len(parts) < 3 || parts[0] != "streams" {
        return "", "", ""
    }
    
    streamID = parts[1]
    resource = parts[2]
    
    if strings.HasSuffix(resource, ".m3u8") {
        return streamID, "playlist", resource
    } else if strings.HasSuffix(resource, ".m4s") {
        return streamID, "segment", resource
    } else if resource == "init.mp4" {
        return streamID, "init", ""
    }
    
    return streamID, "", ""
}
```

## Transcoding Integration

### internal/transcoding/service.go (relevant parts)
```go
package transcoding

import (
    "context"
    "fmt"
    "sync"

    "github.com/asticode/go-astiav"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/internal/ingestion"
    "github.com/zsiec/mirror/pkg/models"
)

// Service handles video transcoding with GPU acceleration
type Service struct {
    ctx    context.Context
    cancel context.CancelFunc
    cfg    config.TranscodingConfig

    // Dependencies
    ingestion *ingestion.Service

    // GPU resource management
    gpuPool *GPUPool

    // Active transcoding pipelines
    pipelines sync.Map // map[string]*Pipeline

    // Output channels for packager
    outputs sync.Map // map[string]chan []byte

    wg sync.WaitGroup
}

// Pipeline represents a transcoding pipeline for a stream
type Pipeline struct {
    streamID   string
    input      <-chan []byte
    output     chan []byte
    
    // FFmpeg components
    decoder    *astiav.CodecContext
    encoder    *astiav.CodecContext
    scaler     *astiav.SwsContext
    
    // GPU assignment
    gpuIndex   int
    
    ctx    context.Context
    cancel context.CancelFunc
}

// GetOutputChannels returns output channels for packaging service
func (s *Service) GetOutputChannels() map[string]<-chan []byte {
    channels := make(map[string]<-chan []byte)
    
    s.outputs.Range(func(key, value interface{}) bool {
        streamID := key.(string)
        ch := value.(chan []byte)
        channels[streamID] = ch
        return true
    })
    
    return channels
}

// NewPipeline creates a new transcoding pipeline
func (s *Service) NewPipeline(streamID string, inputCh <-chan []byte) (*Pipeline, error) {
    // Allocate GPU
    gpuIndex, err := s.gpuPool.Allocate()
    if err != nil {
        return nil, fmt.Errorf("failed to allocate GPU: %w", err)
    }

    // Create output channel
    outputCh := make(chan []byte, 100)
    s.outputs.Store(streamID, outputCh)

    ctx, cancel := context.WithCancel(s.ctx)

    p := &Pipeline{
        streamID: streamID,
        input:    inputCh,
        output:   outputCh,
        gpuIndex: gpuIndex,
        ctx:      ctx,
        cancel:   cancel,
    }

    // Initialize FFmpeg components
    if err := p.initialize(s.cfg); err != nil {
        s.gpuPool.Release(gpuIndex)
        return nil, fmt.Errorf("failed to initialize pipeline: %w", err)
    }

    return p, nil
}

func (p *Pipeline) initialize(cfg config.TranscodingConfig) error {
    // Initialize HEVC decoder
    decoderCodec := astiav.FindDecoder(astiav.CodecIDHevc)
    if decoderCodec == nil {
        return fmt.Errorf("HEVC decoder not found")
    }

    p.decoder = astiav.AllocCodecContext(decoderCodec)
    if p.decoder == nil {
        return fmt.Errorf("failed to allocate decoder context")
    }

    // Configure decoder for GPU if available
    if cfg.UseGPU {
        p.decoder.SetOption("hw_device_ctx", fmt.Sprintf("cuda:%d", p.gpuIndex))
    }

    if err := p.decoder.Open(decoderCodec, nil); err != nil {
        return fmt.Errorf("failed to open decoder: %w", err)
    }

    // Initialize H.264 encoder with NVENC
    var encoderName string
    if cfg.UseGPU {
        encoderName = "h264_nvenc"
    } else {
        encoderName = "libx264"
    }

    encoderCodec := astiav.FindEncoderByName(encoderName)
    if encoderCodec == nil {
        return fmt.Errorf("H.264 encoder not found: %s", encoderName)
    }

    p.encoder = astiav.AllocCodecContext(encoderCodec)
    if p.encoder == nil {
        return fmt.Errorf("failed to allocate encoder context")
    }

    // Configure encoder
    p.encoder.SetWidth(cfg.OutputWidth)
    p.encoder.SetHeight(cfg.OutputHeight)
    p.encoder.SetPixelFormat(astiav.PixelFormatYuv420P)
    p.encoder.SetBitRate(int64(cfg.OutputBitrate))
    p.encoder.SetTimeBase(astiav.NewRational(1, 25)) // 25 FPS
    p.encoder.SetGopSize(25) // 1 second GOP for LL-HLS

    if cfg.UseGPU {
        // NVENC specific options for low latency
        p.encoder.SetOption("preset", "p1") // Fastest preset
        p.encoder.SetOption("tune", "ull")  // Ultra low latency
        p.encoder.SetOption("zerolatency", "1")
        p.encoder.SetOption("rc", "cbr")    // Constant bitrate
        p.encoder.SetOption("gpu", fmt.Sprintf("%d", p.gpuIndex))
    } else {
        // x264 options
        p.encoder.SetOption("preset", "ultrafast")
        p.encoder.SetOption("tune", "zerolatency")
    }

    if err := p.encoder.Open(encoderCodec, nil); err != nil {
        return fmt.Errorf("failed to open encoder: %w", err)
    }

    // Initialize scaler for resolution change
    p.scaler = astiav.SwsGetContext(
        1920, 1080, astiav.PixelFormatYuv420P, // Input
        cfg.OutputWidth, cfg.OutputHeight, astiav.PixelFormatYuv420P, // Output
        astiav.SwsBilinear, nil, nil, nil,
    )

    if p.scaler == nil {
        return fmt.Errorf("failed to create scaler context")
    }

    return nil
}

// Process runs the transcoding pipeline
func (p *Pipeline) Process() {
    defer p.cleanup()

    log.Info().Str("stream_id", p.streamID).Int("gpu", p.gpuIndex).Msg("Starting transcoding pipeline")

    for {
        select {
        case data, ok := <-p.input:
            if !ok {
                return
            }

            // Process video data
            if err := p.processFrame(data); err != nil {
                log.Error().Err(err).Str("stream_id", p.streamID).Msg("Transcoding error")
                continue
            }

        case <-p.ctx.Done():
            return
        }
    }
}

func (p *Pipeline) processFrame(data []byte) error {
    // Parse input packet
    packet := astiav.AllocPacket()
    defer packet.Free()

    packet.SetData(data)

    // Decode frame
    if err := p.decoder.SendPacket(packet); err != nil {
        return fmt.Errorf("failed to send packet to decoder: %w", err)
    }

    frame := astiav.AllocFrame()
    defer frame.Free()

    for {
        if err := p.decoder.ReceiveFrame(frame); err != nil {
            if err == astiav.ErrEagain || err == astiav.ErrEof {
                break
            }
            return fmt.Errorf("failed to receive frame: %w", err)
        }

        // Scale frame if needed
        scaledFrame := astiav.AllocFrame()
        defer scaledFrame.Free()

        scaledFrame.SetWidth(p.encoder.Width())
        scaledFrame.SetHeight(p.encoder.Height())
        scaledFrame.SetPixelFormat(p.encoder.PixelFormat())
        scaledFrame.AllocBuffer()

        if err := p.scaler.Scale(frame, scaledFrame); err != nil {
            return fmt.Errorf("failed to scale frame: %w", err)
        }

        // Encode frame
        if err := p.encoder.SendFrame(scaledFrame); err != nil {
            return fmt.Errorf("failed to send frame to encoder: %w", err)
        }

        // Get encoded packets
        for {
            outPacket := astiav.AllocPacket()
            defer outPacket.Free()

            if err := p.encoder.ReceivePacket(outPacket); err != nil {
                if err == astiav.ErrEagain || err == astiav.ErrEof {
                    break
                }
                return fmt.Errorf("failed to receive packet: %w", err)
            }

            // Send to output
            select {
            case p.output <- outPacket.Data():
            case <-p.ctx.Done():
                return p.ctx.Err()
            }
        }
    }

    return nil
}

func (p *Pipeline) cleanup() {
    if p.decoder != nil {
        p.decoder.Free()
    }
    if p.encoder != nil {
        p.encoder.Free()
    }
    if p.scaler != nil {
        p.scaler.Free()
    }
    close(p.output)
}
```

## CMAF Segmentation

### internal/packaging/segmenter.go
```go
package packaging

import (
    "bytes"
    "fmt"
    "sync"
    "time"

    "github.com/Eyevinn/mp4ff/mp4"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/pkg/models"
)

// Segmenter handles CMAF segmentation for LL-HLS
type Segmenter struct {
    streamID       string
    segmentDuration time.Duration
    chunkDuration   time.Duration
    
    // Current segment being built
    currentSegment  *Segment
    segmentNumber   uint32
    
    // MP4 components
    initSegment    []byte
    trackID        uint32
    
    // Synchronization
    mu sync.Mutex
}

// Segment represents an HLS segment with CMAF chunks
type Segment struct {
    Number    uint32
    Duration  float64
    Timestamp time.Time
    Data      []byte
    Parts     []SegmentPart // CMAF chunks
    Complete  bool
}

// SegmentPart represents a CMAF chunk
type SegmentPart struct {
    Index    int
    Duration float64
    Offset   int
    Length   int
}

// NewSegmenter creates a new CMAF segmenter
func NewSegmenter(streamID string, segmentDuration, chunkDuration time.Duration) *Segmenter {
    return &Segmenter{
        streamID:        streamID,
        segmentDuration: segmentDuration,
        chunkDuration:   chunkDuration,
        trackID:         1,
    }
}

// Initialize creates the init segment
func (s *Segmenter) Initialize(width, height int, codecConfig []byte) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    // Create initialization segment
    init := mp4.InitSegment{
        Timescale: 90000, // 90kHz for video
    }

    // Add video track
    track := &mp4.Track{
        TrackID:   s.trackID,
        Timescale: 90000,
        Handler:   "vide",
        EditList:  nil,
    }

    // Add AVC configuration
    avcC := &mp4.AvcCBox{
        AVCProfileIndication: 66,  // Baseline
        ProfileCompatibility: 192,
        AVCLevelIndication:   30,  // Level 3.0
        SPS:                  [][]byte{codecConfig}, // Simplified
        PPS:                  [][]byte{},
    }

    // Create visual sample entry
    se := &mp4.VisualSampleEntry{
        SampleEntry: mp4.SampleEntry{
            Type: "avc1",
        },
        Width:  uint16(width),
        Height: uint16(height),
    }
    se.AddChild(avcC)

    track.Stsd = &mp4.StsdBox{}
    track.Stsd.AddChild(se)

    init.Tracks = append(init.Tracks, track)

    // Encode init segment
    var buf bytes.Buffer
    if err := init.Encode(&buf); err != nil {
        return fmt.Errorf("failed to encode init segment: %w", err)
    }

    s.initSegment = buf.Bytes()
    log.Info().Str("stream_id", s.streamID).Int("size", len(s.initSegment)).Msg("Created init segment")

    return nil
}

// ProcessData adds video data to current segment
func (s *Segmenter) ProcessData(data []byte, pts time.Duration, keyframe bool) (*Segment, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    // Check if we need a new segment (on keyframe and duration exceeded)
    if s.currentSegment != nil && keyframe && 
       time.Since(s.currentSegment.Timestamp) >= s.segmentDuration {
        // Finalize current segment
        completedSegment := s.finalizeSegment()
        
        // Start new segment
        s.startNewSegment(pts)
        
        // Add data to new segment
        if err := s.addToSegment(data, pts, keyframe); err != nil {
            return nil, err
        }
        
        return completedSegment, nil
    }

    // Start first segment if needed
    if s.currentSegment == nil {
        s.startNewSegment(pts)
    }

    // Add to current segment
    if err := s.addToSegment(data, pts, keyframe); err != nil {
        return nil, err
    }

    // Check if we should emit a partial segment
    if s.shouldEmitPartial() {
        partial := s.createPartialSegment()
        return partial, nil
    }

    return nil, nil
}

func (s *Segmenter) startNewSegment(pts time.Duration) {
    s.segmentNumber++
    s.currentSegment = &Segment{
        Number:    s.segmentNumber,
        Timestamp: time.Now(),
        Parts:     make([]SegmentPart, 0),
    }
    
    log.Debug().
        Str("stream_id", s.streamID).
        Uint32("segment", s.segmentNumber).
        Msg("Started new segment")
}

func (s *Segmenter) addToSegment(data []byte, pts time.Duration, keyframe bool) error {
    if s.currentSegment == nil {
        return fmt.Errorf("no current segment")
    }

    // Create media segment structure
    seg := mp4.MediaSegment{
        Tracks: []*mp4.MediaTrack{
            {
                TrackID: s.trackID,
                Samples: []mp4.Sample{
                    {
                        Data:            data,
                        PresentationTime: uint64(pts.Milliseconds() * 90), // Convert to 90kHz
                        DecodeTime:      uint64(pts.Milliseconds() * 90),
                        Duration:        uint32(s.chunkDuration.Milliseconds() * 90),
                        IsSync:          keyframe,
                    },
                },
            },
        },
    }

    // Encode sample
    var buf bytes.Buffer
    if err := seg.Encode(&buf); err != nil {
        return fmt.Errorf("failed to encode sample: %w", err)
    }

    // Add to segment data
    offset := len(s.currentSegment.Data)
    s.currentSegment.Data = append(s.currentSegment.Data, buf.Bytes()...)

    // Create CMAF chunk entry
    part := SegmentPart{
        Index:    len(s.currentSegment.Parts),
        Duration: s.chunkDuration.Seconds(),
        Offset:   offset,
        Length:   buf.Len(),
    }
    s.currentSegment.Parts = append(s.currentSegment.Parts, part)

    return nil
}

func (s *Segmenter) finalizeSegment() *Segment {
    if s.currentSegment == nil {
        return nil
    }

    s.currentSegment.Complete = true
    s.currentSegment.Duration = time.Since(s.currentSegment.Timestamp).Seconds()
    
    log.Debug().
        Str("stream_id", s.streamID).
        Uint32("segment", s.currentSegment.Number).
        Float64("duration", s.currentSegment.Duration).
        Int("parts", len(s.currentSegment.Parts)).
        Msg("Finalized segment")
    
    return s.currentSegment
}

func (s *Segmenter) shouldEmitPartial() bool {
    if s.currentSegment == nil {
        return false
    }
    
    // Emit partial if we have at least one chunk
    return len(s.currentSegment.Parts) > 0 && 
           len(s.currentSegment.Parts) % 5 == 0 // Every 5 chunks
}

func (s *Segmenter) createPartialSegment() *Segment {
    // Create a copy of current segment for partial delivery
    partial := &Segment{
        Number:    s.currentSegment.Number,
        Duration:  time.Since(s.currentSegment.Timestamp).Seconds(),
        Timestamp: s.currentSegment.Timestamp,
        Data:      make([]byte, len(s.currentSegment.Data)),
        Parts:     make([]SegmentPart, len(s.currentSegment.Parts)),
        Complete:  false,
    }
    
    copy(partial.Data, s.currentSegment.Data)
    copy(partial.Parts, s.currentSegment.Parts)
    
    return partial
}

// GetInitSegment returns the initialization segment
func (s *Segmenter) GetInitSegment() []byte {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.initSegment
}

// ForceFinalize forces completion of current segment
func (s *Segmenter) ForceFinalize() *Segment {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.finalizeSegment()
}
```

## LL-HLS Playlist Generation

### internal/packaging/playlist.go
```go
package packaging

import (
    "fmt"
    "strings"
    "sync"
    "time"

    "github.com/etherlabsio/go-m3u8/m3u8"
    "github.com/rs/zerolog/log"
)

// PlaylistGenerator creates LL-HLS playlists
type PlaylistGenerator struct {
    streamID      string
    targetDuration float64
    partDuration   float64
    
    // Playlist state
    segments      []SegmentInfo
    mediaSequence uint64
    
    mu sync.RWMutex
}

// SegmentInfo contains segment metadata for playlist
type SegmentInfo struct {
    Number       uint32
    Duration     float64
    URI          string
    Parts        []PartInfo
    ProgramDateTime time.Time
}

// PartInfo contains CMAF chunk metadata
type PartInfo struct {
    Duration float64
    URI      string
    Independent bool
}

// NewPlaylistGenerator creates a new playlist generator
func NewPlaylistGenerator(streamID string, targetDuration, partDuration float64) *PlaylistGenerator {
    return &PlaylistGenerator{
        streamID:       streamID,
        targetDuration: targetDuration,
        partDuration:   partDuration,
        segments:       make([]SegmentInfo, 0),
    }
}

// AddSegment adds a segment to the playlist
func (pg *PlaylistGenerator) AddSegment(segment *Segment) {
    pg.mu.Lock()
    defer pg.mu.Unlock()

    info := SegmentInfo{
        Number:          segment.Number,
        Duration:        segment.Duration,
        URI:             fmt.Sprintf("segment_%d.m4s", segment.Number),
        ProgramDateTime: segment.Timestamp,
        Parts:           make([]PartInfo, 0),
    }

    // Add part information
    for i, part := range segment.Parts {
        partInfo := PartInfo{
            Duration:    part.Duration,
            URI:         fmt.Sprintf("segment_%d.m4s?_HLS_part=%d", segment.Number, i),
            Independent: i == 0, // First part is independent
        }
        info.Parts = append(info.Parts, partInfo)
    }

    pg.segments = append(pg.segments, info)

    // Keep only last 20 segments for live playlist
    if len(pg.segments) > 20 {
        removed := len(pg.segments) - 20
        pg.segments = pg.segments[removed:]
        pg.mediaSequence += uint64(removed)
    }

    log.Debug().
        Str("stream_id", pg.streamID).
        Uint32("segment", segment.Number).
        Int("parts", len(info.Parts)).
        Msg("Added segment to playlist")
}

// Generate creates the LL-HLS playlist
func (pg *PlaylistGenerator) Generate() (string, error) {
    pg.mu.RLock()
    defer pg.mu.RUnlock()

    playlist := m3u8.NewMediaPlaylist(20, 20)
    
    // Set LL-HLS version
    playlist.SetVersion(9)
    playlist.SetTargetDuration(int(pg.targetDuration))
    playlist.MediaSequence = pg.mediaSequence
    
    // Add LL-HLS specific tags
    playlist.Custom = []string{
        fmt.Sprintf("#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.1f,CAN-SKIP-UNTIL=%.1f",
            pg.partDuration * 3, pg.targetDuration * 2),
        fmt.Sprintf("#EXT-X-PART-INF:PART-TARGET=%.3f", pg.partDuration),
    }

    // Add segments
    for _, seg := range pg.segments {
        // Add program date time
        playlist.Custom = append(playlist.Custom,
            fmt.Sprintf("#EXT-X-PROGRAM-DATE-TIME:%s", seg.ProgramDateTime.Format(time.RFC3339Nano)))

        // Add parts for incomplete segments
        if len(seg.Parts) > 0 && seg.Duration < pg.targetDuration {
            for _, part := range seg.Parts {
                partTag := fmt.Sprintf("#EXT-X-PART:DURATION=%.3f,URI=\"%s\"", part.Duration, part.URI)
                if part.Independent {
                    partTag += ",INDEPENDENT=YES"
                }
                playlist.Custom = append(playlist.Custom, partTag)
            }
        }

        // Add segment
        if err := playlist.AppendSegment(&m3u8.MediaSegment{
            URI:      seg.URI,
            Duration: seg.Duration,
            Title:    "",
        }); err != nil {
            return "", fmt.Errorf("failed to append segment: %w", err)
        }
    }

    // Add preload hint for next part
    if len(pg.segments) > 0 {
        lastSeg := pg.segments[len(pg.segments)-1]
        nextPart := len(lastSeg.Parts)
        hintURI := fmt.Sprintf("segment_%d.m4s?_HLS_part=%d", lastSeg.Number, nextPart)
        playlist.Custom = append(playlist.Custom,
            fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"%s\"", hintURI))
    }

    return playlist.String(), nil
}

// WaitForSegment blocks until the specified segment is available
func (pg *PlaylistGenerator) WaitForSegment(msn string) {
    targetMSN := parseUint(msn)
    
    for {
        pg.mu.RLock()
        if len(pg.segments) > 0 {
            lastSegment := pg.segments[len(pg.segments)-1]
            if lastSegment.Number >= targetMSN {
                pg.mu.RUnlock()
                return
            }
        }
        pg.mu.RUnlock()
        
        time.Sleep(100 * time.Millisecond)
    }
}

func parseUint(s string) uint32 {
    var n uint32
    fmt.Sscanf(s, "%d", &n)
    return n
}
```

## HTTP/3 Server and Push

### internal/packaging/push.go
```go
package packaging

import (
    "fmt"
    "net/http"
    "sync"
    "time"

    "github.com/hashicorp/golang-lru"
    "github.com/rs/zerolog/log"
)

// PushManager handles HTTP/3 server push for segments
type PushManager struct {
    service *Service
    
    // Track pushed resources per client
    clientCache *lru.Cache
    mu          sync.Mutex
}

// NewPushManager creates a new push manager
func NewPushManager(service *Service) *PushManager {
    cache, _ := lru.New(1000) // Track last 1000 clients
    
    return &PushManager{
        service:     service,
        clientCache: cache,
    }
}

// PushPredictiveSegments pushes upcoming segments via HTTP/3
func (pm *PushManager) PushPredictiveSegments(pusher http.Pusher, packager *StreamPackager, r *http.Request) {
    streamID := packager.streamID
    clientID := getClientID(r)
    
    // Get segments to push
    segmentsToPush := packager.GetUpcomingSegments(3) // Next 3 segments
    
    for _, seg := range segmentsToPush {
        // Check if already pushed to this client
        if pm.alreadyPushed(clientID, streamID, seg.Number) {
            continue
        }
        
        // Push segment
        segmentPath := fmt.Sprintf("/streams/%s/segment_%d.m4s", streamID, seg.Number)
        
        err := pusher.Push(segmentPath, &http.PushOptions{
            Header: http.Header{
                "Content-Type":       []string{"video/mp4"},
                "Cache-Control":      []string{"private, max-age=3"},
                "X-Segment-Duration": []string{fmt.Sprintf("%.3f", seg.Duration)},
            },
        })
        
        if err != nil {
            log.Debug().
                Err(err).
                Str("stream_id", streamID).
                Str("path", segmentPath).
                Msg("Failed to push segment")
            http3PushTotal.WithLabelValues(streamID, "error").Inc()
            continue
        }
        
        // Mark as pushed
        pm.markPushed(clientID, streamID, seg.Number)
        http3PushTotal.WithLabelValues(streamID, "success").Inc()
        
        log.Debug().
            Str("stream_id", streamID).
            Str("client", clientID).
            Uint32("segment", seg.Number).
            Msg("Pushed segment via HTTP/3")
        
        // Also push CMAF chunks for this segment
        pm.pushSegmentParts(pusher, streamID, seg)
    }
}

func (pm *PushManager) pushSegmentParts(pusher http.Pusher, streamID string, seg SegmentInfo) {
    for i, part := range seg.Parts {
        partPath := fmt.Sprintf("/streams/%s/segment_%d.m4s?_HLS_part=%d", streamID, seg.Number, i)
        
        err := pusher.Push(partPath, &http.PushOptions{
            Header: http.Header{
                "Content-Type":     []string{"video/mp4"},
                "Cache-Control":    []string{"max-age=1"},
                "X-Part-Duration":  []string{fmt.Sprintf("%.3f", part.Duration)},
            },
        })
        
        if err == nil {
            log.Debug().
                Str("stream_id", streamID).
                Uint32("segment", seg.Number).
                Int("part", i).
                Msg("Pushed CMAF chunk")
        }
    }
}

func (pm *PushManager) alreadyPushed(clientID, streamID string, segmentNum uint32) bool {
    pm.mu.Lock()
    defer pm.mu.Unlock()
    
    key := fmt.Sprintf("%s:%s:%d", clientID, streamID, segmentNum)
    _, exists := pm.clientCache.Get(key)
    return exists
}

func (pm *PushManager) markPushed(clientID, streamID string, segmentNum uint32) {
    pm.mu.Lock()
    defer pm.mu.Unlock()
    
    key := fmt.Sprintf("%s:%s:%d", clientID, streamID, segmentNum)
    pm.clientCache.Add(key, time.Now())
}

func getClientID(r *http.Request) string {
    // Use a combination of IP and User-Agent for client identification
    return fmt.Sprintf("%s:%s", r.RemoteAddr, r.Header.Get("User-Agent"))
}
```

## DVR Recording

### internal/packaging/packager.go
```go
package packaging

import (
    "fmt"
    "sync"
    "time"

    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/internal/storage"
)

// StreamPackager handles packaging for a single stream
type StreamPackager struct {
    service  *Service
    streamID string
    cfg      *config.Config
    
    // Components
    segmenter   *Segmenter
    playlist    *PlaylistGenerator
    storage     *storage.Service
    
    // Segment management
    segments    sync.Map // map[string]*Segment
    segmentList []SegmentInfo
    segmentMu   sync.RWMutex
    
    // DVR recording
    dvrRecorder *DVRRecorder
    
    // Control
    ctx    context.Context
    cancel context.CancelFunc
}

// NewStreamPackager creates a new packager for a stream
func NewStreamPackager(service *Service, streamID string, cfg *config.Config) *StreamPackager {
    ctx, cancel := context.WithCancel(service.ctx)
    
    p := &StreamPackager{
        service:     service,
        streamID:    streamID,
        cfg:         cfg,
        segmenter:   NewSegmenter(streamID, cfg.Storage.DVR.SegmentLength, 100*time.Millisecond),
        playlist:    NewPlaylistGenerator(streamID, 0.5, 0.1),
        storage:     service.storage,
        segmentList: make([]SegmentInfo, 0),
        ctx:         ctx,
        cancel:      cancel,
    }
    
    // Initialize DVR if enabled
    if cfg.Storage.DVR.Enabled {
        p.dvrRecorder = NewDVRRecorder(streamID, cfg.Storage.DVR, service.storage)
    }
    
    return p
}

// Start begins the packaging process
func (p *StreamPackager) Start() error {
    log.Info().Str("stream_id", p.streamID).Msg("Starting stream packager")
    
    // Initialize segmenter with default params
    if err := p.segmenter.Initialize(640, 360, nil); err != nil {
        return fmt.Errorf("failed to initialize segmenter: %w", err)
    }
    
    // Start DVR recording if enabled
    if p.dvrRecorder != nil {
        if err := p.dvrRecorder.Start(); err != nil {
            log.Error().Err(err).Str("stream_id", p.streamID).Msg("Failed to start DVR recording")
        }
    }
    
    return nil
}

// Stop halts the packaging process
func (p *StreamPackager) Stop() {
    log.Info().Str("stream_id", p.streamID).Msg("Stopping stream packager")
    
    p.cancel()
    
    // Finalize any pending segment
    if segment := p.segmenter.ForceFinalize(); segment != nil {
        p.storeSegment(segment)
    }
    
    // Stop DVR recording
    if p.dvrRecorder != nil {
        p.dvrRecorder.Stop()
    }
}

// ProcessData handles incoming transcoded data
func (p *StreamPackager) ProcessData(data []byte) error {
    // Simple frame detection (would be more complex in production)
    pts := time.Duration(time.Now().UnixNano())
    keyframe := detectKeyframe(data)
    
    // Process through segmenter
    segment, err := p.segmenter.ProcessData(data, pts, keyframe)
    if err != nil {
        return fmt.Errorf("segmentation error: %w", err)
    }
    
    // Store completed segment
    if segment != nil && segment.Complete {
        p.storeSegment(segment)
        
        // Update playlist
        p.playlist.AddSegment(segment)
        
        // Record to DVR
        if p.dvrRecorder != nil {
            p.dvrRecorder.RecordSegment(segment)
        }
        
        // Update metrics
        segmentsGeneratedTotal.WithLabelValues(p.streamID).Inc()
    }
    
    return nil
}

func (p *StreamPackager) storeSegment(segment *Segment) {
    key := fmt.Sprintf("segment_%d.m4s", segment.Number)
    p.segments.Store(key, segment)
    
    // Update segment list
    p.segmentMu.Lock()
    info := SegmentInfo{
        Number:          segment.Number,
        Duration:        segment.Duration,
        URI:             key,
        ProgramDateTime: segment.Timestamp,
    }
    
    // Convert parts
    for i, part := range segment.Parts {
        info.Parts = append(info.Parts, PartInfo{
            Duration:    part.Duration,
            URI:         fmt.Sprintf("%s?_HLS_part=%d", key, i),
            Independent: i == 0,
        })
    }
    
    p.segmentList = append(p.segmentList, info)
    
    // Keep only recent segments in memory
    if len(p.segmentList) > 50 {
        removed := p.segmentList[0]
        p.segmentList = p.segmentList[1:]
        p.segments.Delete(removed.URI)
    }
    p.segmentMu.Unlock()
    
    // Upload to S3 asynchronously
    go p.uploadSegment(segment)
}

func (p *StreamPackager) uploadSegment(segment *Segment) {
    key := fmt.Sprintf("streams/%s/segment_%d.m4s", p.streamID, segment.Number)
    
    if err := p.storage.UploadSegment(p.ctx, key, segment.Data); err != nil {
        log.Error().
            Err(err).
            Str("stream_id", p.streamID).
            Uint32("segment", segment.Number).
            Msg("Failed to upload segment")
    }
}

// GeneratePlaylist creates the current HLS playlist
func (p *StreamPackager) GeneratePlaylist() (string, error) {
    return p.playlist.Generate()
}

// GetSegment retrieves a segment by name
func (p *StreamPackager) GetSegment(name string) (*Segment, error) {
    // Check local cache first
    if seg, ok := p.segments.Load(name); ok {
        return seg.(*Segment), nil
    }
    
    // Fall back to storage
    key := fmt.Sprintf("streams/%s/%s", p.streamID, name)
    data, err := p.storage.GetSegment(p.ctx, key)
    if err != nil {
        return nil, err
    }
    
    // Create segment object
    segment := &Segment{
        Data: data,
    }
    
    return segment, nil
}

// GetPartialSegment retrieves a CMAF chunk
func (p *StreamPackager) GetPartialSegment(segmentName, partNum string) (*SegmentPart, error) {
    segment, err := p.GetSegment(segmentName)
    if err != nil {
        return nil, err
    }
    
    var partIdx int
    fmt.Sscanf(partNum, "%d", &partIdx)
    
    if partIdx >= len(segment.Parts) {
        return nil, fmt.Errorf("part not found")
    }
    
    part := segment.Parts[partIdx]
    
    // Extract part data
    partData := &SegmentPart{
        Index:    partIdx,
        Duration: part.Duration,
        Data:     segment.Data[part.Offset : part.Offset+part.Length],
    }
    
    return partData, nil
}

// GetInitSegment returns the initialization segment
func (p *StreamPackager) GetInitSegment() ([]byte, error) {
    return p.segmenter.GetInitSegment(), nil
}

// GetUpcomingSegments returns segments for HTTP/3 push
func (p *StreamPackager) GetUpcomingSegments(count int) []SegmentInfo {
    p.segmentMu.RLock()
    defer p.segmentMu.RUnlock()
    
    start := len(p.segmentList) - count
    if start < 0 {
        start = 0
    }
    
    return p.segmentList[start:]
}

// WaitForSegment blocks until segment is available
func (p *StreamPackager) WaitForSegment(msn string) {
    p.playlist.WaitForSegment(msn)
}

// Helper function to detect keyframes
func detectKeyframe(data []byte) bool {
    // Simplified - check for H.264 NAL unit type
    if len(data) > 4 {
        nalType := data[4] & 0x1F
        return nalType == 5 // IDR frame
    }
    return false
}

// DVRRecorder handles recording segments for playback
type DVRRecorder struct {
    streamID  string
    cfg       config.DVRConfig
    storage   *storage.Service
    
    segments chan *Segment
    done     chan struct{}
}

// NewDVRRecorder creates a new DVR recorder
func NewDVRRecorder(streamID string, cfg config.DVRConfig, storage *storage.Service) *DVRRecorder {
    return &DVRRecorder{
        streamID: streamID,
        cfg:      cfg,
        storage:  storage,
        segments: make(chan *Segment, 100),
        done:     make(chan struct{}),
    }
}

// Start begins DVR recording
func (r *DVRRecorder) Start() error {
    go r.recordLoop()
    return nil
}

// Stop halts DVR recording
func (r *DVRRecorder) Stop() {
    close(r.done)
}

// RecordSegment adds a segment to DVR
func (r *DVRRecorder) RecordSegment(segment *Segment) {
    select {
    case r.segments <- segment:
    case <-r.done:
    }
}

func (r *DVRRecorder) recordLoop() {
    for {
        select {
        case segment := <-r.segments:
            r.saveSegment(segment)
        case <-r.done:
            return
        }
    }
}

func (r *DVRRecorder) saveSegment(segment *Segment) {
    // Save to DVR storage path
    timestamp := segment.Timestamp.Format("20060102-150405")
    key := fmt.Sprintf("dvr/%s/%s/segment_%d.m4s", r.streamID, timestamp, segment.Number)
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    if err := r.storage.UploadSegment(ctx, key, segment.Data); err != nil {
        log.Error().
            Err(err).
            Str("stream_id", r.streamID).
            Uint32("segment", segment.Number).
            Msg("Failed to save DVR segment")
    }
    
    // Create/update DVR manifest
    r.updateDVRManifest(segment)
}

func (r *DVRRecorder) updateDVRManifest(segment *Segment) {
    // Implementation for updating DVR playlist
    // This would maintain a separate playlist for DVR playback
}
```

## Next Steps

Continue to:
- [Part 5: Admin API and Monitoring](plan-5.md) - Control endpoints and dashboard
- [Part 6: Infrastructure and Deployment](plan-6.md) - AWS setup and Terraform
- [Part 7: Development Setup and CI/CD](plan-7.md) - Local development and GitHub Actions