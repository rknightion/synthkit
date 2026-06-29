// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"fmt"
	"strings"

	nanogit "github.com/grafana/nanogit"
	"github.com/grafana/nanogit/options"
	"github.com/grafana/nanogit/protocol"
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
}

// NewNanogitClient returns a GitClient backed by nanogit.
// tokenLookup maps an env-var name → its value (production: os.Getenv;
// tests: a stub map lookup). Empty envVarName OR empty looked-up value
// means no authentication (public repo).
func NewNanogitClient(tokenLookup func(name string) string) GitClient {
	return &nanogitClient{tokenLookup: tokenLookup}
}

// newClient constructs a nanogit.Client for the given URL, optionally
// attaching a token when tokenEnvVar names a non-empty secret.
// For GitHub and Grafana-hosted repos, token auth uses the token string
// directly (no "Bearer" prefix); the caller is responsible for passing
// the correct format if needed (most Git forges accept a PAT directly).
func (c *nanogitClient) newClient(url, tokenEnvVar string) (nanogit.Client, error) {
	var opts []options.Option
	if tokenEnvVar != "" && c.tokenLookup != nil {
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
	commitHash := r.Hash

	// 2. Fetch the complete flat tree for this commit.
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
