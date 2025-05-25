# Phase 3: GPU-Accelerated Transcoding Pipeline

## Overview
This phase implements the core transcoding pipeline using FFmpeg with NVIDIA NVENC hardware acceleration. We'll build a robust GPU resource pool, implement efficient pipeline orchestration, and handle the transformation from 1920x1080 50Mbps HEVC to 640x360 400kbps H.264 for multi-view streaming.

## Goals
- Integrate go-astiav for FFmpeg bindings with NVENC support
- Implement GPU resource pooling and management
- Create transcoding pipeline with queuing system
- Handle HEVC to H.264 conversion with proper settings
- Extract CEA-608 captions from video streams
- Implement pipeline health monitoring and recovery
- Add transcoding metrics and performance tracking

## New Components Structure
```
internal/
├── transcoding/
│   ├── ffmpeg/
│   │   ├── codec.go            # Codec initialization and management
│   │   ├── context.go          # FFmpeg context wrapper
│   │   └── options.go          # Encoding options and profiles
│   ├── gpu/
│   │   ├── pool.go             # GPU resource pool
│   │   ├── device.go           # GPU device management
│   │   └── monitor.go          # GPU health monitoring
│   ├── pipeline/
│   │   ├── pipeline.go         # Transcoding pipeline orchestration
│   │   ├── stage.go            # Pipeline stages (decode/filter/encode)
│   │   └── queue.go            # Job queue management
│   ├── caption/
│   │   ├── extractor.go        # CEA-608 caption extraction
│   │   └── converter.go        # Caption format conversion
│   └── manager.go              # Transcoding manager
pkg/
└── nvenc/
    ├── params.go               # NVENC parameter helpers
    └── profiles.go             # Encoding profiles
```

## Implementation Details

### 1. Updated Configuration
```go
// internal/config/config.go (additions)
type Config struct {
    // ... existing fields ...
    Transcoding TranscodingConfig `mapstructure:"transcoding"`
}

type TranscodingConfig struct {
    GPU          GPUConfig          `mapstructure:"gpu"`
    Pipeline     PipelineConfig     `mapstructure:"pipeline"`
    Output       OutputConfig       `mapstructure:"output"`
    Caption      CaptionConfig      `mapstructure:"caption"`
}

type GPUConfig struct {
    Devices            []int         `mapstructure:"devices"`          // GPU indices to use
    MaxEncodersPerGPU  int           `mapstructure:"max_encoders_per_gpu"`
    MemoryReserve      int64         `mapstructure:"memory_reserve"`   // MB to keep free
    HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
    RecoveryTimeout    time.Duration `mapstructure:"recovery_timeout"`
}

type PipelineConfig struct {
    MaxConcurrent      int           `mapstructure:"max_concurrent"`
    QueueSize          int           `mapstructure:"queue_size"`
    WorkerCount        int           `mapstructure:"worker_count"`
    JobTimeout         time.Duration `mapstructure:"job_timeout"`
    RetryAttempts      int           `mapstructure:"retry_attempts"`
    RetryDelay         time.Duration `mapstructure:"retry_delay"`
}

type OutputConfig struct {
    VideoCodec         string        `mapstructure:"video_codec"`      // h264_nvenc
    VideoProfile       string        `mapstructure:"video_profile"`    // baseline
    VideoPreset        string        `mapstructure:"video_preset"`     // p4 (balanced)
    Width              int           `mapstructure:"width"`            // 640
    Height             int           `mapstructure:"height"`           // 360
    Bitrate            string        `mapstructure:"bitrate"`          // 400k
    MaxBitrate         string        `mapstructure:"max_bitrate"`      // 450k
    BufferSize         string        `mapstructure:"buffer_size"`      // 800k
    FrameRate          int           `mapstructure:"frame_rate"`       // 30
    KeyframeInterval   int           `mapstructure:"keyframe_interval"` // 15 (0.5s at 30fps)
    AudioCodec         string        `mapstructure:"audio_codec"`      // aac
    AudioBitrate       string        `mapstructure:"audio_bitrate"`    // 64k
    AudioSampleRate    int           `mapstructure:"audio_sample_rate"` // 44100
}

type CaptionConfig struct {
    ExtractCEA608      bool          `mapstructure:"extract_cea608"`
    OutputFormat       string        `mapstructure:"output_format"`     // webvtt
    BufferSize         int           `mapstructure:"buffer_size"`
}
```

