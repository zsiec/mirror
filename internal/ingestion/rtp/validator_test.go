package rtp

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidator_ValidatePacket(t *testing.T) {
	tests := []struct {
		name    string
		packet  *rtp.Packet
		wantErr error
	}{
		{
			name: "valid packet",
			packet: &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    96,
					SequenceNumber: 1000,
					Timestamp:      90000,
					SSRC:           12345,
				},
				Payload: []byte("test data"),
			},
			wantErr: nil,
		},
		{
			name: "invalid version",
			packet: &rtp.Packet{
				Header: rtp.Header{
					Version:     1,
					PayloadType: 96,
				},
			},
			wantErr: ErrInvalidRTPVersion,
		},
		{
			name: "invalid payload type",
			packet: &rtp.Packet{
				Header: rtp.Header{
					Version:     2,
					PayloadType: 50, // Not in allowed list
				},
			},
			wantErr: ErrInvalidPayloadType,
		},
		{
			name:    "nil packet",
			packet:  nil,
			wantErr: ErrPacketTooSmall,
		},
	}

	validator := NewValidator(nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidatePacket(tt.packet)
			if tt.wantErr != nil {
				assert.Equal(t, tt.wantErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidator_SequenceNumberValidation(t *testing.T) {
	validator := NewValidator(&ValidatorConfig{
		AllowedPayloadTypes: []uint8{96},
		MaxSequenceGap:      10,
		MaxTimestampJump:    90000 * 10, // 10 seconds
	})

	ssrc := uint32(12345)

	// First packet establishes baseline
	packet1 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1000,
			Timestamp:      90000,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet1))

	// Next packet in sequence - should pass
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1001,
			Timestamp:      90100,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet2))

	// Small gap - should pass
	packet3 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1005,
			Timestamp:      90500,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet3))

	// Large gap - should fail
	packet4 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1020,
			Timestamp:      92000,
			SSRC:           ssrc,
		},
	}
	assert.Equal(t, ErrSequenceGap, validator.ValidatePacket(packet4))
}

func TestValidator_SequenceNumberWrapAround(t *testing.T) {
	validator := NewValidator(nil)
	ssrc := uint32(12345)

	// Near max sequence number
	packet1 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 65534,
			Timestamp:      90000,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet1))

	// Next packet
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 65535,
			Timestamp:      90100,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet2))

	// Wrap around to 0
	packet3 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 0,
			Timestamp:      90200,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet3))
}

func TestValidator_TimestampValidation(t *testing.T) {
	validator := NewValidator(&ValidatorConfig{
		AllowedPayloadTypes: []uint8{96},
		MaxTimestampJump:    90000, // 1 second at 90kHz
	})

	ssrc := uint32(12345)

	// First packet
	packet1 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1000,
			Timestamp:      90000,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet1))

	// Normal progression
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1001,
			Timestamp:      90100,
			SSRC:           ssrc,
		},
	}
	require.NoError(t, validator.ValidatePacket(packet2))

	// Large jump - should fail
	packet3 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1002,
			Timestamp:      190000, // 100k jump
			SSRC:           ssrc,
		},
	}
	assert.Equal(t, ErrTimestampJump, validator.ValidatePacket(packet3))
}

func TestValidator_MultipleSSRC(t *testing.T) {
	validator := NewValidator(nil)

	// Packets from different SSRCs should be tracked independently
	packet1 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1000,
			SSRC:           11111,
		},
	}
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 2000,
			SSRC:           22222,
		},
	}
	packet3 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1001,
			SSRC:           11111,
		},
	}

	require.NoError(t, validator.ValidatePacket(packet1))
	require.NoError(t, validator.ValidatePacket(packet2))
	require.NoError(t, validator.ValidatePacket(packet3))

	// Check stats
	count1, exists1 := validator.GetSSRCStats(11111)
	assert.True(t, exists1)
	assert.Equal(t, uint64(2), count1)

	count2, exists2 := validator.GetSSRCStats(22222)
	assert.True(t, exists2)
	assert.Equal(t, uint64(1), count2)
}

func TestValidator_ResetSSRC(t *testing.T) {
	validator := NewValidator(nil)
	ssrc := uint32(12345)

	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1000,
			SSRC:           ssrc,
		},
	}

	require.NoError(t, validator.ValidatePacket(packet))
	
	count, exists := validator.GetSSRCStats(ssrc)
	assert.True(t, exists)
	assert.Equal(t, uint64(1), count)

	// Reset SSRC
	validator.ResetSSRC(ssrc)

	count, exists = validator.GetSSRCStats(ssrc)
	assert.False(t, exists)
	assert.Equal(t, uint64(0), count)
}

func TestValidator_ConcurrentAccess(t *testing.T) {
	validator := NewValidator(nil)
	done := make(chan bool)

	// Multiple goroutines validating packets
	for i := 0; i < 10; i++ {
		go func(id int) {
			ssrc := uint32(id)
			for j := 0; j < 100; j++ {
				packet := &rtp.Packet{
					Header: rtp.Header{
						Version:        2,
						PayloadType:    96,
						SequenceNumber: uint16(j),
						SSRC:           ssrc,
					},
				}
				_ = validator.ValidatePacket(packet)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all SSRCs were tracked
	for i := 0; i < 10; i++ {
		count, exists := validator.GetSSRCStats(uint32(i))
		assert.True(t, exists)
		assert.Equal(t, uint64(100), count)
	}
}
