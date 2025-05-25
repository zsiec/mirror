package ingestion

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestStreamHandler_DetectFrameCorruption(t *testing.T) {
	logger := logrus.New()
	
	// Create a minimal handler to test corruption detection
	handler := &StreamHandler{
		codec:  types.CodecH264,
		logger: logger,
	}
	
	tests := []struct {
		name     string
		frame    *types.VideoFrame
		wantCorrupted bool
	}{
		{
			name:          "nil frame",
			frame:         nil,
			wantCorrupted: true,
		},
		{
			name: "empty NAL units",
			frame: &types.VideoFrame{
				ID:          1,
				TotalSize:   100,
				CaptureTime: time.Now(),
				NALUnits:    []types.NALUnit{},
			},
			wantCorrupted: true,
		},
		{
			name: "invalid size",
			frame: &types.VideoFrame{
				ID:          1,
				TotalSize:   0,
				CaptureTime: time.Now(),
				NALUnits:    []types.NALUnit{{Data: []byte{0, 0, 0, 1}}},
			},
			wantCorrupted: true,
		},
		{
			name: "size too large",
			frame: &types.VideoFrame{
				ID:          1,
				TotalSize:   11 * 1024 * 1024, // > 10MB
				CaptureTime: time.Now(),
				NALUnits:    []types.NALUnit{{Data: []byte{0, 0, 0, 1}}},
			},
			wantCorrupted: true,
		},
		{
			name: "zero capture time",
			frame: &types.VideoFrame{
				ID:          1,
				TotalSize:   100,
				CaptureTime: time.Time{},
				NALUnits:    []types.NALUnit{{Data: []byte{0, 0, 0, 1}}},
			},
			wantCorrupted: true,
		},
		{
			name: "future capture time",
			frame: &types.VideoFrame{
				ID:          1,
				TotalSize:   100,
				CaptureTime: time.Now().Add(2 * time.Hour),
				NALUnits:    []types.NALUnit{{Data: []byte{0, 0, 0, 1}}},
			},
			wantCorrupted: true,
		},
		{
			name: "valid frame",
			frame: &types.VideoFrame{
				ID:          1,
				TotalSize:   100,
				CaptureTime: time.Now(),
				NALUnits:    []types.NALUnit{{Data: []byte{0, 0, 0, 1, 0x41}}}, // Valid NAL
			},
			wantCorrupted: false,
		},
		{
			name: "already marked corrupted",
			frame: &types.VideoFrame{
				ID:          1,
				TotalSize:   100,
				CaptureTime: time.Now(),
				NALUnits:    []types.NALUnit{{Data: []byte{0, 0, 0, 1}}},
				Flags:       types.FrameFlagCorrupted,
			},
			wantCorrupted: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handler.detectFrameCorruption(tt.frame)
			assert.Equal(t, tt.wantCorrupted, got)
		})
	}
}

// TestStreamHandler_NALUnitValidation tests NAL unit validation for different scenarios
func TestStreamHandler_NALUnitValidation(t *testing.T) {
	tests := []struct {
		name              string
		nalUnits          []types.NALUnit
		codec             types.CodecType
		shouldBeCorrupted bool
	}{
		{
			name: "valid_h264_with_start_code",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x01, 0x02, 0x03}, // Start code + valid NAL header
				},
			},
			codec:             types.CodecH264,
			shouldBeCorrupted: false,
		},
		{
			name: "valid_h264_without_start_code_rtp",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{0x41, 0x01, 0x02, 0x03}, // No start code (RTP style)
				},
			},
			codec:             types.CodecH264,
			shouldBeCorrupted: false,
		},
		{
			name: "invalid_h264_forbidden_bit_set",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{0x00, 0x00, 0x00, 0x01, 0xC1, 0x01, 0x02, 0x03}, // Forbidden bit set
				},
			},
			codec:             types.CodecH264,
			shouldBeCorrupted: true,
		},
		{
			name: "invalid_partial_start_code",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{0x00, 0x00, 0x02, 0x01}, // Looks like start code but isn't
				},
			},
			codec:             types.CodecH264,
			shouldBeCorrupted: true,
		},
		{
			name: "valid_h265_with_3_byte_start_code",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{0x00, 0x00, 0x01, 0x01, 0x01, 0x02, 0x03}, // 3-byte start code
				},
			},
			codec:             types.CodecHEVC,
			shouldBeCorrupted: false,
		},
		{
			name: "empty_nal_data",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{},
				},
			},
			codec:             types.CodecH264,
			shouldBeCorrupted: true,
		},
		{
			name: "mixed_valid_and_invalid",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{0x41, 0x01, 0x02, 0x03}, // Valid RTP-style
				},
				{
					Type: 6, // SEI
					Data: []byte{}, // Invalid empty
				},
			},
			codec:             types.CodecH264,
			shouldBeCorrupted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler
			handler := &StreamHandler{
				streamID: "test-stream",
				codec:    tt.codec,
				logger:   logrus.NewEntry(logrus.New()),
			}

			// Create frame with NAL units
			frame := &types.VideoFrame{
				ID:          1,
				Type:        types.FrameTypeP,
				NALUnits:    tt.nalUnits,
				TotalSize:   100,
				CaptureTime: time.Now(),
			}

			// Test corruption detection
			isCorrupted := handler.detectFrameCorruption(frame)
			assert.Equal(t, tt.shouldBeCorrupted, isCorrupted, 
				"Expected corruption detection to be %v but got %v", 
				tt.shouldBeCorrupted, isCorrupted)
		})
	}
}

// TestStreamHandler_OnRateChange tests rate change handling for different connection types
func TestStreamHandler_OnRateChange(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	
	tests := []struct {
		name       string
		conn       StreamConnection
		expectLog  string
	}{
		{
			name:      "unknown connection type",
			conn:      &MockConnection{},
			expectLog: "Rate control not supported for connection type",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler with the test connection
			handler := &StreamHandler{
				streamID: "test-stream",
				conn:     tt.conn,
				logger:   logger,
			}
			
			// Call onRateChange - should not panic
			assert.NotPanics(t, func() {
				handler.onRateChange(100000)
			})
		})
	}
}

// MockConnection implements StreamConnection for testing
type MockConnection struct{}

func (m *MockConnection) GetStreamID() string {
	return "test-stream"
}

func (m *MockConnection) Read(b []byte) (int, error) {
	return 0, nil
}

func (m *MockConnection) Close() error {
	return nil
}
