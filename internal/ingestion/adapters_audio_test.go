package ingestion

import (
	"context"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/timestamp"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestRTPConnectionAdapter_AudioSupport(t *testing.T) {
	t.Run("creates audio adapter with AAC codec", func(t *testing.T) {
		// Create adapter directly without session for unit testing
		ctx := context.Background()
		adapter := &RTPConnectionAdapter{
			videoOutput:          make(chan types.TimestampedPacket, 1000),
			audioOutput:          make(chan types.TimestampedPacket, 1000),
			codecType:            types.CodecAAC,
			isAudioTrack:         true,
			audioTimestampMapper: timestamp.NewTimestampMapper(48000),
			ctx:                  ctx,
		}

		assert.NotNil(t, adapter)
		assert.True(t, adapter.isAudioTrack)
		assert.Equal(t, types.CodecAAC, adapter.codecType)
		assert.NotNil(t, adapter.audioTimestampMapper)
		assert.Nil(t, adapter.videoTimestampMapper)
	})

	t.Run("creates video adapter with H264 codec", func(t *testing.T) {
		ctx := context.Background()
		adapter := &RTPConnectionAdapter{
			videoOutput:          make(chan types.TimestampedPacket, 1000),
			audioOutput:          make(chan types.TimestampedPacket, 1000),
			codecType:            types.CodecH264,
			isAudioTrack:         false,
			videoTimestampMapper: timestamp.NewTimestampMapper(90000),
			ctx:                  ctx,
		}

		assert.NotNil(t, adapter)
		assert.False(t, adapter.isAudioTrack)
		assert.Equal(t, types.CodecH264, adapter.codecType)
		assert.NotNil(t, adapter.videoTimestampMapper)
		assert.Nil(t, adapter.audioTimestampMapper)
	})

	t.Run("processes audio packets correctly", func(t *testing.T) {
		ctx := context.Background()
		adapter := &RTPConnectionAdapter{
			videoOutput:          make(chan types.TimestampedPacket, 1000),
			audioOutput:          make(chan types.TimestampedPacket, 1000),
			codecType:            types.CodecAAC,
			isAudioTrack:         true,
			audioTimestampMapper: timestamp.NewTimestampMapper(48000),
			ctx:                  ctx,
		}

		// Create test RTP packet
		rtpPacket := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    96,
				SequenceNumber: 1000,
				Timestamp:      48000, // 1 second at 48kHz
				SSRC:           12345,
				Marker:         true, // End of audio frame
			},
			Payload: []byte{0x01, 0x02, 0x03, 0x04}, // Sample audio data
		}

		// Process packet
		err := adapter.ProcessPacket(rtpPacket)
		require.NoError(t, err)

		// Check audio output
		select {
		case pkt := <-adapter.GetAudioOutput():
			assert.Equal(t, types.PacketTypeAudio, pkt.Type)
			assert.Equal(t, types.CodecAAC, pkt.Codec)
			assert.Equal(t, rtpPacket.Payload, pkt.Data)
			assert.True(t, pkt.HasFlag(types.PacketFlagFrameEnd))
			// First packet should have PTS 0 (base timestamp)
			assert.Equal(t, int64(0), pkt.PTS)
			assert.Equal(t, uint16(1000), pkt.SeqNum)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected audio packet but got none")
		}

		// Video output should be empty
		select {
		case <-adapter.GetVideoOutput():
			t.Fatal("Unexpected video packet")
		default:
			// Good, no video packet
		}
	})

	t.Run("processes video packets correctly", func(t *testing.T) {
		ctx := context.Background()
		adapter := &RTPConnectionAdapter{
			videoOutput:          make(chan types.TimestampedPacket, 1000),
			audioOutput:          make(chan types.TimestampedPacket, 1000),
			codecType:            types.CodecH264,
			isAudioTrack:         false,
			videoTimestampMapper: timestamp.NewTimestampMapper(90000),
			ctx:                  ctx,
		}

		// Create test RTP packet with H.264 IDR NAL
		rtpPacket := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    96,
				SequenceNumber: 2000,
				Timestamp:      90000, // 1 second at 90kHz
				SSRC:           67890,
			},
			Payload: []byte{0x65, 0x88, 0x84, 0x00}, // H.264 IDR slice
		}

		// Process packet
		err := adapter.ProcessPacket(rtpPacket)
		require.NoError(t, err)

		// Check video output
		select {
		case pkt := <-adapter.GetVideoOutput():
			assert.Equal(t, types.PacketTypeVideo, pkt.Type)
			assert.Equal(t, types.CodecH264, pkt.Codec)
			assert.Equal(t, rtpPacket.Payload, pkt.Data)
			assert.True(t, pkt.HasFlag(types.PacketFlagKeyframe))
			// First packet should have PTS 0 (base timestamp)
			assert.Equal(t, int64(0), pkt.PTS)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected video packet but got none")
		}

		// Audio output should be empty
		select {
		case <-adapter.GetAudioOutput():
			t.Fatal("Unexpected audio packet")
		default:
			// Good, no audio packet
		}
	})

	t.Run("handles different audio codec clock rates", func(t *testing.T) {
		tests := []struct {
			codec             types.CodecType
			expectedClockRate uint32
		}{
			{types.CodecAAC, 48000},
			{types.CodecOpus, 48000},
			{types.CodecG711, 8000},
		}

		for _, tt := range tests {
			t.Run(tt.codec.String(), func(t *testing.T) {
				// Test that codec clock rates are correct
				clockRate := tt.codec.GetClockRate()
				assert.Equal(t, tt.expectedClockRate, clockRate)
			})
		}
	})
}

func TestSRTConnectionAdapter_AudioSupport(t *testing.T) {
	t.Run("has separate audio and video outputs", func(t *testing.T) {
		// Create a minimal adapter for testing
		adapter := &SRTConnectionAdapter{
			videoOutput: make(chan types.TimestampedPacket, 1000),
			audioOutput: make(chan types.TimestampedPacket, 1000),
		}

		assert.NotNil(t, adapter.GetVideoOutput())
		assert.NotNil(t, adapter.GetAudioOutput())

		// Verify they are different channels
		assert.NotEqual(t, adapter.GetVideoOutput(), adapter.GetAudioOutput())
	})
}

func BenchmarkRTPAudioProcessing(b *testing.B) {
	ctx := context.Background()
	adapter := &RTPConnectionAdapter{
		videoOutput:          make(chan types.TimestampedPacket, 1000),
		audioOutput:          make(chan types.TimestampedPacket, 1000),
		codecType:            types.CodecAAC,
		isAudioTrack:         true,
		audioTimestampMapper: timestamp.NewTimestampMapper(48000),
		ctx:                  ctx,
	}

	rtpPacket := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1000,
			Timestamp:      48000,
			SSRC:           12345,
		},
		Payload: make([]byte, 1024), // Typical audio frame size
	}

	// Drain the output channel in background
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-adapter.audioOutput:
			case <-done:
				return
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rtpPacket.SequenceNumber = uint16(1000 + i)
		rtpPacket.Timestamp = uint32(48000 + i*1024)
		adapter.ProcessPacket(rtpPacket)
	}

	close(done)
}
