package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"github.com/alphabravo-oss/providah-community/internal/sourcebundle"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
)

func TestAutomationBoundary(t *testing.T) {
	l, e := newTestLauncher("sha256:" + string(bytes.Repeat([]byte("a"), 64)))
	if e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{`{}`, `{"runtime":"opentofu","image":"evil:latest"}`, `{"credential":"secret"}`} {
		w := httptest.NewRecorder()
		l.ServeHTTP(w, httptest.NewRequest("POST", "/automation/validate", bytes.NewBufferString(body)))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
func TestAutomationContainer(t *testing.T) {
	image := os.Getenv("TEST_AUTOMATION_IMAGE")
	if image == "" {
		t.Skip("set TEST_AUTOMATION_IMAGE to a built offline runtime image")
	}
	runtime := os.Getenv("TEST_AUTOMATION_RUNTIME")
	if runtime == "" {
		runtime = "opentofu"
	}
	l, e := newTestLauncher("sha256:" + string(bytes.Repeat([]byte("a"), 64)))
	if e != nil {
		t.Fatal(e)
	}
	id, e := ResolveImage(context.Background(), image)
	if e != nil {
		t.Fatal(e)
	}
	allowed := automation.Runtime{Runtime: runtime, Image: id, Version: "test"}
	if os.Getenv("TEST_DEPENDENCY_BUNDLE") != "" {
		allowed.DependencyHosts = []string{"registry.opentofu.org", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}
	}
	slices.Sort(allowed.DependencyHosts)
	policy, _ := json.Marshal([]automation.Runtime{allowed})
	if e = l.ConfigureAutomation(context.Background(), string(policy), os.Getenv("TEST_PROXY_IMAGE")); e != nil {
		t.Fatal(e)
	}
	entry, good, bad := "main.tf", "terraform {}\noutput \"ok\" { value = 1 }", "invalid {{{"
	if runtime == "ansible" {
		entry = "play.yml"
		good = "- hosts: all\n  tasks: []\n"
		bad = "- hosts: ["
	}
	for _, tc := range []struct{ content, status string }{{good, "succeeded"}, {bad, "failed"}} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, _ := z.Create(entry)
		_, _ = f.Write([]byte(tc.content))
		_ = z.Close()
		m, e := sourcebundle.Validate(b.Bytes(), runtime, entry)
		if e != nil {
			t.Fatal(e)
		}
		request := automation.Request{RuntimePolicy: allowed.Policy(), Runtime: runtime, Image: id, Entrypoint: entry, SHA256: m.SHA256, Archive: b.Bytes()}
		raw, _ := json.Marshal(request)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, httptest.NewRequest("POST", "/automation/validate", bytes.NewReader(raw)))
		var result automation.Result
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != tc.status {
			t.Fatal("container validation failed", w.Code, w.Body.String())
		}
	}

	if runtime == "ansible" {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for name, contents := range map[string]string{"play.yml": "- hosts: all\n  tasks:\n    - import_tasks: '{{ task_file }}'\n", "tasks.yml": "[]\n", "providah.inputs.json": `{"version":1,"inputs":[{"name":"task_file","label":"Task file","type":"string","choices":["tasks.yml"]}]}`} {
			f, _ := z.Create(name)
			_, _ = f.Write([]byte(contents))
		}
		_ = z.Close()
		m, e := sourcebundle.Validate(b.Bytes(), runtime, "play.yml")
		if e != nil {
			t.Fatal(e)
		}
		r := automation.Request{Inputs: json.RawMessage(`{"task_file":"tasks.yml"}`), RuntimePolicy: allowed.Policy(), Runtime: runtime, Image: id, Entrypoint: "play.yml", SHA256: m.SHA256, Archive: b.Bytes()}
		for _, valid := range []bool{true, false} {
			if !valid {
				r.Inputs = json.RawMessage(`{"task_file":"../outside"}`)
			}
			raw, _ := json.Marshal(r)
			w := httptest.NewRecorder()
			l.ServeHTTP(w, httptest.NewRequest("POST", "/automation/validate", bytes.NewReader(raw)))
			if valid {
				var result automation.Result
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "succeeded" {
					t.Fatal("Ansible did not consume constrained input file", w.Code, w.Body.String())
				}
			} else if w.Code != 400 {
				t.Fatal("input constraint bypass at launcher")
			}
		}
	}

	if file := os.Getenv("TEST_DEPENDENCY_BUNDLE"); file != "" {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		m, e := sourcebundle.Validate(raw, runtime, "main.tf")
		if e != nil {
			t.Fatal(e)
		}
		request := automation.Request{Runtime: runtime, Image: id, RuntimePolicy: allowed.Policy(), Entrypoint: "main.tf", SHA256: m.SHA256, Archive: raw}
		payload, _ := json.Marshal(request)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, httptest.NewRequest("POST", "/automation/validate", bytes.NewReader(payload)))
		var result automation.Result
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "succeeded" {
			t.Fatal("locked dependency download failed", w.Code, w.Body.String())
		}
		request.RuntimePolicy = "stale"
		payload, _ = json.Marshal(request)
		w = httptest.NewRecorder()
		l.ServeHTTP(w, httptest.NewRequest("POST", "/automation/validate", bytes.NewReader(payload)))
		if w.Code != 400 {
			t.Fatal("stale network policy accepted", w.Code)
		}
	}
}
