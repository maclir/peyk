package laptop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTrustAddsTheFolderAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	before := `{"numStartups": 7, "projects": {"/c/old": {"hasTrustDialogAccepted": true, "allowedTools": ["Bash"]}, "/c/new": {"lastCost": 0.5}}}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Trust(path, "/c/new"); err != nil {
		t.Fatal(err)
	}
	if err := Trust(path, "/c/fresh"); err != nil {
		t.Fatal(err)
	}
	var got struct {
		NumStartups int                       `json:"numStartups"`
		Projects    map[string]map[string]any `json:"projects"`
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.NumStartups != 7 || got.Projects["/c/old"]["allowedTools"] == nil || got.Projects["/c/new"]["lastCost"] != 0.5 {
		t.Errorf("other settings changed: %s", raw)
	}
	for _, dir := range []string{"/c/old", "/c/new", "/c/fresh"} {
		if got.Projects[dir]["hasTrustDialogAccepted"] != true {
			t.Errorf("%s is not trusted: %s", dir, raw)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestTrustLeavesATrustedFolderUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	before := `{"projects":{"/c/old":{"hasTrustDialogAccepted":true}}}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Trust(path, "/c/old"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != before {
		t.Errorf("rewrote a trusted folder's config: %s", raw)
	}
}

func TestTrustRefusesAConfigItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Trust(path, "/c/new"); err == nil {
		t.Error("overwrote a config it could not decode")
	}
}

func TestTrustAntigravityAddsFolderAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	before := `{"model": "Gemini 4 Neon (High)", "trustedWorkspaces": ["/c/old"]}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := TrustAntigravity(path, "/c/new"); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model             string   `json:"model"`
		TrustedWorkspaces []string `json:"trustedWorkspaces"`
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "Gemini 4 Neon (High)" {
		t.Errorf("other settings changed: %s", raw)
	}
	if len(got.TrustedWorkspaces) != 2 || got.TrustedWorkspaces[0] != "/c/old" || got.TrustedWorkspaces[1] != "/c/new" {
		t.Errorf("trustedWorkspaces = %v, want [/c/old /c/new]", got.TrustedWorkspaces)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestTrustAntigravityLeavesATrustedFolderUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	before := `{"trustedWorkspaces":["/c/old"]}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := TrustAntigravity(path, "/c/old"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != before {
		t.Errorf("rewrote a trusted folder's settings: %s", raw)
	}
}

func TestTrustAntigravityCreatesMissingFileAndRefusesBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	if err := TrustAntigravity(path, "/c/fresh"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		TrustedWorkspaces []string `json:"trustedWorkspaces"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || len(got.TrustedWorkspaces) != 1 || got.TrustedWorkspaces[0] != "/c/fresh" {
		t.Fatalf("created settings = %s", raw)
	}

	bad := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := TrustAntigravity(bad, "/c/new"); err == nil {
		t.Error("overwrote settings it could not decode")
	}
}
