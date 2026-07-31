package lister

import (
	"context"
	"os"
	"os/exec"
)

func listRPM(ctx context.Context, path string, opts Options) ([]Entry, error) {
	if entries, err := listRPMViaRPM2CPIO(ctx, path, opts); err == nil {
		return entries, nil
	}
	return listWithFallback(ctx, path)
}

func listRPMViaRPM2CPIO(ctx context.Context, path string, opts Options) ([]Entry, error) {
	var entries []Entry
	err := listRPMViaRPM2CPIOTo(ctx, path, opts, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listRPMViaRPM2CPIOTo(ctx context.Context, path string, opts Options, emit EntryFunc) error {
	if _, err := exec.LookPath("rpm2cpio"); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "rpm2cpio", path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	listErr := listCPIOPayloadTo("", out, nestedDepth(opts), "rpm", emit)
	if listErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return listErr
	}
	return cmd.Wait()
}

// listRPMPayload converts RPM bytes through rpm2cpio so nested RPM files
// follow the same recursive path as top-level RPM inputs.
func listRPMPayload(parent string, data []byte, depth int) ([]Entry, error) {
	var entries []Entry
	err := listRPMPayloadTo(parent, data, depth, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func listRPMPayloadTo(parent string, data []byte, depth int, emit EntryFunc) error {
	tmp, err := os.CreateTemp("", "lfl-rpm-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return listRPMFileTo(parent, name, depth, emit)
}

func listRPMFileTo(parent, path string, depth int, emit EntryFunc) error {
	if _, err := exec.LookPath("rpm2cpio"); err != nil {
		return emit(Entry{Path: nestedPath(parent, "content"), Type: "file", Format: "rpm", Comment: "RPM package; install rpm2cpio for recursive expansion"})
	}
	cmd := exec.Command("rpm2cpio", path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	listErr := listCPIOPayloadTo(parent, out, depth, "rpm", emit)
	if listErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return listErr
	}
	return cmd.Wait()
}
