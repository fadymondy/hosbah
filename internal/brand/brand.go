// Package brand holds the product's names, and the rules for the names it had before.
//
// The product was called Cloudy until 2026-09-19 and is Hosbah since. Servers, containers,
// .env files and tokens made before the rename still carry the old names, so every reader here
// accepts both, and every writer emits the new one:
//
//	HOSBAH_X   environment variables (CLOUDY_X still read, as a fallback)
//	hosbah     the slug in file, unit, image and container names (cloudy: pre-rename objects)
package brand

import "os"

const (
	// Name is the product's display name.
	Name = "Hosbah"
	// Slug is the lower-case name used in files, units, images and containers.
	Slug = "hosbah"
	// LegacySlug is the slug before the rename. Only readers use it.
	LegacySlug = "cloudy"

	// EnvPrefix prefixes every environment variable Hosbah reads.
	EnvPrefix = "HOSBAH_"
	// LegacyEnvPrefix is the prefix before the rename, read when the new variable is not set.
	LegacyEnvPrefix = "CLOUDY_"
)

// Getenv returns HOSBAH_<key>, or CLOUDY_<key> when the former is empty or unset. key has no
// prefix: Getenv("KEK") reads HOSBAH_KEK, then CLOUDY_KEK.
func Getenv(key string) string {
	if v := os.Getenv(EnvPrefix + key); v != "" {
		return v
	}
	return os.Getenv(LegacyEnvPrefix + key)
}

// LookupEnv is Getenv for variables where "set but empty" means something (an empty apps prefix):
// HOSBAH_<key> wins whenever it is set, even to "", and CLOUDY_<key> is read only when it is not.
func LookupEnv(key string) (string, bool) {
	if v, ok := os.LookupEnv(EnvPrefix + key); ok {
		return v, true
	}
	return os.LookupEnv(LegacyEnvPrefix + key)
}

// EnvName is the name to show a person for key: the new one (HOSBAH_<key>).
func EnvName(key string) string { return EnvPrefix + key }
