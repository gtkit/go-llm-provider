package llmprovider

import "testing"

func TestVersionIsSet(t *testing.T) {
	t.Parallel()

	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
