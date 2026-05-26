package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mjwagner2277/omega-file-lister/internal/lister"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var opts lister.Options
	var jsonOut bool
	var quiet bool

	flags := flag.NewFlagSet("lfl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&jsonOut, "json", false, "emit JSON lines instead of text output")
	flags.BoolVar(&quiet, "quiet", false, "hide progress messages on stderr")
	flags.IntVar(&opts.MaxNestedDepth, "max-nested-depth", 8, "maximum recursive depth for nested archives")
	flags.IntVar(&opts.Workers, "workers", 0, "worker count for mounted ISO nested archive expansion; default is CPU count, capped at 64")
	flags.Usage = func() { printUsage(stderr, flags) }

	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() == 0 {
		printUsage(stderr, flags)
		return 2
	}

	if !quiet {
		opts.Progress = func(event lister.ProgressEvent) {
			printProgress(stderr, event)
		}
	}

	ctx := context.Background()
	encoder := json.NewEncoder(stdout)
	started := time.Now()
	totalEntries := 0

	for _, path := range flags.Args() {
		if !quiet {
			fmt.Fprintf(stderr, "lfl: processing %s\n", path)
		}
		entries, err := lister.List(ctx, path, opts)
		if err != nil {
			fmt.Fprintf(stderr, "lfl: %s: %v\n", path, err)
			return 1
		}
		totalEntries += len(entries)
		if !quiet {
			fmt.Fprintf(stderr, "lfl: writing %d entries for %s\n", len(entries), path)
		}
		for _, entry := range entries {
			if jsonOut {
				if err := encoder.Encode(entry); err != nil {
					fmt.Fprintf(stderr, "lfl: write json: %v\n", err)
					return 1
				}
				continue
			}
			if entry.Comment != "" {
				fmt.Fprintf(stdout, "%s\t# %s\n", entry.Path, entry.Comment)
				continue
			}
			fmt.Fprintln(stdout, entry.Path)
		}
	}

	if !quiet {
		fmt.Fprintf(stderr, "lfl: done: %d entries from %d input(s) in %s\n", totalEntries, flags.NArg(), time.Since(started).Round(time.Millisecond))
	}
	return 0
}

func printUsage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintf(w, `Linux File Lister (lfl)

Usage:
  lfl [flags] <archive-or-iso> [more files...]

What it does:
  Lists files from Linux ISO images and common compressed/archive formats.
  ISO files are mounted read-only on Linux, walked like a normal filesystem,
  and supported compressed files inside the ISO are expanded recursively.

Examples:
  lfl rocky.iso
  lfl -workers 8 large.iso
  lfl -json package.rpm
  lfl -quiet archive.tar.gz > files.txt

Flags:
`)
	flags.PrintDefaults()
}

func printProgress(w io.Writer, event lister.ProgressEvent) {
	parts := []string{"lfl:"}
	if event.Path != "" {
		parts = append(parts, filepath.Base(event.Path)+":")
	}
	if event.Message != "" {
		parts = append(parts, event.Message)
	} else if event.Stage != "" {
		parts = append(parts, event.Stage)
	}
	var details []string
	if event.Count > 0 {
		details = append(details, fmt.Sprintf("count=%d", event.Count))
	}
	if event.Total > 0 {
		details = append(details, fmt.Sprintf("total=%d", event.Total))
	}
	if event.Workers > 0 {
		details = append(details, fmt.Sprintf("workers=%d", event.Workers))
	}
	if len(details) > 0 {
		parts = append(parts, "("+strings.Join(details, ", ")+")")
	}
	fmt.Fprintln(w, strings.Join(parts, " "))
}
