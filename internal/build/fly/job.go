// Package fly runs server builds on Fly.io Machines (decisions.md 039, 041). The builder starts a machine with a
// MachineJob; greyship-buildd on the machine builds, uploads through presigned URLs and reports back to the API.
package fly

import (
	"errors"
	"time"

	"greyship.tech/internal/auth"
)

// MachineJob is everything a build machine is given. It holds no secrets and no long-lived credentials: the source
// URL and the token expire with the build.
type MachineJob struct {
	Deployment string   `json:"deployment"`
	SourceURL  string   `json:"source_url"`
	APIURL     string   `json:"api_url"` // where /v1/internal/builds/{deployment}/… lives
	Token      string   `json:"token"`
	GOARCH     string   `json:"goarch"`
	Targets    []Target `json:"targets"`
}

type Target struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Report is one batch a build machine posts: log lines, and the outcome in the last batch.
type Report struct {
	Lines   []Line   `json:"lines"`
	Outcome *Outcome `json:"outcome,omitempty"`
}

type Line struct {
	Stream string `json:"stream"` // system | build
	Text   string `json:"text"`
}

type Outcome struct {
	OK       bool              `json:"ok"`
	ExitCode int               `json:"exit_code,omitempty"` // the go command's, when the code didn't compile
	Error    string            `json:"error,omitempty"`
	Binaries map[string]Binary `json:"binaries,omitempty"`
	Seconds  float64           `json:"seconds"`
}

type Binary struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// UploadRequest asks the API for a presigned PUT URL once a binary's size is known (signatures cover the size).
type UploadRequest struct {
	Target string `json:"target"`
	Size   int64  `json:"size"`
}

// MaxBinary matches the limit for binaries built anywhere else.
const MaxBinary = 200 << 20

type tokenClaims struct {
	Kind       string `json:"kind"`
	Deployment string `json:"deployment"`
}

var ErrBadToken = errors.New("invalid build token")

// SignToken makes the build token for one deployment. It lives as long as the build may run, plus slack.
func SignToken(s auth.Signer, deployment string, ttl time.Duration) (string, error) {
	return s.Sign(tokenClaims{Kind: "build", Deployment: deployment}, ttl)
}

// CheckToken reports whether token was issued for this deployment's build, and hasn't expired.
func CheckToken(s auth.Signer, token, deployment string) error {
	var c tokenClaims
	if err := s.Verify(token, &c); err != nil || c.Kind != "build" || c.Deployment != deployment {
		return ErrBadToken
	}
	return nil
}
