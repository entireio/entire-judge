package cli

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestScoreCheckoutsOrderAndConcurrency verifies the two guarantees of the
// parallel scoring pass: results come back in CHECKOUT order regardless of which
// submission finishes first, and no more than `jobs` run at once.
func TestScoreCheckoutsOrderAndConcurrency(t *testing.T) {
	checkouts := []string{"a", "b", "c", "d", "e", "f"}
	// Earlier-indexed checkouts sleep longer, so completion order is the REVERSE of
	// checkout order — if the assembly used completion order, the result would be
	// reversed and the test would fail.
	delay := map[string]time.Duration{
		"a": 60 * time.Millisecond, "b": 50 * time.Millisecond, "c": 40 * time.Millisecond,
		"d": 30 * time.Millisecond, "e": 20 * time.Millisecond, "f": 10 * time.Millisecond,
	}
	var inFlight, maxInFlight int32
	scoreOne := func(checkout string) scoreResult {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxInFlight)
			if n <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, n) {
				break
			}
		}
		time.Sleep(delay[checkout])
		atomic.AddInt32(&inFlight, -1)
		return scoreResult{name: checkout}
	}

	const jobs = 3
	results := scoreCheckouts(checkouts, jobs, scoreOne, nil)

	if len(results) != len(checkouts) {
		t.Fatalf("got %d results, want %d", len(results), len(checkouts))
	}
	for i, c := range checkouts {
		if results[i].name != c {
			t.Errorf("results[%d] = %q, want %q (results must be in checkout order, not completion order)", i, results[i].name, c)
		}
	}
	if got := atomic.LoadInt32(&maxInFlight); got > jobs {
		t.Errorf("max in-flight = %d, exceeds jobs = %d", got, jobs)
	} else if got < 2 {
		t.Errorf("max in-flight = %d, expected real concurrency (>=2) with jobs = %d", got, jobs)
	}
}

// TestScoreCheckoutsJobsFloor confirms a non-positive jobs value is treated as
// sequential rather than panicking on a zero-capacity channel.
func TestScoreCheckoutsJobsFloor(t *testing.T) {
	checkouts := []string{"x", "y"}
	results := scoreCheckouts(checkouts, 0, func(c string) scoreResult { return scoreResult{name: c} }, nil)
	if len(results) != 2 || results[0].name != "x" || results[1].name != "y" {
		t.Errorf("jobs=0 should run sequentially in order, got %+v", results)
	}
}
