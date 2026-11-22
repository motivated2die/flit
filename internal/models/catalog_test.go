package models

import (
	"regexp"
	"strings"
	"testing"
)

func TestCatalogEntriesAreComplete(t *testing.T) {
	hexRe := regexp.MustCompile(`^[0-9a-f]{64}$`)
	revRe := regexp.MustCompile(`^[0-9a-f]{40}$`)
	seen := map[string]bool{}
	for _, m := range Catalog {
		if seen[m.ID] {
			t.Errorf("duplicate id %s", m.ID)
		}
		seen[m.ID] = true
		if !hexRe.MatchString(m.SHA256) || !revRe.MatchString(m.Revision) || m.Size <= 0 || m.File == "" || m.License == "" {
			t.Errorf("%s: incomplete pin %+v", m.ID, m)
		}
		if m.Role == RoleASR && len(m.Languages) == 0 {
			t.Errorf("%s: ASR model without languages", m.ID)
		}
		if !strings.HasPrefix(m.URL(), "https://huggingface.co/"+m.Repo+"/resolve/"+m.Revision+"/") {
			t.Errorf("%s: URL %s", m.ID, m.URL())
		}
	}
}

func TestDefaults(t *testing.T) {
	for _, lang := range []string{"de", "en"} {
		m, ok := Default(RoleASR, lang)
		if !ok || m.ID != "parakeet-ultra" {
			t.Errorf("Default(asr, %s) = %s, %v", lang, m.ID, ok)
		}
	}
	if _, ok := Default(RoleASR, "xx"); ok {
		t.Error("a default for an unknown language")
	}
	if m, _ := Default(RoleVAD, ""); m.ID != "silero-vad-v6.2" {
		t.Errorf("VAD default %s", m.ID)
	}
}
