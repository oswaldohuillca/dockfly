package builder

import "math"

// En Windows solo se usa el CLI (dockfly deploy): el servidor y sus builds
// corren en Linux, así que aquí no se comprueba el disco.
func freeDiskBytes(string) (uint64, error) { return math.MaxUint64, nil }
