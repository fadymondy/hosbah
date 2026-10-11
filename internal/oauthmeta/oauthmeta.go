// Package oauthmeta names the URLs of Hosbah's OAuth 2.1 authorization server for MCP connectors
// (internal/api/mcp_oauth.go) and of the protected resource it guards, the team MCP at /api/mcp.
// The MCP transport (internal/mcp) needs the resource-metadata URL for its 401 challenge, and the
// API needs all of them for the discovery documents, so they live here, below both.
package oauthmeta

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	// ResourcePath is the protected resource: the team MCP server.
	ResourcePath = "/api/mcp"
	// ProtectedResourcePath is RFC 9728 protected-resource metadata. The MCP spec has a client try
	// the path-suffixed form first (…/oauth-protected-resource/api/mcp), then this one.
	ProtectedResourcePath = "/.well-known/oauth-protected-resource"
	// AuthorizationServerPath is RFC 8414 authorization-server metadata.
	AuthorizationServerPath = "/.well-known/oauth-authorization-server"

	AuthorizePath = "/oauth/authorize" // the web app's consent page
	TokenPath     = "/oauth/token"
	RegisterPath  = "/oauth/register" // RFC 7591 dynamic client registration
	RevokePath    = "/oauth/revoke"   // RFC 7009
)

// Origin is Hosbah's public origin: APP_URL (production always sets it, so the issuer never depends
// on a request header), else the origin the request came in on (local development and tests).
func Origin(r *http.Request) string {
	if o := appURL(); o != "" {
		return o
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := first(r.Header.Get("X-Forwarded-Proto")); p == "https" || p == "http" {
		scheme = p
	}
	host := r.Host
	if h := first(r.Header.Get("X-Forwarded-Host")); h != "" {
		host = h
	}
	return scheme + "://" + host
}

// Resource is the canonical URI of the team MCP server (RFC 8707 resource indicator).
func Resource(r *http.Request) string { return Origin(r) + ResourcePath }

// ResourceMetadataURL is where the 401 on /api/mcp points a client (WWW-Authenticate resource_metadata).
func ResourceMetadataURL(r *http.Request) string {
	return Origin(r) + ProtectedResourcePath + ResourcePath
}

// ResourceAllowed reports whether a client's resource parameter (RFC 8707) names the team MCP server.
// Empty is allowed (a client that sends no resource indicator) and a trailing slash is forgiven.
// With APP_URL set (production) it must be exactly APP_URL/api/mcp; without it (development, tests)
// any absolute URL whose path is /api/mcp.
func ResourceAllowed(resource string) bool {
	if resource == "" {
		return true
	}
	res := strings.TrimRight(resource, "/")
	if o := appURL(); o != "" {
		return res == o+ResourcePath
	}
	u, err := url.Parse(res)
	return err == nil && u.IsAbs() && u.Path == ResourcePath
}

func appURL() string { return strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/") }

func first(h string) string {
	v, _, _ := strings.Cut(h, ",")
	return strings.TrimSpace(v)
}
