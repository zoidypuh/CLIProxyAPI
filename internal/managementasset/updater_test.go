package managementasset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureNoKeyManagementHTMLPatchesMinifiedPasswordCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManagementFileName)
	html := `<script>function login(){if(!b.trim()){X(t("login.error_required"));return}connect()}</script>` +
		`management_key_placeholder:"Enter the management key"`
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	EnsureNoKeyManagementHTML(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	patched := string(data)
	if strings.Contains(patched, `login.error_required`) {
		t.Fatalf("password-required validation still present: %s", patched)
	}
	if !strings.Contains(patched, `management_key_placeholder:"No password required"`) {
		t.Fatalf("password placeholder was not patched: %s", patched)
	}
}
