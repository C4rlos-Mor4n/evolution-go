package auth_middleware

import (
	"net/url"

	"github.com/gin-gonic/gin"
)

// redactedQueryParams are query parameters that carry credentials: "apikey"
// (instance token, accepted by the call stream WebSocket) and "token" (global
// key of the events /ws endpoint).
var redactedQueryParams = []string{"apikey", "token"}

const originalRawQueryKey = "_original_raw_query"

// RedactQueryCredentials must be registered immediately BEFORE gin's access
// Logger, and RestoreQueryCredentials immediately AFTER it:
//
//	r.Use(RedactQueryCredentials, gin.Logger(), RestoreQueryCredentials, gin.Recovery())
//
// gin's Logger captures URL.RawQuery before running the handlers, so it logs
// the redacted query, while every handler still sees the original one. This
// keeps tokens passed as query parameters (needed by WebSocket clients that
// cannot set headers) out of the logs.
func RedactQueryCredentials(c *gin.Context) {
	raw := c.Request.URL.RawQuery
	if raw != "" {
		if redacted, changed := redactQuery(raw); changed {
			c.Set(originalRawQueryKey, raw)
			c.Request.URL.RawQuery = redacted
		}
	}
	c.Next()
}

// RestoreQueryCredentials puts the original query back (see RedactQueryCredentials).
func RestoreQueryCredentials(c *gin.Context) {
	if raw, ok := c.Get(originalRawQueryKey); ok {
		if s, ok := raw.(string); ok {
			c.Request.URL.RawQuery = s
		}
	}
	c.Next()
}

func redactQuery(raw string) (string, bool) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		// Unparseable query: do not risk logging a credential.
		return "REDACTED", true
	}
	changed := false
	for _, k := range redactedQueryParams {
		if _, ok := values[k]; ok {
			values.Set(k, "REDACTED")
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	return values.Encode(), true
}
