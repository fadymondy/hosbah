// Command api is the Hosbah HTTP entrypoint. It boots the shared togo stack
// (Huma REST + OpenAPI and gqlgen GraphQL on the kernel) and serves it.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fadymondy/hosbah/internal/mahaam"
	"github.com/fadymondy/hosbah/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "openapi" {
		b, err := server.OpenAPI()
		if err != nil {
			panic(err)
		}
		os.Stdout.Write(b)
		return
	}

	a := server.Boot()
	defer a.Kernel.Close()
	k := a.Kernel
	// Mahaam Feedback: every panic togo recovers and every k.ReportError is filed as an issue in
	// Hosbah's Mahaam project (MAHAAM_URL / MAHAAM_FEEDBACK_KEY / MAHAAM_APP_URL; off without them).
	if fb := mahaam.New(mahaam.FromEnv()); fb.Enabled() {
		k.Hooks.On("error", 100, fb.OnError)
		k.Log.Info("mahaam feedback: backend errors are filed as issues")
	}
	fmt.Printf("→ hosbah listening on %s  (GraphQL %s · REST %s · docs %s)\n",
		k.Config.Addr, k.Config.GraphQLPath, k.Config.RESTPath, k.Config.DocsPath)
	if err := k.Serve(context.Background()); err != nil {
		panic(err)
	}
}
