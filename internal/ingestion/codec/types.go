package codec

import (
	"fmt"
	"strings"
)

// Type represents a video codec type
type Type string

const (
	// TypeHEVC represents H.265/HEVC codec
	TypeHEVC Type = "HEVC"
	// TypeH264 represents H.264/AVC codec
	TypeH264 Type = "H264"
	// TypeAV1 represents AV1 codec
	TypeAV1 Type = "AV1"
	// TypeJPEGXS represents JPEG XS codec
	TypeJPEGXS Type = "JPEGXS"
	// TypeUnknown represents an unknown codec
	TypeUnknown Type = "UNKNOWN"
)

// String returns the string representation of the codec type
func (t Type) String() string {
	return string(t)
}

// IsValid checks if the codec type is valid and supported
func (t Type) IsValid() bool {
	switch t {
	case TypeHEVC, TypeH264, TypeAV1, TypeJPEGXS:
		return true
	default:
		return false
	}
}

// ParseType parses a string into a codec type
func ParseType(s string) Type {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "HEVC", "H265", "H.265":
		return TypeHEVC
	case "H264", "H.264", "AVC":
		return TypeH264
	case "AV1":
		return TypeAV1
	case "JPEGXS", "JXS", "JPEG-XS":
		return TypeJPEGXS
	default:
		return TypeUnknown
	}
}

// Info contains codec-specific information
type Info struct {
	Type       Type
	Profile    string
	Level      string
	Width      int
	Height     int
	FrameRate  float64
	BitDepth   int
	ChromaFmt  string // e.g., "4:2:0", "4:2:2"
	Parameters map[string]string
}

// Validate checks if the codec info is valid
func (i *Info) Validate() error {
	if !i.Type.IsValid() {
		return fmt.Errorf("invalid codec type: %s", i.Type)
	}

	// Codec-specific validation
	switch i.Type {
	case TypeH264:
		return i.validateH264()
	case TypeHEVC:
		return i.validateHEVC()
	case TypeAV1:
		return i.validateAV1()
	case TypeJPEGXS:
		return i.validateJPEGXS()
	}

	return nil
}

func (i *Info) validateH264() error {
	validProfiles := map[string]bool{
		"baseline": true,
		"main":     true,
		"high":     true,
		"high10":   true,
		"high422":  true,
		"high444":  true,
	}

	if i.Profile != "" && !validProfiles[strings.ToLower(i.Profile)] {
		return fmt.Errorf("invalid H.264 profile: %s", i.Profile)
	}

	return nil
}

func (i *Info) validateHEVC() error {
	validProfiles := map[string]bool{
		"main":    true,
		"main10":  true,
		"main444": true,
		"mainsp":  true,
	}

	if i.Profile != "" && !validProfiles[strings.ToLower(i.Profile)] {
		return fmt.Errorf("invalid HEVC profile: %s", i.Profile)
	}

	return nil
}

func (i *Info) validateAV1() error {
	// AV1 profiles: 0 (Main), 1 (High), 2 (Professional)
	validProfiles := map[string]bool{
		"0":            true,
		"1":            true,
		"2":            true,
		"main":         true,
		"high":         true,
		"professional": true,
	}

	if i.Profile != "" && !validProfiles[strings.ToLower(i.Profile)] {
		return fmt.Errorf("invalid AV1 profile: %s", i.Profile)
	}

	return nil
}

func (i *Info) validateJPEGXS() error {
	validProfiles := map[string]bool{
		"light":         true,
		"main":          true,
		"high":          true,
		"high444.12":    true,
		"light-subline": true,
		"main-subline":  true,
	}

	if i.Profile != "" && !validProfiles[strings.ToLower(i.Profile)] {
		return fmt.Errorf("invalid JPEG XS profile: %s", i.Profile)
	}

	return nil
}
