// Command openindex is the introspection CLI for the OpenIndex project.
//
// At this milestone the engine is a library: every subsystem (storage, crawler,
// index, vector, rank, serve, answer, control, open, telemetry, capacity) ships
// as an interface seam with a tested in-process reference implementation, and
// the production bindings are mechanical swaps behind those seams. There is no
// long-running server to start yet, so the one binary the release ships is this
// small reporter: it prints the build version, the milestone plan the project is
// built against, and the risk register, all read from the capacity package, so
// an operator can confirm which build they hold and what it is meant to do.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"openindex/capacity"
)

// version is stamped at build time with -ldflags "-X main.version=...". A plain
// go build leaves it "dev".
var version = "dev"

func main() {
	flag.Usage = func() { render(os.Stderr, writeUsage) }
	versionFlag := flag.Bool("version", false, "print the build version and exit")
	flag.Parse()

	if *versionFlag {
		render(os.Stdout, writeVersion)
		return
	}

	switch flag.Arg(0) {
	case "version":
		render(os.Stdout, writeVersion)
	case "plan":
		render(os.Stdout, writePlan)
	case "risks":
		render(os.Stdout, writeRisks)
	case "", "help":
		render(os.Stdout, writeUsage)
	default:
		render(os.Stderr, func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "openindex: unknown command %q\n\n", flag.Arg(0))
			return err
		})
		render(os.Stderr, writeUsage)
		os.Exit(2)
	}
}

// render runs a write function against w and exits non-zero if the write fails,
// since a CLI that cannot emit its own output has nothing useful left to do.
func render(w io.Writer, fn func(io.Writer) error) {
	if err := fn(w); err != nil {
		os.Exit(1)
	}
}

// errWriter accumulates the first write error so a sequence of formatted writes
// reads without a check on every line and is verified once at the end (the
// Effective Go pattern).
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) printf(format string, a ...any) {
	if ew.err == nil {
		_, ew.err = fmt.Fprintf(ew.w, format, a...)
	}
}

func writeVersion(w io.Writer) error {
	ew := &errWriter{w: w}
	ew.printf("%s\n", version)
	return ew.err
}

func writeUsage(w io.Writer) error {
	ew := &errWriter{w: w}
	ew.printf(`openindex %s

OpenIndex is a web-scale search engine with an open, auditable index. This
release is the foundational library: every subsystem is an interface seam with a
tested in-process reference implementation. This command reports the build and
the plan it is built against.

usage:
  openindex <command>
  openindex -version

commands:
  version   print the build version
  plan      print the milestone build sequence and its gates
  risks     print the risk register
  help      print this message
`, version)
	return ew.err
}

// writePlan renders the milestone build sequence as an aligned table, reading
// the plan from the capacity package so the binary and the spec never drift.
func writePlan(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	ew := &errWriter{w: tw}
	ew.printf("MILESTONE\tTITLE\tGATE\n")
	for _, m := range capacity.BuildSequence() {
		ew.printf("%s\t%s\t%s\n", m.ID, m.Title, m.Gate)
	}
	if ew.err != nil {
		return ew.err
	}
	return tw.Flush()
}

// writeRisks renders the risk register, marking the primary risk and listing the
// mitigations recorded for each entry.
func writeRisks(w io.Writer) error {
	ew := &errWriter{w: w}
	for _, r := range capacity.RiskRegister() {
		marker := ""
		if r.Primary {
			marker = " (primary)"
		}
		ew.printf("%s%s\n", r.Name, marker)
		for _, m := range r.Mitigations {
			ew.printf("  - %s\n", m)
		}
		ew.printf("\n")
	}
	return ew.err
}
