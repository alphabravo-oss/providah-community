package sourcebundle

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, tt := range []struct {
		name, entry string
		mode        fs.FileMode
		content     string
		ok          bool
	}{
		{"main.tf", "main.tf", 0600, "terraform {}", true},
		{"playbook.yml", "playbook.yml", 0600, "- hosts: all", true},
		{"../main.tf", "../main.tf", 0600, "", false},
		{"/main.tf", "main.tf", 0600, "", false},
		{"a\\main.tf", "main.tf", 0600, "", false},
		{"main.tf", "main.tf", fs.ModeSymlink | 0600, "target", false},
		{"main.tf", "missing.tf", 0600, "", false},
		{"main.tfstate", "main.tfstate", 0600, "secret", false},
		{"main.tf", "main.tf", 0600, strings.Repeat("a", MaxExpanded+1), false},
	} {
		t.Run(tt.name+tt.entry+tt.mode.String(), func(t *testing.T) {
			var b bytes.Buffer
			w := zip.NewWriter(&b)
			h := &zip.FileHeader{Name: tt.name, Method: zip.Deflate}
			h.SetMode(tt.mode)
			f, e := w.CreateHeader(h)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Write([]byte(tt.content)); e != nil {
				t.Fatal(e)
			}
			if e = w.Close(); e != nil {
				t.Fatal(e)
			}
			runtime := "opentofu"
			if strings.HasSuffix(tt.entry, ".yml") {
				runtime = "ansible"
			}
			m, e := Validate(b.Bytes(), runtime, tt.entry)
			if (e == nil) != tt.ok {
				t.Fatalf("accepted=%v error=%v", e == nil, e)
			}
			if tt.ok && (len(m.SHA256) != 64 || len(m.Files) != 1) {
				t.Fatal(m)
			}
		})
	}
	for _, names := range [][]string{{"main.tf", "MAIN.tf"}, {"main.tf", ".env"}, {"main.tf", ".terraform/cache"}, {"main.tf", "a", "a/b"}} {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, name := range names {
			f, e := w.Create(name)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = f.Write([]byte("x"))
		}
		_ = w.Close()
		if _, e := Validate(b.Bytes(), "terraform", "main.tf"); e == nil {
			t.Fatal("unsafe archive accepted", names)
		}
	}
}
