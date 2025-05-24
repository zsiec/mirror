# Phase 4: Low-Latency HLS Packaging and HTTP/3 Delivery

## Overview
This phase implements the HLS packaging layer with CMAF (Common Media Application Format) segmentation for ultra-low latency streaming. We'll create LL-HLS compliant playlists, implement HTTP/3 server push for predictive segment delivery, and build an efficient local cache system using memory-mapped files.

## Goals
- Implement CMAF segmenter producing 500ms segments with 100ms chunks
- Generate LL-HLS compliant playlists with partial segment support
- Build HTTP/3 server with push capabilities for segment preloading
- Create memory-mapped file cache for zero-copy segment serving
- Implement blocking playlist reload (_HLS_msn parameter support)
- Add DVR functionality with configurable retention
- Create segment upload pipeline to S3/CDN

## New Components Structure
```
internal/
├── packaging/
│   ├── cmaf/
│   │   ├── segmenter.go        # CMAF segmentation logic
│   │   ├── chunk.go            # Chunk management
│   │   └── metadata.go         # Segment metadata
│   ├── hls/
│   │   ├── playlist.go         # LL-HLS playlist generation
│   │   ├── master.go           # Master playlist management
│   │   ├── tags.go             # Custom HLS tags
│   │   └── validator.go        # Playlist validation
│   ├── cache/
│   │   ├── mmap_cache.go       # Memory-mapped file cache
│   │   ├── segment_store.go    # Segment storage interface
│   │   └── eviction.go         # Cache eviction policies
│   ├── delivery/
│   │   ├── http3_server.go     # HTTP/3 server implementation
│   │   ├── push_predictor.go   # Predictive push logic
│   │   └── handlers.go         # Request handlers
│   └── manager.go              # Packaging manager
├── storage/
│   ├── s3/
│   │   ├── uploader.go         # S3 segment uploader
│   │   └── lifecycle.go        # S3 lifecycle policies
│   └── dvr/
│       ├── recorder.go         # DVR recording logic
│       └── retention.go        # Retention management
```

## Implementation Details

### 1. Updated Configuration
```go
// internal/config/config.go (additions)
type Config struct {
    // ... existing fields ...
    Packaging PackagingConfig `mapstructure:"packaging"`
    Storage   StorageConfig   `mapstructure:"storage"`
}

type PackagingConfig struct {
    CMAF     CMAFConfig     `mapstructure:"cmaf"`
    HLS      HLSConfig      `mapstructure:"hls"`
    Cache    CacheConfig    `mapstructure:"cache"`
    Delivery DeliveryConfig `mapstructure:"delivery"`
}

type CMAFConfig struct {
    SegmentDuration   time.Duration `mapstructure:"segment_duration"`    // 500ms
    ChunkDuration     time.Duration `mapstructure:"chunk_duration"`      // 100ms
    ChunksPerSegment  int          `mapstructure:"chunks_per_segment"`  // 5
    LookaheadChunks   int          `mapstructure:"lookahead_chunks"`    // 2
    InitSegmentSize   int          `mapstructure:"init_segment_size"`   // bytes
}

type HLSConfig struct {
    PlaylistWindow    int           `mapstructure:"playlist_window"`     // segments
    PartHoldBack      float64       `mapstructure:"part_hold_back"`      // 0.3s
    CanBlockReload    bool          `mapstructure:"can_block_reload"`    // true
    CanSkipUntil      float64       `mapstructure:"can_skip_until"`      // 6.0s
    MaxSkippedSegments int          `mapstructure:"max_skipped_segments"`
    PlaylistType      string        `mapstructure:"playlist_type"`       // EVENT or VOD
}

type CacheConfig struct {
    MaxSize           int64         `mapstructure:"max_size"`            // bytes
    SegmentTTL        time.Duration `mapstructure:"segment_ttl"`
    ChunkTTL          time.Duration `mapstructure:"chunk_ttl"`
    MmapEnabled       bool          `mapstructure:"mmap_enabled"`
    PreloadCount      int           `mapstructure:"preload_count"`       // segments to preload
}

type DeliveryConfig struct {
    HTTP3Enabled      bool          `mapstructure:"http3_enabled"`
    PushEnabled       bool          `mapstructure:"push_enabled"`
    MaxPushStreams    int           `mapstructure:"max_push_streams"`
    PushTimeout       time.Duration `mapstructure:"push_timeout"`
    CORSEnabled       bool          `mapstructure:"cors_enabled"`
    CORSOrigins       []string      `mapstructure:"cors_origins"`
}

type StorageConfig struct {
    S3       S3Config       `mapstructure:"s3"`
    DVR      DVRConfig      `mapstructure:"dvr"`
}

type S3Config struct {
    Enabled           bool          `mapstructure:"enabled"`
    Bucket            string        `mapstructure:"bucket"`
    Region            string        `mapstructure:"region"`
    Prefix            string        `mapstructure:"prefix"`
    Endpoint          string        `mapstructure:"endpoint"`         // For MinIO
    AccessKey         string        `mapstructure:"access_key"`
    SecretKey         string        `mapstructure:"secret_key"`
    UploadConcurrency int           `mapstructure:"upload_concurrency"`
    PartSize          int64         `mapstructure:"part_size"`        // bytes
}

type DVRConfig struct {
    Enabled           bool          `mapstructure:"enabled"`
    RetentionPeriod   time.Duration `mapstructure:"retention_period"`
    SegmentPattern    string        `mapstructure:"segment_pattern"`
    IndexInterval     time.Duration `mapstructure:"index_interval"`
}
```

