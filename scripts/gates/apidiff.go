package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// api-diff refetches the discovery documents and rewrites the snapshot.
//
// Manual, and deliberately outside `make check`. A gate that reaches the
// network fails when Google is slow, and a gate that fails for reasons
// nobody caused is one people learn to re-run until it passes. What CI
// holds is the snapshot this writes (`gates api-coverage`), which is the
// difference between a completeness claim that is checked on every push
// and one that is merely checkable by somebody who remembers.
//
// It reports NEW, GONE and CHANGED. Changed matters as much as the other
// two and is the one a list of names cannot show: a method that keeps
// its name and moves to a different path is a break, and the only way to
// see it is to have recorded the path.

// discoveryDocs are the APIs this server can reach.
var discoveryDocs = []apiDoc{
	{API: "sheets", Version: "v4", URL: "https://sheets.googleapis.com/$discovery/rest?version=v4"},
	{API: "drive", Version: "v3", URL: "https://www.googleapis.com/discovery/v1/apis/drive/v3/rest"},
}

// apiDiff fetches, compares and rewrites.
func apiDiff(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fresh := apiSurface{
		Fetched: time.Now().UTC().Format("2006-01-02"),
		Note: "Written by `gates api-diff`; nobody edits this. One verdict per item lives in " +
			apiCoveragePath + ". A `method` is an API method; a `request` is a member of the Sheets " +
			"batchUpdate union, which is where this API keeps most of its capability.",
	}
	for _, doc := range discoveryDocs {
		items, revision, err := fetchDoc(ctx, doc)
		if err != nil {
			return err
		}
		doc.Revision = revision
		fresh.APIs = append(fresh.APIs, doc)
		fresh.Items = append(fresh.Items, items...)
	}
	sort.Slice(fresh.Items, func(i, j int) bool {
		a, b := fresh.Items[i], fresh.Items[j]
		if a.API != b.API {
			return a.API < b.API
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})

	old, err := readSurface()
	if err != nil {
		// The first run has nothing to compare against, which is not a
		// failure — it is how the file comes into being.
		_, _ = fmt.Fprintf(out, "no snapshot yet; writing the first one\n")
		old = &apiSurface{}
	}
	reportDiff(out, old.Items, fresh.Items)

	body, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(apiSurfacePath, append(body, '\n'), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "wrote %s: %d item(s) across %d API(s)\n", apiSurfacePath, len(fresh.Items), len(fresh.APIs))
	_, _ = fmt.Fprintf(out, "now run `gates api-coverage`; a new item with no verdict fails it, which is the point\n")
	return nil
}

// reportDiff says what moved, in the three ways it can.
func reportDiff(out io.Writer, before, after []apiItem) {
	was := make(map[string]apiItem, len(before))
	for _, it := range before {
		was[it.key()] = it
	}
	now := make(map[string]apiItem, len(after))
	for _, it := range after {
		now[it.key()] = it
	}
	var news, gone, changed []string
	for _, it := range after {
		old, existed := was[it.key()]
		switch {
		case !existed:
			news = append(news, fmt.Sprintf("%s %s.%s %s", it.Kind, it.API, it.Name, where(it)))
		case old.Verb != it.Verb || old.Path != it.Path:
			changed = append(changed, fmt.Sprintf("%s %s.%s: %s -> %s",
				it.Kind, it.API, it.Name, where(old), where(it)))
		}
	}
	for _, it := range before {
		if _, still := now[it.key()]; !still {
			gone = append(gone, fmt.Sprintf("%s %s.%s %s", it.Kind, it.API, it.Name, where(it)))
		}
	}
	for _, group := range []struct {
		label string
		lines []string
	}{{"NEW", news}, {"GONE", gone}, {"CHANGED", changed}} {
		sort.Strings(group.lines)
		for _, line := range group.lines {
			_, _ = fmt.Fprintf(out, "%-8s %s\n", group.label, line)
		}
	}
	if len(news)+len(gone)+len(changed) == 0 && len(before) > 0 {
		_, _ = fmt.Fprintf(out, "no change against the last snapshot\n")
	}
}

func where(it apiItem) string {
	if it.Verb == "" && it.Path == "" {
		return ""
	}
	return it.Verb + " " + it.Path
}

// fetchDoc reads one discovery document into items.
func fetchDoc(ctx context.Context, doc apiDoc) ([]apiItem, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, doc.URL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetching the %s discovery document: %w", doc.API, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetching the %s discovery document: HTTP %d", doc.API, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", err
	}

	var parsed struct {
		Revision  string                     `json:"revision"`
		Resources map[string]json.RawMessage `json:"resources"`
		Schemas   map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", fmt.Errorf("the %s discovery document is not JSON: %w", doc.API, err)
	}

	var items []apiItem
	for name, raw := range parsed.Resources {
		items = append(items, methodsIn(doc.API, name, raw)...)
	}
	// The Sheets batchUpdate union, which is where this API keeps most
	// of what it can do: thirteen methods against sixty-nine request
	// kinds, and a record keyed only on methods would miss all of them.
	if doc.API == "sheets" {
		for name := range parsed.Schemas["Request"].Properties {
			items = append(items, apiItem{API: doc.API, Kind: kindRequest, Name: name})
		}
	}
	if len(items) == 0 {
		return nil, "", fmt.Errorf("the %s discovery document yielded no methods, so this read nothing", doc.API)
	}
	return items, parsed.Revision, nil
}

// methodsIn walks a resource and the resources nested in it.
//
// The short name rather than the document's full id: `spreadsheets.get`
// is what this server's own client calls it, and a record that named it
// `sheets.spreadsheets.get` would need a translation table between the
// two halves of this gate — which is a third place to be wrong.
func methodsIn(api, resource string, raw json.RawMessage) []apiItem {
	var node struct {
		Methods map[string]struct {
			HTTPMethod string `json:"httpMethod"`
			Path       string `json:"path"`
		} `json:"methods"`
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil
	}
	var items []apiItem
	for name, m := range node.Methods {
		items = append(items, apiItem{
			API: api, Kind: kindMethod, Name: shortName(api, resource, name),
			Verb: m.HTTPMethod, Path: m.Path,
		})
	}
	for nested, sub := range node.Resources {
		items = append(items, methodsIn(api, resource+"."+nested, sub)...)
	}
	return items
}

// shortName is the name this server's client uses for a method.
func shortName(api, resource, method string) string {
	if api == "drive" {
		return "drive." + resource + "." + method
	}
	// Sheets: the client drops the leading `spreadsheets.` on everything
	// nested inside it, and keeps it on the spreadsheet's own methods.
	trimmed := strings.TrimPrefix(resource, "spreadsheets")
	trimmed = strings.TrimPrefix(trimmed, ".")
	if trimmed == "" {
		return "spreadsheets." + method
	}
	return trimmed + "." + method
}
