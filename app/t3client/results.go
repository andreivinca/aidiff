package t3client

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// rpcExit is how the server ended a request: Success with its value, or
// Failure with an Effect cause.
type rpcExit struct {
	Tag   string          `json:"_tag"` // Success | Failure
	Value json.RawMessage `json:"value"`
	Cause json.RawMessage `json:"cause"`
}

// errUnauthorized means the server refused the access token.
var errUnauthorized = errors.New("access token rejected")

// rpcError is a failed request: a typed server error or a defect.
type rpcError struct {
	Method string
	Tag    string // error tag, e.g. VcsRepositoryDetectionError; empty for defects
	Detail string
}

func (e *rpcError) Error() string {
	if e.Tag == "" {
		return fmt.Sprintf("t3 %s: %s", e.Method, e.Detail)
	}
	return fmt.Sprintf("t3 %s: %s: %s", e.Method, e.Tag, e.Detail)
}

// defectError reads the server's report of its own failure.
func defectError(data []byte) error {
	var msg struct {
		Defect json.RawMessage `json:"defect"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return errors.New("T3 server failed")
	}
	var s string
	if json.Unmarshal(msg.Defect, &s) == nil {
		return fmt.Errorf("T3 server failed: %s", s)
	}
	return fmt.Errorf("T3 server failed: %s", compactJSON(msg.Defect))
}

// decodeExit turns an Exit frame into the call's result.
func decodeExit(method string, ex rpcExit, out any) error {
	if ex.Tag == "Success" {
		if out == nil || len(ex.Value) == 0 || string(ex.Value) == "null" {
			return nil
		}
		if err := json.Unmarshal(ex.Value, out); err != nil {
			return fmt.Errorf("t3 %s: decode result: %w", method, err)
		}
		return nil
	}
	return failureError(method, ex.Cause)
}

// failureError reads the first entry of an Effect failure cause.
func failureError(method string, raw json.RawMessage) error {
	var causes []struct {
		Tag    string          `json:"_tag"` // Fail | Die | Interrupt
		Error  json.RawMessage `json:"error"`
		Defect json.RawMessage `json:"defect"`
	}
	if err := json.Unmarshal(raw, &causes); err != nil || len(causes) == 0 {
		return &rpcError{Method: method, Detail: "request failed"}
	}
	c := causes[0]
	switch c.Tag {
	case "Fail":
		var e struct {
			Tag     string `json:"_tag"`
			Detail  string `json:"detail"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(c.Error, &e)
		detail := e.Detail
		if detail == "" {
			detail = e.Message
		}
		if detail == "" {
			detail = string(c.Error)
		}
		return &rpcError{Method: method, Tag: e.Tag, Detail: detail}
	case "Die":
		var s string
		if json.Unmarshal(c.Defect, &s) != nil {
			s = string(c.Defect)
		}
		return &rpcError{Method: method, Detail: s}
	}
	return &rpcError{Method: method, Detail: strings.ToLower(c.Tag)}
}