### 2. CMAF Segmenter Implementation
```go
// internal/packaging/cmaf/segmenter.go
package cmaf

import (
    "bytes"
    "encoding/binary"
    "fmt"
    "sync"
    "time"
    
    "github.com/Eyevinn/mp4ff/mp4"
    "github.com/sirupsen/logrus"
)

type Segmenter struct {
    streamID        string
    config          *CMAFConfig
    logger          *logrus.Logger
    
    initSegment     []byte
    currentSegment  *Segment
    segmentNum      uint32
    
    outputChan      chan *Segment
    chunkChan       chan *Chunk
    
    videoTimescale  uint32
    audioTimescale  uint32
    
    mu              sync.RWMutex
}

type Segment struct {
    StreamID    string
    Number      uint32
    Duration    time.Duration
    Chunks      []*Chunk
    Data        []byte
    Timestamp   time.Time
    Keyframe    bool
}

type Chunk struct {
    StreamID    string
    SegmentNum  uint32
    ChunkNum    uint32
    Duration    time.Duration
    Data        []byte
    Offset      int64
    Size        int64
    Independent bool
}

func NewSegmenter(streamID string, config *CMAFConfig, logger *logrus.Logger) *Segmenter {
    return &Segmenter{
        streamID:   streamID,
        config:     config,
        logger:     logger,
        outputChan: make(chan *Segment, 10),
        chunkChan:  make(chan *Chunk, 50),
        videoTimescale: 90000, // Standard for video
        audioTimescale: 44100, // Standard for audio
    }
}

func (s *Segmenter) Start(input <-chan *transcoding.Segment) error {
    // Create init segment
    if err := s.createInitSegment(); err != nil {
        return fmt.Errorf("failed to create init segment: %w", err)
    }
    
    go s.processInput(input)
    
    return nil
}

func (s *Segmenter) createInitSegment() error {
    init := mp4.NewInit()
    
    // Add ftyp box
    init.AddChild(&mp4.FtypBox{
        MajorBrand:   "iso6",
        MinorVersion: 1,
        CompatibleBrands: []string{
            "iso6", "isom", "hlsf", "msdh", "msix",
        },
    })
    
    // Create moov box
    moov := &mp4.MoovBox{}
    
    // Add mvhd (movie header)
    moov.Mvhd = &mp4.MvhdBox{
        Timescale: 1000,
        Duration:  0, // Live stream
        NextTrackID: 3,
    }
    
    // Add video track
    videoTrak := s.createVideoTrack()
    moov.AddChild(videoTrak)
    
    // Add audio track
    audioTrak := s.createAudioTrack()
    moov.AddChild(audioTrak)
    
    // Add mvex (movie extends) for fragmentation
    mvex := &mp4.MvexBox{}
    
    // Video trex
    mvex.AddChild(&mp4.TrexBox{
        TrackID: 1,
        DefaultSampleDescriptionIndex: 1,
        DefaultSampleDuration: 0,
        DefaultSampleSize: 0,
        DefaultSampleFlags: 0,
    })
    
    // Audio trex
    mvex.AddChild(&mp4.TrexBox{
        TrackID: 2,
        DefaultSampleDescriptionIndex: 1,
        DefaultSampleDuration: 0,
        DefaultSampleSize: 0,
        DefaultSampleFlags: 0,
    })
    
    moov.AddChild(mvex)
    init.AddChild(moov)
    
    // Encode init segment
    buf := bytes.NewBuffer(nil)
    if err := init.Encode(buf); err != nil {
        return fmt.Errorf("failed to encode init segment: %w", err)
    }
    
    s.initSegment = buf.Bytes()
    s.logger.Infof("Created init segment for stream %s: %d bytes", s.streamID, len(s.initSegment))
    
    return nil
}

func (s *Segmenter) processInput(input <-chan *transcoding.Segment) {
    var chunkBuffer []*transcoding.Segment
    chunkStartTime := time.Now()
    segmentStartTime := time.Now()
    currentChunkNum := uint32(0)
    
    for segment := range input {
        chunkBuffer = append(chunkBuffer, segment)
        
        // Check if we should create a chunk
        if time.Since(chunkStartTime) >= s.config.ChunkDuration {
            chunk := s.createChunk(chunkBuffer, currentChunkNum)
            s.chunkChan <- chunk
            
            // Add chunk to current segment
            if s.currentSegment == nil {
                s.currentSegment = &Segment{
                    StreamID:  s.streamID,
                    Number:    s.segmentNum,
                    Chunks:    make([]*Chunk, 0, s.config.ChunksPerSegment),
                    Timestamp: segmentStartTime,
                    Keyframe:  segment.Keyframe,
                }
            }
            
            s.currentSegment.Chunks = append(s.currentSegment.Chunks, chunk)
            
            // Reset chunk buffer
            chunkBuffer = nil
            chunkStartTime = time.Now()
            currentChunkNum++
            
            // Check if we should complete the segment
            if len(s.currentSegment.Chunks) >= s.config.ChunksPerSegment {
                s.finalizeSegment()
                s.segmentNum++
                currentChunkNum = 0
                segmentStartTime = time.Now()
            }
        }
    }
}

func (s *Segmenter) createChunk(samples []*transcoding.Segment, chunkNum uint32) *Chunk {
    // Create moof (movie fragment) box
    moof := &mp4.MoofBox{}
    
    // Add mfhd (movie fragment header)
    moof.Mfhd = &mp4.MfhdBox{
        SequenceNumber: s.segmentNum*uint32(s.config.ChunksPerSegment) + chunkNum,
    }
    
    // Add traf (track fragment) for video
    videoTraf := &mp4.TrafBox{
        Tfhd: &mp4.TfhdBox{
            TrackID: 1,
            Flags: mp4.TfhdDefaultBaseIsMoof | 
                   mp4.TfhdDefaultSampleDurationPresent |
                   mp4.TfhdDefaultSampleSizePresent,
        },
        Tfdt: &mp4.TfdtBox{
            BaseMediaDecodeTimeV1: calculateDecodeTime(samples[0]),
        },
    }
    
    // Add trun (track run) with sample information
    trun := &mp4.TrunBox{
        Flags: mp4.TrunDataOffsetPresent |
               mp4.TrunSampleDurationPresent |
               mp4.TrunSampleSizePresent |
               mp4.TrunSampleFlagsPresent,
        SampleCount: uint32(len(samples)),
    }
    
    // Calculate data offset and add samples
    dataOffset := int32(0) // Will be updated after encoding
    for _, sample := range samples {
        flags := uint32(0)
        if sample.Keyframe {
            flags = 0x02000000 // Key frame
        } else {
            flags = 0x01010000 // Non-key frame
        }
        
        trun.Samples = append(trun.Samples, mp4.Sample{
            Duration: uint32(s.config.ChunkDuration.Milliseconds()),
            Size:     uint32(len(sample.Data)),
            Flags:    flags,
        })
    }
    
    videoTraf.AddChild(trun)
    moof.AddChild(videoTraf)
    
    // Create mdat (media data) box
    mdat := &mp4.MdatBox{}
    for _, sample := range samples {
        mdat.Data = append(mdat.Data, sample.Data...)
    }
    
    // Encode chunk
    buf := bytes.NewBuffer(nil)
    moof.Encode(buf)
    
    // Update data offset
    moofSize := buf.Len()
    trun.DataOffset = int32(moofSize + 8) // mdat header size
    
    // Re-encode with correct offset
    buf.Reset()
    moof.Encode(buf)
    mdat.Encode(buf)
    
    chunk := &Chunk{
        StreamID:    s.streamID,
        SegmentNum:  s.segmentNum,
        ChunkNum:    chunkNum,
        Duration:    s.config.ChunkDuration,
        Data:        buf.Bytes(),
        Size:        int64(buf.Len()),
        Independent: samples[0].Keyframe,
    }
    
    return chunk
}

func (s *Segmenter) finalizeSegment() {
    if s.currentSegment == nil {
        return
    }
    
    // Combine all chunks into segment data
    var segmentData bytes.Buffer
    
    for i, chunk := range s.currentSegment.Chunks {
        chunk.Offset = int64(segmentData.Len())
        segmentData.Write(chunk.Data)
        
        // Clear chunk data to save memory (already in segment)
        if i < len(s.currentSegment.Chunks)-1 {
            chunk.Data = nil
        }
    }
    
    s.currentSegment.Data = segmentData.Bytes()
    s.currentSegment.Duration = time.Duration(len(s.currentSegment.Chunks)) * s.config.ChunkDuration
    
    // Send completed segment
    s.outputChan <- s.currentSegment
    s.currentSegment = nil
}
```

