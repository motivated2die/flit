package main

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/motivated2die/flit/internal/download"
	"github.com/motivated2die/flit/internal/llm"
	"github.com/motivated2die/flit/internal/models"
)

// runtimeDir is where `flitdev llm` unpacks the pinned llama-server.
func runtimeDir(root string) string {
	return filepath.Join(root, ".cache", "llama-"+llm.RuntimeVersion)
}

// fetchLLM downloads the pinned llama.cpp build and the given language
// models (default: every candidate but the largest).
func fetchLLM(ctx context.Context, root string, ids []string) error {
	fmt.Printf("runtime: llama.cpp %s (Vulkan + CPU)\n", llm.RuntimeVersion)
	zipPath := filepath.Join(root, ".cache", path.Base(llm.RuntimeURL))
	if err := get(ctx, download.File{URL: llm.RuntimeURL, Dest: zipPath, SHA256: llm.RuntimeSHA256, Size: llm.RuntimeSize}); err != nil {
		return err
	}
	n, err := llm.ExtractRuntime(zipPath, runtimeDir(root))
	if err != nil {
		return err
	}
	fmt.Printf("  extracted %d files to %s\n", n, runtimeDir(root))
	if len(ids) == 0 {
		ids = []string{"qwen3.5-2b", "granite-4.0-h-micro", "qwen3.5-4b", "gemma-4-e2b"}
	}
	dir := filepath.Join(root, "models")
	for _, id := range ids {
		m, ok := models.ByID(id)
		if !ok || m.Role != models.RoleLLM {
			return fmt.Errorf("unknown language model %q", id)
		}
		fmt.Printf("model %s (%s, %s)\n", m.ID, mb(m.Size), m.License)
		if err := get(ctx, download.File{URL: m.URL(), Dest: m.Path(dir), SHA256: m.SHA256, Size: m.Size}); err != nil {
			return err
		}
	}
	return nil
}
