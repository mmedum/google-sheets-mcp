package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestPrintsOutside is the rule watched failing. The load-bearing case
// is a print in an ordinary step function: it is what a contributor
// adds while debugging and then leaves, and it is invisible in review
// because it looks exactly like the ones that are allowed.
func TestPrintsOutside(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "the redacting helper may print",
			src:  "package main\nimport \"fmt\"\nfunc line(s string) { fmt.Println(s) }\n",
			want: 0,
		},
		{
			name: "the section header must go through the helper too",
			src:  "package main\nimport \"fmt\"\nfunc sec(t string) { fmt.Printf(\"== %s\", t) }\n",
			want: 1,
		},
		{
			name: "a step may not",
			src:  "package main\nimport \"fmt\"\nfunc step() { fmt.Println(\"raw\") }\n",
			want: 1,
		},
		{
			name: "Printf and Print count too",
			src:  "package main\nimport \"fmt\"\nfunc step() { fmt.Printf(\"a\"); fmt.Print(\"b\") }\n",
			want: 2,
		},
		{
			name: "Sprintf is not a print",
			src:  "package main\nimport \"fmt\"\nfunc step() string { return fmt.Sprintf(\"a\") }\n",
			want: 0,
		},
		{
			name: "Fprintf to a caller's writer is not a print",
			src:  "package main\nimport (\"fmt\"; \"io\")\nfunc step(w io.Writer) { fmt.Fprintf(w, \"a\") }\n",
			want: 0,
		},
		{
			name: "a nested closure does not hide one",
			src:  "package main\nimport \"fmt\"\nfunc step() { f := func() { fmt.Println(\"x\") }; f() }\n",
			want: 1,
		},
		{
			name: "something else called Println is not fmt's",
			src:  "package main\ntype l struct{}\nfunc (l) Println(string) {}\nvar log l\nfunc step() { log.Println(\"a\") }\n",
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "x.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := printsOutside(fset, file, allowedIn)
			if len(got) != tc.want {
				t.Errorf("found %d prints, want %d: %v", len(got), tc.want, got)
			}
			for _, g := range got {
				if !strings.Contains(g, "fmt.") {
					t.Errorf("finding does not name the call: %q", g)
				}
			}
		})
	}
}
