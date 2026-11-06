package github

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Public repositories are read without the app and without GitHub's REST API, whose 60 calls an hour per IP the
// whole server would share (decisions.md 043): refs come from git's own smart-HTTP advertisement, source from
// codeload, the same archive the API redirects to.

var (
	ErrBadURL    = errors.New("that isn't a GitHub repository link (like https://github.com/owner/repo)")
	ErrNotPublic = errors.New("this repository isn't public. Install the Greyship GitHub app to deploy private repositories")
)

// ParseRepoURL reads a GitHub repository link: https://github.com/o/r, o/r.git, …/tree/<branch>, github.com/o/r or
// plain o/r. It returns owner/repo and the branch the link names, if any. Other hosts are refused.
func ParseRepoURL(raw string) (repo, branch string, err error) {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "://") && strings.HasPrefix(s, "github.com/") {
		s = "https://" + s
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, "github.com") && !strings.EqualFold(u.Host, "www.github.com") {
			return "", "", ErrBadURL
		}
		s = strings.Trim(u.Path, "/")
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return "", "", ErrBadURL
	}
	repo = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if len(parts) >= 4 && parts[2] == "tree" {
		branch = strings.Join(parts[3:], "/")
	} else if len(parts) > 2 {
		return "", "", ErrBadURL
	}
	if !ValidRepo(repo) {
		return "", "", ErrBadURL
	}
	return repo, branch, nil
}

// PublicRefs lists a public repo's branches with their commits, and names its default branch.
func (a *App) PublicRefs(ctx context.Context, repo string) (string, map[string]string, error) {
	u := strings.TrimRight(a.Web, "/") + "/" + repo + ".git/info/refs?service=git-upload-pack"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "git/2 (greyship)")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", repo, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
		return "", nil, ErrNotPublic // GitHub asks for credentials for private and missing repos alike
	case resp.StatusCode != http.StatusOK:
		return "", nil, fmt.Errorf("read %s: GitHub answered %d", repo, resp.StatusCode)
	}
	return parseRefs(io.LimitReader(resp.Body, 16<<20))
}

// parseRefs reads git's pkt-line ref advertisement: a 4-hex-digit length, then "<sha> <ref>", with capabilities
// after a NUL on the first ref (where symref=HEAD:refs/heads/<x> names the default branch). 0000 is a flush.
func parseRefs(r io.Reader) (string, map[string]string, error) {
	br := bufio.NewReader(r)
	heads := map[string]string{}
	defaultBranch := ""
	for {
		var size [4]byte
		if _, err := io.ReadFull(br, size[:]); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", nil, errors.New("GitHub's ref list is cut short")
		}
		n, err := strconv.ParseUint(string(size[:]), 16, 16)
		if err != nil {
			return "", nil, errors.New("GitHub's ref list isn't in git's format")
		}
		if n == 0 {
			continue
		}
		if n < 4 {
			return "", nil, errors.New("GitHub's ref list isn't in git's format")
		}
		line := make([]byte, n-4)
		if _, err := io.ReadFull(br, line); err != nil {
			return "", nil, errors.New("GitHub's ref list is cut short")
		}
		text := strings.TrimSuffix(string(line), "\n")
		if strings.HasPrefix(text, "#") {
			continue
		}
		ref, caps, _ := strings.Cut(text, "\x00")
		for _, c := range strings.Fields(caps) {
			if target, ok := strings.CutPrefix(c, "symref=HEAD:refs/heads/"); ok {
				defaultBranch = target
			}
		}
		sha, name, ok := strings.Cut(ref, " ")
		if branch, isHead := strings.CutPrefix(name, "refs/heads/"); ok && isHead && len(sha) == 40 {
			heads[branch] = sha
		}
	}
	if len(heads) == 0 {
		return "", nil, errors.New("the repository has no branches yet")
	}
	return defaultBranch, heads, nil
}

// PublicTarball downloads a public repo at a commit from codeload, refusing archives larger than limit.
func (a *App) PublicTarball(ctx context.Context, repo, sha string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.Codeload, "/")+"/"+repo+"/tar.gz/"+url.PathEscape(sha), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "greyship")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s@%s: %w", repo, sha, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s@%s: GitHub answered %d", repo, sha, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s@%s: %w", repo, sha, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the repository archive is larger than %d MB", limit>>20)
	}
	return body, nil
}
