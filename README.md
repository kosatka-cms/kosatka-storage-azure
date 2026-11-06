# Azure storage

The `azure` file storage for [Kosatka](https://github.com/kosatka-cms/kosatka):
the site's public and private files in containers of an Azure Blob
Storage account instead of the server's disk.

## Install

A site with packages from outside the engine builds its own binary
(`kosatka.Main`); `app module add` adds this one to it:

```bash
app module add github.com/kosatka-cms/kosatka-storage-azure
go build -o app .
```

Then set the storage in the site's config, with the account's access key
in its environment (never in the config file):

```yaml
files:
  storage: azure
  azure:
    account: mysite
    container: files
    prefix: blog/              # optional
    public_url: https://mysite.blob.core.windows.net/files/blog/   # optional
```

```bash
FILES_AZURE_KEY=...
```

Copy the existing files into the container with `app files migrate` and
restart the site. The whole guide - how the files are served, the
private files' container, moving a site with files - is
[Files in object storage](https://kosatka-cms.github.io/kosatka/guides/file-storage/#azure-blob-storage).

## Settings

| Key | Variable | |
|---|---|---|
| `files.azure.account` | `FILES_AZURE_ACCOUNT` | the storage account |
| `files.azure.container` | `FILES_AZURE_CONTAINER` | the container |
| `files.azure.prefix` | `FILES_AZURE_PREFIX` | the container's folder for the site's files |
| `files.azure.endpoint` | `FILES_AZURE_ENDPOINT` | the account's blob address; empty - `https://<account>.blob.core.windows.net` (the Azurite emulator: `http://127.0.0.1:10000/devstoreaccount1`) |
| `files.azure.public_url` | `FILES_AZURE_PUBLIC_URL` | where the container or its CDN serves the files itself |
| - | `FILES_AZURE_KEY` | the account's access key |
| `files.private_azure.container` | `FILES_PRIVATE_AZURE_CONTAINER` | the private files' container (`files.private_storage: azure`), in `files.azure`'s account |
| `files.private_azure.prefix` | `FILES_PRIVATE_AZURE_PREFIX` | its folder for the site's files |

An empty folder is kept as a `.folder` blob in it (Azure advises against
blob names ending in `/`). The site refuses to start when the private
files would be where the public files' address reaches.

## Development

```bash
go test ./...                      # the unit tests
./scripts/test_integration.sh      # and the tests on Azurite (Docker)
```

## License

[MIT](LICENSE)
