// Package version exposes the running backend's semantic version so the
// gateway can report it over HTTP (GET /api/v3/version). The default value
// mirrors the repo-root VERSION file; production docker images override it
// at build time via -ldflags "-X .../internal/version.Version=$(cat VERSION)"
// (see api-gateway/Dockerfile), so the endpoint always reports the exact
// version baked into the running image.
package version

// Version is the backend's semantic version (semver: MAJOR.MINOR.PATCH).
// It is a var (not a const) so the build can override it via -ldflags.
// Keep it in sync with the repo-root VERSION file — both are bumped on
// every change per the Versioning Requirement in CLAUDE.md.
var Version = "4.4.4"
