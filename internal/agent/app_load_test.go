package agent

import (
	"context"
	"strings"
	"testing"
)

func TestAppLoadsSumsContainersPerApp(t *testing.T) {
	run := func(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(cmd, "ps --filter label=hosbah.app "):
			return []byte("shop-web-1\tapp-1\nshop-worker-1\tapp-1\nblog-web-1\tapp-2\n"), nil
		case strings.HasPrefix(cmd, "ps "):
			return []byte("old-web\tapp-3\n"), nil
		case strings.HasPrefix(cmd, "stats "):
			return []byte(`{"Name":"shop-web-1","CPUPerc":"3.5%","MemUsage":"100MiB / 1GiB"}
{"Name":"shop-worker-1","CPUPerc":"1.5%","MemUsage":"50MiB / 1GiB"}
{"Name":"blog-web-1","CPUPerc":"0.25%","MemUsage":"10MiB / 512MiB"}
{"Name":"stranger","CPUPerc":"90%","MemUsage":"1GiB / 2GiB"}
`), nil
		}
		return nil, nil
	}
	got := appLoads(context.Background(), run)
	if len(got) != 2 || got[0].AppID != "app-1" || got[1].AppID != "app-2" {
		t.Fatalf("apps: %+v", got)
	}
	if got[0].Containers != 2 || got[0].CPUPercent != 5 || got[0].MemUsedBytes != 150<<20 || got[0].MemLimitBytes != 2<<30 {
		t.Fatalf("app-1: %+v", got[0])
	}
}
