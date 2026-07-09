package harmonyquery

import (
	"os"
	"testing"
)

func TestReadOnlyFromEnv(t *testing.T) {
	t.Setenv(DefaultReadOnlyEnv, "")
	if ReadOnlyFromEnv() {
		t.Fatal("expected false for empty env")
	}

	for _, v := range []string{"true", "TRUE", "1", "yes", "Yes"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(DefaultReadOnlyEnv, v)
			if !ReadOnlyFromEnv() {
				t.Fatalf("expected true for %q", v)
			}
		})
	}

	for _, v := range []string{"false", "0", "no", "maybe"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(DefaultReadOnlyEnv, v)
			if ReadOnlyFromEnv() {
				t.Fatalf("expected false for %q", v)
			}
		})
	}
}

func TestReadOnlyFromEnvUnset(t *testing.T) {
	_ = os.Unsetenv(DefaultReadOnlyEnv)
	if ReadOnlyFromEnv() {
		t.Fatal("expected false when env unset")
	}
}
