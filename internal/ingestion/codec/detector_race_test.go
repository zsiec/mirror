package codec

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

// TestP2_9_CodecDetectorRaceCondition demonstrates the race condition
// when multiple goroutines access the detector concurrently
func TestP2_9_CodecDetectorRaceCondition(t *testing.T) {
	// This test will fail when run with -race flag
	// go test -race -run TestP2_9_CodecDetectorRaceCondition
	
	detector := NewDetector()
	
	// Number of concurrent operations
	numGoroutines := 10
	iterations := 100
	
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // 3 types of operations
	
	// Track if race was detected (for demonstration)
	raceDetected := false
	
	// Goroutines that parse SDP (writes to payloadTypeMap)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				sdp := fmt.Sprintf(`v=0
o=- 0 0 IN IP4 127.0.0.1
s=Test
c=IN IP4 127.0.0.1
t=0 0
m=video 5004 RTP/AVP %d
a=rtpmap:%d H264/90000
a=fmtp:%d profile-level-id=42e01e`, id+96, id+96, id+96)
				
				_, _, err := detector.DetectFromSDP(sdp)
				if err != nil {
					t.Logf("SDP detection error: %v", err)
				}
			}
		}(i)
	}
	
	// Goroutines that detect from RTP packets (reads from payloadTypeMap)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				packet := &rtp.Packet{
					Header: rtp.Header{
						PayloadType: uint8(id + 96),
					},
					Payload: []byte{0x41, 0x00, 0x00, 0x00}, // H.264 NAL
				}
				
				_, err := detector.DetectFromRTPPacket(packet)
				if err != nil {
					// This is expected when map doesn't have the entry yet
				}
			}
		}(i)
	}
	
	// Goroutines that reset the detector (writes to payloadTypeMap)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				detector.Reset()
				time.Sleep(time.Microsecond) // Small delay to increase race likelihood
			}
		}()
	}
	
	// Wait for all goroutines
	done := make(chan bool)
	go func() {
		wg.Wait()
		close(done)
	}()
	
	// Wait with timeout
	select {
	case <-done:
		// Completed without deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("Test timeout - possible deadlock")
		raceDetected = true
	}
	
	// Note: This test demonstrates the race condition
	// Run with: go test -race -run TestP2_9_CodecDetectorRaceCondition
	// The race detector will report concurrent map access
	t.Log("Test completed. Run with -race flag to detect the race condition")
	_ = raceDetected
}

// TestP2_9_CodecDetectorConcurrentSafety shows the desired behavior after fix
func TestP2_9_CodecDetectorConcurrentSafety(t *testing.T) {
	// Test is no longer skipped - thread-safe detector has been implemented
	
	// After fix, the detector should handle concurrent access safely
	detector := NewDetector()
	
	numGoroutines := 20
	iterations := 1000
	
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*iterations)
	
	// Concurrent SDP parsing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				sdp := fmt.Sprintf(`v=0
m=video 5004 RTP/AVP %d
a=rtpmap:%d H264/90000`, id+96, id+96)
				
				codecType, info, err := detector.DetectFromSDP(sdp)
				if err != nil {
					errors <- err
					continue
				}
				
				// Verify detection worked correctly
				if codecType != TypeH264 {
					errors <- fmt.Errorf("expected H264, got %v", codecType)
				}
				if info.Type != TypeH264 {
					errors <- fmt.Errorf("info type mismatch")
				}
			}
		}(i)
	}
	
	// Concurrent RTP detection
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// Wait a bit for SDP parsing to populate the map
			time.Sleep(10 * time.Millisecond)
			
			for j := 0; j < iterations; j++ {
				packet := &rtp.Packet{
					Header: rtp.Header{
						PayloadType: uint8(id + 96),
					},
					Payload: []byte{0x41, 0x00, 0x00, 0x00},
				}
				
				codecType, err := detector.DetectFromRTPPacket(packet)
				if err == nil && codecType != TypeH264 {
					errors <- fmt.Errorf("expected H264 from RTP, got %v", codecType)
				}
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Logf("Error: %v", err)
		errorCount++
	}
	
	assert.Equal(t, 0, errorCount, "No errors should occur with thread-safe implementation")
}

// TestP2_9_DetectorStateCorruption verifies state corruption is prevented
func TestP2_9_DetectorStateCorruption(t *testing.T) {
	detector := NewDetector()
	
	// Pre-populate with some mappings via SDP
	for i := 96; i < 106; i++ {
		sdp := fmt.Sprintf(`v=0
m=video 0 RTP/AVP %d
a=rtpmap:%d H264/90000`, i, i)
		_, _, err := detector.DetectFromSDP(sdp)
		assert.NoError(t, err)
	}
	
	var wg sync.WaitGroup
	detectedTypes := make(chan Type, 10000)
	
	// Reader goroutine - continuously detects from RTP packets
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			// Try to detect codec for various payload types
			for j := 96; j < 106; j++ {
				packet := &rtp.Packet{
					Header: rtp.Header{
						PayloadType: uint8(j),
					},
					Payload: []byte{0x41, 0x00, 0x00, 0x00},
				}
				codecType, _ := detector.DetectFromRTPPacket(packet)
				if codecType != TypeUnknown {
					detectedTypes <- codecType
				}
			}
		}
	}()
	
	// Writer goroutine - resets and repopulates
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			// Reset clears the mappings
			detector.Reset()
			
			// Re-populate with HEVC
			for j := 96; j < 106; j++ {
				sdp := fmt.Sprintf(`v=0
m=video 0 RTP/AVP %d
a=rtpmap:%d H265/90000`, j, j)
				_, _, _ = detector.DetectFromSDP(sdp)
			}
			
			time.Sleep(time.Microsecond)
		}
	}()
	
	wg.Wait()
	close(detectedTypes)
	
	// Verify we got valid detections without panics
	h264Count := 0
	hevcCount := 0
	for codecType := range detectedTypes {
		switch codecType {
		case TypeH264:
			h264Count++
		case TypeHEVC:
			hevcCount++
		}
	}
	
	t.Logf("Detected H264: %d, HEVC: %d", h264Count, hevcCount)
	// We should have detected both types during the concurrent operations
	assert.Greater(t, h264Count, 0, "Should have detected some H264")
	assert.Greater(t, hevcCount, 0, "Should have detected some HEVC after reset")
}
