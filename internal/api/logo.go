package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// Uploaded logos (servers and apps): one validation and one way of serving them.

// MaxLogoBytes is the largest logo accepted (keep in step with server_logos_size_check and
// app_logos_size_check).
const MaxLogoBytes = 512 << 10

// logoTypes are the accepted image types. No SVG: it is a document that can carry script.
var logoTypes = []string{"image/png", "image/jpeg", "image/webp"}

// logoURL is where an uploaded logo is served (kind "servers"; an app's is deploy.AppLogoURL, which
// the deployments feed shares), versioned by its etag so the response can be cached forever.
func logoURL(teamID, kind, id, etag string) *string {
	v := etag
	if len(v) > 16 {
		v = v[:16]
	}
	u := "/api/teams/" + teamID + "/" + kind + "/" + id + "/logo?v=" + v
	return &u
}

// sniffLogoType says what the bytes really are (magic bytes), or "".
func sniffLogoType(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// checkLogo validates an upload: 415 for another Content-Type, 422 for no body or bytes that are
// not what the Content-Type says, 413 over the limit. It returns the type and the etag (hex SHA-256).
func checkLogo(contentType string, body []byte) (ct, etag string, err error) {
	ct, _, _ = mime.ParseMediaType(contentType)
	ct = strings.ToLower(ct)
	ok := false
	for _, t := range logoTypes {
		ok = ok || t == ct
	}
	if !ok {
		return "", "", huma.Error415UnsupportedMediaType("a logo is a PNG, JPEG or WebP image (Content-Type image/png, image/jpeg or image/webp)")
	}
	if len(body) == 0 {
		return "", "", huma.Error422UnprocessableEntity("the request body is the image")
	}
	if len(body) > MaxLogoBytes {
		return "", "", huma.NewError(http.StatusRequestEntityTooLarge, "a logo is at most 512 KiB")
	}
	if sniffLogoType(body) != ct {
		return "", "", huma.Error422UnprocessableEntity("the file is not a " + ct + " image")
	}
	sum := sha256.Sum256(body)
	return ct, hex.EncodeToString(sum[:]), nil
}

// logoBinary is the OpenAPI content of a logo body.
func logoBinary(desc string) map[string]*huma.MediaType {
	m := map[string]*huma.MediaType{}
	for _, t := range logoTypes {
		m[t] = &huma.MediaType{Schema: &huma.Schema{Type: "string", Format: "binary", Description: desc}}
	}
	return m
}

// logoOut is a served logo: never sniffed, sandboxed, cached forever (its URL is versioned).
type logoOut struct {
	ContentType string `header:"Content-Type"`
	NoSniff     string `header:"X-Content-Type-Options"`
	CSP         string `header:"Content-Security-Policy"`
	Cache       string `header:"Cache-Control"`
	ETag        string `header:"ETag"`
	Body        []byte
}

// serveLogo fills out, or answers 304 when the client already has this etag.
func serveLogo(out *logoOut, ifNoneMatch, contentType, etag string, data []byte) error {
	if ifNoneMatch == `"`+etag+`"` {
		return huma.Status304NotModified()
	}
	*out = logoOut{ContentType: contentType, NoSniff: "nosniff", CSP: "default-src 'none'; sandbox",
		Cache: "private, max-age=31536000, immutable", ETag: `"` + etag + `"`, Body: data}
	return nil
}
