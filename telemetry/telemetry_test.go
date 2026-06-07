package telemetry

import (
	"errors"
	"testing"
	"time"
)

type capture struct {
	method string
	dur    time.Duration
	err    error
	calls  int
}

func (c *capture) Observe(method string, dur time.Duration, err error) {
	c.method, c.dur, c.err, c.calls = method, dur, err, c.calls+1
}

func TestTimerDoneRecordsDurationAndError(t *testing.T) {
	c := &capture{}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	tm := startAt(c, "Leaf.Search", clock)
	now = now.Add(5 * time.Millisecond)

	wantErr := errors.New("boom")
	tm.Done(&wantErr)

	if c.calls != 1 {
		t.Fatalf("Observe called %d times, want 1", c.calls)
	}
	if c.method != "Leaf.Search" {
		t.Errorf("method = %q", c.method)
	}
	if c.dur != 5*time.Millisecond {
		t.Errorf("dur = %v, want 5ms", c.dur)
	}
	if !errors.Is(c.err, wantErr) {
		t.Errorf("err = %v, want %v", c.err, wantErr)
	}
}

func TestTimerNilErrPointer(t *testing.T) {
	c := &capture{}
	Start(c, "x").Done(nil)
	if c.err != nil {
		t.Errorf("err = %v, want nil", c.err)
	}
}

func TestNilMeterDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	Start(nil, "x").Done(nil)
}
