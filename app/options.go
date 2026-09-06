package app

import "github.com/alphabravo-oss/providah-community/sdk/provider"

// Options configures application composition.
type Options struct {
	Edition       string
	RuntimePolicy func(provider.Runtime) provider.Runtime
}
