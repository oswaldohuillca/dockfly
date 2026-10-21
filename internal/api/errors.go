package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Errores de la API. Cada uno tiene un código estable, que el panel traduce
// (los textos de cada idioma viven solo en el frontend), y un mensaje en
// inglés que sirve de respaldo (para el CLI y para códigos que el panel aún
// no conozca). Los datos variables van en Params y el mensaje los nombra
// entre llaves: "an upload can't exceed {max_mb} MiB".
//
// Respuesta: {"error": "<mensaje>", "code": "<código>", "params": {…}}
//
// La lista de códigos se exporta al frontend (web/src/lib/api-errors.gen.ts):
// después de añadir o cambiar uno, ejecuta
//
//	go test ./internal/api -run TestAPIErrorsTS -update
type apiError struct {
	Code    string
	Message string
	Params  map[string]any
}

// errorCatalog son todos los códigos con su mensaje (para generar el TS).
var errorCatalog = map[string]string{}

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// newError registra un código. Se usa en variables de paquete:
//
//	var errServerNameTaken = newError("server_name_taken", "A server with that name already exists")
func newError(code, message string) apiError {
	if !codeRe.MatchString(code) {
		panic("código de error inválido: " + code)
	}
	if old, dup := errorCatalog[code]; dup && old != message {
		panic("código de error repetido: " + code)
	}
	errorCatalog[code] = message
	return apiError{Code: code, Message: message}
}

// With añade datos al error (pares nombre, valor).
func (e apiError) With(kv ...any) apiError {
	params := make(map[string]any, len(e.Params)+len(kv)/2)
	for k, v := range e.Params {
		params[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		params[fmt.Sprint(kv[i])] = kv[i+1]
	}
	e.Params = params
	return e
}

// Error es el mensaje en inglés con los datos sustituidos.
func (e apiError) Error() string {
	msg := e.Message
	for k, v := range e.Params {
		msg = strings.ReplaceAll(msg, "{"+k+"}", fmt.Sprint(v))
	}
	return msg
}

func writeError(w http.ResponseWriter, status int, e apiError) {
	body := map[string]any{"error": e.Error(), "code": e.Code}
	if len(e.Params) > 0 {
		body["params"] = e.Params
	}
	writeJSON(w, status, body)
}

// writeErrorFrom responde con err: si es (o envuelve) un apiError, ese; si
// no, fallback con el texto de err como detalle ({detail}).
func writeErrorFrom(w http.ResponseWriter, status int, err error, fallback apiError) {
	var e apiError
	if errors.As(err, &e) {
		writeError(w, status, e)
		return
	}
	writeError(w, status, fallback.With("detail", err.Error()))
}

// Errores comunes a toda la API.
var (
	errInternal        = newError("internal_error", "Internal error")
	errInvalidJSON     = newError("invalid_json", "Invalid JSON: {detail}")
	errNotFoundRoute   = newError("route_not_found", "Route not found")
	errMethod          = newError("method_not_allowed", "Method not allowed")
	errTooManyAttempts = newError("too_many_attempts", "Too many attempts, wait a minute")
	errJSONRequired    = newError("json_required", "application/json is required")
	errUnauthenticated = newError("unauthenticated", "Not authenticated or session expired")
	errBadAPIToken     = newError("invalid_api_token", "Invalid, expired or revoked API token")
	errInvalidID       = newError("invalid_id", "Invalid ID")
	// errUnexpected envuelve errores sin código (de GitHub, Docker…): {detail} es su texto.
	errUnexpected = newError("unexpected_error", "{detail}")
)

// errorCodes devuelve los códigos ordenados (para generar el TS).
func errorCodes() []string {
	codes := make([]string, 0, len(errorCatalog))
	for c := range errorCatalog {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}