### 3. LL-HLS Playlist Generation
```go
// internal/packaging/hls/playlist.go
package hls

import (
    "fmt"
    "strings"
    "sync"
    "time"
    
    "github.com/etherlabsio/go-m3u8/m3u8"
    "github.com/sirupsen/logrus"
)

type PlaylistGenerator struct {
    streamID      string
    config        *HLSConfig
    logger        *logrus.Logger
    
    segments      []*SegmentInfo
    currentMSN    uint64  // Media Sequence Number
    currentPart   uint64  // Part number
    
    mu            sync.RWMutex
}

type SegmentInfo struct {
    Number        uint32
    Duration      float64
    URI           string
    Parts         []*PartInfo
    ProgramDateTime time.Time
    Independent   bool
}

type PartInfo struct {
    Duration     float64
    URI          string
    Independent  bool
    ByteRange    string
}

func NewPlaylistGenerator(streamID string, config *HLSConfig, logger *logrus.Logger) *PlaylistGenerator {
    return &PlaylistGenerator{
        streamID: streamID,
        config:   config,
        logger:   logger,
        segments: make([]*SegmentInfo, 0),
    }
}

func (p *PlaylistGenerator) AddSegment(segment *cmaf.Segment) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    segInfo := &SegmentInfo{
        Number:          segment.Number,
        Duration:        segment.Duration.Seconds(),
        URI:             fmt.Sprintf("segment_%d.m4s", segment.Number),
        ProgramDateTime: segment.Timestamp,
        Independent:     segment.Keyframe,
        Parts:           make([]*PartInfo, 0),
    }
    
    // Add parts (chunks)
    for i, chunk := range segment.Chunks {
        part := &PartInfo{
            Duration:    chunk.Duration.Seconds(),
            URI:         segInfo.URI,
            Independent: chunk.Independent,
            ByteRange:   fmt.Sprintf("%d@%d", chunk.Size, chunk.Offset),
        }
        segInfo.Parts = append(segInfo.Parts, part)
        
        // Last chunk keeps data for push
        if i == len(segment.Chunks)-1 {
            part.URI = fmt.Sprintf("segment_%d.m4s?_HLS_part=%d", segment.Number, i)
        }
    }
    
    p.segments = append(p.segments, segInfo)
    p.currentMSN = uint64(segment.Number)
    p.currentPart = uint64(len(segment.Chunks) - 1)
    
    // Maintain playlist window
    if len(p.segments) > p.config.PlaylistWindow {
        p.segments = p.segments[len(p.segments)-p.config.PlaylistWindow:]
    }
}

func (p *PlaylistGenerator) Generate(msn, part string, skip bool) ([]byte, error) {
    p.mu.RLock()
    defer p.mu.RUnlock()
    
    playlist := m3u8.NewMediaPlaylist()
    
    // Set version for LL-HLS
    playlist.SetVersion(9)
    
    // Basic tags
    playlist.SetTargetDuration(1) // 1 second (2x segment duration)
    playlist.SetAllowCache(false)
    
    // LL-HLS specific tags
    playlist.SetCustomTag("EXT-X-SERVER-CONTROL", fmt.Sprintf(
        "CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.1f,CAN-SKIP-UNTIL=%.1f",
        p.config.PartHoldBack,
        p.config.CanSkipUntil,
    ))
    
    playlist.SetCustomTag("EXT-X-PART-INF", fmt.Sprintf(
        "PART-TARGET=%.3f", 0.1, // 100ms
    ))
    
    // Calculate starting point
    startIdx := 0
    if skip && p.config.CanSkipUntil > 0 {
        // Skip to near live edge
        skipSegments := int(p.config.CanSkipUntil / 0.5) // 500ms segments
        if skipSegments < len(p.segments) {
            startIdx = len(p.segments) - skipSegments
            playlist.SetCustomTag("EXT-X-SKIP", fmt.Sprintf(
                "SKIPPED-SEGMENTS=%d", startIdx,
            ))
        }
    }
    
    // Add segments
    for i := startIdx; i < len(p.segments); i++ {
        seg := p.segments[i]
        
        // Add program date time for first segment after skip
        if i == startIdx {
            playlist.SetProgramDateTime(seg.ProgramDateTime.Format(time.RFC3339))
        }
        
        // Add parts for this segment
        var partTags []string
        for j, part := range seg.Parts {
            partTag := fmt.Sprintf("#EXT-X-PART:DURATION=%.3f,URI=\"%s\"",
                part.Duration, part.URI)
            
            if part.ByteRange != "" {
                partTag += fmt.Sprintf(",BYTERANGE=\"%s\"", part.ByteRange)
            }
            
            if part.Independent {
                partTag += ",INDEPENDENT=YES"
            }
            
            partTags = append(partTags, partTag)
            
            // Add preload hint for last part of last segment
            if i == len(p.segments)-1 && j == len(seg.Parts)-1 {
                // This is the latest part
                nextPart := j + 1
                if nextPart >= p.config.ChunksPerSegment {
                    // Next segment
                    playlist.SetCustomTag("EXT-X-PRELOAD-HINT", fmt.Sprintf(
                        "TYPE=PART,URI=\"segment_%d.m4s?_HLS_part=0\"",
                        seg.Number+1,
                    ))
                } else {
                    // Next part in same segment
                    playlist.SetCustomTag("EXT-X-PRELOAD-HINT", fmt.Sprintf(
                        "TYPE=PART,URI=\"segment_%d.m4s?_HLS_part=%d\"",
                        seg.Number, nextPart,
                    ))
                }
            }
        }
        
        // Create segment entry
        segment := &m3u8.MediaSegment{
            URI:      seg.URI,
            Duration: seg.Duration,
            Title:    "",
        }
        
        // Add custom tags for parts
        for _, tag := range partTags {
            segment.SetCustomTag(tag)
        }
        
        playlist.AppendSegment(segment)
    }
    
    // Add rendition report for other variants (if any)
    playlist.SetCustomTag("EXT-X-RENDITION-REPORT", fmt.Sprintf(
        "URI=\"playlist.m3u8\",LAST-MSN=%d,LAST-PART=%d",
        p.currentMSN, p.currentPart,
    ))
    
    return playlist.Encode(), nil
}

// internal/packaging/hls/master.go
package hls

import (
    "fmt"
    "github.com/etherlabsio/go-m3u8/m3u8"
)

type MasterPlaylistGenerator struct {
    streams map[string]*StreamVariant
}

type StreamVariant struct {
    StreamID   string
    Bandwidth  int
    Resolution string
    Codecs     string
    FrameRate  float64
}

func NewMasterPlaylistGenerator() *MasterPlaylistGenerator {
    return &MasterPlaylistGenerator{
        streams: make(map[string]*StreamVariant),
    }
}

func (m *MasterPlaylistGenerator) AddStream(variant *StreamVariant) {
    m.streams[variant.StreamID] = variant
}

func (m *MasterPlaylistGenerator) Generate() ([]byte, error) {
    playlist := m3u8.NewMasterPlaylist()
    
    for _, variant := range m.streams {
        playlist.Append(fmt.Sprintf(
            "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s,CODECS=\"%s\",FRAME-RATE=%.3f",
            variant.Bandwidth,
            variant.Resolution,
            variant.Codecs,
            variant.FrameRate,
        ))
        playlist.Append(fmt.Sprintf("stream/%s/playlist.m3u8", variant.StreamID))
    }
    
    return []byte(playlist.String()), nil
}
```

