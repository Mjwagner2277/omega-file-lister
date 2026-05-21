package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Mjwagner2277/omega-file-lister/internal/lister"
)

func main() {
	var opts lister.Options
	var jsonOut bool
	flag.BoolVar(&jsonOut, "json", false, "emit JSON lines")
	flag.IntVar(&opts.MaxNestedDepth, "max-nested-depth", 8, "maximum recursive depth for nested archives")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: lfl [flags] archive...")
		flag.PrintDefaults()
		os.Exit(2)
	}

	ctx := context.Background()
	for _, path := range flag.Args() {
		entries, err := lister.List(ctx, path, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		for _, entry := range entries {
			if jsonOut {
				if err := json.NewEncoder(os.Stdout).Encode(entry); err != nil {
					fmt.Fprintf(os.Stderr, "write json: %v\n", err)
					os.Exit(1)
				}
				continue
			}
			if entry.Comment != "" {
				fmt.Printf("%s\t# %s\n", entry.Path, entry.Comment)
				continue
			}
			fmt.Println(entry.Path)
		}
	}
}
