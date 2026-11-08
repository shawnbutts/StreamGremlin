package api

import (
	"encoding/json"
	"net/http"
)

// Stable error codes. Clients may switch on these; messages are for people.
const (
	CodeUnauthorized          = "unauthorized"
	CodeForbidden             = "forbidden"
	CodeNotFound              = "not_found"
	CodeMethodNotAllowed      = "method_not_allowed"
	CodeInvalidRequest        = "invalid_request"
	CodePayloadTooLarge       = "payload_too_large"
	CodeUnsupportedAPIVersion = "unsupported_api_version"
	CodeConflict              = "conflict"
	CodeUnavailable           = "unavailable"
	CodeRateLimited           = "rate_limited"
	CodeNotSent               = "not_sent" // Twitch accepted the request but didn't post it
	CodeInternal              = "internal"
)

// ErrorBody is the shape of every error response.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var body ErrorBody
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
