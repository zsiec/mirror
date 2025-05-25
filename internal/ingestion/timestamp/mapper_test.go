package timestamp

import (
	"testing"
	"time"
)

func TestTimestampMapper_Basic(t *testing.T) {
	mapper := NewTimestampMapper(90000)

	// First timestamp should return base PTS (0)
	pts1 := mapper.ToPTS(1000)
	if pts1 != 0 {
		t.Errorf("First PTS should be 0, got %d", pts1)
	}

	// Second timestamp should show delta
	pts2 := mapper.ToPTS(4000)
	expectedPTS2 := int64(3000) // 4000 - 1000
	if pts2 != expectedPTS2 {
		t.Errorf("Second PTS should be %d, got %d", expectedPTS2, pts2)
	}

	// Third timestamp
	pts3 := mapper.ToPTS(7000)
	expectedPTS3 := int64(6000) // 7000 - 1000
	if pts3 != expectedPTS3 {
		t.Errorf("Third PTS should be %d, got %d", expectedPTS3, pts3)
	}
}

func TestTimestampMapper_Wrap(t *testing.T) {
	mapper := NewTimestampMapper(90000)

	// Start near wrap boundary
	baseTimestamp := uint32(0xFFFFF000)
	pts1 := mapper.ToPTS(baseTimestamp)
	if pts1 != 0 {
		t.Errorf("First PTS should be 0, got %d", pts1)
	}

	// Timestamp before wrap
	pts2 := mapper.ToPTS(0xFFFFFF00)
	expectedPTS2 := int64(0xF00)
	if pts2 != expectedPTS2 {
		t.Errorf("Pre-wrap PTS should be %d, got %d", expectedPTS2, pts2)
	}

	// Timestamp after wrap
	pts3 := mapper.ToPTS(0x00000100)
	// Expected: 0x100 + 2^32 - base
	expectedPTS3 := int64(0x100) + (int64(1) << 32) - int64(baseTimestamp)
	if pts3 != expectedPTS3 {
		t.Errorf("Post-wrap PTS should be %d, got %d", expectedPTS3, pts3)
	}

	// Check wrap count
	if mapper.GetWrapCount() != 1 {
		t.Errorf("Wrap count should be 1, got %d", mapper.GetWrapCount())
	}
}

func TestTimestampMapper_MultipleWraps(t *testing.T) {
	mapper := NewTimestampMapper(90000)

	// First, establish base
	_ = mapper.ToPTS(0)

	// Set to just before wrap
	mapper.lastRTPTime = 0xFFFFFFF0

	// Force first wrap with small timestamp
	pts1 := mapper.ToPTS(0x10)
	if mapper.GetWrapCount() != 1 {
		t.Errorf("Should have 1 wrap after first wrap, got %d", mapper.GetWrapCount())
	}

	// Set to just before wrap again
	mapper.lastRTPTime = 0xFFFFFFF0

	// Force second wrap
	pts2 := mapper.ToPTS(0x10)
	if mapper.GetWrapCount() != 2 {
		t.Errorf("Should have 2 wraps after second wrap, got %d", mapper.GetWrapCount())
	}

	// Verify PTS continues to increase
	if pts2 <= pts1 {
		t.Errorf("PTS should continue increasing: pts1=%d, pts2=%d", pts1, pts2)
	}
}

func TestTimestampMapper_WallClockTime(t *testing.T) {
	mapper := NewTimestampMapper(90000)

	// Set base time
	baseTime := time.Now()
	mapper.baseWallTime = baseTime

	// First timestamp
	pts1 := mapper.ToPTS(0)
	wallTime1 := mapper.GetWallClockTime(pts1)
	if !wallTime1.Equal(baseTime) {
		t.Errorf("First wall time should equal base time")
	}

	// One second later (90000 ticks at 90kHz)
	pts2 := mapper.ToPTS(90000)
	wallTime2 := mapper.GetWallClockTime(pts2)
	expectedTime2 := baseTime.Add(1 * time.Second)
	if diff := wallTime2.Sub(expectedTime2); diff > time.Millisecond {
		t.Errorf("Wall time 2 off by %v", diff)
	}

	// Half second later
	pts3 := mapper.ToPTS(45000)
	wallTime3 := mapper.GetWallClockTime(pts3)
	expectedTime3 := baseTime.Add(500 * time.Millisecond)
	if diff := wallTime3.Sub(expectedTime3); diff > time.Millisecond {
		t.Errorf("Wall time 3 off by %v", diff)
	}
}

func TestTimestampMapper_ValidateTimestamp(t *testing.T) {
	mapper := NewTimestampMapper(90000)

	// First timestamp is always valid
	if !mapper.ValidateTimestamp(1000) {
		t.Error("First timestamp should be valid")
	}

	// Small delta is valid
	mapper.ToPTS(1000) // Set base
	if !mapper.ValidateTimestamp(4000) {
		t.Error("Small delta should be valid")
	}

	// Large jump is invalid (more than 10 seconds)
	if mapper.ValidateTimestamp(1000000) {
		t.Error("Large jump should be invalid")
	}

	// Backward jump near wrap is valid
	mapper.lastRTPTime = 0xFFFFF000
	if !mapper.ValidateTimestamp(0x100) {
		t.Error("Wrap-around should be valid")
	}
}

func TestTimestampMapper_Reset(t *testing.T) {
	mapper := NewTimestampMapper(90000)

	// Use mapper
	mapper.ToPTS(1000)
	mapper.ToPTS(2000)
	mapper.ToPTS(3000)

	// Verify state is set
	stats := mapper.GetStats()
	if stats.LastRTPTime == 0 || stats.LastPTS == 0 {
		t.Error("Mapper should have state before reset")
	}

	// Reset
	mapper.Reset()

	// Verify state is cleared
	stats = mapper.GetStats()
	if stats.LastRTPTime != 0 || stats.LastPTS != 0 || stats.WrapCount != 0 {
		t.Error("Mapper state should be cleared after reset")
	}
}

func TestTimestampMapper_AudioClockRate(t *testing.T) {
	// Test with 48kHz audio clock
	mapper := NewTimestampMapper(48000)

	// First timestamp
	pts1 := mapper.ToPTS(0)
	if pts1 != 0 {
		t.Errorf("First PTS should be 0, got %d", pts1)
	}

	// One second of audio (48000 ticks)
	pts2 := mapper.ToPTS(48000)
	if pts2 != 48000 {
		t.Errorf("PTS after 1 second should be 48000, got %d", pts2)
	}

	// Check wall clock conversion
	wallTime := mapper.GetWallClockTime(48000)
	expectedTime := mapper.baseWallTime.Add(1 * time.Second)
	if diff := wallTime.Sub(expectedTime); diff > time.Millisecond {
		t.Errorf("Wall time off by %v", diff)
	}
}

func BenchmarkTimestampMapper_ToPTS(b *testing.B) {
	mapper := NewTimestampMapper(90000)
	timestamp := uint32(1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mapper.ToPTS(timestamp)
		timestamp += 3003 // Simulate 33ms increments
	}
}

func BenchmarkTimestampMapper_Concurrent(b *testing.B) {
	mapper := NewTimestampMapper(90000)

	b.RunParallel(func(pb *testing.PB) {
		timestamp := uint32(1000)
		for pb.Next() {
			mapper.ToPTS(timestamp)
			timestamp += 3003
		}
	})
}
