package launcher

import (
	"errors"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"os"
	"time"
)

func ReadRuntimeTrust(keysPath, releasesPath string) (*provider.RuntimeTrust, error) {
	if keysPath == "" && releasesPath == "" {
		return nil, nil
	}
	if keysPath == "" || releasesPath == "" {
		return nil, errors.New("configure both runtime trust roots and signed releases")
	}
	read := func(path string) ([]byte, error) {
		f, e := os.Open(path)
		if e != nil {
			return nil, errors.New("runtime trust file unavailable")
		}
		defer func() { _ = f.Close() }()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			return nil, errors.New("runtime trust requires regular files")
		}
		return io.ReadAll(io.LimitReader(f, 65537))
	}
	keys, e := read(keysPath)
	if e != nil {
		return nil, e
	}
	releases, e := read(releasesPath)
	if e != nil {
		return nil, e
	}
	return provider.ParseRuntimeTrust(keys, releases, time.Now())
}
