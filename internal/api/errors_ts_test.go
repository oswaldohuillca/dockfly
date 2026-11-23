package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "regenerate web/src/lib/api-errors.gen.ts")

const errorsTSPath = "../../web/src/lib/api-errors.gen.ts"

// errorsTS generates the frontend's error catalog: one Lingui message per
// code (the context keeps two codes with the same English text from sharing
// a translation).
func errorsTS() []byte {
	var b bytes.Buffer
	b.WriteString("// Generated from internal/api/errors.go: do not edit by hand.\n")
	b.WriteString("// go test ./internal/api -run TestAPIErrorsTS -update\n")
	b.WriteString("import type { MessageDescriptor } from '@lingui/core';\n")
	b.WriteString("import { msg } from '@lingui/core/macro';\n\n")
	b.WriteString("export const apiErrors: Record<string, MessageDescriptor> = {\n")
	for _, code := range errorCodes() {
		message, _ := json.Marshal(errorCatalog[code])
		fmt.Fprintf(&b, "  %s: msg({ context: %q, message: %s }),\n", code, "api:"+code, message)
	}
	b.WriteString("};\n")
	return b.Bytes()
}

// The frontend catalog must be up to date with the backend's codes.
func TestAPIErrorsTS(t *testing.T) {
	want := errorsTS()
	if *update {
		if err := os.WriteFile(filepath.FromSlash(errorsTSPath), want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(filepath.FromSlash(errorsTSPath))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date: run go test ./internal/api -run TestAPIErrorsTS -update", errorsTSPath)
	}
}
