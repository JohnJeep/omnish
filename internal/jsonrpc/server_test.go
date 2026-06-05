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
