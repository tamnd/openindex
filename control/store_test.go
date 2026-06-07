package control

import (
	"context"
	"testing"
	"time"
)

func TestMemStorePutGetDelete(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Fatal("empty store should miss")
	}
	if err := s.Put(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	v, ok, _ := s.Get(ctx, "k")
	if !ok || string(v) != "v" {
		t.Fatalf("get after put failed: %q %v", v, ok)
	}
	_ = s.Delete(ctx, "k")
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Fatal("get after delete should miss")
	}
}

func TestMemStoreList(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	_ = s.Put(ctx, "node/a", []byte("1"), 0)
	_ = s.Put(ctx, "node/b", []byte("2"), 0)
	_ = s.Put(ctx, "shard/x", []byte("3"), 0)
	got, _ := s.List(ctx, "node/")
	if len(got) != 2 {
		t.Fatalf("prefix list should return two keys, got %d", len(got))
	}
}

func TestMemStoreLeaseExpiryDeletesKeys(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	lease, _ := s.Grant(ctx, 10)
	_ = s.Put(ctx, "node/a/health", []byte("ok"), lease)
	_ = s.Put(ctx, "node/a/addr", []byte("10.0.0.1"), lease)
	// A key with no lease should survive the expiry.
	_ = s.Put(ctx, "config/x", []byte("keep"), 0)

	s.Expire(lease)

	if _, ok, _ := s.Get(ctx, "node/a/health"); ok {
		t.Fatal("lease expiry should delete its attached keys")
	}
	if _, ok, _ := s.Get(ctx, "node/a/addr"); ok {
		t.Fatal("lease expiry should delete every attached key")
	}
	if _, ok, _ := s.Get(ctx, "config/x"); !ok {
		t.Fatal("a leaseless key should survive an unrelated lease expiry")
	}
}

func TestMemStoreRevokeIsLikeExpiry(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	lease, _ := s.Grant(ctx, 10)
	_ = s.Put(ctx, "node/a", []byte("ok"), lease)
	_ = s.Revoke(ctx, lease)
	if _, ok, _ := s.Get(ctx, "node/a"); ok {
		t.Fatal("revoke should delete the lease's keys")
	}
}

func TestMemStoreWatchSeesPutAndDelete(t *testing.T) {
	s := NewMemStore()
	ctx := t.Context()
	ch, _ := s.Watch(ctx, "shard/")

	_ = s.Put(ctx, "shard/1", []byte("v"), 0)
	got := recv(t, ch)
	if got.Type != EventPut || got.Key != "shard/1" || string(got.Value) != "v" {
		t.Fatalf("expected a put event, got %+v", got)
	}

	_ = s.Delete(ctx, "shard/1")
	got = recv(t, ch)
	if got.Type != EventDelete || got.Key != "shard/1" {
		t.Fatalf("expected a delete event, got %+v", got)
	}
}

func TestMemStoreWatchFiltersByPrefix(t *testing.T) {
	s := NewMemStore()
	ctx := t.Context()
	ch, _ := s.Watch(ctx, "shard/")
	_ = s.Put(ctx, "node/a", []byte("v"), 0) // outside the prefix
	_ = s.Put(ctx, "shard/1", []byte("v"), 0)
	got := recv(t, ch)
	if got.Key != "shard/1" {
		t.Fatalf("watch should only see its prefix, got %q", got.Key)
	}
}

func TestMemStoreWatchClosesOnCancel(t *testing.T) {
	s := NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := s.Watch(ctx, "x/")
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			// Drain until closed.
			for range ch {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("cancelling the context should close the watch channel")
	}
}

func TestMemStoreLeaseExpiryFiresWatch(t *testing.T) {
	s := NewMemStore()
	ctx := t.Context()
	lease, _ := s.Grant(ctx, 10)
	_ = s.Put(ctx, "node/a", []byte("ok"), lease)
	ch, _ := s.Watch(ctx, "node/")
	s.Expire(lease)
	got := recv(t, ch)
	if got.Type != EventDelete || got.Key != "node/a" {
		t.Fatalf("lease expiry should fire a delete on the watch, got %+v", got)
	}
}

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a watch event")
		return Event{}
	}
}
