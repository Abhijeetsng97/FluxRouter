// Env tests: ports old/tests/unit.test.ts loadDotEnv cases (item 18).
package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFillsMissingKeysRealEnvWins(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.env")
	b := filepath.Join(dir, "b.env")
	_ = os.WriteFile(a, []byte("FLUX_T_LOW=from_a\nFLUX_T_SHARED=from_a\nFLUX_T_REAL=from_a\n"), 0o644)
	_ = os.WriteFile(b, []byte("FLUX_T_HIGH=from_b\nFLUX_T_SHARED=from_b\nFLUX_T_REAL=from_b\n"), 0o644)
	t.Setenv("FLUX_T_REAL", "from_environment")
	os.Unsetenv("FLUX_T_LOW")
	os.Unsetenv("FLUX_T_HIGH")
	os.Unsetenv("FLUX_T_SHARED")
	defer func() {
		for _, k := range []string{"FLUX_T_LOW", "FLUX_T_HIGH", "FLUX_T_SHARED", "FLUX_T_REAL"} {
			os.Unsetenv(k)
		}
	}()

	res := Load(a)
	if len(res.Loaded) != 1 || res.Loaded[0] != a {
		t.Fatalf("loaded = %v, want [%s]", res.Loaded, a)
	}
	if os.Getenv("FLUX_T_LOW") != "from_a" {
		t.Fatalf("FLUX_T_LOW = %q", os.Getenv("FLUX_T_LOW"))
	}
	if os.Getenv("FLUX_T_REAL") != "from_environment" {
		t.Fatal("real env must never be overridden")
	}
	// Loading another file must not override values set by the first.
	Load(b)
	if os.Getenv("FLUX_T_SHARED") != "from_a" {
		t.Fatal("first-loaded file must win")
	}
	if os.Getenv("FLUX_T_HIGH") != "from_b" {
		t.Fatal("later file fills only missing keys")
	}
}

func TestLoadMissingFileIsNoop(t *testing.T) {
	res := Load("definitely-not-a-file.env")
	if len(res.Loaded) != 0 {
		t.Fatalf("loaded = %v, want empty", res.Loaded)
	}
}

func TestLoadQuotedValuesAndComments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "q.env")
	_ = os.WriteFile(p, []byte("# comment\nFLUX_T_Q=\"quoted value\"\nFLUX_T_S='single'\nexport FLUX_T_E=exported\nFLUX_T_NOEq\n"), 0o644)
	res := Load(p)
	if len(res.Loaded) != 1 {
		t.Fatalf("loaded = %v", res.Loaded)
	}
	defer func() {
		for _, k := range []string{"FLUX_T_Q", "FLUX_T_S", "FLUX_T_E"} {
			os.Unsetenv(k)
		}
	}()
	if os.Getenv("FLUX_T_Q") != "quoted value" || os.Getenv("FLUX_T_S") != "single" || os.Getenv("FLUX_T_E") != "exported" {
		t.Fatalf("values wrong: %q %q %q", os.Getenv("FLUX_T_Q"), os.Getenv("FLUX_T_S"), os.Getenv("FLUX_T_E"))
	}
}