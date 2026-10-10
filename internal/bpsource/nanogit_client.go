// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	nanogit "github.com/grafana/nanogit"
	"github.com/grafana/nanogit/options"
	"github.com/grafana/nanogit/protocol"
	"github.com/grafana/nanogit/protocol/hash"
)

// Compile-time assertion: nanogitClient implements GitClient.
var _ GitClient = (*nanogitClient)(nil)

// nanogitClient implements GitClient using github.com/grafana/nanogit.
// A fresh nanogit.Client is constructed per call (nanogit clients are
// per-URL; constructing one is cheap — it does not open a connection).
//
// Ref naming: callers must pass a full ref name (e.g. "refs/heads/main").
// Bare branch names like "main" will not be found; GetRef performs a
// prefix-match and requires an exact full name. Source.Ref is documented
// as the full form (e.g. "refs/heads/main") so this is consistent.
type nanogitClient struct {
	tokenLookup func(name string) string
	policy      SourcePolicy
}

// NewNanogitClient returns a GitClient backed by nanogit.
// tokenLookup maps an env-var name → its value (production: os.Getenv;
// tests: a stub map lookup). An empty name is passed to tokenLookup for the
// GIT_TOKEN fallback; an empty looked-up value means no authentication.
func NewNanogitClient(tokenLookup func(name string) string) GitClient {
	return NewNanogitClientWithPolicy(tokenLookup, SourcePolicy{})
}

// NewNanogitClientWithPolicy enforces the resolved process policy before looking
// up credentials or making requests, including for persisted sources.
func NewNanogitClientWithPolicy(tokenLookup func(name string) string, policy SourcePolicy) GitClient {
	return &nanogitClient{tokenLookup: tokenLookup, policy: policy}
}

// newClient constructs a nanogit.Client for the given URL, optionally
// attaching a token when tokenEnvVar names a non-empty secret.
// For GitHub and Grafana-hosted repos, token auth uses the token string
// directly (no "Bearer" prefix); the caller is responsible for passing
// the correct format if needed (most Git forges accept a PAT directly).
func (c *nanogitClient) newClient(url, tokenEnvVar string) (nanogit.Client, error) {
	if err := validateTokenEnvVar(tokenEnvVar); err != nil {
		return nil, err
	}
	if err := c.policy.validateURL(url); err != nil {
		return nil, err
	}
	// Do not follow redirects: even an initially allowed host must not redirect
	// a credential-bearing request to another host (or downgrade to HTTP).
	opts := []options.Option{options.WithHTTPClient(&http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return fmt.Errorf("git source redirects are not permitted; configure the final HTTPS URL")
		},
	})}
	if c.tokenLookup != nil {
		if token := c.tokenLookup(tokenEnvVar); token != "" {
			// For GitHub, the convention is "token <PAT>" or just the PAT as password.
			// nanogit's WithBasicAuth accepts (username, password); Git forges accept
			// any non-empty username + PAT as password. Use "git" as the conventional
			// username for token-as-password flows.
			opts = append(opts, options.WithBasicAuth("git", token))
		}
	}
	client, err := nanogit.NewHTTPClient(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nanogit: create client for %q: %w", url, err)
	}
	return client, nil
}

// HeadSHA returns the commit SHA the ref currently points to.
// ref must be a full reference name (e.g. "refs/heads/main").
func (c *nanogitClient) HeadSHA(ctx context.Context, url, ref, tokenEnvVar string) (string, error) {
	client, err := c.newClient(url, tokenEnvVar)
	if err != nil {
		return "", err
	}
	r, err := client.GetRef(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("nanogit: GetRef %q on %q: %w", ref, url, err)
	}
	return r.Hash.String(), nil
}

