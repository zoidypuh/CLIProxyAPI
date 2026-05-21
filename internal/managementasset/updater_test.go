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
	if !strings.Contains(patched, `src="/management-local-overrides.js"`) {
		t.Fatalf("local override script was not injected: %s", patched)
	}
}

func TestEnsureNoKeyManagementHTMLPatchesTemplateLiteralPasswordCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManagementFileName)
	html := `<script>async function login(){if(!m.trim()){j(e(` + "`login.error_required`" + `));return}connect()}</script>` +
		`remember_password_label:` + "`Remember password`" +
		`,management_key_label:` + "`Management Key:`" +
		`,management_key_placeholder:` + "`Enter the management key`" +
		`,error_invalid:` + "`Connection failed, please check address and key`"
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
	for _, forbidden := range []string{
		"Remember password",
		"Management Key:",
		"Enter the management key",
		"Connection failed, please check address and key",
	} {
		if strings.Contains(patched, forbidden) {
			t.Fatalf("password wording %q still present: %s", forbidden, patched)
		}
	}
	for _, expected := range []string{
		"remember_password_label:`Remember connection`",
		"management_key_label:`Management Access:`",
		"management_key_placeholder:`No password required`",
		"error_invalid:`Connection failed, please check address`",
	} {
		if !strings.Contains(patched, expected) {
			t.Fatalf("patched fixture missing %q: %s", expected, patched)
		}
	}
}

func TestEnsureNoKeyManagementHTMLInjectsLocalOverridesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManagementFileName)
	html := `<html><body><div id="root"></div></body></html>`
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	EnsureNoKeyManagementHTML(path)
	EnsureNoKeyManagementHTML(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	patched := string(data)
	if count := strings.Count(patched, `src="/management-local-overrides.js"`); count != 1 {
		t.Fatalf("local override script count = %d, want 1: %s", count, patched)
	}
	if !strings.Contains(patched, `<div id="root"></div>    <script defer src="/management-local-overrides.js"></script>
  </body>`) {
		t.Fatalf("local override script was not injected before body close: %s", patched)
	}
}
