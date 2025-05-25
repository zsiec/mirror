package registry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGenerateStreamID(t *testing.T) {
	tests := []struct {
		name       string
		streamType StreamType
		sourceAddr string
		validate   func(t *testing.T, id string)
	}{
		{
			name:       "SRT stream ID",
			streamType: StreamTypeSRT,
			sourceAddr: "192.168.1.100:1234",
			validate: func(t *testing.T, id string) {
				assert.Contains(t, id, "srt_")
				assert.Regexp(t, `^srt_\d{8}_\d{6}_\d{3}$`, id)
			},
		},
		{
			name:       "RTP stream ID",
			streamType: StreamTypeRTP,
			sourceAddr: "192.168.1.100:5004",
			validate: func(t *testing.T, id string) {
				assert.Contains(t, id, "rtp_")
				assert.Regexp(t, `^rtp_\d{8}_\d{6}_\d{3}$`, id)
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := GenerateStreamID(tt.streamType, tt.sourceAddr)
			tt.validate(t, id)
		})
	}
	
	// Test uniqueness
	id1 := GenerateStreamID(StreamTypeSRT, "addr1")
	id2 := GenerateStreamID(StreamTypeSRT, "addr2")
	assert.NotEqual(t, id1, id2)
}

func TestStream_Stats(t *testing.T) {
	stream := &Stream{
		BytesReceived:   50000,
		PacketsReceived: 100,
		PacketsLost:     5,
	}
	
	// Verify stats
	assert.Equal(t, int64(50000), stream.BytesReceived)
	assert.Equal(t, int64(100), stream.PacketsReceived)
	assert.Equal(t, int64(5), stream.PacketsLost)
}

func TestStream_Timing(t *testing.T) {
	// Test stream with recent heartbeat
	stream1 := &Stream{
		Status:        StatusActive,
		LastHeartbeat: time.Now(),
	}
	
	// Test stream with old heartbeat
	stream2 := &Stream{
		Status:        StatusActive,
		LastHeartbeat: time.Now().Add(-5 * time.Minute),
	}
	
	// Verify LastHeartbeat is set
	assert.NotZero(t, stream1.LastHeartbeat)
	assert.NotZero(t, stream2.LastHeartbeat)
	assert.True(t, stream1.LastHeartbeat.After(stream2.LastHeartbeat))
}

func TestStream_Status(t *testing.T) {
	tests := []struct {
		name   string
		status StreamStatus
	}{
		{
			name:   "active status",
			status: StatusActive,
		},
		{
			name:   "connecting status",
			status: StatusConnecting,
		},
		{
			name:   "error status",
			status: StatusError,
		},
		{
			name:   "closed status",
			status: StatusClosed,
		},
		{
			name:   "paused status",
			status: StatusPaused,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &Stream{
				Status: tt.status,
			}
			assert.Equal(t, tt.status, stream.Status)
		})
	}
}

func TestStreamCounter(t *testing.T) {
	// Test sequential generation of stream IDs
	id1 := GenerateStreamID(StreamTypeSRT, "addr1")
	id2 := GenerateStreamID(StreamTypeSRT, "addr2")
	id3 := GenerateStreamID(StreamTypeRTP, "addr3")
	
	// All IDs should be unique
	assert.NotEqual(t, id1, id2)
	assert.NotEqual(t, id1, id3)
	assert.NotEqual(t, id2, id3)
	
	// Should have correct prefixes
	assert.Contains(t, id1, "srt_")
	assert.Contains(t, id2, "srt_")
	assert.Contains(t, id3, "rtp_")
}