### 4. Memory-Mapped Cache Implementation
```go
// internal/packaging/cache/mmap_cache.go
package cache

import (
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "syscall"
    
    "github.com/edsrzf/mmap-go"
    "github.com/sirupsen/logrus"
)

type MmapCache struct {
    baseDir    string
    maxSize    int64
    currentSize int64
    
    files      map[string]*MmapFile
    lru        *LRUList
    
    mu         sync.RWMutex
    logger     *logrus.Logger
}

type MmapFile struct {
    path       string
    size       int64
    mmap       mmap.MMap
    file       *os.File
    refCount   int32
    lastAccess time.Time
}

func NewMmapCache(baseDir string, maxSize int64, logger *logrus.Logger) (*MmapCache, error) {
    if err := os.MkdirAll(baseDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create cache dir: %w", err)
    }
    
    return &MmapCache{
        baseDir: baseDir,
        maxSize: maxSize,
        files:   make(map[string]*MmapFile),
        lru:     NewLRUList(),
        logger:  logger,
    }, nil
}

func (c *MmapCache) Store(key string, data []byte) error {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    // Check if already exists
    if _, exists := c.files[key]; exists {
        return nil
    }
    
    // Check space
    if c.currentSize+int64(len(data)) > c.maxSize {
        // Evict old files
        c.evictLocked(int64(len(data)))
    }
    
    // Create file
    path := filepath.Join(c.baseDir, key)
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return fmt.Errorf("failed to create dir: %w", err)
    }
    
    file, err := os.Create(path)
    if err != nil {
        return fmt.Errorf("failed to create file: %w", err)
    }
    
    // Write data
    if _, err := file.Write(data); err != nil {
        file.Close()
        os.Remove(path)
        return fmt.Errorf("failed to write data: %w", err)
    }
    
    // Memory map the file
    mapped, err := mmap.Map(file, mmap.RDONLY, 0)
    if err != nil {
        file.Close()
        os.Remove(path)
        return fmt.Errorf("failed to mmap file: %w", err)
    }
    
    mmapFile := &MmapFile{
        path:       path,
        size:       int64(len(data)),
        mmap:       mapped,
        file:       file,
        lastAccess: time.Now(),
    }
    
    c.files[key] = mmapFile
    c.currentSize += mmapFile.size
    c.lru.Add(key, mmapFile)
    
    return nil
}

func (c *MmapCache) Get(key string) ([]byte, error) {
    c.mu.RLock()
    mmapFile, exists := c.files[key]
    c.mu.RUnlock()
    
    if !exists {
        return nil, os.ErrNotExist
    }
    
    // Update access time
    c.mu.Lock()
    mmapFile.lastAccess = time.Now()
    c.lru.MoveToFront(key)
    c.mu.Unlock()
    
    // Return zero-copy slice
    return mmapFile.mmap, nil
}

func (c *MmapCache) GetReader(key string) (*MmapReader, error) {
    data, err := c.Get(key)
    if err != nil {
        return nil, err
    }
    
    return &MmapReader{
        data:   data,
        offset: 0,
    }, nil
}

type MmapReader struct {
    data   []byte
    offset int
}

func (r *MmapReader) Read(p []byte) (n int, err error) {
    if r.offset >= len(r.data) {
        return 0, io.EOF
    }
    
    n = copy(p, r.data[r.offset:])
    r.offset += n
    return n, nil
}

func (r *MmapReader) Seek(offset int64, whence int) (int64, error) {
    var newOffset int64
    
    switch whence {
    case io.SeekStart:
        newOffset = offset
    case io.SeekCurrent:
        newOffset = int64(r.offset) + offset
    case io.SeekEnd:
        newOffset = int64(len(r.data)) + offset
    default:
        return 0, errors.New("invalid whence")
    }
    
    if newOffset < 0 || newOffset > int64(len(r.data)) {
        return 0, errors.New("seek out of range")
    }
    
    r.offset = int(newOffset)
    return newOffset, nil
}

func (c *MmapCache) evictLocked(needed int64) {
    for c.currentSize+needed > c.maxSize && c.lru.Len() > 0 {
        key, mmapFile := c.lru.RemoveOldest()
        
        // Unmap and close
        mmapFile.mmap.Unmap()
        mmapFile.file.Close()
        os.Remove(mmapFile.path)
        
        delete(c.files, key)
        c.currentSize -= mmapFile.size
        
        c.logger.Debugf("Evicted %s from cache (size: %d)", key, mmapFile.size)
    }
}
```

