package helps

import (
	"sync"
	"testing"
	"time"
)

func TestVertexRegionCooldownConcurrentFork(t *testing.T) {
	cooldown := &VertexRegionCooldownFork{}
	now := time.Unix(300, 0)
	a := VertexRegionKeyFork{Project: "project", Model: "gemini-model", Location: "us"}
	b := VertexRegionKeyFork{Project: "project", Model: "gemini-model", Location: "eu"}
	first, _ := cooldown.Claim(a, now)
	overlap, _ := cooldown.Claim(a, now)
	third, _ := cooldown.Claim(a, now)
	if first == nil || overlap == nil || third == nil {
		t.Fatal("healthy primary requests must not serialize")
	}
	until := now.Add(7 * time.Second)
	first(until)
	overlap(now.Add(time.Second))
	third(time.Time{})
	if finish, got := cooldown.Claim(a, now.Add(time.Second)); finish != nil || !got.Equal(until) {
		t.Fatal("another overlapping failure or success erased cooldown")
	}
	if finish, _ := cooldown.Claim(b, now.Add(time.Second)); finish == nil {
		t.Fatal("cooldown leaked to another region")
	} else {
		finish(time.Time{})
	}
	var wg sync.WaitGroup
	claims := make(chan func(time.Time), 16)
	for range 16 {
		wg.Go(func() {
			if finish, _ := cooldown.Claim(a, until); finish != nil {
				claims <- finish
			}
		})
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatalf("expired regional cooldown admitted %d simultaneous probes", len(claims))
	}
	for finish := range claims {
		finish(time.Time{})
	}
	if next, _ := cooldown.Claim(a, until); next == nil {
		t.Fatal("successful probe did not return region to normal traffic")
	} else {
		next(time.Time{})
	}
}

func TestVertexRegionBackoffBoundsFork(t *testing.T) {
	for index, window := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second} {
		for range 100 {
			value := VertexRegionBackoffFork(index)
			if value < window/2 || value > window {
				t.Fatalf("backoff[%d] = %s not in [%s,%s]", index, value, window/2, window)
			}
		}
	}
}
