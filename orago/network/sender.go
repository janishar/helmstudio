package network

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SendJSON marshals before writing anything, so a failure can still answer 500. The header is set here because
// ctx.Data keeps a Content-Type a handler already set, and ctx.JSON would add "; charset=utf-8". It aborts the
// chain, so no later handler runs.
func SendJSON(ctx *gin.Context, status int, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		SendMixedError(ctx, err)
		return
	}
	ctx.Header("Content-Type", "application/json")
	ctx.Data(status, "application/json", body)
	ctx.Abort()
}

// SendError writes the contract's error shape.
func SendError(ctx *gin.Context, status int, detail string) {
	SendJSON(ctx, status, map[string]string{"detail": detail})
}

// SendNoContent answers 204 and aborts the chain.
func SendNoContent(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusNoContent)
}

// SendMixedError answers an *APIError with its status and message. Any other error is logged and hidden: its text
// can hold SQL or addresses, so it never reaches a client.
func SendMixedError(ctx *gin.Context, err error) {
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError == nil {
		log.Printf("internal error: %v", err)
		SendError(ctx, http.StatusInternalServerError, "internal error")
		return
	}
	if apiError.Code >= http.StatusInternalServerError && apiError.Err != nil {
		log.Printf("%s: %v", apiError.Message, apiError.Err)
	}
	SendError(ctx, apiError.Code, apiError.Message)
}
