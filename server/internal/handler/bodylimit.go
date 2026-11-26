package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// maxAPIBodyBytes caps the parsed request body on /api (F-0728), matching the
// agent-WS frame cap: admin writes are small JSON and bill file imports are bounded
// uploads, so an over-limit body fails the request instead of buffering unbounded
// bytes ahead of the auth+audit chain.
const maxAPIBodyBytes = 8 << 20

// bodyLimit wraps the request body with http.MaxBytesReader; handlers see a
// "request body too large" bind error and answer 400.
func (h *Handler) bodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAPIBodyBytes)
		}
		c.Next()
	}
}
