package rate

import (
	"context"
	"sync"
	"time"

	"github.com/jon-cooper/skeetdelete/internal/types"
	"golang.org/x/time/rate"
)

type Limiter struct {
	limiter *rate.Limiter
	cfg     types.RateLimitConfig

	mu          sync.Mutex
	hourlyCount int
	hourlyStart time.Time
	dailyCount  int
	dailyStart  time.Time
}

func NewLimiter(cfg types.RateLimitConfig) *Limiter {
	now := time.Now()
	return &Limiter{
		limiter:     rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), 1),
		cfg:         cfg,
		hourlyStart: now,
		dailyStart:  now,
	}
}

func (l *Limiter) Wait(ctx context.Context) error {
	return l.limiter.Wait(ctx)
}

func (l *Limiter) Allow() bool {
	return l.limiter.Allow()
}

func (l *Limiter) RecordRequest() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	if now.Sub(l.hourlyStart) > time.Hour {
		l.hourlyCount = 0
		l.hourlyStart = now
	}

	if now.Sub(l.dailyStart) > 24*time.Hour {
		l.dailyCount = 0
		l.dailyStart = now
	}

	l.hourlyCount++
	l.dailyCount++
}

func (l *Limiter) HourlyCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	if now.Sub(l.hourlyStart) > time.Hour {
		l.hourlyCount = 0
		l.hourlyStart = now
	}

	return l.hourlyCount
}

func (l *Limiter) DailyCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	if now.Sub(l.dailyStart) > 24*time.Hour {
		l.dailyCount = 0
		l.dailyStart = now
	}

	return l.dailyCount
}

func (l *Limiter) CanMakeRequest() bool {
	return l.HourlyCount() < l.cfg.MaxPerHour && l.DailyCount() < l.cfg.MaxPerDay
}
