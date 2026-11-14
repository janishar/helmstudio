package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/helmstudio/orago/utility"
)

// maxJSONBody caps a JSON request body at 64 KiB.
const maxJSONBody = 64 << 10

// Validator is implemented by request DTOs. Validate returns the 422 detail, or "" when the DTO is valid.
type Validator interface {
	Validate() string
}

// ReqBody decodes a required JSON body, then calls Validate when the DTO has one.
func ReqBody[T any](ctx *gin.Context) (*T, error) {
	body := new(T)
	if err := bodyDecoder(ctx).Decode(body); err != nil {
		return nil, NewUnprocessableError("invalid JSON body", err)
	}
	if validator, ok := any(body).(Validator); ok {
		if detail := validator.Validate(); detail != "" {
			return nil, NewUnprocessableError(detail, nil)
		}
	}
	return body, nil
}

// ReqBodyOptional decodes a JSON body that may be absent, giving the zero value. It leaves Validate to the caller,
// whose 404 and 409 checks must come before a 422.
func ReqBodyOptional[T any](ctx *gin.Context) (*T, error) {
	body := new(T)
	if err := bodyDecoder(ctx).Decode(body); err != nil && !errors.Is(err, io.EOF) {
		return nil, NewUnprocessableError("invalid JSON body", err)
	}
	return body, nil
}

// bodyDecoder reads the body up to maxJSONBody; a JSON value that runs past it fails the decode.
func bodyDecoder(ctx *gin.Context) *json.Decoder {
	return json.NewDecoder(http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxJSONBody))
}

// PathUUID reads a path parameter holding an id. A malformed id can't name anything, so it is a 404, not a 422.
func PathUUID(ctx *gin.Context, name string) (string, error) {
	value := ctx.Param(name)
	if !utility.IsUUID(value) {
		return "", NewNotFoundError("not found", nil)
	}
	return value, nil
}

// QueryInt reads an optional integer query parameter between minimum and maximum, inclusive.
func QueryInt(ctx *gin.Context, name string, fallback, minimum, maximum int) (int, error) {
	value := ctx.Query(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < minimum || number > maximum {
		message := fmt.Sprintf("%s must be an integer from %d to %d", name, minimum, maximum)
		return 0, NewUnprocessableError(message, nil)
	}
	return number, nil
}

// QueryTime reads an optional RFC 3339 timestamp (a pagination cursor), nil when absent.
func QueryTime(ctx *gin.Context, name string) (*time.Time, error) {
	value := ctx.Query(name)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, NewUnprocessableError(name+" must be an RFC 3339 date-time", nil)
	}
	return &parsed, nil
}
