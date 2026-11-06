package azurestorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/kosatka-cms/kosatka/pkg/filestore"
)

// Options - a container of an Azure Blob Storage account.
type Options struct {
	// Account and Key - the storage account and its access key.
	Account, Key string
	Container    string
	// Prefix - the container's folder the store keeps its files in ("" -
	// its root): several sites can share a container.
	Prefix string
	// Endpoint - the account's blob address ("" -
	// https://<account>.blob.core.windows.net; the Azurite emulator:
	// http://127.0.0.1:10000/<account>).
	Endpoint string
	// CacheControl - the files' Cache-Control when the container serves
	// them itself ("" - none).
	CacheControl string
}

// azureFolderMarker - the empty blob Mkdir stores in a folder so that an
// empty folder exists (Azure advises against blob names ending in "/").
// It is a dotfile: never a public file, never listed.
const azureFolderMarker = ".folder"

// Store keeps the files as blobs of a container: the key is the blob's
// name after the prefix, a folder is a name prefix ending in "/".
type Store struct {
	container    *container.Client
	name         string // the container
	prefix       string // "" or ending in "/"
	cacheControl string
}

// New returns the store of a container. It doesn't contact the
// storage; the first operation does.
func New(cfg Options) (*Store, error) {
	if cfg.Account == "" || cfg.Key == "" {
		return nil, errors.New("azurestorage: no Azure storage account or key")
	}
	if cfg.Container == "" {
		return nil, errors.New("azurestorage: no Azure container")
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://" + cfg.Account + ".blob.core.windows.net"
	}
	if u, err := url.Parse(endpoint); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("azurestorage: the Azure endpoint %q isn't an http(s) address", cfg.Endpoint)
	}
	cred, err := azblob.NewSharedKeyCredential(cfg.Account, cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("azurestorage: Azure key: %w", err)
	}
	client, err := azblob.NewClientWithSharedKeyCredential(endpoint+"/", cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azurestorage: Azure client: %w", err)
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		if !validKey(prefix) {
			return nil, fmt.Errorf("azurestorage: the Azure prefix %q isn't a folder name", cfg.Prefix)
		}
		prefix += "/"
	}
	return &Store{
		container:    client.ServiceClient().NewContainerClient(cfg.Container),
		name:         cfg.Container,
		prefix:       prefix,
		cacheControl: cfg.CacheControl,
	}, nil
}

// String - where the store keeps the files: azure://<container>/<prefix>.
func (s *Store) String() string { return "azure://" + s.name + "/" + s.prefix }

func (s *Store) blob(key string) string { return s.prefix + key }

func (s *Store) folder(dir string) string {
	if dir == "" {
		return s.prefix
	}
	return s.prefix + dir + "/"
}

func (s *Store) key(blob string) string { return strings.TrimPrefix(blob, s.prefix) }

// isMarker reports whether a blob is a folder's marker (Mkdir).
func isMarker(name string) bool { return path.Base(name) == azureFolderMarker }

func azureNotFound(err error) bool {
	return bloberror.HasCode(err, bloberror.BlobNotFound)
}

func azureWrap(key string, err error) error {
	if azureNotFound(err) {
		return fmt.Errorf("%s: %w", key, filestore.ErrNotExist)
	}
	return err
}

// Put uploads the blob; with Exclusive the storage refuses an existing
// one (If-None-Match).
func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts filestore.PutOptions) (int64, error) {
	key, err := filestore.CheckKey(key)
	if err != nil {
		return 0, err
	}
	ct := opts.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(key))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	headers := &blob.HTTPHeaders{BlobContentType: to.Ptr(ct)}
	if !inline(ct) {
		headers.BlobContentDisposition = to.Ptr("attachment")
	}
	if s.cacheControl != "" {
		headers.BlobCacheControl = to.Ptr(s.cacheControl)
	}
	uo := &blockblob.UploadStreamOptions{HTTPHeaders: headers, Concurrency: 4}
	if opts.Exclusive {
		uo.AccessConditions = &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: to.Ptr(azcore.ETagAny)}}
	}
	cr := &countingReader{r: r}
	if _, err := s.container.NewBlockBlobClient(s.blob(key)).UploadStream(ctx, cr, uo); err != nil {
		if bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet) {
			return 0, fmt.Errorf("%s: %w", key, filestore.ErrExist)
		}
		return 0, err
	}
	return cr.n, nil
}

// azureFile reads a blob in ranges from where it is read: seeking costs
// nothing until the next read.
type azureFile struct {
	ctx  context.Context
	blob *blob.Client
	size int64
	off  int64
	body io.ReadCloser // the range being read, from off
}