// FetchYAML returns every *.yaml blob under subpath at the given ref, keyed by a flat storage
// filename derived from the path RELATIVE to subpath (see flattenKey). Files directly under the
// subpath keep their plain name ("my-blueprint.yaml"); nested files are flattened ("a/svc.yaml" →
// "a-svc.yaml") so two files sharing a base name in different sub-directories don't clobber each
// other in the flat git/<id>/ staging dir. The blueprint's identity is its YAML `name:` (namespaced
// by the source), never the stored filename, so the flattening is purely a storage detail.
//
// subpath="" means the repository root. Matching is path-prefix based:
// a file at "dir/sub/foo.yaml" is included when subpath="dir/sub".
// The comparison normalises trailing slashes so both "dir/sub" and
// "dir/sub/" work correctly.
//
// ref must be a full reference name (e.g. "refs/heads/main").
func (c *nanogitClient) FetchYAML(ctx context.Context, url, ref, subpath, tokenEnvVar string) (map[string][]byte, error) {
	client, err := c.newClient(url, tokenEnvVar)
	if err != nil {
		return nil, err
	}

	// 1. Resolve the ref to a commit hash.
	r, err := client.GetRef(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("nanogit: GetRef %q on %q: %w", ref, url, err)
	}
	return fetchYAMLTree(ctx, client, url, r.Hash, subpath)
}

// FetchYAMLAtCommit fetches only the tree and blobs identified by commitSHA.
// Unlike FetchYAML it never resolves a mutable ref. Backend snapshots require
// this additive capability so their files and receipt refer to the same commit.
func (c *nanogitClient) FetchYAMLAtCommit(ctx context.Context, url, commitSHA, subpath, tokenEnvVar string) (map[string][]byte, error) {
	commitHash, err := exactCommitHash(commitSHA)
	if err != nil {
		return nil, err
	}
	client, err := c.newClient(url, tokenEnvVar)
	if err != nil {
		return nil, err
	}
	return fetchYAMLTree(ctx, client, url, commitHash, subpath)
}

func exactCommitHash(sha string) (hash.Hash, error) {
	h, err := hash.FromHex(sha)
	if err != nil || h == hash.Zero || sha != h.String() {
		return hash.Zero, fmt.Errorf("bpsource: immutable fetch requires a nonzero canonical 40-character commit SHA")
	}
	return h, nil
}

func fetchYAMLTree(ctx context.Context, client nanogit.Client, url string, commitHash hash.Hash, subpath string) (map[string][]byte, error) {
	// Fetch the complete flat tree for this exact commit.
	flatTree, err := client.GetFlatTree(ctx, commitHash)
	if err != nil {
		return nil, fmt.Errorf("nanogit: GetFlatTree for commit %s on %q: %w", commitHash.String(), url, err)
	}

	// Normalise subpath: trim leading/trailing slashes for clean prefix matching.
	prefix := strings.Trim(subpath, "/")

	// 3. Filter blob entries under subpath whose path ends with .yaml.
	result := make(map[string][]byte)
	for _, entry := range flatTree.Entries {
		if entry.Type != protocol.ObjectTypeBlob {
			continue
		}
		if !strings.HasSuffix(entry.Path, ".yaml") {
			continue
		}
		if !isUnderSubpath(entry.Path, prefix) {
			continue
		}

		// 4. Fetch the blob content.
		blob, err := client.GetBlob(ctx, entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("nanogit: GetBlob %s (%s) on %q: %w", entry.Path, entry.Hash.String(), url, err)
		}

		// Key by the flattened subpath-relative path (collision-free across sub-directories).
		result[flattenKey(entry.Path, prefix)] = blob.Content
	}

	return result, nil
}

// flattenKey derives a flat storage filename for a repo blob path, relative to the subpath prefix,
// with "/" flattened to "-" so distinct files under nested sub-directories (e.g. "a/svc.yaml" and
// "b/svc.yaml") don't collide on base name in the flat git/<id>/ staging dir. A file directly under
// the subpath keeps its plain filename.
func flattenKey(entryPath, prefix string) string {
	rel := entryPath
	if prefix != "" {
		rel = strings.TrimPrefix(rel, prefix+"/")
	}
	return strings.ReplaceAll(rel, "/", "-")
}

// isUnderSubpath reports whether filePath is under the given prefix directory.
// prefix="" means the repository root (all paths qualify).
// The check is exact-directory-boundary: "foo/bar.yaml" is under "foo" but
// not under "fo".
func isUnderSubpath(filePath, prefix string) bool {
	if prefix == "" {
		return true
	}
	// The file must be the prefix itself (edge case: file AT the prefix path,
	// which can't be a blob if prefix is a dir — but guard anyway) or under it.
	return filePath == prefix ||
		strings.HasPrefix(filePath, prefix+"/")
}
