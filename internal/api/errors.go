package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// API errors. Each one has a stable code, which the panel translates (the
// texts for each language live only in the frontend), and an English message
// that serves as a fallback (for the CLI and for codes the panel doesn't know
// yet). The variable data goes in Params and the message names it inside
// braces: "an upload can't exceed {max_mb} MiB".
//
// Response: {"error": "<message>", "code": "<code>", "params": {…}}
//
// The list of codes is exported to the frontend (web/src/lib/api-errors.gen.ts):
// after adding or changing one, run
//
//	go test ./internal/api -run TestAPIErrorsTS -update
type apiError struct {
	Code    string
	Message string
	Params  map[string]any
}

// errorCatalog is every code with its message (used to generate the TS).
var errorCatalog = map[string]string{}

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// newError registers a code. It's used in package-level variables:
//
//	var errServerNameTaken = newError("server_name_taken", "A server with that name already exists")
func newError(code, message string) apiError {
	if !codeRe.MatchString(code) {
		panic("invalid error code: " + code)
	}
	if old, dup := errorCatalog[code]; dup && old != message {
		panic("duplicate error code: " + code)
	}
	errorCatalog[code] = message
	return apiError{Code: code, Message: message}
}

// With adds data to the error (name, value pairs).
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

// Error is the English message with the data substituted in.
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

// writeErrorFrom answers with err: if it is (or wraps) an apiError, that one;
// otherwise fallback, with err's text as the detail ({detail}).
func writeErrorFrom(w http.ResponseWriter, status int, err error, fallback apiError) {
	var e apiError
	if errors.As(err, &e) {
		writeError(w, status, e)
		return
	}
	writeError(w, status, fallback.With("detail", err.Error()))
}

// Errors common to the whole API.
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
	// errUnexpected wraps errors with no code (from GitHub, Docker…): {detail} is their text.
	errUnexpected = newError("unexpected_error", "{detail}")
)

// errorCodes returns the codes in order (used to generate the TS).
func errorCodes() []string {
	codes := make([]string, 0, len(errorCatalog))
	for c := range errorCatalog {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}