### 5. HTTP/3 Server with Push Support
```go
// internal/packaging/delivery/http3_server.go
package delivery

import (
    "context"
    "fmt"
    "net/http"
    "strconv"
    "sync"
    "time"
    
    "github.com/quic-go/quic-go/http3"
    "github.com/gorilla/mux"
    "github.com/sirupsen/logrus"
)

type HTTP3Server struct {
    config       *DeliveryConfig
    server       *http3.Server
    router       *mux.Router
    cache        *cache.MmapCache
    playlists    map[string]*hls.PlaylistGenerator
    predictor    *PushPredictor
    logger       *logrus.Logger
    
    pushSessions sync.Map // clientID -> *PushSession
}

type PushSession struct {
    clientID     string
    streamID     string
    lastSegment  uint32
    lastPart     uint32
    pushedItems  *LRUCache
    created      time.Time
}

func NewHTTP3Server(config *DeliveryConfig, cache *cache.MmapCache, logger *logrus.Logger) *HTTP3Server {
    router := mux.NewRouter()
    
    return &HTTP3Server{
        config:    config,
        router:    router,
        cache:     cache,
        playlists: make(map[string]*hls.PlaylistGenerator),
        predictor: NewPushPredictor(),
        logger:    logger,
    }
}

func (s *HTTP3Server) Start(addr string) error {
    s.server = &http3.Server{
        Addr:    addr,
        Handler: s,
    }
    
    // Setup routes
    s.setupRoutes()
    
    s.logger.Infof("Starting HTTP/3 server on %s", addr)
    return s.server.ListenAndServe()
}

func (s *HTTP3Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Add CORS headers if enabled
    if s.config.CORSEnabled {
        origin := r.Header.Get("Origin")
        allowed := false
        
        for _, allowedOrigin := range s.config.CORSOrigins {
            if allowedOrigin == "*" || allowedOrigin == origin {
                allowed = true
                break
            }
        }
        
        if allowed {
            w.Header().Set("Access-Control-Allow-Origin", origin)
            w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
            w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Range")
            w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range")
            w.Header().Set("Access-Control-Max-Age", "86400")
        }
        
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusNoContent)
            return
        }
    }
    
    // Handle HTTP/3 push if enabled
    if s.config.PushEnabled && r.ProtoMajor == 3 {
        s.handlePush(w, r)
    }
    
    s.router.ServeHTTP(w, r)
}

func (s *HTTP3Server) setupRoutes() {
    // Stream routes
    stream := s.router.PathPrefix("/stream/{streamID}").Subrouter()
    
    // Playlist
    stream.HandleFunc("/playlist.m3u8", s.handlePlaylist).Methods("GET")
    
    // Init segment
    stream.HandleFunc("/init.mp4", s.handleInitSegment).Methods("GET")
    
    // Media segments
    stream.HandleFunc("/segment_{num}.m4s", s.handleSegment).Methods("GET")
    
    // Master playlist
    s.router.HandleFunc("/master.m3u8", s.handleMasterPlaylist).Methods("GET")
}

func (s *HTTP3Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["streamID"]
    
    // Get playlist generator
    generator, exists := s.playlists[streamID]
    if !exists {
        http.Error(w, "Stream not found", http.StatusNotFound)
        return
    }
    
    // Parse LL-HLS query parameters
    msn := r.URL.Query().Get("_HLS_msn")
    part := r.URL.Query().Get("_HLS_part")
    skip := r.URL.Query().Get("_HLS_skip") == "YES"
    
    // Block if requesting future segment
    if msn != "" {
        requestedMSN, _ := strconv.ParseUint(msn, 10, 64)
        requestedPart := uint64(0)
        if part != "" {
            requestedPart, _ = strconv.ParseUint(part, 10, 64)
        }
        
        // Wait for requested segment/part
        ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
        defer cancel()
        
        if err := s.waitForSegment(ctx, generator, requestedMSN, requestedPart); err != nil {
            http.Error(w, "Timeout waiting for segment", http.StatusRequestTimeout)
            return
        }
    }
    
    // Generate playlist
    playlist, err := generator.Generate(msn, part, skip)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    // Set headers
    w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("X-Accel-Buffering", "no")
    
    w.Write(playlist)
}

func (s *HTTP3Server) handleSegment(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["streamID"]
    segNum := vars["num"]
    
    // Check for partial segment request
    partNum := r.URL.Query().Get("_HLS_part")
    
    key := fmt.Sprintf("%s/segment_%s.m4s", streamID, segNum)
    
    // Get from cache
    data, err := s.cache.Get(key)
    if err != nil {
        http.Error(w, "Segment not found", http.StatusNotFound)
        return
    }
    
    // Handle byte range for parts
    if partNum != "" {
        part, _ := strconv.Atoi(partNum)
        byteRange := s.getPartByteRange(data, part)
        
        if byteRange == nil {
            http.Error(w, "Part not found", http.StatusNotFound)
            return
        }
        
        data = data[byteRange.Start:byteRange.End]
        w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", 
            byteRange.Start, byteRange.End-1, byteRange.Total))
    }
    
    // Set headers
    w.Header().Set("Content-Type", "video/mp4")
    w.Header().Set("Content-Length", strconv.Itoa(len(data)))
    w.Header().Set("Cache-Control", "max-age=31536000, immutable")
    
    w.Write(data)
}

func (s *HTTP3Server) handlePush(w http.ResponseWriter, r *http.Request) {
    pusher, ok := w.(http.Pusher)
    if !ok {
        return
    }
    
    // Get or create push session
    clientID := s.getClientID(r)
    sessionKey := fmt.Sprintf("%s:%s", clientID, r.URL.Path)
    
    session, _ := s.pushSessions.LoadOrStore(sessionKey, &PushSession{
        clientID:    clientID,
        created:     time.Now(),
        pushedItems: NewLRUCache(100),
    })
    
    pushSession := session.(*PushSession)
    
    // Predict next segments/parts to push
    predictions := s.predictor.Predict(r.URL.Path, pushSession)
    
    for _, prediction := range predictions {
        // Check if already pushed
        if pushSession.pushedItems.Contains(prediction.URI) {
            continue
        }
        
        // Attempt push
        err := pusher.Push(prediction.URI, &http.PushOptions{
            Header: http.Header{
                "Content-Type":  []string{prediction.ContentType},
                "Cache-Control": []string{prediction.CacheControl},
            },
        })
        
        if err == nil {
            pushSession.pushedItems.Add(prediction.URI, true)
            s.logger.Debugf("Pushed %s to client %s", prediction.URI, clientID)
        }
    }
}

func (s *HTTP3Server) waitForSegment(ctx context.Context, generator *hls.PlaylistGenerator, msn, part uint64) error {
    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            currentMSN, currentPart := generator.GetCurrent()
            
            // Check if requested segment/part is available
            if currentMSN > msn || (currentMSN == msn && currentPart >= part) {
                return nil
            }
        }
    }
}
```

