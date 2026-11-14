// Package common holds what is shared across the features' controllers: the claims a request carries.
package common

import (
	"github.com/gin-gonic/gin"

	"github.com/helmstudio/auth_service/api/auth/model"
)

// claimsKey is the gin.Context key of the verified claims.
const claimsKey = "claims"

// SetClaims stores the verified claims for the handlers after the authentication middleware.
func SetClaims(ctx *gin.Context, claims *model.Claims) {
	ctx.Set(claimsKey, claims)
}

// MustClaims returns the claims SetClaims stored. It panics on a route without authentication, a programming error
// the error catcher answers as a 500.
func MustClaims(ctx *gin.Context) *model.Claims {
	return ctx.MustGet(claimsKey).(*model.Claims)
}
