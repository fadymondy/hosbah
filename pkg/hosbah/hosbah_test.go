package hosbah

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeMCP(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mcp" || r.Header.Get("Authorization") != "Bearer cld_ok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"invalid token"}}`))
			return
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"deploy_app","description":"d","inputSchema":{}}]}}`))
		case req.Params.Name == "deploy_app" && req.Params.Arguments["app"] == "shop" && req.Params.Arguments["wait"] == true:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Deployment #3 succeeded."}]}}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"app not found"}],"isError":true}}`))
		}
	}))
}

func TestCallAndHelpers(t *testing.T) {
	srv := fakeMCP(t)
	defer srv.Close()
	c := New(srv.URL+"/", "cld_ok")
	ctx := context.Background()

	tools, err := c.Tools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "deploy_app" {
		t.Fatalf("tools = %+v err = %v", tools, err)
	}
	out, err := c.Deploy(ctx, "shop", Wait())
	if err != nil || out != "Deployment #3 succeeded." {
		t.Fatalf("deploy = %q err = %v", out, err)
	}
	_, err = c.Deploy(ctx, "nope")
	var te *ToolError
	if !errors.As(err, &te) || te.Message != "app not found" {
		t.Fatalf("err = %v", err)
	}
}

func TestBadTokenAndNoToken(t *testing.T) {
	srv := fakeMCP(t)
	defer srv.Close()
	_, err := New(srv.URL, "cld_bad").Apps(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 || ae.Message != "invalid token" {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(srv.URL, "").Apps(context.Background()); err == nil {
		t.Fatal("expected an error without a token")
	}
}

// A wait longer than one request comes back as a chain: each reply but the last ends with a
// continue: line, which Call follows until the deployment has finished.
func TestCallFollowsContinue(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls++
		text := ""
		switch {
		case req.Params.Name == "deploy_app":
			text = "Deploy #4 of shop queued.\n#4 is running (step build).\ncontinue: wait_deployment {\"app\":\"shop\",\"deployment\":\"4\",\"until\":\"2026-09-25T12:00:00Z\"}\n"
		case req.Params.Name == "wait_deployment" && req.Params.Arguments["deployment"] == "4" && calls == 2:
			text = "#4 is running (step release).\ncontinue: wait_deployment {\"app\":\"shop\",\"deployment\":\"4\",\"until\":\"2026-09-25T12:00:00Z\"}\n"
		case req.Params.Name == "wait_deployment" && req.Params.Arguments["until"] == "2026-09-25T12:00:00Z":
			text = "✓ #4 succeeded in 1m2s.\n"
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	out, err := New(srv.URL, "hbu_ok").Deploy(context.Background(), "shop", Wait())
	if err != nil || out != "Deploy #4 of shop queued.\n#4 is running (step build).\n✓ #4 succeeded in 1m2s.\n" || calls != 3 {
		t.Fatalf("deploy = %q err = %v calls = %d", out, err, calls)
	}
}
