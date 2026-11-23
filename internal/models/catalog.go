// Package models lists the speech and language models flit can use, pinned to exact
// files (Hugging Face repository, revision, SHA-256), and where they live
// on disk. The entries come from the transcribe.cpp model catalog (as
// bundled with Handy); see docs/research/models.md for why these.
package models

import (
	"fmt"
	"path/filepath"
)

// Role is what a model does.
type Role string

const (
	RoleASR     Role = "asr"
	RoleVAD     Role = "vad"
	RoleDiarize Role = "diarize"
	RoleLLM     Role = "llm" // writes summaries
)

// Model is one downloadable model file.
type Model struct {
	ID         string
	Name       string
	Role       Role
	Languages  []string // ISO 639-1; empty for language-independent roles
	Repo       string   // Hugging Face repository
	Revision   string   // pinned commit
	File       string
	Size       int64
	SHA256     string
	License    string
	Timestamps bool // word timestamps
	// TestOnly models are small stand-ins for tests, never offered in the app.
	TestOnly bool
}

// URL is the model file's pinned download URL.
func (m Model) URL() string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s", m.Repo, m.Revision, m.File)
}

// Path is where the model lives under the models directory.
func (m Model) Path(modelsDir string) string {
	return filepath.Join(modelsDir, m.ID, m.File)
}

// Supports reports whether the model transcribes lang.
func (m Model) Supports(lang string) bool {
	for _, l := range m.Languages {
		if l == lang {
			return true
		}
	}
	return false
}

