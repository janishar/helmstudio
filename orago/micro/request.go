package micro

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/helmstudio/orago/network"
)

// RequestNats sends data to subject through client and decodes the reply, as goserve's RequestNats does; the client's
// Timeout bounds the wait. Bodies are flat JSON, as the message contract fixes them.
func RequestNats[S, R any](ctx context.Context, client NatsClient, subject string, data *S) (*R, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	reply, err := client.Request(ctx, subject, body)
	if err != nil {
		return nil, err
	}
	return JsonToMsg[R](reply)
}

// JsonToMsg decodes a flat JSON body, then calls Validate when the type has one (network.Validator), as
// network.ReqBody does for HTTP. Both failures are bad-request APIErrors, so RespondNatsError sends their text:
// "bad request: invalid JSON" or "bad request: <detail>".
func JsonToMsg[T any](data []byte) (*T, error) {
	message := new(T)
	if err := json.Unmarshal(data, message); err != nil {
		return nil, network.NewBadRequestError("bad request: invalid JSON", err)
	}
	if validator, ok := any(message).(network.Validator); ok {
		if detail := validator.Validate(); detail != "" {
			return nil, network.NewBadRequestError("bad request: "+detail, nil)
		}
	}
	return message, nil
}

// RespondNatsMessage sends data as flat JSON. A failure to encode or send is logged: the requester times out, and
// there is nothing else to tell it.
func RespondNatsMessage[T any](request NatsRequest, data *T) {
	body, err := json.Marshal(data)
	if err == nil {
		err = request.Respond(body)
	}
	if err != nil {
		log.Printf("%s: respond: %v", request.Subject(), err)
	}
}

// RespondNatsError sends {"error": "<text>"}, flat, as the message contract fixes error replies. Like
// network.SendMixedError, an APIError is sent with its own message, and any other error is logged and sent as
// "internal error", so no internal detail reaches the requester.
func RespondNatsError(request NatsRequest, err error) {
	var apiError *network.APIError
	if !errors.As(err, &apiError) || apiError == nil {
		log.Printf("%s: internal error: %v", request.Subject(), err)
		RespondNatsMessage(request, &errorReply{Error: "internal error"})
		return
	}
	RespondNatsMessage(request, &errorReply{Error: apiError.Message})
}

// errorReply is the body of an error reply.
type errorReply struct {
	Error string `json:"error"`
}
