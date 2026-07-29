package wecom

import (
	"sync"
	"time"
)

// wecomRateLimitMinute is the per-chat per-minute message limit.
const wecomRateLimitMinute = 30

// wecomRateLimitHour is the per-chat per-hour message limit.
const wecomRateLimitHour = 1000

// wecomRateMinuteBuffer is how many messages we reserve before hitting the limit.
const wecomRateMinuteBuffer = 5

// wecomRateHourBuffer is how many messages we reserve before hitting the limit.
const wecomRateHourBuffer = 50

// chatRateTracker tracks per-chat message send counts for WeCom rate limiting.
type chatRateTracker struct {
	mu    sync.Mutex
	chats map[string]*chatWindow
}

// chatWindow holds the recent send timestamps for a single chat.
type chatWindow struct {
	minute []time.Time // sends in the last 60 seconds
	hour   []time.Time // sends in the last 3600 seconds
}

// record adds a send timestamp for the given chat ID.
func (t *chatRateTracker) record(chatID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.chats == nil {
		t.chats = make(map[string]*chatWindow)
	}
	cw := t.chats[chatID]
	if cw == nil {
		cw = &chatWindow{}
		t.chats[chatID] = cw
	}

	now := time.Now()
	cw.minute = append(cw.minute, now)
	cw.hour = append(cw.hour, now)

	// Prune expired entries
	minuteCutoff := now.Add(-1 * time.Minute)
	hourCutoff := now.Add(-1 * time.Hour)
	cw.minute = pruneBefore(cw.minute, minuteCutoff)
	cw.hour = pruneBefore(cw.hour, hourCutoff)
}

// check returns the current send counts and whether the caller should wait
// before sending to avoid hitting WeCom rate limits.
// minuteCount and hourCount are the counts within the last 1 and 60 minutes.
// needWait is the minimum duration to wait (0 = send immediately).
func (t *chatRateTracker) check(chatID string) (minuteCount, hourCount int, needWait time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cw := t.chats[chatID]
	if cw == nil {
		return 0, 0, 0
	}

	minuteCount = len(cw.minute)
	hourCount = len(cw.hour)

	// Check minute limit
	if minuteCount >= wecomRateLimitMinute-wecomRateMinuteBuffer {
		oldest := cw.minute[0]
		needWait = oldest.Add(1 * time.Minute).Sub(time.Now())
		if needWait < 0 {
			needWait = 0
		}
	}

	// Check hour limit (whichever is longer)
	if hourCount >= wecomRateLimitHour-wecomRateHourBuffer {
		oldest := cw.hour[0]
		hourWait := oldest.Add(1 * time.Hour).Sub(time.Now())
		if hourWait > needWait {
			needWait = hourWait
		}
	}

	return minuteCount, hourCount, needWait
}

// pruneBefore removes timestamps that are before the cutoff.
func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	for i, t := range ts {
		if !t.Before(cutoff) {
			return ts[i:]
		}
	}
	return nil
}
