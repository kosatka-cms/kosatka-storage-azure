//go:build integration

package azurestorage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/kosatka-cms/kosatka/pkg/filestore"
	"github.com/kosatka-cms/kosatka/pkg/filestore/filestoretest"
)

// Azurite's own account: the emulator's documented development account.
const (
	azuriteAccount = "devstoreaccount1"
	azuriteKey     = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

// azureConfig - the Azurite emulator at KOSATKA_TEST_AZURE (its blob
// address, scripts/test_integration.sh), its test container made if needed; the
// test is skipped without it.
func azureConfig(t *testing.T) Options {
	t.Helper()
	endpoint := os.Getenv("KOSATKA_TEST_AZURE")
	if endpoint == "" {
		t.Skip("KOSATKA_TEST_AZURE not set (scripts/test_integration.sh sets it)")
	}
	cfg := Options{Account: azuriteAccount, Key: azuriteKey, Container: "kosatka-test", Endpoint: endpoint}
	cred, err := azblob.NewSharedKeyCredential(cfg.Account, cfg.Key)
	if err != nil {
		t.Fatal(err)
	}
	client, err := azblob.NewClientWithSharedKeyCredential(endpoint+"/", cred, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateContainer(context.Background(), cfg.Container, nil); err != nil && !bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
		t.Fatalf("making the test container: %v", err)
	}
	return cfg
}

// TestAzure - the store's contract on Azure Blob Storage (Azurite), each
// test under a prefix of its own in the shared container.
func TestAzure(t *testing.T) {
	cfg := azureConfig(t)
	filestoretest.Run(t, func(t *testing.T) filestore.Store {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		c := cfg
		c.Prefix = "test-" + hex.EncodeToString(b)
		s, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// TestAzureContentHeaders - what the container serves itself: the media
// type, a download for anything but images and PDF, the Cache-Control.
func TestAzureContentHeaders(t *testing.T) {
	cfg := azureConfig(t)
	cfg.Prefix = "headers"
	cfg.CacheControl = "public, max-age=60"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred, _ := azblob.NewSharedKeyCredential(cfg.Account, cfg.Key)
	client, _ := azblob.NewClientWithSharedKeyCredential(cfg.Endpoint+"/", cred, nil)
	for key, want := range map[string][2]string{
		"a.jpg":   {"image/jpeg", ""},
		"doc.pdf": {"application/pdf", ""},
		"x.html":  {"text/html; charset=utf-8", "attachment"},
	} {
		if _, err := s.Put(ctx, key, strings.NewReader("x"), filestore.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		props, err := client.ServiceClient().NewContainerClient(cfg.Container).NewBlobClient("headers/"+key).GetProperties(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		str := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		if str(props.ContentType) != want[0] || str(props.ContentDisposition) != want[1] || str(props.CacheControl) != cfg.CacheControl {
			t.Errorf("%s: %q %q %q", key, str(props.ContentType), str(props.ContentDisposition), str(props.CacheControl))
		}
		_ = s.Delete(ctx, key)
	}
	if s.String() != "azure://kosatka-test/headers/" {
		t.Errorf("String() = %q", s.String())
	}
}