### 6. S3 Upload Pipeline
```go
// internal/storage/s3/uploader.go
package s3

import (
    "bytes"
    "context"
    "fmt"
    "sync"
    "time"
    
    "github.com/minio/minio-go/v7"
    "github.com/minio/minio-go/v7/pkg/credentials"
    "github.com/sirupsen/logrus"
)

type Uploader struct {
    client       *minio.Client
    config       *S3Config
    uploadQueue  chan *UploadJob
    workers      sync.WaitGroup
    logger       *logrus.Logger
    
    ctx          context.Context
    cancel       context.CancelFunc
}

type UploadJob struct {
    Key         string
    Data        []byte
    ContentType string
    Metadata    map[string]string
    RetryCount  int
}

func NewUploader(config *S3Config, logger *logrus.Logger) (*Uploader, error) {
    // Initialize MinIO client
    client, err := minio.New(config.Endpoint, &minio.Options{
        Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
        Secure: true,
        Region: config.Region,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to create S3 client: %w", err)
    }
    
    ctx, cancel := context.WithCancel(context.Background())
    
    u := &Uploader{
        client:      client,
        config:      config,
        uploadQueue: make(chan *UploadJob, 1000),
        logger:      logger,
        ctx:         ctx,
        cancel:      cancel,
    }
    
    // Start upload workers
    for i := 0; i < config.UploadConcurrency; i++ {
        u.workers.Add(1)
        go u.uploadWorker(i)
    }
    
    return u, nil
}

func (u *Uploader) Upload(key string, data []byte, contentType string, metadata map[string]string) error {
    job := &UploadJob{
        Key:         key,
        Data:        data,
        ContentType: contentType,
        Metadata:    metadata,
    }
    
    select {
    case u.uploadQueue <- job:
        return nil
    case <-u.ctx.Done():
        return u.ctx.Err()
    default:
        return fmt.Errorf("upload queue full")
    }
}

func (u *Uploader) uploadWorker(id int) {
    defer u.workers.Done()
    
    for {
        select {
        case <-u.ctx.Done():
            return
        case job := <-u.uploadQueue:
            u.processUpload(job)
        }
    }
}

func (u *Uploader) processUpload(job *UploadJob) {
    start := time.Now()
    
    // Prepare object key
    objectKey := fmt.Sprintf("%s/%s", u.config.Prefix, job.Key)
    
    // Upload options
    opts := minio.PutObjectOptions{
        ContentType: job.ContentType,
        UserMetadata: job.Metadata,
    }
    
    // Set cache control for different content types
    switch job.ContentType {
    case "application/vnd.apple.mpegurl":
        opts.CacheControl = "no-cache"
    case "video/mp4":
        opts.CacheControl = "max-age=31536000, immutable"
    }
    
    // Upload to S3
    _, err := u.client.PutObject(
        u.ctx,
        u.config.Bucket,
        objectKey,
        bytes.NewReader(job.Data),
        int64(len(job.Data)),
        opts,
    )
    
    if err != nil {
        u.logger.Errorf("Failed to upload %s: %v", job.Key, err)
        
        // Retry logic
        if job.RetryCount < 3 {
            job.RetryCount++
            select {
            case u.uploadQueue <- job:
            default:
                u.logger.Errorf("Failed to requeue upload for %s", job.Key)
            }
        }
        return
    }
    
    duration := time.Since(start)
    u.logger.Debugf("Uploaded %s (size: %d, duration: %v)", job.Key, len(job.Data), duration)
}
```

