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

var update = flag.Bool("update", false, "regenera web/src/lib/api-errors.gen.ts")

const errorsTSPath = "../../web/src/lib/api-errors.gen.ts"

// errorsTS genera el catálogo de errores del frontend: un mensaje de Lingui
// por código (el contexto evita que dos códigos con el mismo texto inglés
// compartan traducción).
func errorsTS() []byte {
	var b bytes.Buffer
	b.WriteString("// Generado desde internal/api/errors.go: no editar a mano.\n")
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

// El catálogo del frontend debe estar al día con los códigos del backend.
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
		t.Fatalf("%s no está al día: ejecuta go test ./internal/api -run TestAPIErrorsTS -update", errorsTSPath)
	}
}
