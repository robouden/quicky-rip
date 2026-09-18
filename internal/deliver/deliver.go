// Package deliver sends the digest and owns the retry policy: 3 attempts,
// 5-minute then 15-minute backoff, driven off digests.send_attempts /
// next_retry_at by a polling worker — never a sleeping goroutine.
package deliver

import "time"

// Backoff is the delay before attempt n (1-based). Attempt 4 does not happen.
func Backoff(attempt int) (time.Duration, bool) {
	switch attempt {
	case 1:
		return 0, true
	case 2:
		return 5 * time.Minute, true
	case 3:
		return 15 * time.Minute, true
	default:
		return 0, false
	}
}