### 2. GPU Resource Pool Implementation
```go
// internal/transcoding/gpu/device.go
package gpu

import (
    "fmt"
    "sync"
    "time"
    
    "github.com/NVIDIA/go-nvml/pkg/nvml"
    "github.com/sirupsen/logrus"
)

type Device struct {
    Index          int
    UUID           string
    Name           string
    MemoryTotal    uint64
    MemoryFree     uint64
    EncoderCount   int
    ActiveSessions int
    Temperature    int
    Utilization    int
    
    mu             sync.RWMutex
    healthy        bool
    lastCheck      time.Time
}

func NewDevice(index int) (*Device, error) {
    ret := nvml.Init()
    if ret != nvml.SUCCESS {
        return nil, fmt.Errorf("failed to initialize NVML: %v", nvml.ErrorString(ret))
    }
    
    device, ret := nvml.DeviceGetHandleByIndex(index)
    if ret != nvml.SUCCESS {
        return nil, fmt.Errorf("failed to get device %d: %v", index, nvml.ErrorString(ret))
    }
    
    name, _ := device.GetName()
    uuid, _ := device.GetUUID()
    memory, _ := device.GetMemoryInfo()
    
    return &Device{
        Index:       index,
        UUID:        uuid,
        Name:        name,
        MemoryTotal: memory.Total,
        MemoryFree:  memory.Free,
        healthy:     true,
        lastCheck:   time.Now(),
    }, nil
}

func (d *Device) UpdateStats() error {
    d.mu.Lock()
    defer d.mu.Unlock()
    
    device, ret := nvml.DeviceGetHandleByIndex(d.Index)
    if ret != nvml.SUCCESS {
        d.healthy = false
        return fmt.Errorf("failed to get device handle: %v", nvml.ErrorString(ret))
    }
    
    // Memory info
    memory, ret := device.GetMemoryInfo()
    if ret == nvml.SUCCESS {
        d.MemoryFree = memory.Free
    }
    
    // Temperature
    temp, ret := device.GetTemperature(nvml.TEMPERATURE_GPU)
    if ret == nvml.SUCCESS {
        d.Temperature = int(temp)
    }
    
    // Utilization
    utilization, ret := device.GetUtilizationRates()
    if ret == nvml.SUCCESS {
        d.Utilization = int(utilization.Gpu)
    }
    
    // Encoder sessions
    sessions, _, ret := device.GetEncoderSessions()
    if ret == nvml.SUCCESS {
        d.ActiveSessions = int(sessions)
    }
    
    d.healthy = true
    d.lastCheck = time.Now()
    
    return nil
}

// internal/transcoding/gpu/pool.go
package gpu

import (
    "context"
    "errors"
    "fmt"
    "sync"
    "time"
    
    "github.com/sirupsen/logrus"
)

var (
    ErrNoAvailableGPU = errors.New("no available GPU")
    ErrGPUNotHealthy  = errors.New("GPU not healthy")
)

type Pool struct {
    devices         []*Device
    config          *GPUConfig
    logger          *logrus.Logger
    
    allocations     map[string]int // streamID -> deviceIndex
    mu              sync.RWMutex
    
    ctx             context.Context
    cancel          context.CancelFunc
}

func NewPool(config *GPUConfig, logger *logrus.Logger) (*Pool, error) {
    ctx, cancel := context.WithCancel(context.Background())
    
    pool := &Pool{
        config:      config,
        logger:      logger,
        devices:     make([]*Device, 0),
        allocations: make(map[string]int),
        ctx:         ctx,
        cancel:      cancel,
    }
    
    // Initialize devices
    for _, idx := range config.Devices {
        device, err := NewDevice(idx)
        if err != nil {
            logger.Warnf("Failed to initialize GPU %d: %v", idx, err)
            continue
        }
        pool.devices = append(pool.devices, device)
        logger.Infof("Initialized GPU %d: %s (Memory: %d MB)", 
            idx, device.Name, device.MemoryTotal/(1024*1024))
    }
    
    if len(pool.devices) == 0 {
        return nil, errors.New("no GPUs available")
    }
    
    // Start monitoring
    go pool.monitor()
    
    return pool, nil
}

func (p *Pool) Allocate(streamID string) (int, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    // Check if already allocated
    if deviceIdx, exists := p.allocations[streamID]; exists {
        return deviceIdx, nil
    }
    
    // Find best device
    var bestDevice *Device
    var bestIndex int
    minSessions := int(^uint(0) >> 1) // Max int
    
    for i, device := range p.devices {
        device.mu.RLock()
        healthy := device.healthy
        sessions := device.ActiveSessions
        memFree := device.MemoryFree
        device.mu.RUnlock()
        
        if !healthy {
            continue
        }
        
        // Check memory
        requiredMem := uint64(p.config.MemoryReserve * 1024 * 1024)
        if memFree < requiredMem {
            continue
        }
        
        // Check encoder limit
        if sessions >= p.config.MaxEncodersPerGPU {
            continue
        }
        
        // Select device with least sessions
        if sessions < minSessions {
            minSessions = sessions
            bestDevice = device
            bestIndex = i
        }
    }
    
    if bestDevice == nil {
        return -1, ErrNoAvailableGPU
    }
    
    // Allocate
    p.allocations[streamID] = bestIndex
    bestDevice.mu.Lock()
    bestDevice.ActiveSessions++
    bestDevice.mu.Unlock()
    
    p.logger.Infof("Allocated GPU %d for stream %s (sessions: %d)", 
        bestDevice.Index, streamID, bestDevice.ActiveSessions)
    
    return bestDevice.Index, nil
}

func (p *Pool) Release(streamID string) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    deviceIdx, exists := p.allocations[streamID]
    if !exists {
        return
    }
    
    delete(p.allocations, streamID)
    
    if deviceIdx < len(p.devices) {
        device := p.devices[deviceIdx]
        device.mu.Lock()
        device.ActiveSessions--
        device.mu.Unlock()
        
        p.logger.Infof("Released GPU %d for stream %s (sessions: %d)", 
            device.Index, streamID, device.ActiveSessions)
    }
}

func (p *Pool) monitor() {
    ticker := time.NewTicker(p.config.HealthCheckInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-p.ctx.Done():
            return
        case <-ticker.C:
            for _, device := range p.devices {
                if err := device.UpdateStats(); err != nil {
                    p.logger.Errorf("Failed to update GPU %d stats: %v", device.Index, err)
                }
                
                // Log warnings
                device.mu.RLock()
                if device.Temperature > 85 {
                    p.logger.Warnf("GPU %d temperature high: %d°C", device.Index, device.Temperature)
                }
                if device.Utilization > 90 {
                    p.logger.Warnf("GPU %d utilization high: %d%%", device.Index, device.Utilization)
                }
                device.mu.RUnlock()
            }
        }
    }
}
```

