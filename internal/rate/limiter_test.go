package rate

import (
	"sync"
	"testing"
	"time"

	"github.com/jon-cooper/skeetdelete/internal/types"
)

func TestCanMakeRequestReturnsTrueWhenUnderLimits(t *testing.T) {
	cfg := types.RateLimitConfig{
		RequestsPerSecond: 1,
		MaxPerHour:        100,
		MaxPerDay:         1000,
	}
	l := NewLimiter(cfg)

	if !l.CanMakeRequest() {
		t.Error("CanMakeRequest() should return true when no requests have been made")
	}
}

func TestCanMakeRequestReturnsFalseWhenHourlyLimitExceeded(t *testing.T) {
	cfg := types.RateLimitConfig{
		RequestsPerSecond: 1000,
		MaxPerHour:        3,
		MaxPerDay:         1000,
	}
	l := NewLimiter(cfg)

	for i := 0; i < 3; i++ {
		l.RecordRequest()
	}

	if l.CanMakeRequest() {
		t.Error("CanMakeRequest() should return false after hourly limit exceeded")
	}
}

func TestCanMakeRequestReturnsFalseWhenDailyLimitExceeded(t *testing.T) {
	cfg := types.RateLimitConfig{
		RequestsPerSecond: 1000,
		MaxPerHour:        1000,
		MaxPerDay:         3,
	}
	l := NewLimiter(cfg)

	for i := 0; i < 3; i++ {
		l.RecordRequest()
	}

	if l.CanMakeRequest() {
		t.Error("CanMakeRequest() should return false after daily limit exceeded")
	}
}

func TestRecordRequestIncrementsCounts(t *testing.T) {
	cfg := types.RateLimitConfig{
		RequestsPerSecond: 1,
		MaxPerHour:        100,
		MaxPerDay:         1000,
	}
	l := NewLimiter(cfg)

	l.RecordRequest()
	if l.HourlyCount() != 1 {
		t.Errorf("expected hourly count 1, got %d", l.HourlyCount())
	}
	if l.DailyCount() != 1 {
		t.Errorf("expected daily count 1, got %d", l.DailyCount())
	}

	l.RecordRequest()
	if l.HourlyCount() != 2 {
		t.Errorf("expected hourly count 2, got %d", l.HourlyCount())
	}
	if l.DailyCount() != 2 {
		t.Errorf("expected daily count 2, got %d", l.DailyCount())
	}
}

func TestHourlyWindowResetsAfterOneHour(t *testing.T) {
	cfg := types.RateLimitConfig{
		RequestsPerSecond: 1000,
		MaxPerHour:        1,
		MaxPerDay:         1000,
	}
	l := NewLimiter(cfg)

	l.RecordRequest()
	if l.CanMakeRequest() {
		t.Error("should be at hourly limit after 1 request")
	}

	// Simulate moving the hourly window forward by overwriting hourlyStart
	l.mu.Lock()
	l.hourlyStart = time.Now().Add(-time.Hour - time.Second)
	l.mu.Unlock()

	// HourlyCount should reset since the window expired
	if l.HourlyCount() != 0 {
		t.Errorf("expected hourly count 0 after window expired, got %d", l.HourlyCount())
	}
	if !l.CanMakeRequest() {
		t.Error("CanMakeRequest() should return true after hourly window resets")
	}
}

func TestDailyWindowResetsAfter24Hours(t *testing.T) {
	cfg := types.RateLimitConfig{
		RequestsPerSecond: 1000,
		MaxPerHour:        1000,
		MaxPerDay:         1,
	}
	l := NewLimiter(cfg)

	l.RecordRequest()
	if l.CanMakeRequest() {
		t.Error("should be at daily limit after 1 request")
	}

	// Simulate moving the daily window forward
	l.mu.Lock()
	l.dailyStart = time.Now().Add(-25 * time.Hour)
	l.mu.Unlock()

	if l.DailyCount() != 0 {
		t.Errorf("expected daily count 0 after window expired, got %d", l.DailyCount())
	}
	if !l.CanMakeRequest() {
		t.Error("CanMakeRequest() should return true after daily window resets")
	}
}

func TestConcurrentRecordRequest(t *testing.T) {
	cfg := types.RateLimitConfig{
		RequestsPerSecond: 1000,
		MaxPerHour:        10000,
		MaxPerDay:         100000,
	}
	l := NewLimiter(cfg)

	const goroutines = 100
	const requestsPerGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				l.RecordRequest()
			}
		}()
	}
	wg.Wait()

	expected := goroutines * requestsPerGoroutine
	if l.HourlyCount() != expected {
		t.Errorf("expected hourly count %d, got %d", expected, l.HourlyCount())
	}
	if l.DailyCount() != expected {
		t.Errorf("expected daily count %d, got %d", expected, l.DailyCount())
	}
}
