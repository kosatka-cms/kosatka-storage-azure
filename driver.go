// Package azurestorage - the "azure" file storage of Kosatka: the site's
// public and private files in containers of an Azure Blob Storage
// account. Importing the package registers it:
//
//	import _ "github.com/kosatka-cms/kosatka-storage-azure"
//
// (app module add github.com/kosatka-cms/kosatka-storage-azure writes the
// import), then files.storage: azure (FILES_STORAGE=azure) with
// files.azure, and files.private_storage: azure with files.private_azure.
package azurestorage

import (
	"fmt"
	"strings"

	"github.com/kosatka-cms/kosatka/modulekit"
	"github.com/kosatka-cms/kosatka/pkg/filestore"
)

// Name - files.storage's value choosing the storage.
const Name = "azure"

// Settings - the container of the public files: files.azure. The
// account's key comes only from the environment, never the config file.
type Settings struct {
	Account   string `yaml:"account" env:"FILES_AZURE_ACCOUNT"`
	Container string `yaml:"container" env:"FILES_AZURE_CONTAINER"`
	// Prefix - the container's folder for this site's files ("" - its
	// root).
	Prefix string `yaml:"prefix" env:"FILES_AZURE_PREFIX"`
	// Endpoint - the account's blob address ("" -
	// https://<account>.blob.core.windows.net).
	Endpoint string `yaml:"endpoint" env:"FILES_AZURE_ENDPOINT"`
	// PublicURL - where the container (or a CDN in front of it) serves the
	// files itself: the site redirects its files' addresses there. Empty -
	// the site serves them from the container.
	PublicURL string `yaml:"public_url" env:"FILES_AZURE_PUBLIC_URL"`
	Key       string `yaml:"-" env:"FILES_AZURE_KEY"`
}

// PrivateSettings - the container of the private files:
// files.private_azure, in files.azure's account. One the public files'
// address never reaches: another container, or a prefix outside theirs.
type PrivateSettings struct {
	Container string `yaml:"container" env:"FILES_PRIVATE_AZURE_CONTAINER"`
	Prefix    string `yaml:"prefix" env:"FILES_PRIVATE_AZURE_PREFIX"`
}

func init() {
	modulekit.AddFileStorage(modulekit.FileStorage{Name: Name, Public: openPublic, Private: openPrivate})
}

// openPublic - the public files' store and the address the container
// serves them at itself.
func openPublic(c modulekit.FileStorageConfig) (filestore.Store, string, error) {
	var s Settings
	if err := c.Decode("azure", &s); err != nil {
		return nil, "", err
	}
	store, err := New(Options{
		Account: s.Account, Key: s.Key, Endpoint: s.Endpoint,
		Container: s.Container, Prefix: s.Prefix,
		CacheControl: c.CacheControl,
	})
	if err != nil {
		return nil, "", fmt.Errorf("files.azure: %w", err)
	}
	return store, s.PublicURL, nil
}

// openPrivate - the private files' store: files.private_azure's container
// in files.azure's account, never where the public files' address
// reaches.
func openPrivate(c modulekit.FileStorageConfig) (filestore.Store, error) {
	var s Settings
	if err := c.Decode("azure", &s); err != nil {
		return nil, err
	}
	var priv PrivateSettings
	if err := c.Decode("private_azure", &priv); err != nil {
		return nil, err
	}
	if c.PublicStorage == Name && priv.Container == s.Container && prefixesOverlap(s.Prefix, priv.Prefix) {
		return nil, fmt.Errorf("files.private_azure: the container %q and prefix %q reach the public files (files.azure): use another container or a prefix outside theirs", priv.Container, priv.Prefix)
	}
	store, err := New(Options{
		Account: s.Account, Key: s.Key, Endpoint: s.Endpoint,
		Container: priv.Container, Prefix: priv.Prefix,
	})
	if err != nil {
		return nil, fmt.Errorf("files.private_azure: %w", err)
	}
	return store, nil
}

// prefixesOverlap reports whether one prefix holds the other ("" - the
// whole container - holds every one).
func prefixesOverlap(a, b string) bool {
	a, b = strings.Trim(a, "/"), strings.Trim(b, "/")
	return a == "" || b == "" || a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
