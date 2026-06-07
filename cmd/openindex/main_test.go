package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWritePlanListsEveryMilestone(t *testing.T) {
	var b bytes.Buffer
	if err := writePlan(&b); err != nil {
		t.Fatalf("writePlan: %v", err)
	}
	out := b.String()
	for _, id := range []string{"M0", "M1", "M2", "M3", "M4", "M5", "M6", "M7", "M8", "M9"} {
		if !strings.Contains(out, id) {
			t.Fatalf("plan output is missing %s", id)
		}
	}
	if !strings.Contains(out, "GATE") {
		t.Fatal("plan output is missing the gate column header")
	}
}

func TestWriteRisksMarksOnePrimary(t *testing.T) {
	var b bytes.Buffer
	if err := writeRisks(&b); err != nil {
		t.Fatalf("writeRisks: %v", err)
	}
	out := b.String()
	if n := strings.Count(out, "(primary)"); n != 1 {
		t.Fatalf("expected exactly one primary risk marker, got %d", n)
	}
	if !strings.Contains(out, "Index poisoning") {
		t.Fatal("risk output should name the index-poisoning risk")
	}
}

func TestUsageNamesEveryCommand(t *testing.T) {
	var b bytes.Buffer
	if err := writeUsage(&b); err != nil {
		t.Fatalf("writeUsage: %v", err)
	}
	out := b.String()
	for _, cmd := range []string{"version", "plan", "risks", "help"} {
		if !strings.Contains(out, cmd) {
			t.Fatalf("usage is missing the %q command", cmd)
		}
	}
}

func TestVersionDefault(t *testing.T) {
	if version != "dev" {
		t.Fatalf("default version = %q, want dev (the release stamps the real value)", version)
	}
}
