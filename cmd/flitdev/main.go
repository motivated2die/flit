// Command flitdev prepares a development checkout: it fetches the pinned
// engine DLLs, the models the tests use, and the public test data.
//
//	go run ./cmd/flitdev engine            # transcribe.cpp DLLs → internal/engine/native/files
//	go run ./cmd/flitdev models [ids…]     # test models (default) or the given ones → models/
//	go run ./cmd/flitdev testdata          # AMI excerpt + synthetic German meetings → testdata/meetings
//	go run ./cmd/flitdev icon              # assets/flit.ico from assets/logo.png
//	go run ./cmd/flitdev release-server -exe flit.exe -version 0.9.9
//	                                       # a fake GitHub release, for testing updates
//
// Everything is checked against pinned SHA-256 hashes and cached in .cache/.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/download"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "engine":
		withVulkan := len(args) > 0 && (args[0] == "-vulkan" || args[0] == "--vulkan")
		err = fetchEngine(ctx, root, withVulkan)
	case "models":
		err = fetchModels(ctx, root, args)
	case "testdata":
		err = fetchTestdata(ctx, root, args)
	case "icon":
		err = makeIcon(root)
	case "release-server":
		err = releaseServer(ctx, args)
	case "llm":
		err = fetchLLM(ctx, root, args)
	case "summary":
		err = summaryCmd(ctx, root, args)
	case "callnames-sim":
		err = callnamesSim(args)
	default:
		usage()
	}
	if err != nil {
		fail(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: flitdev engine | models [ids…] | testdata | icon | release-server -exe <flit.exe> -version <x.y.z> | callnames-sim -meeting <dir> -segments <file>")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "flitdev:", err)
	os.Exit(1)
}

// repoRoot finds the folder with go.mod, from the working directory up.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above the working directory")
		}
		dir = parent
	}
}

var client = download.Client{UserAgent: "flitdev"}

// get downloads one file with a one-line progress display.
func get(ctx context.Context, f download.File) error {
	name := filepath.Base(f.Dest)
	if ok, _ := download.Verify(f); ok {
		fmt.Printf("  %s: already there\n", name)
		return nil
	}
	start := time.Now()
	err := client.Get(ctx, f, func(done, total int64) {
		if total > 0 {
			fmt.Printf("\r  %s: %5.1f%% of %s", name, 100*float64(done)/float64(total), mb(total))
		} else {
			fmt.Printf("\r  %s: %s", name, mb(done))
		}
	})
	fmt.Println()
	if err == nil {
		fmt.Printf("  %s: done in %s\n", name, time.Since(start).Round(time.Second))
	}
	return err
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }

func list(s []string) string { return strings.Join(s, ", ") }
