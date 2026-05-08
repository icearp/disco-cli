//go:build paid

package serve

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// jwtMiddleware verifies a HS256 bearer token on every request, then
// hard-checks that the `tenant` claim matches the server's pinned tenantID.
// Defence in depth against Lambda misroute (token issued for tenant A
// reaching a container started for tenant B). The tenant-mismatch case
// returns 403 (auth was valid; authorisation failed) — distinct from 401
// for missing/expired/wrong-secret.
func jwtMiddleware(secret []byte, tenantID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeError(w, http.StatusUnauthorized, errCodeUnauthorized, "missing bearer token")
			return
		}
		raw := strings.TrimPrefix(auth, "Bearer ")
		tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing alg")
			}
			return secret, nil
		})
		if err != nil || !tok.Valid {
			writeError(w, http.StatusUnauthorized, errCodeUnauthorized, "invalid or expired token")
			return
		}
		claims, ok := tok.Claims.(jwt.MapClaims)
		if !ok {
			writeError(w, http.StatusUnauthorized, errCodeUnauthorized, "missing claims")
			return
		}
		claimTenant, _ := claims["tenant"].(string)
		// constant-time compare so timing analysis can't leak the pinned ID.
		// UUIDs are short and same-length so this is overkill, but free.
		if subtle.ConstantTimeCompare([]byte(claimTenant), []byte(tenantID)) != 1 {
			writeError(w, http.StatusForbidden, errCodeTenantMismatch,
				"jwt tenant claim does not match server tenant")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeError emits the canonical {"error": {code, message}} envelope at
// the requested status. No external error lib; minimal allocations.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errorEnvelope{Error: errorBody{Code: code, Message: message}})
	_, _ = w.Write(body)
}

// writeErrorWithDetails is the same with a details map.
func writeErrorWithDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errorEnvelope{Error: errorBody{Code: code, Message: message, Details: details}})
	_, _ = w.Write(body)
}
