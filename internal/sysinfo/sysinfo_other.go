//go:build !linux

package sysinfo

import "context"

// Read only exists on Linux, where the panel and the agents run.
func Read(context.Context, string) (Stats, error) { return Stats{}, ErrUnsupported }
