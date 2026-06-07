package serve

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// blockExcept returns target's value fast and blocks every other replica until
// its context is cancelled. It models a fleet where only one replica is healthy.
func blockExcept(target, value int) func(context.Context, int) (int, error) {
	return func(ctx context.Context, replica int) (int, error) {
		if replica == target {
			return value, nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}
}

func TestHedgeFiresBackupWhenLeaderSlow(t *testing.T) {
	// Replica 0 hangs; after the delay the backup (replica 1) answers fast.
	got, err := Hedge(context.Background(), 3, 20*time.Millisecond, blockExcept(1, 42))
	if err != nil {
		t.Fatalf("hedge should succeed via the backup: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want the backup's value 42", got)
	}
}

func TestHedgeTakesFirstSuccess(t *testing.T) {
	// Leader answers immediately; no backup should be needed, and the backup is
	// never even launched within the delay.
	var launched int32
	fn := func(_ context.Context, replica int) (int, error) {
		atomic.AddInt32(&launched, 1)
		if replica == 0 {
			return 7, nil
		}
		time.Sleep(time.Second)
		return 0, nil
	}
	got, err := Hedge(context.Background(), 3, 50*time.Millisecond, fn)
	if err != nil || got != 7 {
		t.Fatalf("leader should win: got %d err %v", got, err)
	}
	if n := atomic.LoadInt32(&launched); n != 1 {
		t.Fatalf("only the leader should have launched, got %d", n)
	}
}

func TestHedgeFailFastTriesNext(t *testing.T) {
	// The leader errors at once; hedge should try the next replica without
	// waiting for the full delay.
	fn := func(_ context.Context, replica int) (int, error) {
		if replica == 0 {
			return 0, errors.New("leader error")
		}
		return replica * 10, nil
	}
	start := time.Now()
	got, err := Hedge(context.Background(), 3, time.Second, fn)
	if err != nil || got != 10 {
		t.Fatalf("should recover on replica 1: got %d err %v", got, err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("a leader error should not wait out the hedge delay")
	}
}

func TestHedgeAllFail(t *testing.T) {
	wantErr := errors.New("boom")
	fn := func(_ context.Context, _ int) (int, error) { return 0, wantErr }
	_, err := Hedge(context.Background(), 3, 5*time.Millisecond, fn)
	if !errors.Is(err, wantErr) {
		t.Fatalf("all-fail should return the last error, got %v", err)
	}
}

func TestHedgeSingleReplica(t *testing.T) {
	got, err := Hedge(context.Background(), 1, 10*time.Millisecond, func(_ context.Context, _ int) (int, error) {
		return 5, nil
	})
	if err != nil || got != 5 {
		t.Fatalf("single replica should just call: got %d err %v", got, err)
	}
}

func TestHedgeZeroDelayRaces(t *testing.T) {
	// With no delay every replica launches at once; the fast one wins.
	got, err := Hedge(context.Background(), 3, 0, blockExcept(2, 99))
	if err != nil || got != 99 {
		t.Fatalf("racing should return the fast replica: got %d err %v", got, err)
	}
}

func TestHedgeContextCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	// Every replica hangs; the parent deadline must end the hedge.
	_, err := Hedge(ctx, 3, 5*time.Millisecond, func(c context.Context, _ int) (int, error) {
		<-c.Done()
		return 0, c.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestHedgeNoReplicas(t *testing.T) {
	_, err := Hedge(context.Background(), 0, time.Millisecond, func(context.Context, int) (int, error) {
		return 0, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("zero replicas should return canceled, got %v", err)
	}
}
