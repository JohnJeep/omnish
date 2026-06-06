package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestServer() *Server {
	reg := NewRegistry()
	reg.AddService("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		return string(params), nil
	})
	reg.AddService("error.method", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, &RPCError{Code: -32000, Message: "test error"}
	})
	return NewServer(reg)
}

func rpcRequest(method string, params any, id any) string {
	m := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}
	if params != nil {
		b, _ := json.Marshal(params)
		m["params"] = json.RawMessage(b)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestEchoMethod(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	req := rpcRequest("echo", map[string]string{"msg": "hello"}, 1)
	var out bytes.Buffer
	if err := srv.ReadFrom(ctx, strings.NewReader(req+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v\n%s", err, out.String())
	}
	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}
	if resp["result"] == nil {
		t.Error("expected non-nil result")
	}
}

func TestMethodNotFound(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	req := rpcRequest("no.such.method", nil, 2)
	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(req+"\n"), &out)

	var resp map[string]any
	_ = json.Unmarshal(out.Bytes(), &resp)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp)
	}
	if int(errObj["code"].(float64)) != CodeMethodNotFound {
		t.Errorf("expected CodeMethodNotFound(%d), got %v", CodeMethodNotFound, errObj["code"])
	}
}

func TestRPCError(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	req := rpcRequest("error.method", nil, 3)
	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(req+"\n"), &out)

	var resp map[string]any
	_ = json.Unmarshal(out.Bytes(), &resp)
	errObj, ok := resp["error"].(map[string]any)
	if !ok || errObj["message"] != "test error" {
		t.Errorf("unexpected error object: %v", resp["error"])
	}
}

func TestBadJSON(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader("not json\n"), &out)

	var resp map[string]any
	_ = json.Unmarshal(out.Bytes(), &resp)
	errObj, ok := resp["error"].(map[string]any)
	if !ok || int(errObj["code"].(float64)) != CodeParseError {
		t.Errorf("expected parse error, got %v", resp)
	}
}

func TestRPCList(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	req := rpcRequest("rpc.list", nil, 10)
	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(req+"\n"), &out)

	var resp struct {
		Result []string `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Result) == 0 {
		t.Error("expected at least one method in rpc.list")
	}
}

// TestJSONRPCResponseHasVersion verifies all success responses include "jsonrpc":"2.0".
// Spec §5: A Response object MUST contain the "jsonrpc" member set to "2.0".
func TestJSONRPCResponseHasVersion(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(rpcRequest("echo", nil, 1)+"\n"), &out)

	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Errorf(`"jsonrpc": want "2.0", got %v`, resp["jsonrpc"])
	}
}

// TestJSONRPCErrorResponseHasVersion verifies error responses also include "jsonrpc":"2.0".
// Spec §5.1: error responses are Response objects and must include the "jsonrpc" member.
func TestJSONRPCErrorResponseHasVersion(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(rpcRequest("no.such.method", nil, 2)+"\n"), &out)

	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Errorf(`"jsonrpc" in error response: want "2.0", got %v`, resp["jsonrpc"])
	}
}

// TestJSONRPCNotification verifies that a request without an "id" field produces no response.
// Spec §4: A Notification is a Request without an "id". Server MUST NOT reply.
func TestJSONRPCNotification(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// Deliberately omit "id" field to mark this as a notification
	notification := `{"jsonrpc":"2.0","method":"echo","params":[]}` + "\n"
	var out bytes.Buffer
	if err := srv.ReadFrom(ctx, strings.NewReader(notification), &out); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no response for notification, got %d bytes: %s", out.Len(), out.String())
	}
}

// TestJSONRPCIntegerID verifies that a numeric "id" is echoed verbatim in the response.
// Spec §4: id may be a String, Number, or Null; response id MUST match the request id.
func TestJSONRPCIntegerID(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(rpcRequest("echo", nil, 42)+"\n"), &out)

	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	idVal, ok := resp["id"].(float64) // JSON numbers decode as float64
	if !ok {
		t.Fatalf(`"id" type: want float64, got %T (%v)`, resp["id"], resp["id"])
	}
	if idVal != 42 {
		t.Errorf(`"id": want 42, got %v`, idVal)
	}
}

// TestJSONRPCInvalidRequest verifies that a request missing the "method" field returns -32600.
// Spec §5.1 error code -32600: "Invalid Request – The JSON sent is not a valid Request object."
func TestJSONRPCInvalidRequest(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	raw := `{"jsonrpc":"2.0","id":5}` + "\n" // no "method" key
	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(raw), &out)

	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Errorf(`"jsonrpc": want "2.0", got %v`, resp["jsonrpc"])
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp["error"])
	}
	if int(errObj["code"].(float64)) != CodeInvalidRequest {
		t.Errorf("error code: want CodeInvalidRequest (%d), got %v", CodeInvalidRequest, errObj["code"])
	}
}

// TestJSONRPCNullID verifies that "id":null in a request is echoed as null in the response.
// Spec §4: id=null is valid (distinct from absent id); response id must mirror the request id.
func TestJSONRPCNullID(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	raw := `{"jsonrpc":"2.0","method":"no.such","id":null}` + "\n"
	var out bytes.Buffer
	_ = srv.ReadFrom(ctx, strings.NewReader(raw), &out)

	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	// JSON null decodes to nil in map[string]any; key must be present with nil value
	idVal, hasID := resp["id"]
	if !hasID {
		t.Fatal(`"id" key missing from response`)
	}
	if idVal != nil {
		t.Errorf(`"id": want null (nil), got %v (%T)`, idVal, idVal)
	}
}
