package server

import (
	"github.com/togo-framework/auth"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/circlexo"
)

// installCircleXO wires the CircleXO hub integration (docs/circlexo.md). With CIRCLEXO_ISSUER unset
// it does nothing and the process is exactly the one without it. A half-set configuration is logged
// as an error and leaves the integration off rather than stopping the API: Hosbah's own sign-in
// must not depend on the hub being configured correctly.
func installCircleXO(k *togo.Kernel, a *app.App) {
	authSvc, _ := auth.FromKernel(k)
	s, err := circlexo.FromEnv(a.SQLDB, a.Keys, a.Log, authSvc)
	if err != nil {
		a.Log.Error("circlexo: not enabled, fix the CIRCLEXO_* configuration", "err", err)
		s = nil
	}
	circlexo.Install(s) // also clears the hooks when off, for a process that boots more than once
	k.Router.Get(circlexo.ConfigPath, circlexo.ConfigHandler)
	if s != nil {
		s.Mount(k.Router)
		a.Log.Info("circlexo: hub integration enabled")
	}
}
