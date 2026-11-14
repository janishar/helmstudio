package network

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// Header names the services use.
const (
	APIKeyHeader        = "x-api-key"
	AuthorizationHeader = "Authorization"
)

// BearerToken returns the token from the Authorization header, or "" when there is none.
func BearerToken(ctx *gin.Context) string {
	header := ctx.GetHeader(AuthorizationHeader)
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}
