package control

import (
	"slices"
	"testing"
)

func TestAssignmentNodes(t *testing.T) {
	a := NewAssignment()
	a.Shards[0] = ReplicaSet{Shard: 0, Nodes: []NodeID{"n2", "n1"}}
	a.Shards[1] = ReplicaSet{Shard: 1, Nodes: []NodeID{"n1", "n3"}}
	got := a.Nodes()
	want := []NodeID{"n1", "n2", "n3"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAssignmentShardsOn(t *testing.T) {
	a := NewAssignment()
	a.Shards[5] = ReplicaSet{Shard: 5, Nodes: []NodeID{"n1", "n2"}}
	a.Shards[2] = ReplicaSet{Shard: 2, Nodes: []NodeID{"n2", "n3"}}
	a.Shards[9] = ReplicaSet{Shard: 9, Nodes: []NodeID{"n1", "n3"}}
	got := a.ShardsOn("n1")
	want := []ShardID{5, 9}
	if !slices.Equal(got, want) {
		t.Fatalf("shards on n1: got %v want %v", got, want)
	}
}

func TestAssignmentShardsOnSortedAndEmpty(t *testing.T) {
	a := NewAssignment()
	a.Shards[0] = ReplicaSet{Shard: 0, Nodes: []NodeID{"n1"}}
	if got := a.ShardsOn("absent"); len(got) != 0 {
		t.Fatalf("a node holding nothing should return empty, got %v", got)
	}
}
