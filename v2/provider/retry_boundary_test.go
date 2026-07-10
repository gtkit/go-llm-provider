package provider

import (
	"math"
	"testing"
	"time"
)

// TestExponentialBackoffWithJitterMaxDuration 回归：上界 MaxInt64 时
// int64(d)+1 曾溢出为负并触发 rand.Int64N panic。
func TestExponentialBackoffWithJitterMaxDuration(t *testing.T) {
	t.Parallel()

	fn := ExponentialBackoffWithJitter(time.Duration(math.MaxInt64), time.Duration(math.MaxInt64))
	for attempt := 1; attempt <= 3; attempt++ {
		d := fn(attempt)
		if d < 0 {
			t.Fatalf("jitter 结果不得为负: %v", d)
		}
	}
}
