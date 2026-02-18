package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/security"
)

func TestPacketValidator_ValidateMPEGTSPacket(t *testing.T) {
	v := NewPacketValidator()

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid packet",
			data:    make([]byte, 188),
			wantErr: false,
		},
		{
			name:    "valid packet with sync byte",
			data:    append([]byte{0x47}, make([]byte, 187)...),
			wantErr: false,
		},
		{
			name:    "invalid size - too small",
			data:    make([]byte, 100),
			wantErr: true,
			errMsg:  "invalid MPEG-TS packet size",
		},
		{
			name:    "invalid size - too large",
			data:    make([]byte, 200),
			wantErr: true,
			errMsg:  "invalid MPEG-TS packet size",
		},
		{
			name:    "invalid sync byte",
			data:    append([]byte{0x48}, make([]byte, 187)...),
			wantErr: true,
			errMsg:  "invalid MPEG-TS sync byte",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set sync byte for valid packets
			if tt.name == "valid packet" && len(tt.data) == 188 {
				tt.data[0] = 0x47
			}

			err := v.ValidateMPEGTSPacket(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPacketValidator_ValidateRTPPacket(t *testing.T) {
	v := NewPacketValidator()

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid RTP packet",
			data:    append([]byte{0x80, 0x60}, make([]byte, 10)...), // Version 2
			wantErr: false,
		},
		{
			name:    "too small",
			data:    make([]byte, 5),
			wantErr: true,
			errMsg:  "RTP packet too small",
		},
		{
			name:    "too large",
			data:    make([]byte, 70000),
			wantErr: true,
			errMsg:  "RTP packet too large",
		},
		{
			name:    "invalid version",
			data:    append([]byte{0x00, 0x60}, make([]byte, 10)...), // Version 0
			wantErr: true,
			errMsg:  "invalid RTP version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateRTPPacket(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPacketValidator_ValidateNALUnit(t *testing.T) {
	v := NewPacketValidator()

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid NAL unit",
			data:    []byte{0x67, 0x42, 0x00, 0x0A}, // SPS NAL unit
			wantErr: false,
		},
		{
			name:    "empty NAL unit",
			data:    []byte{},
			wantErr: true,
			errMsg:  "empty NAL unit",
		},
		{
			name:    "forbidden bit set",
			data:    []byte{0x80, 0x42, 0x00, 0x0A}, // Forbidden bit set
			wantErr: true,
			errMsg:  "forbidden_zero_bit set",
		},
		{
			name:    "too large",
			data:    make([]byte, security.MaxNALUnitSize+1),
			wantErr: true,
			errMsg:  "NAL unit too large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateNALUnit(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBoundaryValidator_ValidateBufferAccess(t *testing.T) {
	v := NewBoundaryValidator()
	buffer := make([]byte, 100)

	tests := []struct {
		name    string
		offset  int
		length  int
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid access",
			offset:  10,
			length:  20,
			wantErr: false,
		},
		{
			name:    "access entire buffer",
			offset:  0,
			length:  100,
			wantErr: false,
		},
		{
			name:    "negative offset",
			offset:  -1,
			length:  10,
			wantErr: true,
			errMsg:  "negative offset",
		},
		{
			name:    "negative length",
			offset:  0,
			length:  -1,
			wantErr: true,
			errMsg:  "negative length",
		},
		{
			name:    "offset exceeds buffer",
			offset:  101,
			length:  1,
			wantErr: true,
			errMsg:  "offset 101 exceeds buffer size",
		},
		{
			name:    "access exceeds buffer",
			offset:  90,
			length:  20,
			wantErr: true,
			errMsg:  "exceeds buffer size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateBufferAccess(buffer, tt.offset, tt.length)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBoundaryValidator_ValidateStartCode(t *testing.T) {
	v := NewBoundaryValidator()

	tests := []struct {
		name    string
		data    []byte
		pos     int
		wantLen int
		wantErr bool
	}{
		{
			name:    "3-byte start code",
			data:    []byte{0x00, 0x00, 0x01, 0x67},
			pos:     0,
			wantLen: 3,
			wantErr: false,
		},
		{
			name:    "4-byte start code",
			data:    []byte{0x00, 0x00, 0x00, 0x01, 0x67},
			pos:     0,
			wantLen: 4,
			wantErr: false,
		},
		{
			name:    "no start code",
			data:    []byte{0x00, 0x00, 0x02, 0x67},
			pos:     0,
			wantLen: 0,
			wantErr: true,
		},
		{
			name:    "insufficient bytes",
			data:    []byte{0x00, 0x00},
			pos:     0,
			wantLen: 0,
			wantErr: true,
		},
		{
			name:    "start code at offset",
			data:    []byte{0xFF, 0xFF, 0x00, 0x00, 0x01, 0x67},
			pos:     2,
			wantLen: 3,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			length, err := v.ValidateStartCode(tt.data, tt.pos)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantLen, length)
			}
		})
	}
}

func TestSizeValidator(t *testing.T) {
	v := NewSizeValidator()

	// Test default limits
	tests := []struct {
		name     string
		sizeType string
		size     int64
		wantErr  bool
	}{
		{
			name:     "valid packet size",
			sizeType: "packet",
			size:     1000,
			wantErr:  false,
		},
		{
			name:     "packet too large",
			sizeType: "packet",
			size:     70000,
			wantErr:  true,
		},
		{
			name:     "negative size",
			sizeType: "packet",
			size:     -1,
			wantErr:  true,
		},
		{
			name:     "unknown type",
			sizeType: "unknown",
			size:     100,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateSize(tt.sizeType, tt.size)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}

	// Test custom limit
	v.SetLimit("custom", 500)
	err := v.ValidateSize("custom", 600)
	assert.Error(t, err)

	err = v.ValidateSize("custom", 400)
	assert.NoError(t, err)
}

func TestTimestampValidator(t *testing.T) {
	v := NewTimestampValidator()

	tests := []struct {
		name      string
		pts       int64
		dts       int64
		testPTS   bool
		testDTS   bool
		testOrder bool
		wantErr   bool
		errMsg    string
	}{
		{
			name:    "valid PTS",
			pts:     1000000,
			testPTS: true,
			wantErr: false,
		},
		{
			name:    "negative PTS",
			pts:     -1,
			testPTS: true,
			wantErr: true,
			errMsg:  "negative PTS",
		},
		{
			name:    "PTS exceeds range",
			pts:     1 << 34,
			testPTS: true,
			wantErr: true,
			errMsg:  "exceeds 33-bit range",
		},
		{
			name:    "valid DTS",
			dts:     500000,
			testDTS: true,
			wantErr: false,
		},
		{
			name:      "valid PTS/DTS order",
			pts:       2000000,
			dts:       1000000,
			testOrder: true,
			wantErr:   false,
		},
		{
			name:      "invalid PTS/DTS order",
			pts:       1000000,
			dts:       2000000,
			testOrder: true,
			wantErr:   true,
			errMsg:    "PTS (1000000) is less than DTS (2000000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error

			if tt.testPTS {
				err = v.ValidatePTS(tt.pts)
			} else if tt.testDTS {
				err = v.ValidateDTS(tt.dts)
			} else if tt.testOrder {
				err = v.ValidatePTSDTSOrder(tt.pts, tt.dts)
			}

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
