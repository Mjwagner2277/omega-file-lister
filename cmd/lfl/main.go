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
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	var opts lister.Options
	var jsonOut bool
	var quiet bool
	var noSudoMount bool

	flags := flag.NewFlagSet("lfl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&jsonOut, "json", false, "write JSON lines to <input_name>_files.json")
	flags.BoolVar(&quiet, "quiet", false, "hide progress messages on stderr")
	flags.BoolVar(&noSudoMount, "no-sudo-mount", false, "do not use sudo for ISO mount/umount when running as a non-root user")
	flags.StringVar(&opts.MountRoot, "mount-dir", "", "directory where ISO mount points are created; default is the system temp directory")
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

	opts.SudoMount = !noSudoMount

	if !quiet {
		opts.Progress = func(event lister.ProgressEvent) {
			printProgress(stderr, event)
		}
	}

	ctx := context.Background()
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

		out, outPath, closeOut, err := outputWriter(path, jsonOut)
		if err != nil {
			fmt.Fprintf(stderr, "lfl: %s: %v\n", path, err)
			return 1
		}
		if closeOut != nil {
			defer closeOut()
		}

		if !quiet {
			fmt.Fprintf(stderr, "lfl: writing %d entries to %s\n", len(entries), outPath)
		}
		if err := writeEntries(out, entries, jsonOut); err != nil {
			fmt.Fprintf(stderr, "lfl: write output: %v\n", err)
			return 1
		}
		if closeOut != nil {
			if err := closeOut(); err != nil {
				fmt.Fprintf(stderr, "lfl: close output: %v\n", err)
				return 1
			}
			closeOut = nil
		}
	}

	if !quiet {
		fmt.Fprintf(stderr, "lfl: done: %d entries from %d input(s) in %s\n", totalEntries, flags.NArg(), time.Since(started).Round(time.Millisecond))
	}
	return 0
}

func outputWriter(input string, jsonOut bool) (io.Writer, string, func() error, error) {
	path := defaultOutputPath(input, jsonOut)
	file, err := os.Create(path)
	if err != nil {
		return nil, path, nil, fmt.Errorf("create output %s: %w", path, err)
	}
	return file, path, file.Close, nil
}

func defaultOutputPath(input string, jsonOut bool) string {
	name := strings.Trim(filepath.Base(input), ".")
	name = strings.NewReplacer(".", "_", string(os.PathSeparator), "_").Replace(name)
	if name == "" {
		name = "output"
	}
	if jsonOut {
		return name + "_files.json"
	}
	return name + "_files"
}

func writeEntries(w io.Writer, entries []lister.Entry, jsonOut bool) error {
	if jsonOut {
		encoder := json.NewEncoder(w)
		for _, entry := range entries {
			if err := encoder.Encode(entry); err != nil {
				return err
			}
		}
		return nil
	}
	for _, entry := range entries {
		if entry.Comment != "" {
			if _, err := fmt.Fprintf(w, "%s\t# %s\n", entry.Path, entry.Comment); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(w, entry.Path); err != nil {
			return err
		}
	}
	return nil
}

func printUsage(w io.Writer, flags *flag.FlagSet) {
	fmt.Fprintf(w, `Linux File Lister (lfl)

Usage:
  lfl [flags] <archive-or-iso> [more files...]

What it does:
  Lists files from Linux ISO images and common compressed/archive formats.
  Each input writes to <input_name>_files in the current working directory.
  With -json, each input writes JSON lines to <input_name>_files.json.
  ISO files are mounted read-only on Linux, walked like a normal filesystem,
  and supported compressed files inside the ISO are expanded recursively.
  Non-root users use sudo mount by default; sudo may prompt for a password.

Examples:
  lfl rocky.iso                         # writes rocky_iso_files
  lfl -workers 8 large.iso              # writes large_iso_files
  lfl -mount-dir /mnt/lfl rocky.iso     # creates /mnt/lfl/lfl-iso-*
  lfl -json package.rpm                 # writes package_rpm_files.json
  lfl -quiet archive.tar.gz

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
