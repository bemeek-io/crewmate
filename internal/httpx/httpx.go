// Package httpx provides small JSON request/response helpers with a uniform
// error envelope: {"error": {"code": "...", "message": "..."}}.
package httpx

import (
	"encoding/json"
	"net/http"
)

type errBody struct {
	Error errDetail `json:"error"`
}

type errDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, errBody{Error: errDetail{Code: code, Message: message}})
}

// Decode parses a JSON body with a size cap already applied by middleware.
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		Error(w, http.StatusBadRequest, "bad_json", "invalid request body")
		return false
	}
	return true
}

// MaxBytes limits request body size.
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