### 3. FFmpeg Integration with go-astiav
```go
// internal/transcoding/ffmpeg/context.go
package ffmpeg

import (
    "fmt"
    "sync"
    
    "github.com/asticode/go-astiav"
    "github.com/sirupsen/logrus"
)

type Context struct {
    streamID        string
    gpuIndex        int
    
    // Input
    inputFormatCtx  *astiav.FormatContext
    videoDecoder    *astiav.CodecContext
    audioDecoder    *astiav.CodecContext
    videoStreamIdx  int
    audioStreamIdx  int
    
    // Output
    outputFormatCtx *astiav.FormatContext
    videoEncoder    *astiav.CodecContext
    audioEncoder    *astiav.CodecContext
    
    // Filters
    filterGraph     *astiav.FilterGraph
    bufferSrc       *astiav.FilterContext
    bufferSink      *astiav.FilterContext
    
    logger          *logrus.Logger
    mu              sync.Mutex
}

func NewContext(streamID string, gpuIndex int, logger *logrus.Logger) *Context {
    return &Context{
        streamID:       streamID,
        gpuIndex:       gpuIndex,
        logger:         logger,
        videoStreamIdx: -1,
        audioStreamIdx: -1,
    }
}

func (c *Context) InitializeInput(buffer *buffer.RingBuffer) error {
    // Create custom IO context for ring buffer
    ioCtx := astiav.AllocIOContext(
        4096, // buffer size
        false, // write flag
        buffer, // opaque
        readPacket, // read callback
        nil, // write callback
        nil, // seek callback
    )
    
    // Allocate format context
    c.inputFormatCtx = astiav.AllocFormatContext()
    c.inputFormatCtx.SetIOContext(ioCtx)
    
    // Open input
    if err := c.inputFormatCtx.OpenInput("", nil, nil); err != nil {
        return fmt.Errorf("failed to open input: %w", err)
    }
    
    // Find stream info
    if err := c.inputFormatCtx.FindStreamInfo(nil); err != nil {
        return fmt.Errorf("failed to find stream info: %w", err)
    }
    
    // Find video and audio streams
    for i, stream := range c.inputFormatCtx.Streams() {
        codecParams := stream.CodecParameters()
        
        switch codecParams.MediaType() {
        case astiav.MediaTypeVideo:
            if c.videoStreamIdx == -1 {
                c.videoStreamIdx = i
                
                // Initialize video decoder
                codec := astiav.FindDecoder(codecParams.CodecID())
                if codec == nil {
                    return fmt.Errorf("video codec not found: %v", codecParams.CodecID())
                }
                
                c.videoDecoder = astiav.AllocCodecContext(codec)
                if err := codecParams.ToCodecContext(c.videoDecoder); err != nil {
                    return fmt.Errorf("failed to copy codec params: %w", err)
                }
                
                // Set hardware acceleration
                c.videoDecoder.SetHardwareDeviceType(astiav.HardwareDeviceTypeCUDA)
                
                if err := c.videoDecoder.Open(codec, nil); err != nil {
                    return fmt.Errorf("failed to open video decoder: %w", err)
                }
            }
            
        case astiav.MediaTypeAudio:
            if c.audioStreamIdx == -1 {
                c.audioStreamIdx = i
                
                // Initialize audio decoder
                codec := astiav.FindDecoder(codecParams.CodecID())
                if codec == nil {
                    return fmt.Errorf("audio codec not found: %v", codecParams.CodecID())
                }
                
                c.audioDecoder = astiav.AllocCodecContext(codec)
                if err := codecParams.ToCodecContext(c.audioDecoder); err != nil {
                    return fmt.Errorf("failed to copy codec params: %w", err)
                }
                
                if err := c.audioDecoder.Open(codec, nil); err != nil {
                    return fmt.Errorf("failed to open audio decoder: %w", err)
                }
            }
        }
    }
    
    if c.videoStreamIdx == -1 {
        return fmt.Errorf("no video stream found")
    }
    
    c.logger.Infof("Input initialized for stream %s: video=%d, audio=%d", 
        c.streamID, c.videoStreamIdx, c.audioStreamIdx)
    
    return nil
}

// internal/transcoding/ffmpeg/codec.go
package ffmpeg

import (
    "fmt"
    
    "github.com/asticode/go-astiav"
)

func (c *Context) InitializeOutput(config *OutputConfig) error {
    // Create output format context
    c.outputFormatCtx = astiav.AllocFormatContext()
    
    // Initialize video encoder
    videoCodec := astiav.FindEncoderByName(config.VideoCodec)
    if videoCodec == nil {
        return fmt.Errorf("video codec %s not found", config.VideoCodec)
    }
    
    c.videoEncoder = astiav.AllocCodecContext(videoCodec)
    
    // Set video encoding parameters
    c.videoEncoder.SetWidth(config.Width)
    c.videoEncoder.SetHeight(config.Height)
    c.videoEncoder.SetPixelFormat(astiav.PixelFormatYUV420P)
    c.videoEncoder.SetTimeBase(astiav.NewRational(1, config.FrameRate))
    c.videoEncoder.SetFramerate(astiav.NewRational(config.FrameRate, 1))
    c.videoEncoder.SetGOPSize(config.KeyframeInterval)
    c.videoEncoder.SetMaxBFrames(0) // No B-frames for low latency
    
    // Set bitrate
    bitrate, _ := parseBitrate(config.Bitrate)
    c.videoEncoder.SetBitRate(bitrate)
    
    maxBitrate, _ := parseBitrate(config.MaxBitrate)
    c.videoEncoder.SetRCMaxRate(maxBitrate)
    
    bufferSize, _ := parseBitrate(config.BufferSize)
    c.videoEncoder.SetRCBufferSize(bufferSize)
    
    // Set NVENC options
    options := map[string]string{
        "preset":     config.VideoPreset,
        "profile":    config.VideoProfile,
        "gpu":        fmt.Sprintf("%d", c.gpuIndex),
        "rc":         "cbr", // Constant bitrate for streaming
        "rc-lookahead": "0", // Disable lookahead for low latency
        "surfaces":   "8",   // Number of surfaces
        "delay":      "0",   // Zero delay
        "forced-idr": "1",   // Force IDR frames
    }
    
    if err := c.videoEncoder.Open(videoCodec, options); err != nil {
        return fmt.Errorf("failed to open video encoder: %w", err)
    }
    
    // Initialize audio encoder if present
    if c.audioStreamIdx != -1 {
        audioCodec := astiav.FindEncoderByName(config.AudioCodec)
        if audioCodec == nil {
            return fmt.Errorf("audio codec %s not found", config.AudioCodec)
        }
        
        c.audioEncoder = astiav.AllocCodecContext(audioCodec)
        
        // Set audio encoding parameters
        c.audioEncoder.SetSampleFormat(astiav.SampleFormatFLTP)
        c.audioEncoder.SetSampleRate(config.AudioSampleRate)
        c.audioEncoder.SetChannelLayout(astiav.ChannelLayoutStereo)
        c.audioEncoder.SetChannels(2)
        
        audioBitrate, _ := parseBitrate(config.AudioBitrate)
        c.audioEncoder.SetBitRate(audioBitrate)
        
        if err := c.audioEncoder.Open(audioCodec, nil); err != nil {
            return fmt.Errorf("failed to open audio encoder: %w", err)
        }
    }
    
    return nil
}

func (c *Context) InitializeFilters() error {
    // Create filter graph
    c.filterGraph = astiav.AllocFilterGraph()
    
    // Create buffer source
    bufferSrc := astiav.FindFilterByName("buffer")
    if bufferSrc == nil {
        return fmt.Errorf("buffer filter not found")
    }
    
    args := fmt.Sprintf("video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=%d/%d",
        c.videoDecoder.Width(), c.videoDecoder.Height(),
        c.videoDecoder.PixelFormat(),
        c.videoDecoder.TimeBase().Num(), c.videoDecoder.TimeBase().Den(),
        c.videoDecoder.SampleAspectRatio().Num(), c.videoDecoder.SampleAspectRatio().Den())
    
    c.bufferSrc = c.filterGraph.NewFilterContext(bufferSrc, "in", args)
    
    // Create buffer sink
    bufferSink := astiav.FindFilterByName("buffersink")
    if bufferSink == nil {
        return fmt.Errorf("buffersink filter not found")
    }
    
    c.bufferSink = c.filterGraph.NewFilterContext(bufferSink, "out", "")
    
    // Create filter chain: scale + format
    outputs := c.bufferSrc.Outputs()[0]
    inputs := c.bufferSink.Inputs()[0]
    
    filterSpec := fmt.Sprintf(
        "scale=%d:%d:flags=fast_bilinear,format=pix_fmts=yuv420p",
        c.videoEncoder.Width(), c.videoEncoder.Height())
    
    if err := c.filterGraph.Parse(filterSpec, inputs, outputs); err != nil {
        return fmt.Errorf("failed to parse filter graph: %w", err)
    }
    
    if err := c.filterGraph.Configure(); err != nil {
        return fmt.Errorf("failed to configure filter graph: %w", err)
    }
    
    return nil
}
```

