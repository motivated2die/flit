// Package config is flit's config.json in its data folder.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config holds the user's settings. Fields absent from an older file keep
// their defaults (Load starts from Default).
type Config struct {
	// Language is the meeting language ahead of transcription ("de", "en").
	Language string `json:"language"`
	// Model is the ASR model's ID (internal/models); "" picks the default
	// for the language.
	Model string `json:"model,omitempty"`
	// Vocabulary are custom terms (names, products, jargon) the model is
	// nudged towards.
	Vocabulary []string `json:"vocabulary,omitempty"`

	// Mic is the chosen microphone; empty ID = automatic: the microphone
	// the meeting app uses, else Windows' default. The name finds a device
	// again whose ID changed.
	MicID   string `json:"micId,omitempty"`
	MicName string `json:"micName,omitempty"`

	// Output is the device whose playback is recorded when the meeting
	// app itself can't be (manual recordings, fallback); empty ID =
	// Windows' default output.
	OutputID   string `json:"outputId,omitempty"`
	OutputName string `json:"outputName,omitempty"`

	// Providers maps a meeting app's ID (internal/detect) to whether it
	// triggers the meeting prompt. Missing entries use the app's default.
	Providers map[string]bool `json:"providers,omitempty"`

	// KeepAudio keeps both tracks of a session after transcription.
	KeepAudio bool `json:"keepAudio"`
	// Speakers tells the people on the meeting's track apart (diarization).
	Speakers bool `json:"speakers"`
	// AutoRecord records a detected meeting after a short countdown that
	// can be cancelled, instead of asking.
	AutoRecord bool `json:"autoRecord"`
	// CalendarURL is a calendar subscription link (ICS). It opens the
	// calendar, so it is never logged.
	CalendarURL string `json:"calendarUrl,omitempty"`
	// LiveTranscribe transcribes during meetings, capped at LiveLoad
	// ("light", "balanced", "fast": 15, 25, 40% of the processor).
	LiveTranscribe bool   `json:"liveTranscribe"`
	LiveLoad       string `json:"liveLoad,omitempty"`
	// LimitFinal caps the transcription after a meeting at FinalLoad
	// ("light", "balanced", "fast": 25, 50, 75% of the processor).
	LimitFinal bool   `json:"limitFinal"`
	FinalLoad  string `json:"finalLoad,omitempty"`

	// NoUpdateCheck stops the twice-daily question to GitHub whether a
	// newer release exists (internal/update); "Check now" still works.
	NoUpdateCheck bool `json:"noUpdateCheck,omitempty"`

	SetupComplete bool `json:"setupComplete,omitempty"`
}

// Default returns the settings of a new installation.
func Default() Config {
	return Config{Language: "de", KeepAudio: true, Speakers: true, AutoRecord: true,
		LiveTranscribe: true, LiveLoad: "balanced", LimitFinal: true, FinalLoad: "balanced"}
}

// Dir is flit's data folder: FLIT_DATA_DIR, else %LOCALAPPDATA%\flit.
func Dir() string {
	if d := os.Getenv("FLIT_DATA_DIR"); d != "" {
		return d
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "flit")
}

// Path is config.json in dir.
func Path(dir string) string { return filepath.Join(dir, "config.json") }

// Load reads dir's config.json. A missing file gives the defaults. A file
// that can't be parsed is kept as config.broken-<time>.json (so nothing is
// silently lost when the defaults are saved over it) and the defaults are
// returned with an error saying so.
func Load(dir string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		backup := filepath.Join(dir, "config.broken-"+time.Now().Format("20060102-150405")+".json")
		_ = os.Rename(Path(dir), backup)
		return Default(), fmt.Errorf("config.json unreadable (%v), kept as %s", err, filepath.Base(backup))
	}
	if cfg.Language == "" {
		cfg.Language = Default().Language
	}
	return cfg, nil
}

// Save writes cfg atomically (temporary file, then rename).
func Save(dir string, cfg Config) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dir))
}

// ProviderEnabled reports whether a meeting app triggers prompts.
func (c Config) ProviderEnabled(id string, def bool) bool {
	if v, ok := c.Providers[id]; ok {
		return v
	}
	return def
}
