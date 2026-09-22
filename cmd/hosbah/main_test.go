package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	got := parseArgs([]string{"server=edge", "--hostname", "nas.example.com", "--wait", "--port=8080", "tls=off", "enabled=false", "zip=0123", "--dry-run"})
	want := map[string]any{"server": "edge", "hostname": "nas.example.com", "wait": true, "port": float64(8080), "tls": "off", "enabled": false, "zip": "0123", "dry_run": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestShortcutCallsTool(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = req.Params
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"queued"}]}}`))
	}))
	defer srv.Close()
	t.Setenv("HOSBAH_URL", srv.URL)
	t.Setenv("HOSBAH_TOKEN", "hbu_x")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"deploy", "shop", "--wait"}, &out); err != nil {
		t.Fatal(err)
	}
	args, _ := seen["arguments"].(map[string]any)
	if seen["name"] != "deploy_app" || args["app"] != "shop" || args["wait"] != true || !strings.Contains(out.String(), "queued") {
		t.Fatalf("seen %+v out %q", seen, out.String())
	}
}