// Catalog lists every model flit knows, in order of preference per role.
var Catalog = []Model{
	{
		ID: "parakeet-ultra", Name: "Parakeet Ultra", Role: RoleASR,
		Languages: european25,
		Repo:      "handy-computer/parakeet-ultra-gguf", Revision: "39eeb55181f0d354fd934f06e92fd8d5037fed8e",
		File: "parakeet-ultra-Q8_0.gguf", Size: 740363168,
		SHA256:  "007a59761e9258779f189df396b50d12b83bb5b79e66df4b955c230d2f2a0a59",
		License: "CC-BY-4.0", Timestamps: true,
	},
	{
		ID: "parakeet-tdt-0.6b-v3", Name: "Parakeet TDT 0.6B v3", Role: RoleASR,
		Languages: european25,
		Repo:      "handy-computer/parakeet-tdt-0.6b-v3-gguf", Revision: "85ac09ea12fc4b1112fa76810059364bc6adc9de",
		File: "parakeet-tdt-0.6b-v3-Q8_0.gguf", Size: 739508576,
		SHA256:  "5859f77944efcd8eafa23a6350731960b2b55b2203df51f319665c807d802cc7",
		License: "CC-BY-4.0", Timestamps: true,
	},
	{
		ID: "silero-vad-v6.2", Name: "Silero VAD v6.2", Role: RoleVAD,
		Repo: "handy-computer/silero-vad-v6.2-gguf", Revision: "97bbd75179f535e4708bccdf61e9278b8b60dfc7",
		File: "silero-vad-v6.2-F32.gguf", Size: 1241056,
		SHA256:  "0d871a800c420fdd5d1b17a3aea4296d1c39608f0ff4a9295eb76457eb1c88e6",
		License: "MIT",
	},
	{
		ID: "sortformer-4spk-v2.1", Name: "Streaming Sortformer 4spk v2.1", Role: RoleDiarize,
		Repo: "handy-computer/diar_streaming_sortformer_4spk-v2.1-gguf", Revision: "ae4afbb5c3d33b71cf2dbf600022b655ee706dd0",
		File: "diar_streaming_sortformer_4spk-v2.1-Q8_0.gguf", Size: 139310336,
		SHA256:  "a5dacdc650790266c7a362e54e6bf51952015487edaa606c4e11632bc32442a9",
		License: "NVIDIA Open Model License",
	},

	// Language models for summaries, lightest first; candidates under
	// measurement (docs/research/local-summaries.md#models). Text only: the
	// vision projectors these repositories also hold are not needed.
	{
		ID: "qwen3.5-2b", Name: "Qwen3.5 2B", Role: RoleLLM,
		Repo: "unsloth/Qwen3.5-2B-GGUF", Revision: "f6d5376be1edb4d416d56da11e5397a961aca8ae",
		File: "Qwen3.5-2B-Q4_K_M.gguf", Size: 1280835840,
		SHA256:  "aaf42c8b7c3cab2bf3d69c355048d4a0ee9973d48f16c731c0520ee914699223",
		License: "Apache-2.0",
	},
	{
		ID: "granite-4.0-h-micro", Name: "Granite 4.0 H Micro", Role: RoleLLM,
		Repo: "ibm-granite/granite-4.0-h-micro-GGUF", Revision: "4a3531463f3d8d2294ee6f629e2f691f15f8f5c3",
		File: "granite-4.0-h-micro-Q4_K_M.gguf", Size: 1942564512,
		SHA256:  "bcc78b9b25450101d1ad90d4b9a264e1bac892f534dfb76066f4eec792fdf023",
		License: "Apache-2.0",
	},
	{
		ID: "qwen3.5-4b", Name: "Qwen3.5 4B", Role: RoleLLM,
		Repo: "unsloth/Qwen3.5-4B-GGUF", Revision: "e87f176479d0855a907a41277aca2f8ee7a09523",
		File: "Qwen3.5-4B-Q4_K_M.gguf", Size: 2740937888,
		SHA256:  "00fe7986ff5f6b463e62455821146049db6f9313603938a70800d1fb69ef11a4",
		License: "Apache-2.0",
	},
	{
		ID: "gemma-4-e2b", Name: "Gemma 4 E2B", Role: RoleLLM,
		Repo: "ggml-org/gemma-4-E2B-it-GGUF", Revision: "2a8538c2e2d4687953a86c422c7c623cfd022880",
		File: "gemma-4-E2B-it-Q4_0.gguf", Size: 2841481184,
		SHA256:  "8e30dff3ac4c8434c49a7036fa15564bdbb6044e42bf04550bf1a096ad7e6a52",
		License: "Apache-2.0",
	},
	{
		ID: "gemma-4-e4b", Name: "Gemma 4 E4B", Role: RoleLLM,
		Repo: "google/gemma-4-E4B-it-qat-q4_0-gguf", Revision: "4b4a2c1d584be7264f87aac328a1bc739ce81b6c",
		File: "gemma-4-E4B_q4_0-it.gguf", Size: 5154941280,
		SHA256:  "676c35070db6dbe52f93e9c864ee0fba4eddea94b9c875d9cb10daff453fbaee",
		License: "Apache-2.0",
	},

	// Small models for tests only.
	{
		ID: "whisper-tiny", Name: "Whisper tiny", Role: RoleASR,
		Languages: []string{"en", "de"},
		Repo:      "handy-computer/whisper-tiny-gguf", Revision: "6687f30c99641ee265df421e582354adbc8848fc",
		File: "whisper-tiny-Q8_0.gguf", Size: 45981088,
		SHA256:  "325b9c7997cd1eff81ef709d55766565e71be696130cc3a3d444713798706834",
		License: "MIT", Timestamps: true, TestOnly: true,
	},
	{
		ID: "parakeet-tdt_ctc-110m", Name: "Parakeet TDT-CTC 110M (English)", Role: RoleASR,
		Languages: []string{"en"},
		Repo:      "handy-computer/parakeet-tdt_ctc-110m-gguf", Revision: "9d66d34f9e1594075c5dd72c90c0f4c321b29f21",
		File: "parakeet-tdt_ctc-110m-Q8_0.gguf", Size: 135373280,
		SHA256:  "7dd44c74a331d788a4e5f8b16913b3feb29ced22cf5613aad0e0f6cd30516296",
		License: "CC-BY-4.0", Timestamps: true, TestOnly: true,
	},
}

var european25 = []string{"bg", "cs", "da", "de", "el", "en", "es", "et", "fi", "fr", "hr", "hu", "it", "lt", "lv", "mt", "nl", "pl", "pt", "ro", "ru", "sk", "sl", "sv", "uk"}

// ByID returns the model with that ID.
func ByID(id string) (Model, bool) {
	for _, m := range Catalog {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Default returns the preferred app model for a role and, for ASR, a
// language.
func Default(role Role, lang string) (Model, bool) {
	for _, m := range Catalog {
		if m.TestOnly || m.Role != role {
			continue
		}
		if role == RoleASR && !m.Supports(lang) {
			continue
		}
		return m, true
	}
	return Model{}, false
}
