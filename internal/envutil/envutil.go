// Package envutil provides tiny environment-variable helpers shared by all
// three service main packages.
package envutil

import "os"

// Get returns the environment variable value or fallback if unset/empty.
func Get(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