### 4. Transcoding Pipeline
```go
// internal/transcoding/pipeline/pipeline.go
package pipeline

import (
    "context"
    "fmt"
    "sync"
    "time"
    
    "github.com/asticode/go-astiav"
    "github.com/sirupsen/logrus"
)

type Pipeline struct {
    streamID    string
    input       *buffer.RingBuffer
    output      chan *Segment
    
    ctx         context.Context
    cancel      context.CancelFunc
    
    ffmpeg      *ffmpeg.Context
    gpu         *gpu.Pool
    config      *PipelineConfig
    logger      *logrus.Logger
    
    stats       PipelineStats
    mu          sync.RWMutex
}

type PipelineStats struct {
    FramesProcessed uint64
    FramesDropped   uint64
    BytesProcessed  uint64
    StartTime       time.Time
    LastFrameTime   time.Time
    EncodingSpeed   float64
}

type Segment struct {
    StreamID    string
    SegmentNum  int
    Duration    time.Duration
    Data        []byte
    Keyframe    bool
    PTS         int64
    DTS         int64
}

func NewPipeline(streamID string, input *buffer.RingBuffer, gpu *gpu.Pool, config *PipelineConfig, logger *logrus.Logger) *Pipeline {
    ctx, cancel := context.WithCancel(context.Background())
    
    return &Pipeline{
        streamID: streamID,
        input:    input,
        output:   make(chan *Segment, 100),
        ctx:      ctx,
        cancel:   cancel,
        gpu:      gpu,
        config:   config,
        logger:   logger,
        stats: PipelineStats{
            StartTime: time.Now(),
        },
    }
}

func (p *Pipeline) Start() error {
    // Allocate GPU
    gpuIndex, err := p.gpu.Allocate(p.streamID)
    if err != nil {
        return fmt.Errorf("failed to allocate GPU: %w", err)
    }
    
    // Initialize FFmpeg context
    p.ffmpeg = ffmpeg.NewContext(p.streamID, gpuIndex, p.logger)
    
    // Initialize input
    if err := p.ffmpeg.InitializeInput(p.input); err != nil {
        p.gpu.Release(p.streamID)
        return fmt.Errorf("failed to initialize input: %w", err)
    }
    
    // Initialize output
    if err := p.ffmpeg.InitializeOutput(&p.config.Output); err != nil {
        p.gpu.Release(p.streamID)
        return fmt.Errorf("failed to initialize output: %w", err)
    }
    
    // Initialize filters
    if err := p.ffmpeg.InitializeFilters(); err != nil {
        p.gpu.Release(p.streamID)
        return fmt.Errorf("failed to initialize filters: %w", err)
    }
    
    // Start processing
    go p.process()
    
    p.logger.Infof("Pipeline started for stream %s on GPU %d", p.streamID, gpuIndex)
    
    return nil
}

func (p *Pipeline) process() {
    defer p.cleanup()
    
    packet := astiav.AllocPacket()
    defer packet.Free()
    
    frame := astiav.AllocFrame()
    defer frame.Free()
    
    filteredFrame := astiav.AllocFrame()
    defer filteredFrame.Free()
    
    for {
        select {
        case <-p.ctx.Done():
            return
        default:
            // Read packet from input
            if err := p.ffmpeg.inputFormatCtx.ReadFrame(packet); err != nil {
                if err == astiav.ErrEOF {
                    p.logger.Infof("End of stream for %s", p.streamID)
                    return
                }
                p.logger.Errorf("Failed to read packet: %v", err)
                continue
            }
            
            // Process based on stream type
            if packet.StreamIndex() == p.ffmpeg.videoStreamIdx {
                p.processVideoPacket(packet, frame, filteredFrame)
            } else if packet.StreamIndex() == p.ffmpeg.audioStreamIdx {
                p.processAudioPacket(packet, frame)
            }
            
            packet.Unref()
        }
    }
}

func (p *Pipeline) processVideoPacket(packet *astiav.Packet, frame, filteredFrame *astiav.Frame) {
    // Send packet to decoder
    if err := p.ffmpeg.videoDecoder.SendPacket(packet); err != nil {
        p.logger.Warnf("Failed to send packet to decoder: %v", err)
        return
    }
    
    // Receive frames from decoder
    for {
        if err := p.ffmpeg.videoDecoder.ReceiveFrame(frame); err != nil {
            if err == astiav.ErrEOF || err == astiav.ErrEAGAIN {
                break
            }
            p.logger.Errorf("Failed to receive frame: %v", err)
            return
        }
        
        // Send frame to filter
        if err := p.ffmpeg.bufferSrc.BufferSrcAddFrame(frame, astiav.BufferSrcFlagKeepRef); err != nil {
            p.logger.Errorf("Failed to add frame to filter: %v", err)
            frame.Unref()
            continue
        }
        
        // Get filtered frame
        for {
            if err := p.ffmpeg.bufferSink.BufferSinkGetFrame(filteredFrame); err != nil {
                if err == astiav.ErrEOF || err == astiav.ErrEAGAIN {
                    break
                }
                p.logger.Errorf("Failed to get filtered frame: %v", err)
                break
            }
            
            // Send to encoder
            if err := p.ffmpeg.videoEncoder.SendFrame(filteredFrame); err != nil {
                p.logger.Errorf("Failed to send frame to encoder: %v", err)
                filteredFrame.Unref()
                continue
            }
            
            // Receive encoded packets
            p.receiveEncodedPackets()
            
            filteredFrame.Unref()
        }
        
        frame.Unref()
        
        // Update stats
        p.mu.Lock()
        p.stats.FramesProcessed++
        p.stats.LastFrameTime = time.Now()
        p.mu.Unlock()
    }
}

func (p *Pipeline) receiveEncodedPackets() {
    packet := astiav.AllocPacket()
    defer packet.Free()
    
    for {
        if err := p.ffmpeg.videoEncoder.ReceivePacket(packet); err != nil {
            if err == astiav.ErrEOF || err == astiav.ErrEAGAIN {
                break
            }
            p.logger.Errorf("Failed to receive packet from encoder: %v", err)
            return
        }
        
        // Create segment
        segment := &Segment{
            StreamID:   p.streamID,
            SegmentNum: int(packet.PTS() / int64(p.config.Output.KeyframeInterval)),
            Data:       make([]byte, packet.Size()),
            Keyframe:   packet.Flags()&astiav.PacketFlagKey != 0,
            PTS:        packet.PTS(),
            DTS:        packet.DTS(),
        }
        
        copy(segment.Data, packet.Data())
        
        select {
        case p.output <- segment:
        case <-p.ctx.Done():
            return
        default:
            p.logger.Warn("Output channel full, dropping segment")
            p.mu.Lock()
            p.stats.FramesDropped++
            p.mu.Unlock()
        }
        
        packet.Unref()
    }
}

func (p *Pipeline) cleanup() {
    if p.ffmpeg != nil {
        p.ffmpeg.Close()
    }
    p.gpu.Release(p.streamID)
    close(p.output)
}
```