func (f *azureFile) Read(p []byte) (int, error) {
	if f.off >= f.size {
		return 0, io.EOF
	}
	if f.body == nil {
		resp, err := f.blob.DownloadStream(f.ctx, &blob.DownloadStreamOptions{Range: blob.HTTPRange{Offset: f.off}})
		if err != nil {
			return 0, err
		}
		f.body = resp.Body
	}
	n, err := f.body.Read(p)
	f.off += int64(n)
	if errors.Is(err, io.EOF) && f.off < f.size {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (f *azureFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += f.off
	case io.SeekEnd:
		offset += f.size
	}
	if offset < 0 {
		return 0, errors.New("azurestorage: seek before the start")
	}
	if offset != f.off && f.body != nil {
		_ = f.body.Close()
		f.body = nil
	}
	f.off = offset
	return offset, nil
}

func (f *azureFile) Close() error {
	if f.body != nil {
		return f.body.Close()
	}
	return nil
}

// Open opens the blob key for reading; a folder's marker is ErrNotExist.
func (s *Store) Open(ctx context.Context, key string) (filestore.File, filestore.Info, error) {
	key, err := filestore.CheckKey(key)
	if err != nil {
		return nil, filestore.Info{}, err
	}
	if isMarker(key) {
		return nil, filestore.Info{}, fmt.Errorf("%s: %w", key, filestore.ErrNotExist)
	}
	b := s.container.NewBlobClient(s.blob(key))
	props, err := b.GetProperties(ctx, nil)
	if err != nil {
		return nil, filestore.Info{}, azureWrap(key, err)
	}
	info := fileInfo(key, props.ContentLength, props.LastModified)
	return &azureFile{ctx: ctx, blob: b, size: info.Size}, info, nil
}

func fileInfo(key string, size *int64, modified *time.Time) filestore.Info {
	in := filestore.Info{Key: key, Name: path.Base(key)}
	if size != nil {
		in.Size = *size
	}
	if modified != nil {
		in.ModTime = *modified
	}
	return in
}

// Stat describes the blob key, else the folder key when a blob is under
// it (an empty folder keeps its marker).
func (s *Store) Stat(ctx context.Context, key string) (filestore.Info, error) {
	key, err := filestore.CheckDir(key)
	if err != nil {
		return filestore.Info{}, err
	}
	if key == "" {
		return filestore.Info{IsDir: true}, nil
	}
	if !isMarker(key) {
		props, err := s.container.NewBlobClient(s.blob(key)).GetProperties(ctx, nil)
		if err == nil {
			return fileInfo(key, props.ContentLength, props.LastModified), nil
		}
		if !azureNotFound(err) {
			return filestore.Info{}, err
		}
	}
	ok, err := s.folderExists(ctx, key)
	if err != nil {
		return filestore.Info{}, err
	}
	if !ok {
		return filestore.Info{}, fmt.Errorf("%s: %w", key, filestore.ErrNotExist)
	}
	return dirInfo(key), nil
}

// folderExists reports whether a blob - a file or a folder's marker -
// has a name under the folder dir.
func (s *Store) folderExists(ctx context.Context, dir string) (bool, error) {
	pager := s.container.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: to.Ptr(s.folder(dir)), MaxResults: to.Ptr(int32(1))})
	if !pager.More() {
		return false, nil
	}
	page, err := pager.NextPage(ctx)
	if err != nil {
		return false, err
	}
	return len(page.Segment.BlobItems) > 0, nil
}

// List lists the folder dir a level deep (its blobs and folders, the
// markers left out).
func (s *Store) List(ctx context.Context, dir string) ([]filestore.Info, error) {
	dir, err := filestore.CheckDir(dir)
	if err != nil {
		return nil, err
	}
	folder := s.folder(dir)
	var out []filestore.Info
	found := dir == ""
	pager := s.container.NewListBlobsHierarchyPager("/", &container.ListBlobsHierarchyOptions{Prefix: to.Ptr(folder)})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range page.Segment.BlobPrefixes {
			if p.Name == nil {
				continue
			}
			found = true
			// A storage keeping folders (a hierarchical namespace) lists
			// an emptied one too.
			key := strings.TrimSuffix(s.key(*p.Name), "/")
			ok, err := s.folderExists(ctx, key)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, dirInfo(key))
			}
		}
		for _, b := range page.Segment.BlobItems {
			if b.Name == nil {
				continue
			}
			found = true
			if isMarker(*b.Name) {
				continue
			}
			var size *int64
			var modified *time.Time
			if b.Properties != nil {
				size, modified = b.Properties.ContentLength, b.Properties.LastModified
			}
			out = append(out, fileInfo(s.key(*b.Name), size, modified))
		}
	}
	if !found {
		return nil, fmt.Errorf("%s: %w", dir, filestore.ErrNotExist)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// walkBlobs calls fn for every blob under the folder dir, markers
// included.
func (s *Store) walkBlobs(ctx context.Context, dir string, fn func(name string, size *int64, modified *time.Time) error) error {
	pager := s.container.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: to.Ptr(s.folder(dir))})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, b := range page.Segment.BlobItems {
			if b.Name == nil {
				continue
			}
			var size *int64
			var modified *time.Time
			if b.Properties != nil {
				size, modified = b.Properties.ContentLength, b.Properties.LastModified
			}
			if err := fn(*b.Name, size, modified); err != nil {
				return err
			}
		}
	}
	return nil
}

