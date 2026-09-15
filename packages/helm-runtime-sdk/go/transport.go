package helm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type transport struct {
	base  string
	token string
	http  *http.Client
}

// do sends one request and returns the response when its status is 2xx. Any
// other status is read into *Error and the body closed.
func (t *transport) do(ctx context.Context, method, path string, query url.Values, header http.Header, body io.Reader, contentType string) (*http.Response, error) {
	u := t.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("helm: building %s %s: %w", method, path, err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("helm: %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	e := &Error{Status: resp.StatusCode}
	if json.Unmarshal(raw, e) != nil || e.Code == "" {
		e.Code = "unexpected_response"
		e.Message = strings.TrimSpace(string(raw))
	}
	return nil, e
}

func encodeJSON(v any) (io.Reader, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("helm: encoding the request body: %w", err)
	}
	return bytes.NewReader(b), nil
}

func decodeJSON[T any](resp *http.Response) (*T, error) {
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("helm: decoding the %s response: %w", resp.Request.URL.Path, err)
	}
	return &v, nil
}

func drain(resp *http.Response) error {
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// RawResponse is a byte stream: an asset's bytes or a thumbnail. Close it.
type RawResponse struct {
	Status        int
	ContentType   string
	ContentLength int64
	ContentRange  string
	ETag          string
	Body          io.ReadCloser
}

func newRawResponse(resp *http.Response) *RawResponse {
	return &RawResponse{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"),
		ContentLength: resp.ContentLength, ContentRange: resp.Header.Get("Content-Range"),
		ETag: resp.Header.Get("ETag"), Body: resp.Body}
}

// Event is one server-sent event.
type Event struct {
	ID   string
	Name string
	Data json.RawMessage
}

// Decode unmarshals the event's data, for example into an ItemEvent.
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }

// EventStream reads server-sent events until the stream ends or Close.
type EventStream struct {
	body io.ReadCloser
	r    *bufio.Reader
}

func newEventStream(resp *http.Response) *EventStream {
	return &EventStream{body: resp.Body, r: bufio.NewReaderSize(resp.Body, 64<<10)}
}

// Next returns the next event, skipping comments. It returns io.EOF when the
// stream ends.
func (s *EventStream) Next() (Event, error) {
	var ev Event
	var data []string
	for {
		line, err := s.r.ReadString('\n')
		if err != nil && line == "" {
			if len(data) > 0 || ev.Name != "" {
				ev.Data = json.RawMessage(strings.Join(data, "\n"))
				return ev, nil
			}
			return Event{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if ev.Name != "" || len(data) > 0 {
				ev.Data = json.RawMessage(strings.Join(data, "\n"))
				return ev, nil
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "id:"):
			ev.ID = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "event:"):
			ev.Name = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " "))
		}
	}
}

// Close ends the stream.
func (s *EventStream) Close() error { return s.body.Close() }
