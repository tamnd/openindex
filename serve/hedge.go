package serve

import (
	"context"
	"time"
)

// Hedge issues a request to one replica and, if no reply arrives within delay,
// issues a backup to the next replica, taking the first success and cancelling
// the rest (doc 08.2). delay is set to the expected 95th-percentile latency, so
// the backup fires only for the slow tail and the extra load is a few percent.
// This is the single highest-value tail technique: Google's BigTable benchmark
// cut P99.9 from 1,800 ms to 74 ms for about 2 percent more requests with a
// 10 ms hedge delay.
//
// fn runs one attempt against the given replica index under a context that is
// cancelled the moment another attempt wins or the parent context ends, so a
// losing attempt stops work promptly. Hedge returns the first successful
// result. If every attempt fails, it returns the last error; if the parent
// context ends first, it returns that error. With delay <= 0 every replica is
// launched at once (pure racing); with one replica it is a plain call.
func Hedge[T any](ctx context.Context, replicas int, delay time.Duration, fn func(ctx context.Context, replica int) (T, error)) (T, error) {
	var zero T
	if replicas <= 0 {
		return zero, context.Canceled
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type outcome struct {
		val T
		err error
	}
	done := make(chan outcome, replicas)
	launch := func(replica int) {
		go func() {
			val, err := fn(ctx, replica)
			done <- outcome{val, err}
		}()
	}

	launch(0)
	launched := 1

	var timer *time.Timer
	var timerC <-chan time.Time
	arm := func() {
		if launched < replicas && delay > 0 {
			timer = time.NewTimer(delay)
			timerC = timer.C
		} else {
			timerC = nil
		}
	}
	// With delay <= 0, launch every backup immediately.
	if delay <= 0 {
		for launched < replicas {
			launch(launched)
			launched++
		}
	}
	arm()
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	var lastErr error
	pending := launched
	for pending > 0 {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-timerC:
			// The current leader is slow; fire a backup and re-arm.
			launch(launched)
			launched++
			pending++
			arm()
		case o := <-done:
			pending--
			if o.err == nil {
				return o.val, nil // cancel() via defer stops the losers
			}
			lastErr = o.err
			// A failure frees us to try the next replica right away.
			if launched < replicas {
				if timer != nil {
					timer.Stop()
				}
				launch(launched)
				launched++
				pending++
				arm()
			}
		}
	}
	return zero, lastErr
}
