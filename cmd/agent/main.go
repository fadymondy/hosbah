// Command cloudy-agent runs on every server Hosbah manages.
//
//	cloudy-agent enroll --url https://cloudy.example --token <one-time token> [--state /etc/cloudy/agent.json]
//	cloudy-agent run [--state /etc/cloudy/agent.json]
//	cloudy-agent facts      print what enrolment would report, and the checks
//	cloudy-agent version
//
// Build it static: CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X main.version=<v>" ./cmd/agent
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agent"
	"github.com/fadymondy/hosbah/internal/agentproto"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version)
	case "facts":
		f := agent.CollectFacts(context.Background())
		out, _ := json.MarshalIndent(map[string]any{"facts": f, "checks": agentproto.EvaluateChecks(f)}, "", "  ")
		fmt.Println(string(out))
	case "enroll":
		os.Exit(enroll(os.Args[2:]))
	case "run":
		os.Exit(run(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cloudy-agent enroll --url URL --token TOKEN | run | facts | version")
}

func enroll(args []string) int {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	url := fs.String("url", "", "control plane origin, e.g. https://cloudy-app.example.com")
	token := fs.String("token", "", "one-time enrolment token")
	state := fs.String("state", agent.DefaultStatePath, "where to write the agent's identity")
	_ = fs.Parse(args)
	if *url == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "enroll needs --url and --token")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, checks, err := agent.Enroll(ctx, *url, *token, version, nil)
	printChecks(checks)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		var ee *agent.EnrollError
		if errors.As(err, &ee) && ee.Status == 422 {
			fmt.Fprintln(os.Stderr, "  Fix the failed checks above, then ask Hosbah for a new install command.")
		}
		return 1
	}
	if err := st.Save(*state); err != nil {
		fmt.Fprintln(os.Stderr, "✗ enrolled, but could not save the agent's identity:", err)
		return 1
	}
	fmt.Printf("✓ enrolled as %s (%s)\n", st.ServerName, st.ServerID)
	return 0
}

func printChecks(checks []agentproto.Check) {
	for _, c := range checks {
		mark := map[string]string{agentproto.CheckPass: "✓", agentproto.CheckWarn: "!", agentproto.CheckFail: "✗"}[c.Status]
		fmt.Printf("  %s %-8s %s\n", mark, c.Name, c.Detail)
	}
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	state := fs.String("state", agent.DefaultStatePath, "the agent's identity (written by enroll)")
	heartbeat := fs.Duration("heartbeat", 15*time.Second, "heartbeat interval")
	metricsEvery := fs.Duration("metrics", 5*time.Second, "live metrics interval (0 turns the light readings off)")
	_ = fs.Parse(args)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, err := agent.LoadState(*state)
	if err != nil {
		log.Error("cannot start", "err", err)
		return 1
	}
	a, err := agent.New(st, version, log)
	if err != nil {
		log.Error("cannot start", "err", err)
		return 1
	}
	a.HeartbeatEvery = *heartbeat
	a.MetricsEvery = *metricsEvery
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := catalog.Builtin(); err != nil { // recipe.install would refuse everything: say why at once
		log.Error("the embedded recipe catalog did not load", "err", err)
		return 1
	}
	log.Info("cloudy-agent starting", "version", version, "server", st.ServerName, "control_plane", st.ControlPlane)
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("stopped", "err", err)
		return 1
	}
	return 0
}
