// Package llm runs a local language model for summaries: llama.cpp's
// llama-server as a child process on the loopback interface, and a client
// for its OpenAI-compatible API. The tray process never loads a model;
// the server starts when a summary is asked for and is stopped after it.
// Design: docs/architecture/summaries.md, decision: ADR 0011.
package llm

import (
	"path"
	"strings"
)

// The pinned llama.cpp build: its Windows x64 Vulkan release, which holds
// the CPU backends too (it picks AVX2/AVX-512 variants at start), so one
// download serves CPU-only PCs and every Vulkan GPU, integrated or not.
const (
	RuntimeVersion = "b12633"
	RuntimeURL     = "https://github.com/ggml-org/llama.cpp/releases/download/b12633/llama-b12633-bin-win-vulkan-x64.zip"
	RuntimeSHA256  = "720d59998a51423a1c50c99326a5d5358b80e89d75f8fdafc50a26479d1bad68"
	RuntimeSize    = 38877434
	ServerExe      = "llama-server.exe"
)

// RuntimeKept reports whether a file of the release archive is needed: the
// server and the libraries next to it, not the other tools.
func RuntimeKept(name string) bool {
	base := strings.ToLower(path.Base(strings.ReplaceAll(name, `\`, "/")))
	if strings.HasSuffix(base, "-impl.dll") { // one per tool
		return base == "llama-server-impl.dll"
	}
	return base == ServerExe || strings.HasSuffix(base, ".dll") || strings.HasPrefix(base, "license")
}