### 5. Caption Extraction
```go
// internal/transcoding/caption/extractor.go
package caption

import (
    "bytes"
    "encoding/binary"
    "fmt"
    "time"
)

type CEA608Extractor struct {
    streamID string
    output   chan *Caption
    buffer   bytes.Buffer
}

type Caption struct {
    StreamID  string
    Timestamp time.Duration
    Text      string
    Type      string // cea608, cea708
}

func NewCEA608Extractor(streamID string) *CEA608Extractor {
    return &CEA608Extractor{
        streamID: streamID,
        output:   make(chan *Caption, 100),
    }
}

func (e *CEA608Extractor) ExtractFromSEI(seiData []byte, pts int64) error {
    // Parse SEI NAL unit for CEA-608 data
    reader := bytes.NewReader(seiData)
    
    // Skip SEI header
    var seiType uint8
    binary.Read(reader, binary.BigEndian, &seiType)
    
    if seiType != 4 { // user_data_registered_itu_t_t35
        return nil
    }
    
    // Read payload size
    payloadSize, err := readUEV(reader)
    if err != nil {
        return err
    }
    
    // Read ITU-T T.35 header
    var countryCode uint8
    binary.Read(reader, binary.BigEndian, &countryCode)
    
    if countryCode == 0xB5 { // USA
        var providerCode uint16
        binary.Read(reader, binary.BigEndian, &providerCode)
        
        if providerCode == 0x0031 { // ATSC
            // Read user identifier
            var userID uint32
            binary.Read(reader, binary.BigEndian, &userID)
            
            if userID == 0x47413934 { // "GA94"
                // Read user data type
                var userDataType uint8
                binary.Read(reader, binary.BigEndian, &userDataType)
                
                if userDataType == 0x03 { // cc_data
                    return e.parseCCData(reader, pts)
                }
            }
        }
    }
    
    return nil
}

func (e *CEA608Extractor) parseCCData(reader *bytes.Reader, pts int64) error {
    // Read cc_count
    var ccCount uint8
    binary.Read(reader, binary.BigEndian, &ccCount)
    ccCount &= 0x1F // 5 bits
    
    // Process each CC packet
    for i := 0; i < int(ccCount); i++ {
        var ccData [3]byte
        reader.Read(ccData[:])
        
        valid := ccData[0]&0x04 != 0
        ccType := ccData[0] & 0x03
        
        if valid && ccType == 0 { // CEA-608 field 1
            char1 := ccData[1] & 0x7F
            char2 := ccData[2] & 0x7F
            
            // Process CEA-608 characters
            if char1 >= 0x20 {
                e.buffer.WriteByte(char1)
            }
            if char2 >= 0x20 {
                e.buffer.WriteByte(char2)
            }
            
            // Check for control codes (line break, etc.)
            if char1 == 0x14 && char2 == 0x2C { // EDM (end of caption)
                caption := &Caption{
                    StreamID:  e.streamID,
                    Timestamp: time.Duration(pts) * time.Millisecond,
                    Text:      e.buffer.String(),
                    Type:      "cea608",
                }
                
                select {
                case e.output <- caption:
                default:
                    // Drop if channel full
                }
                
                e.buffer.Reset()
            }
        }
    }
    
    return nil
}
```

