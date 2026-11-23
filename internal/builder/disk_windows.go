package builder

import "math"

// On Windows only the CLI is used (dockfly deploy): the server and its builds
// run on Linux, so there is no disk check here.
func freeDiskBytes(string) (uint64, error) { return math.MaxUint64, nil }
