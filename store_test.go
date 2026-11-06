package azurestorage

import "testing"

// TestNewChecks - New refuses no account or container, an endpoint that
// isn't an http(s) address, a key that isn't base64 or a prefix that
// isn't a folder name; a store says where it keeps the files.
func TestNewChecks(t *testing.T) {
	for _, cfg := range []Options{
		{Container: "c", Key: "a2V5"},
		{Account: "acc", Key: "a2V5"},
		{Account: "acc", Key: "a2V5", Container: "c", Endpoint: "storage.example.com"},
		{Account: "acc", Key: "not base64!", Container: "c"},
		{Account: "acc", Key: "a2V5", Container: "c", Prefix: "../x"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%+v: accepted", cfg)
		}
	}
	s, err := New(Options{Account: "acc", Key: "a2V5", Container: "site", Prefix: "/blog/"})
	if err != nil || s.String() != "azure://site/blog/" {
		t.Errorf("%v %v", s, err)
	}
}
