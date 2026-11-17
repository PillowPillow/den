//go:build !unix

package repos

// configOpenFlags is empty here: den ships for darwin and linux only, and this file only keeps
// the windows typecheck building.
const configOpenFlags = 0
