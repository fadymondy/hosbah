package deploy

import (
	"fmt"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

/*
Pinned build and edge tools. Each release was checked on 2026-09-14:

  - Caddy 2.11.4 (Apache-2.0), published 2026-06-03. SHA-512 from the release's
    caddy_2.11.4_checksums.txt. Newer than any distribution package (Ubuntu 24.04 ships 2.6.2),
    and the on-demand TLS `permission` module needs ≥ 2.8.
  - Railpack 0.39.0 (MIT, github.com/railwayapp/railpack, Railway's successor to the frozen
    Nixpacks), published 2026-09-03. SHA-256 from the release's checksums.txt. Builds run through
    its BuildKit frontend image of the same version, as Railpack recommends for platforms.

The version, URL and hash travel inside the signed provisioning params; the agent installs only
bytes that hash to the signed value. Upgrading = changing these constants (and re-running the steps).
*/

const (
	CaddyVersion     = "2.11.4"
	RailpackVersion  = "0.39.0"
	RailpackFrontend = "ghcr.io/railwayapp/railpack-frontend:v" + RailpackVersion
)

var caddySHA512 = map[string]string{
	"amd64": "8220d1f013b6f27510247b2360c9e0ca9f018feebd82515f07635318b34ff9777ccc8fd0b6e6f2486ce3a33fe389fbb7db12d05baa474f4587509fb4f5ebf1c9",
	"arm64": "d5a7c423853c24a799765e0e8210d5c7c22a8f56ed37a3cae2fb9f58be138853c02b4efd6b59d576e6d8c7c0d30b9c1592deeaa6a536ff69bcca23b8c1ea709c",
}

var railpackSHA256 = map[string]string{
	"amd64": "728407f5cdb9e9bc1cdd07f568419344a20e71b0a5a9fd90a9cfbaca0a6c94f7",
	"arm64": "42eb3fa68e38f44be3610a7d74f714ec0d808d70c105fbe35e053b6e6cfb20be",
}

// CaddyTool is caddy.install's pinned download for a server's architecture.
func CaddyTool(arch string) (*agentproto.ToolInstall, error) {
	sum, ok := caddySHA512[arch]
	if !ok {
		return nil, fmt.Errorf("no Caddy build pinned for %q", arch)
	}
	return &agentproto.ToolInstall{Version: CaddyVersion, SHA512: sum,
		URL: fmt.Sprintf("https://github.com/caddyserver/caddy/releases/download/v%[1]s/caddy_%[1]s_linux_%[2]s.tar.gz", CaddyVersion, arch)}, nil
}

// RailpackTool is railpack.install's pinned download.
func RailpackTool(arch string) (*agentproto.ToolInstall, error) {
	sum, ok := railpackSHA256[arch]
	if !ok {
		return nil, fmt.Errorf("no Railpack build pinned for %q", arch)
	}
	triple := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[arch]
	return &agentproto.ToolInstall{Version: RailpackVersion, SHA256: sum,
		URL: fmt.Sprintf("https://github.com/railwayapp/railpack/releases/download/v%[1]s/railpack-v%[1]s-%[2]s-unknown-linux-musl.tar.gz", RailpackVersion, triple)}, nil
}