### 6. Transcoding Manager
```go
// internal/transcoding/manager.go
package transcoding

import (
    "context"
    "fmt"
    "sync"
    
    "github.com/sirupsen/logrus"
)

type Manager struct {
    config      *TranscodingConfig
    gpu         *gpu.Pool
    pipelines   sync.Map // streamID -> *Pipeline
    queue       *JobQueue
    logger      *logrus.Logger
    
    wg          sync.WaitGroup
    ctx         context.Context
    cancel      context.CancelFunc
}

type Job struct {
    StreamID    string
    Input       *buffer.RingBuffer
    Priority    int
    CreatedAt   time.Time
}

func NewManager(config *TranscodingConfig, logger *logrus.Logger) (*Manager, error) {
    ctx, cancel := context.WithCancel(context.Background())
    
    // Initialize GPU pool
    gpuPool, err := gpu.NewPool(&config.GPU, logger)
    if err != nil {
        return nil, fmt.Errorf("failed to initialize GPU pool: %w", err)
    }
    
    m := &Manager{
        config: config,
        gpu:    gpuPool,
        queue:  NewJobQueue(config.Pipeline.QueueSize),
        logger: logger,
        ctx:    ctx,
        cancel: cancel,
    }
    
    // Start workers
    for i := 0; i < config.Pipeline.WorkerCount; i++ {
        m.wg.Add(1)
        go m.worker(i)
    }
    
    return m, nil
}

func (m *Manager) StartTranscoding(streamID string, input *buffer.RingBuffer) (<-chan *Segment, error) {
    // Check if already transcoding
    if _, exists := m.pipelines.Load(streamID); exists {
        return nil, fmt.Errorf("stream %s already being transcoded", streamID)
    }
    
    // Create pipeline
    pipeline := pipeline.NewPipeline(streamID, input, m.gpu, &m.config.Pipeline, m.logger)
    
    // Start pipeline
    if err := pipeline.Start(); err != nil {
        return nil, fmt.Errorf("failed to start pipeline: %w", err)
    }
    
    // Store pipeline
    m.pipelines.Store(streamID, pipeline)
    
    // Monitor pipeline
    m.wg.Add(1)
    go m.monitorPipeline(streamID, pipeline)
    
    return pipeline.output, nil
}

func (m *Manager) StopTranscoding(streamID string) error {
    value, exists := m.pipelines.Load(streamID)
    if !exists {
        return fmt.Errorf("stream %s not found", streamID)
    }
    
    pipeline := value.(*pipeline.Pipeline)
    pipeline.Stop()
    
    m.pipelines.Delete(streamID)
    
    return nil
}

func (m *Manager) monitorPipeline(streamID string, p *pipeline.Pipeline) {
    defer m.wg.Done()
    
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-m.ctx.Done():
            return
        case <-p.Done():
            m.pipelines.Delete(streamID)
            m.logger.Infof("Pipeline for stream %s completed", streamID)
            return
        case <-ticker.C:
            stats := p.Stats()
            m.logger.WithFields(logrus.Fields{
                "stream_id":        streamID,
                "frames_processed": stats.FramesProcessed,
                "frames_dropped":   stats.FramesDropped,
                "encoding_speed":   stats.EncodingSpeed,
            }).Info("Pipeline stats")
        }
    }
}

func (m *Manager) GetStats() map[string]interface{} {
    stats := make(map[string]interface{})
    
    // GPU stats
    gpuStats := m.gpu.Stats()
    stats["gpu"] = gpuStats
    
    // Pipeline stats
    pipelineStats := make(map[string]interface{})
    m.pipelines.Range(func(key, value interface{}) bool {
        streamID := key.(string)
        pipeline := value.(*pipeline.Pipeline)
        pipelineStats[streamID] = pipeline.Stats()
        return true
    })
    stats["pipelines"] = pipelineStats
    
    return stats
}
```

