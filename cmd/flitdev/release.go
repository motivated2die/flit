package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

// releaseServer stands in for GitHub's release API, to test the in-app
// update locally (docs/BUILDING.md, "Testing updates"). It offers one exe
// as the latest release the way GitHub does: /releases/latest, the exe and
// its SHA256SUMS.txt.
//
//	go run ./cmd/flitdev release-server -exe $HOME\Desktop\flit-0.9.9\flit.exe -version 0.9.9
//	$env:FLIT_UPDATE_API = "http://127.0.0.1:8090"   # then start flit from this shell
func releaseServer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("release-server", flag.ContinueOnError)
	exe := fs.String("exe", "", "flit.exe to offer as the latest release (required)")
	version := fs.String("version", "", "its version, e.g. 0.9.9 (required)")
	addr := fs.String("addr", "127.0.0.1:8090", "listen address")
	status := fs.Int("status", 0, "answer /releases/latest with this HTTP status instead (404: no release, 403: rate limit)")
	badSum := fs.Bool("bad-checksum", false, "publish a wrong SHA-256, to see the update refused")
	slow := fs.Duration("slow", 0, "spread the exe's download over this long, to watch the progress")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *exe == "" || *version == "" {
		fs.Usage()
		return errors.New("-exe and -version are required")
	}
	data, err := os.ReadFile(*exe)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if *badSum {
		hash = fmt.Sprintf("%064x", 0)
	}
	base, tag := "http://"+*addr, "v"+*version

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%s %s (%s)\n", r.Method, r.URL.Path, r.UserAgent())
		if *status != 0 {
			w.WriteHeader(*status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag, "name": "flit " + *version, "html_url": base + "/notes",
			"draft": false, "prerelease": false,
			"assets": []map[string]any{
				{"name": "flit.exe", "browser_download_url": base + "/download/flit.exe", "size": len(data)},
				{"name": "SHA256SUMS.txt", "browser_download_url": base + "/download/SHA256SUMS.txt", "size": 76},
			},
		})
	})
	mux.HandleFunc("/download/flit.exe", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%s %s\n", r.Method, r.URL.Path)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		if *slow <= 0 {
			_, _ = w.Write(data)
			return
		}
		const chunks = 50
		for i := range chunks {
			_, _ = w.Write(data[len(data)*i/chunks : len(data)*(i+1)/chunks])
			w.(http.Flusher).Flush()
			time.Sleep(*slow / chunks)
		}
	})
	mux.HandleFunc("/download/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%s %s\n", r.Method, r.URL.Path)
		fmt.Fprintf(w, "%s  flit.exe\n", hash)
	})
	mux.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Release notes of %s (flitdev release-server)\n", tag)
	})

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	fmt.Printf("offering %s: %s (%d bytes, sha256 %s)\n", tag, *exe, len(data), hash)
	fmt.Printf("start flit from a shell with  $env:FLIT_UPDATE_API = %q\n", base)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
