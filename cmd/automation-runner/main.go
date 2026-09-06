// This binary is only launched inside the restricted validation container.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"github.com/alphabravo-oss/providah-community/internal/egress"
	"github.com/alphabravo-oss/providah-community/internal/inputspec"
	"github.com/alphabravo-oss/providah-community/internal/sourcebundle"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func validate(ctx context.Context, r automation.Request) error {
	if e := r.Validate(); e != nil {
		return e
	}
	dir, e := os.MkdirTemp("/work", "source-")
	if e != nil {
		return e
	}
	defer func() { _ = os.RemoveAll(dir) }()
	archive, e := zip.NewReader(bytes.NewReader(r.Archive), int64(len(r.Archive)))
	if e != nil {
		return e
	}
	for _, f := range archive.File {
		target := filepath.Join(dir, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(target, 0700); e != nil {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		input, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			_ = input.Close()
			return e
		}
		_, e = io.Copy(out, input)
		_ = input.Close()
		closeErr := out.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if r.DependencyProxy {
		relay, e := egress.Relay(ctx, "/run/egress/proxy.sock")
		if e != nil {
			return e
		}
		defer func() { _ = relay.Close() }()
	}
	work := filepath.Join(dir, filepath.Dir(r.Entrypoint))
	inputsPath := ""
	if r.Inputs != nil {
		manifest, e := sourcebundle.Validate(r.Archive, r.Runtime, r.Entrypoint)
		if e != nil {
			return e
		}
		values, e := inputspec.Values(manifest.Inputs, r.Inputs)
		if e != nil {
			return e
		}
		file, e := os.CreateTemp("/tmp", "providah-inputs-*.json")
		if e != nil {
			return e
		}
		inputsPath = file.Name()
		defer func() { _ = os.Remove(inputsPath) }()
		_, e = file.Write(values)
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}

	run := func(binary string, args ...string) error {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = work
		cmd.WaitDelay = time.Second
		cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp", "TF_IN_AUTOMATION=1", "TF_INPUT=0", "TF_CLI_CONFIG_FILE=/etc/providah/terraform.rc", "ANSIBLE_LOCAL_TEMP=/tmp/ansible", "ANSIBLE_NOCOLOR=1"}
		if r.DependencyProxy {
			cmd.Env = append(cmd.Env, "HTTPS_PROXY=http://127.0.0.1:8080", "https_proxy=http://127.0.0.1:8080")
		}
		// Tool diagnostics can contain source secrets. Only an advisory status leaves the worker.
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run()
	}
	if r.Runtime == "ansible" {
		args := []string{"--syntax-check", "--inventory", "localhost,"}
		if inputsPath != "" {
			args = append(args, "--extra-vars", "@"+inputsPath)
		}
		return run("/usr/local/bin/ansible-playbook", append(args, "./"+filepath.Base(r.Entrypoint))...)
	}
	binary := "/usr/local/bin/terraform"
	if r.Runtime == "opentofu" {
		binary = "/usr/local/bin/tofu"
	}
	if e = run(binary, "init", "-backend=false", "-input=false", "-lockfile=readonly", "-no-color"); e != nil {
		return e
	}
	return run(binary, "validate", "-no-color")
}
func main() {
	result := automation.Result{Status: "failed"}
	var r automation.Request
	d := json.NewDecoder(io.LimitReader(os.Stdin, 6<<20))
	d.DisallowUnknownFields()
	e := d.Decode(&r)
	if e == nil && d.Decode(new(any)) != io.EOF {
		e = errors.New("invalid input")
	}
	if e == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		e = validate(ctx, r)
	}
	if e == nil {
		result.Status = "succeeded"
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
}