// Walk calls fn for every blob under the folder dir, the markers left
// out, in the storage's order.
func (s *Store) Walk(ctx context.Context, dir string, fn func(filestore.Info) error) error {
	dir, err := filestore.CheckDir(dir)
	if err != nil {
		return err
	}
	return s.walkBlobs(ctx, dir, func(name string, size *int64, modified *time.Time) error {
		if isMarker(name) {
			return nil
		}
		return fn(fileInfo(s.key(name), size, modified))
	})
}

// Delete deletes the blob key; a missing one is no error.
func (s *Store) Delete(ctx context.Context, key string) error {
	key, err := filestore.CheckKey(key)
	if err != nil {
		return err
	}
	if _, err := s.container.NewBlobClient(s.blob(key)).Delete(ctx, nil); err != nil && !azureNotFound(err) {
		return err
	}
	return nil
}

// DeleteDir deletes every blob under the folder dir, its markers too.
func (s *Store) DeleteDir(ctx context.Context, dir string) error {
	dir, err := filestore.CheckKey(dir)
	if err != nil {
		return err
	}
	var names []string
	if err := s.walkBlobs(ctx, dir, func(name string, _ *int64, _ *time.Time) error {
		names = append(names, name)
		return nil
	}); err != nil {
		return err
	}
	for _, name := range names {
		if _, err := s.container.NewBlobClient(name).Delete(ctx, nil); err != nil && !azureNotFound(err) {
			return fmt.Errorf("removing %s: %w", s.key(name), err)
		}
	}
	return nil
}

// Mkdir stores the folder's marker blob; its parents exist through it.
func (s *Store) Mkdir(ctx context.Context, dir string) error {
	dir, err := filestore.CheckDir(dir)
	if err != nil || dir == "" {
		return err
	}
	_, err = s.container.NewBlockBlobClient(s.folder(dir)+azureFolderMarker).UploadBuffer(ctx, nil, nil)
	return err
}

// Rename copies the blob (or every blob of the folder) to the new name
// and removes the old one: not atomic, a failure midway leaves both.
func (s *Store) Rename(ctx context.Context, from, to string) error {
	from, err := filestore.CheckKey(from)
	if err != nil {
		return err
	}
	if to, err = filestore.CheckKey(to); err != nil {
		return err
	}
	src, err := s.Stat(ctx, from)
	if err != nil {
		return err
	}
	if taken, err := filestore.Exists(ctx, s, to); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("%s: %w", to, filestore.ErrExist)
	}
	if !src.IsDir {
		if err := s.copyBlob(ctx, s.blob(from), s.blob(to)); err != nil {
			return err
		}
		return s.Delete(ctx, from)
	}
	var names []string
	if err := s.walkBlobs(ctx, from, func(name string, _ *int64, _ *time.Time) error {
		names = append(names, name)
		return nil
	}); err != nil {
		return err
	}
	for _, name := range names {
		if err := s.copyBlob(ctx, name, s.folder(to)+strings.TrimPrefix(name, s.folder(from))); err != nil {
			return err
		}
	}
	return s.DeleteDir(ctx, from)
}

// copyBlob copies a blob through the site (download, upload): the
// server-side copy of a blob needs a SAS or a public source.
func (s *Store) copyBlob(ctx context.Context, from, to string) error {
	src := s.container.NewBlobClient(from)
	props, err := src.GetProperties(ctx, nil)
	if err != nil {
		return azureWrap(s.key(from), err)
	}
	resp, err := src.DownloadStream(ctx, nil)
	if err != nil {
		return azureWrap(s.key(from), err)
	}
	defer func() { _ = resp.Body.Close() }()
	headers := &blob.HTTPHeaders{
		BlobContentType:        props.ContentType,
		BlobContentDisposition: props.ContentDisposition,
		BlobCacheControl:       props.CacheControl,
	}
	_, err = s.container.NewBlockBlobClient(to).UploadStream(ctx, resp.Body, &blockblob.UploadStreamOptions{HTTPHeaders: headers, Concurrency: 4})
	return err
}

// inline - the media types a browser may show in place; the bucket
// serves everything else as a download (Content-Disposition).
func inline(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") || contentType == "application/pdf"
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func dirInfo(key string) filestore.Info {
	return filestore.Info{Key: key, Name: path.Base(key), IsDir: true}
}

// validKey - p is a valid key of a file (filestore.CheckKey).
func validKey(p string) bool {
	_, err := filestore.CheckKey(p)
	return err == nil
}

var _ filestore.Store = (*Store)(nil)