### 7. Packaging Manager
```go
// internal/packaging/manager.go
package packaging

import (
    "context"
    "fmt"
    "sync"
    
    "github.com/sirupsen/logrus"
)

type Manager struct {
    config       *PackagingConfig
    cache        *cache.MmapCache
    http3Server  *delivery.HTTP3Server
    s3Uploader   *s3.Uploader
    
    segmenters   sync.Map // streamID -> *cmaf.Segmenter
    generators   sync.Map // streamID -> *hls.PlaylistGenerator
    
    logger       *logrus.Logger
    ctx          context.Context
    cancel       context.CancelFunc
}

func NewManager(config *PackagingConfig, storageConfig *StorageConfig, logger *logrus.Logger) (*Manager, error) {
    ctx, cancel := context.WithCancel(context.Background())
    
    // Initialize cache
    cache, err := cache.NewMmapCache("/tmp/mirror/cache", config.Cache.MaxSize, logger)
    if err != nil {
        return nil, fmt.Errorf("failed to create cache: %w", err)
    }
    
    // Initialize S3 uploader
    var s3Uploader *s3.Uploader
    if storageConfig.S3.Enabled {
        s3Uploader, err = s3.NewUploader(&storageConfig.S3, logger)
        if err != nil {
            return nil, fmt.Errorf("failed to create S3 uploader: %w", err)
        }
    }
    
    // Initialize HTTP/3 server
    http3Server := delivery.NewHTTP3Server(&config.Delivery, cache, logger)
    
    return &Manager{
        config:      config,
        cache:       cache,
        http3Server: http3Server,
        s3Uploader:  s3Uploader,
        logger:      logger,
        ctx:         ctx,
        cancel:      cancel,
    }, nil
}

func (m *Manager) StartPackaging(streamID string, input <-chan *transcoding.Segment) error {
    // Create segmenter
    segmenter := cmaf.NewSegmenter(streamID, &m.config.CMAF, m.logger)
    if err := segmenter.Start(input); err != nil {
        return fmt.Errorf("failed to start segmenter: %w", err)
    }
    
    // Create playlist generator
    generator := hls.NewPlaylistGenerator(streamID, &m.config.HLS, m.logger)
    
    // Store references
    m.segmenters.Store(streamID, segmenter)
    m.generators.Store(streamID, generator)
    m.http3Server.AddPlaylist(streamID, generator)
    
    // Process segments
    go m.processSegments(streamID, segmenter, generator)
    
    m.logger.Infof("Started packaging for stream %s", streamID)
    
    return nil
}

func (m *Manager) processSegments(streamID string, segmenter *cmaf.Segmenter, generator *hls.PlaylistGenerator) {
    // Cache init segment
    initKey := fmt.Sprintf("%s/init.mp4", streamID)
    if err := m.cache.Store(initKey, segmenter.GetInitSegment()); err != nil {
        m.logger.Errorf("Failed to cache init segment: %v", err)
    }
    
    // Upload init segment to S3
    if m.s3Uploader != nil {
        m.s3Uploader.Upload(initKey, segmenter.GetInitSegment(), "video/mp4", nil)
    }
    
    // Process segments
    for segment := range segmenter.Output() {
        // Add to playlist
        generator.AddSegment(segment)
        
        // Cache segment
        segmentKey := fmt.Sprintf("%s/segment_%d.m4s", streamID, segment.Number)
        if err := m.cache.Store(segmentKey, segment.Data); err != nil {
            m.logger.Errorf("Failed to cache segment: %v", err)
            continue
        }
        
        // Upload to S3
        if m.s3Uploader != nil {
            metadata := map[string]string{
                "stream-id": streamID,
                "segment":   fmt.Sprintf("%d", segment.Number),
                "duration":  fmt.Sprintf("%.3f", segment.Duration.Seconds()),
            }
            m.s3Uploader.Upload(segmentKey, segment.Data, "video/mp4", metadata)
        }
        
        // Process chunks for push
        for _, chunk := range segment.Chunks {
            chunkKey := fmt.Sprintf("%s/segment_%d_part_%d.m4s", 
                streamID, segment.Number, chunk.ChunkNum)
            
            // Only cache last chunk (others are in segment)
            if chunk.ChunkNum == uint32(len(segment.Chunks)-1) {
                if err := m.cache.Store(chunkKey, chunk.Data); err != nil {
                    m.logger.Errorf("Failed to cache chunk: %v", err)
                }
            }
        }
    }
    
    m.logger.Infof("Packaging completed for stream %s", streamID)
}

func (m *Manager) Start() error {
    // Start HTTP/3 server
    go func() {
        if err := m.http3Server.Start(":443"); err != nil {
            m.logger.Fatalf("HTTP/3 server error: %v", err)
        }
    }()
    
    return nil
}
```

