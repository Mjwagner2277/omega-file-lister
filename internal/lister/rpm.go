package lister

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	if _, err := exec.LookPath("rpm2cpio"); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "rpm2cpio", path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(out)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	entries, listErr := listCPIOPayload("", bytes.NewReader(payload), defaultMaxNestedDepth, "rpm")
	if listErr != nil {
		return nil, listErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return entries, nil
}

// listRPMPayload converts RPM bytes through rpm2cpio so nested RPM files
// follow the same recursive path as top-level RPM inputs.
func listRPMPayload(parent string, data []byte, depth int) ([]Entry, error) {
	if _, err := exec.LookPath("rpm2cpio"); err != nil {
		return []Entry{{Path: nestedPath(parent, "content"), Type: "file", Format: "rpm", Comment: "RPM package; install rpm2cpio for recursive expansion"}}, nil
	}
	tmp, err := os.CreateTemp("", "lfl-rpm-*")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	cmd := exec.Command("rpm2cpio", name)
	payload, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return listCPIOPayload(parent, bytes.NewReader(payload), depth, "rpm")
}

func listPayloadWithHelper(ctx context.Context, payload []byte, helper string, args ...string) ([]Entry, error) {
	if _, err := exec.LookPath(helper); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, helper, args...)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(out)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	entries, listErr := listCPIOPayload("", bytes.NewReader(payload), defaultMaxNestedDepth, "rpm")
	if listErr != nil {
		return nil, listErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return entries, nil
}

func readAllAt(path string, off int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	return b, nil
}