### 7. Updated Configuration
```yaml
# configs/default.yaml (additions)
transcoding:
  gpu:
    devices: [0, 1]              # Use GPU 0 and 1
    max_encoders_per_gpu: 15     # Max concurrent encoders per GPU
    memory_reserve: 1024         # Keep 1GB free
    health_check_interval: 10s
    recovery_timeout: 30s
    
  pipeline:
    max_concurrent: 25           # Max concurrent transcoding jobs
    queue_size: 50               # Job queue size
    worker_count: 4              # Number of worker goroutines
    job_timeout: 5m              # Max time for a job
    retry_attempts: 3
    retry_delay: 5s
    
  output:
    video_codec: "h264_nvenc"    # NVIDIA hardware encoder
    video_profile: "baseline"    # H.264 baseline profile
    video_preset: "p4"           # P4 preset (balanced)
    width: 640
    height: 360
    bitrate: "400k"
    max_bitrate: "450k"
    buffer_size: "800k"
    frame_rate: 30
    keyframe_interval: 15        # 0.5s at 30fps
    audio_codec: "aac"
    audio_bitrate: "64k"
    audio_sample_rate: 44100
    
  caption:
    extract_cea608: true
    output_format: "webvtt"
    buffer_size: 1024
```

## Testing Requirements

