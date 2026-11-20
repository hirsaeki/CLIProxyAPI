package helps

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// VertexRegionCooldownFork coordinates capacity cooldowns across executor
// snapshots. Normal traffic is not serialized. After a known cooldown,
// a single probe can enter until its response status is classified.
type VertexRegionCooldownFork struct {
	mu       sync.Mutex
	states   map[VertexRegionKeyFork]*vertexRegionStateFork
	overflow time.Time
}

type VertexRegionKeyFork struct {
	Project, Model, Location string
}

type vertexRegionStateFork struct {
	probe bool
	until time.Time
}

const maxVertexRegionCooldownsFork = 4096

// Ready conservatively estimates the earliest permitted regional attempt.
// Claim must still be used atomically before dispatch.
func (c *VertexRegionCooldownFork) Ready(key VertexRegionKeyFork, now time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready(key, now)
}

func (c *VertexRegionCooldownFork) ready(key VertexRegionKeyFork, now time.Time) time.Time {
	if state := c.states[key]; state != nil {
		if state.until.After(now) {
			return state.until
		}
		if state.probe {
			return now.Add(100 * time.Millisecond)
		}
		return now
	}
	// Overflow is only set if a known capacity error could not be stored
	// because the bounded map is full. Fail closed for untracked keys.
	if c.overflow.After(now) {
		return c.overflow
	}
	return now
}

// Claim admits an attempt, or returns the active cooldown. Call finish once,
// even on errors or cancellation; a zero time does not create a cooldown.
func (c *VertexRegionCooldownFork) Claim(key VertexRegionKeyFork, now time.Time) (finish func(time.Time), until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ready := c.ready(key, now); ready.After(now) {
		return nil, ready
	}
	probe := c.states[key]
	if probe != nil {
		probe.probe = true
	}
	return func(until time.Time) {
		c.mu.Lock()
		defer c.mu.Unlock()
		state := c.states[key]
		if probe != nil && state == probe {
			state.probe = false
		}
		if until.IsZero() {
			if probe != nil && state == probe && !state.until.After(now) {
				delete(c.states, key)
			}
			return
		}
		if state == nil {
			if len(c.states) >= maxVertexRegionCooldownsFork {
				for k, candidate := range c.states {
					if !candidate.probe && !candidate.until.After(now) {
						delete(c.states, k)
					}
				}
				if len(c.states) >= maxVertexRegionCooldownsFork {
					if until.After(c.overflow) {
						c.overflow = until
					}
					return
				}
			}
			if c.states == nil {
				c.states = make(map[VertexRegionKeyFork]*vertexRegionStateFork)
			}
			state = &vertexRegionStateFork{}
			c.states[key] = state
		}
		// Earlier successful attempts and shorter overlapping errors must
		// not erase a later Retry-After.
		if until.After(state.until) {
			state.until = until
		}
	}, time.Time{}
}

// VertexRegionWaitFork waits without introducing an HTTP read deadline after
// a generation starts.
func VertexRegionWaitFork(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// VertexRegionBackoffFork uses equal jitter over a capped exponential schedule.
func VertexRegionBackoffFork(retry int) time.Duration {
	window := time.Duration(1<<min(retry, 3)) * time.Second
	half := window / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
