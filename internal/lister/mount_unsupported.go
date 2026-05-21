//go:build !linux

package lister

import (
	"context"
	"fmt"
)

func ListMountedISO(ctx context.Context, path string, opts Options) ([]Entry, error) {
	return nil, fmt.Errorf("ISO listing requires Linux mount support")
}
