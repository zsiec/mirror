package security

import (
	"bytes"
	"testing"
)

func TestReadLEB128(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		want      uint64
		wantBytes int
		wantErr   bool
		errType   error
	}{
		{
			name:      "zero",
			data:      []byte{0x00},
			want:      0,
			wantBytes: 1,
		},
		{
			name:      "one_byte_value",
			data:      []byte{0x7F},
			want:      127,
			wantBytes: 1,
		},
		{
			name:      "two_byte_value",
			data:      []byte{0x80, 0x01},
			want:      128,
			wantBytes: 2,
		},
		{
			name:      "max_uint32",
			data:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F},
			want:      0xFFFFFFFF,
			wantBytes: 5,
		},
		{
			name:      "large_value",
			data:      []byte{0xE5, 0x8E, 0x26},
			want:      624485,
			wantBytes: 3,
		},
		{
			name:      "max_uint64",
			data:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01},
			want:      0xFFFFFFFFFFFFFFFF,
			wantBytes: 10,
		},
		{
			name:      "incomplete_encoding",
			data:      []byte{0x80}, // Continuation bit set but no more bytes
			wantErr:   true,
			errType:   ErrLEB128Incomplete,
			wantBytes: 1,
		},
		{
			name:      "empty_data",
			data:      []byte{},
			wantErr:   true,
			errType:   ErrLEB128Incomplete,
			wantBytes: 0,
		},
		{
			name:      "too_many_bytes",
			data:      []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}, // 9 bytes with continuation
			wantErr:   true,
			errType:   ErrLEB128Incomplete, // Last byte has continuation bit, so it's incomplete
			wantBytes: 9,
		},
		{
			name:      "overflow_uint64",
			data:      []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x02}, // Would overflow uint64
			wantErr:   true,
			errType:   ErrLEB128Overflow,
			wantBytes: 10,
		},
		{
			name:      "with_trailing_data",
			data:      []byte{0x7F, 0xAA, 0xBB, 0xCC}, // Extra bytes after complete LEB128
			want:      127,
			wantBytes: 1,
		},
		{
			name:      "exactly_too_many_bytes",
			data:      []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}, // 11 bytes
			wantErr:   true,
			errType:   ErrLEB128TooManyBytes,
			wantBytes: MaxLEB128Bytes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotBytes, err := ReadLEB128(tt.data)

			if (err != nil) != tt.wantErr {
				t.Errorf("ReadLEB128() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errType != nil && err != tt.errType {
				t.Errorf("ReadLEB128() error = %v, want %v", err, tt.errType)
			}

			if gotBytes != tt.wantBytes {
				t.Errorf("ReadLEB128() gotBytes = %v, want %v", gotBytes, tt.wantBytes)
			}

			if !tt.wantErr && got != tt.want {
				t.Errorf("ReadLEB128() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadLEB128FromReader(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		want      uint64
		wantBytes int
		wantErr   bool
	}{
		{
			name:      "simple_value",
			data:      []byte{0x7F},
			want:      127,
			wantBytes: 1,
		},
		{
			name:      "multi_byte_value",
			data:      []byte{0xE5, 0x8E, 0x26},
			want:      624485,
			wantBytes: 3,
		},
		{
			name:      "eof_incomplete",
			data:      []byte{0x80}, // Continuation bit but EOF
			wantErr:   true,
			wantBytes: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader(tt.data)
			got, gotBytes, err := ReadLEB128FromReader(r)

			if (err != nil) != tt.wantErr {
				t.Errorf("ReadLEB128FromReader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if gotBytes != tt.wantBytes {
				t.Errorf("ReadLEB128FromReader() gotBytes = %v, want %v", gotBytes, tt.wantBytes)
			}

			if !tt.wantErr && got != tt.want {
				t.Errorf("ReadLEB128FromReader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteLEB128(t *testing.T) {
	tests := []struct {
		name  string
		value uint64
		want  []byte
	}{
		{
			name:  "zero",
			value: 0,
			want:  []byte{0x00},
		},
		{
			name:  "small_value",
			value: 127,
			want:  []byte{0x7F},
		},
		{
			name:  "two_bytes",
			value: 128,
			want:  []byte{0x80, 0x01},
		},
		{
			name:  "large_value",
			value: 624485,
			want:  []byte{0xE5, 0x8E, 0x26},
		},
		{
			name:  "max_uint32",
			value: 0xFFFFFFFF,
			want:  []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WriteLEB128(tt.value)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("WriteLEB128() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLEB128RoundTrip(t *testing.T) {
	// Test that encoding and decoding produces the same value
	values := []uint64{
		0, 1, 127, 128, 255, 256, 1000,
		0xFFFF, 0xFFFFFF, 0xFFFFFFFF,
		0xFFFFFFFFFFFF, 0xFFFFFFFFFFFFFF,
	}

	for _, value := range values {
		encoded := WriteLEB128(value)
		decoded, bytes, err := ReadLEB128(encoded)
		if err != nil {
			t.Errorf("Failed to decode LEB128 for value %d: %v", value, err)
			continue
		}
		if decoded != value {
			t.Errorf("Round trip failed: got %d, want %d", decoded, value)
		}
		if bytes != len(encoded) {
			t.Errorf("Byte count mismatch: got %d, want %d", bytes, len(encoded))
		}
	}
}

func TestLEB128Size(t *testing.T) {
	tests := []struct {
		value uint64
		want  int
	}{
		{0, 1},
		{127, 1},
		{128, 2},
		{16383, 2},
		{16384, 3},
		{0xFFFFFFFF, 5},
		{0xFFFFFFFFFFFF, 7},
		{0xFFFFFFFFFFFFFF, 8},
		{0xFFFFFFFFFFFFFFFF, 10},
	}

	for _, tt := range tests {
		got := LEB128Size(tt.value)
		if got != tt.want {
			t.Errorf("LEB128Size(%d) = %d, want %d", tt.value, got, tt.want)
		}

		// Verify by encoding
		encoded := WriteLEB128(tt.value)
		if len(encoded) != tt.want {
			t.Errorf("Encoded length for %d = %d, want %d", tt.value, len(encoded), tt.want)
		}
	}
}

func TestLEB128EdgeCases(t *testing.T) {
	// Test malicious inputs that could cause issues
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		desc    string
	}{
		{
			name:    "all_continuation_bits",
			data:    []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
			wantErr: true,
			desc:    "All bytes have continuation bit set",
		},
		{
			name:    "overflow_by_one",
			data:    []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02},
			wantErr: true,
			desc:    "Value that would be exactly 2^64",
		},
		{
			name:    "max_bytes_with_valid_end",
			data:    []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00},
			wantErr: false,
			desc:    "Maximum allowed bytes but valid encoding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ReadLEB128(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("%s: error = %v, wantErr = %v", tt.desc, err, tt.wantErr)
			}
		})
	}
}

func BenchmarkReadLEB128(b *testing.B) {
	// Benchmark with various sized values
	data := []byte{0xE5, 0x8E, 0x26} // 3-byte encoding

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ReadLEB128(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriteLEB128(b *testing.B) {
	value := uint64(624485)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = WriteLEB128(value)
	}
}
