package azurestorage

import (
	"strings"
	"testing"

	"github.com/kosatka-cms/kosatka/modulekit/testkit"
)

// publicSection - files.azure of the tests (the key comes from the
// environment).
func publicSection(t *testing.T) map[string]any {
	t.Setenv("FILES_AZURE_KEY", "a2V5")
	return map[string]any{"account": "acc", "container": "site", "prefix": "public", "public_url": "https://cdn.example.com/site"}
}

// TestPublic - files.azure gives the store of its container and the
// address the container serves the files at; no key, no store.
func TestPublic(t *testing.T) {
	s, url, err := openPublic(testkit.FileStorageConfig(Name, map[string]any{"azure": publicSection(t)}))
	if err != nil || url != "https://cdn.example.com/site" || s.(*Store).String() != "azure://site/public/" {
		t.Fatalf("%v %q %v", s, url, err)
	}
	t.Setenv("FILES_AZURE_KEY", "")
	if _, _, err := openPublic(testkit.FileStorageConfig(Name, map[string]any{"azure": map[string]any{"account": "acc", "container": "site"}})); err == nil || !strings.Contains(err.Error(), "files.azure") {
		t.Errorf("no key: %v", err)
	}
}

// TestPrivate - the private files' container is in files.azure's account
// and never where the public files' address reaches.
func TestPrivate(t *testing.T) {
	for name, tc := range map[string]struct {
		publicStorage string
		priv          map[string]any
		ok            bool
	}{
		"another container":    {Name, map[string]any{"container": "site-private"}, true},
		"a prefix beside":      {Name, map[string]any{"container": "site", "prefix": "private"}, true},
		"public files on disk": {"local", map[string]any{"container": "site"}, true},
		"the same prefix":      {Name, map[string]any{"container": "site", "prefix": "public"}, false},
		"no container":         {Name, map[string]any{}, false},
	} {
		s, err := openPrivate(testkit.FileStorageConfig(tc.publicStorage, map[string]any{"azure": publicSection(t), "private_azure": tc.priv}))
		if (err == nil) != tc.ok || (tc.ok && s == nil) {
			t.Errorf("%s: %v %v", name, s, err)
		}
	}
}
