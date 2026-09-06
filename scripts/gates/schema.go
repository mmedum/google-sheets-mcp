package main

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// schemaDiff compares this build's tool schemas with the last tag's.
//
// One invalid schema is not one broken tool: a client validating against
// draft 2020-12 rejects the whole request, so five bad schemas killed
// every one of forty-four tools in a shipped server's session, reporting
// only an array index that named no server. So the dump runs on every
// build, and the diff says which changes break a caller: a tool or a
// field that disappeared, or a field that became required.
func schemaDiff(bin string) error {
	raw, current, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	if len(current.Tools) == 0 {
		return fmt.Errorf("this build registered no tools; the diff is not looking at a server")
	}
	// The artifact CI keeps is the same bytes the diff was computed
	// from, rather than a second run of the binary.
	if err := os.WriteFile("schemas.json", raw, 0o644); err != nil {
		return err
	}

	tag := lastTag()
	if tag == "" {
		fmt.Printf("%d tools; no previous tag to diff against\n", len(current.Tools))
		return nil
	}
	previous, err := schemasAtTag(tag)
	if err != nil {
		fmt.Printf("%d tools; the schemas at %s could not be built (%v), so nothing was compared\n", len(current.Tools), tag, err)
		return nil
	}

	breaking, added := compare(previous, current)
	fmt.Printf("against %s: added %s; breaking %s\n", tag, list(added), list(breaking))
	if len(breaking) > 0 {
		return fmt.Errorf("%d breaking schema change(s) since %s", len(breaking), tag)
	}
	return nil
}

// schemasAtTag builds the server as the tag left it, in a throwaway
// worktree, and dumps its schemas. Building rather than reading a
// committed file, so the comparison is against what that tag actually
// registered.
func schemasAtTag(tag string) (*schemaDump, error) {
	dir, err := os.MkdirTemp("", "gates-schema-*")
	if err != nil {
		return nil, err
	}
	worktree := filepath.Join(dir, "tree")
	defer func() {
		_, _ = git("worktree", "remove", "--force", worktree)
		_ = os.RemoveAll(dir)
	}()
	if _, err := git("worktree", "add", "--detach", worktree, tag); err != nil {
		return nil, err
	}
	bin := filepath.Join(dir, Binary)
	build := exec.Command("go", "build", "-o", bin, "./cmd/"+Binary)
	build.Dir = worktree
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s: %s", tag, strings.TrimSpace(string(out)))
	}
	_, dump, err := dumpSchemas(bin)
	return dump, err
}

// compare reports what changed and what of it breaks a caller.
func compare(old, current *schemaDump) (breaking, added []string) {
	type tool = struct {
		required   []string
		properties map[string]bool
	}
	index := func(d *schemaDump) map[string]tool {
		m := map[string]tool{}
		for _, t := range d.Tools {
			props := map[string]bool{}
			for name := range t.InputSchema.Properties {
				props[name] = true
			}
			m[t.Name] = tool{required: t.InputSchema.Required, properties: props}
		}
		return m
	}
	o, n := index(old), index(current)

	for _, name := range slices.Sorted(maps.Keys(o)) {
		nt, ok := n[name]
		if !ok {
			breaking = append(breaking, "tool removed: "+name)
			continue
		}
		for _, f := range nt.required {
			if !slices.Contains(o[name].required, f) {
				breaking = append(breaking, fmt.Sprintf("%s: new required field %s", name, f))
			}
		}
		for _, f := range slices.Sorted(maps.Keys(o[name].properties)) {
			if !nt.properties[f] {
				breaking = append(breaking, fmt.Sprintf("%s: field removed %s", name, f))
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(n)) {
		if _, ok := o[name]; !ok {
			added = append(added, name)
		}
	}
	return breaking, added
}

func list(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return "[" + strings.Join(xs, "; ") + "]"
}
