package bus

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	userBurst  = 5
	userEvery  = time.Minute
	ipBurst    = 20
	ipEvery    = 10 * time.Second
	sweepEvery = time.Minute
)

// throttle holds the login limiters. One mutex guards both maps and is held
// only around limiter calls, never across hashing or the database.
type throttle struct {
	mu        sync.Mutex
	users     map[string]*rate.Limiter
	ips       map[string]*rate.Limiter
	lastSweep time.Time
}

func newThrottle() *throttle {
	return &throttle{users: map[string]*rate.Limiter{}, ips: map[string]*rate.Limiter{}}
}

// sweep drops every limiter whose bucket is full again: a full bucket and no
// bucket behave the same. It runs at most once per sweepEvery.
func (t *throttle) sweep(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(t.lastSweep) < sweepEvery {
		return
	}
	t.lastSweep = now
	for _, m := range []map[string]*rate.Limiter{t.users, t.ips} {
		for k, l := range m {
			if l.TokensAt(now) >= float64(l.Burst()) {
				delete(m, k)
			}
		}
	}
}

func (t *throttle) takeIP(ip string, now time.Time) (time.Duration, bool) {
	return t.take(t.ips, ip, rate.Every(ipEvery), ipBurst, now)
}

func (t *throttle) takeUser(username string, now time.Time) (time.Duration, bool) {
	return t.take(t.users, username, rate.Every(userEvery), userBurst, now)
}

// take spends one token from key's limiter. If none is available now, the
// reservation is given back, so being refused does not push the wait out, and
// the wait is returned with false.
func (t *throttle) take(m map[string]*rate.Limiter, key string, every rate.Limit, burst int, now time.Time) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := m[key]
	if !ok {
		l = rate.NewLimiter(every, burst)
		m[key] = l
	}
	r := l.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d, false
	}
	return 0, true
}

func (t *throttle) entries() (users, ips int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.users), len(t.ips)
}