### Unit Tests
- GPU device initialization and stats
- FFmpeg context creation
- Filter graph setup
- Caption extraction from SEI data
- Pipeline state management

### Integration Tests
- Full transcoding pipeline with test video
- GPU allocation and release
- Multiple concurrent pipelines
- Error recovery and retry
- Memory leak detection

### Performance Tests
- 25 concurrent HEVC streams transcoding
- GPU utilization monitoring
- Memory usage tracking
- Latency measurements
- Throughput validation

## Monitoring Metrics

### Prometheus Metrics
```go
// Transcoding metrics
transcoding_pipelines_active{gpu_index}
transcoding_frames_processed_total{stream_id}
transcoding_frames_dropped_total{stream_id}
transcoding_encoding_speed{stream_id}
transcoding_latency_seconds{stream_id, stage}

// GPU metrics
gpu_utilization_percent{index}
gpu_memory_used_bytes{index}
gpu_temperature_celsius{index}
gpu_encoder_sessions{index}
gpu_errors_total{index, error_type}
```

## Deliverables
1. Complete FFmpeg/NVENC integration with go-astiav
2. GPU resource pool with health monitoring
3. Transcoding pipeline with queuing system
4. CEA-608 caption extraction
5. Comprehensive test suite
6. Performance benchmarks
7. Prometheus metrics
8. Documentation for GPU setup

## Success Criteria
- Transcode 25 concurrent 50Mbps HEVC streams
- Output 640x360 400kbps H.264 streams
- Less than 100ms transcoding latency
- GPU utilization between 70-85%
- Zero dropped frames under normal load
- Automatic recovery from GPU errors
- Memory usage stable over 24 hours
- All tests passing

## Next Phase Preview
Phase 4 will implement the HLS packaging layer with CMAF segmentation, LL-HLS playlist generation, HTTP/3 push support, and local cache management.
