// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import "context"

type fakeGit struct {
	head map[string]string            // "url@ref" → sha
	yaml map[string]map[string][]byte // "url@ref" → filename → bytes
	err  error
}

func key(url, ref string) string { return url + "@" + ref }

func (f *fakeGit) HeadSHA(_ context.Context, url, ref, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.head[key(url, ref)], nil
}

func (f *fakeGit) FetchYAML(_ context.Context, url, ref, _, _ string) (map[string][]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.yaml[key(url, ref)], nil
}

type fakeConfig struct{ list []Source }

func (c *fakeConfig) Sources() []Source { return c.list }

func (c *fakeConfig) UpsertSource(s Source) error {
	for i := range c.list {
		if c.list[i].ID == s.ID {
			c.list[i] = s
			return nil
		}
	}
	c.list = append(c.list, s)
	return nil
}

func (c *fakeConfig) RemoveSource(id string) error {
	out := c.list[:0]
	for _, s := range c.list {
		if s.ID != id {
			out = append(out, s)
		}
	}
	c.list = out
	return nil
}
