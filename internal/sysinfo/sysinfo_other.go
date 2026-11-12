//go:build !linux

package sysinfo

import "context"

// Read solo existe en Linux, donde corren el panel y los agentes.
func Read(context.Context, string) (Stats, error) { return Stats{}, ErrUnsupported }
