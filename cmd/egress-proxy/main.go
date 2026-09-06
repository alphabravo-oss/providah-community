package main

import (
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/egress"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		c, e := net.DialTimeout("unix", "/run/egress/proxy.sock", time.Second)
		if e != nil {
			os.Exit(1)
		}
		_ = c.Close()
		return
	}
	var hosts []string
	if json.Unmarshal([]byte(os.Getenv("DEPENDENCY_HOSTS")), &hosts) != nil || len(hosts) == 0 || len(hosts) > 16 {
		os.Exit(1)
	}
	for _, h := range hosts {
		if !egress.ValidHost(h) {
			os.Exit(1)
		}
	}
	listener, e := net.Listen("unix", "/run/egress/proxy.sock")
	if e != nil {
		os.Exit(1)
	}
	defer func() { _ = listener.Close() }()
	if os.Chmod("/run/egress/proxy.sock", 0666) != nil {
		os.Exit(1)
	}
	server := &http.Server{Handler: &egress.Proxy{Hosts: hosts, Slots: make(chan struct{}, 16)}, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	go func() { <-ctx.Done(); _ = server.Close(); os.Exit(0) }()
	if server.Serve(listener) != http.ErrServerClosed {
		os.Exit(1)
	}
}