### 8. Updated Configuration
```yaml
# configs/default.yaml (additions)
packaging:
  cmaf:
    segment_duration: 500ms       # Fixed 500ms segments
    chunk_duration: 100ms         # 100ms chunks
    chunks_per_segment: 5         # 5 chunks per segment
    lookahead_chunks: 2           # Pre-generate 2 chunks
    init_segment_size: 4096       # Initial buffer size
    
  hls:
    playlist_window: 20           # Keep 20 segments (10 seconds)
    part_hold_back: 0.3          # 300ms part hold back
    can_block_reload: true       # Enable blocking reload
    can_skip_until: 6.0          # Skip up to 6 seconds
    max_skipped_segments: 12     # Max segments to skip
    playlist_type: "EVENT"       # EVENT for live streams
    
  cache:
    max_size: 10737418240        # 10GB cache
    segment_ttl: 300s            # 5 minutes
    chunk_ttl: 60s               # 1 minute
    mmap_enabled: true           # Use memory-mapped files
    preload_count: 3             # Preload 3 segments
    
  delivery:
    http3_enabled: true          # Enable HTTP/3
    push_enabled: true           # Enable server push
    max_push_streams: 100        # Max concurrent push streams
    push_timeout: 5s             # Push timeout
    cors_enabled: true           # Enable CORS
    cors_origins:                # Allowed origins
      - "*"

storage:
  s3:
    enabled: true
    bucket: "mirror-streams"
    region: "us-east-1"
    prefix: "live"
    endpoint: "s3.amazonaws.com"
    access_key: "${S3_ACCESS_KEY}"
    secret_key: "${S3_SECRET_KEY}"
    upload_concurrency: 10
    part_size: 5242880           # 5MB parts
    
  dvr:
    enabled: true
    retention_period: 24h        # Keep 24 hours
    segment_pattern: "dvr/{stream_id}/{date}/{hour}/segment_{num}.m4s"
    index_interval: 1h           # Update index every hour
```

## Testing Requirements

### Unit Tests
- CMAF segmentation with correct timing
- LL-HLS playlist generation with all tags
- Memory-mapped cache operations
- HTTP/3 push session management
- S3 upload retry logic

### Integration Tests
- Full packaging pipeline from transcoded input
- HTTP/3 server with push functionality
- Blocking playlist reload behavior
- Cache eviction under memory pressure
- S3 upload with lifecycle policies

### Performance Tests
- 25 concurrent streams packaging
- 5000 concurrent HTTP/3 connections
- Zero-copy serving from mmap cache
- Push prediction accuracy
- CDN offload effectiveness

## Monitoring Metrics

### Prometheus Metrics
```go
// Packaging metrics
packaging_segments_created_total{stream_id}
packaging_chunks_created_total{stream_id}
packaging_segment_duration_seconds{stream_id}
packaging_playlist_requests_total{stream_id, type}
packaging_playlist_wait_duration_seconds{stream_id}

// Cache metrics
cache_size_bytes
cache_hits_total
cache_misses_total
cache_evictions_total
cache_mmap_operations_total{operation}

// HTTP/3 metrics
http3_connections_active
http3_push_attempts_total{result}
http3_push_bytes_total
http3_request_duration_seconds{endpoint}

// S3 metrics
s3_uploads_total{status}
s3_upload_duration_seconds
s3_upload_bytes_total
s3_upload_queue_size
```

## Deliverables
1. Complete CMAF segmenter with 500ms/100ms timing
2. LL-HLS playlist generator with all required tags
3. HTTP/3 server with push support
4. Memory-mapped file cache implementation
5. S3 upload pipeline with retry logic
6. DVR recording functionality
7. Comprehensive test suite
8. Performance benchmarks

## Success Criteria
- 500ms segments with 100ms chunks consistently
- LL-HLS playlists validate correctly
- HTTP/3 push reduces latency by 20%+
- Zero-copy serving from cache
- 99.9% S3 upload success rate
- Support 5000 concurrent viewers
- End-to-end latency under 2 seconds
- All tests passing

## Next Phase Preview
Phase 5 will implement the complete storage and CDN integration layer, including S3 lifecycle policies, CloudFront configuration, DVR functionality with retention management, and multi-region failover.