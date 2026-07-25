// Package gcsfetch downloads gs:// Google Cloud Storage object URIs to local
// files, using application default credentials.
package gcsfetch

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// IsGCSURI reports whether uri looks like a gs://bucket/object URI.
func IsGCSURI(uri string) bool {
	return strings.HasPrefix(uri, "gs://")
}

// Download fetches a gs://bucket/object URI into destDir (created if
// needed), returning the local path to the downloaded file. Uses
// application default credentials (gcloud auth application-default login,
// or a service account via GOOGLE_APPLICATION_CREDENTIALS).
func Download(ctx context.Context, uri, destDir string) (string, error) {
	bucket, object, err := parse(uri)
	if err != nil {
		return "", err
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("gcsfetch: creating storage client (check application default credentials): %w", err)
	}
	defer client.Close()

	rc, err := client.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return "", fmt.Errorf("gcsfetch: opening gs://%s/%s: %w", bucket, object, err)
	}
	defer rc.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	localPath := filepath.Join(destDir, filepath.Base(object))
	f, err := os.Create(localPath)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		os.Remove(localPath)
		return "", fmt.Errorf("gcsfetch: downloading gs://%s/%s: %w", bucket, object, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return localPath, nil
}

// ListPrefix lists objects matching a gs://bucket/prefix URI. If uri points to
// a single object (does not end with / and matches an exact object), it returns
// [uri].
func ListPrefix(ctx context.Context, uri string) ([]string, error) {
	bucket, prefix, err := parsePrefix(uri)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcsfetch: creating storage client: %w", err)
	}
	defer client.Close()

	query := &storage.Query{Prefix: prefix}
	it := client.Bucket(bucket).Objects(ctx, query)
	var uris []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcsfetch: listing gs://%s/%s: %w", bucket, prefix, err)
		}
		// Skip directory marker objects
		if strings.HasSuffix(attrs.Name, "/") {
			continue
		}
		uris = append(uris, fmt.Sprintf("gs://%s/%s", bucket, attrs.Name))
	}
	if len(uris) == 0 {
		return nil, fmt.Errorf("gcsfetch: no objects found matching %s", uri)
	}
	return uris, nil
}

func parsePrefix(uri string) (bucket, prefix string, err error) {
	if !IsGCSURI(uri) {
		return "", "", fmt.Errorf("gcsfetch: not a gs:// URI: %s", uri)
	}
	rest := strings.TrimPrefix(uri, "gs://")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 1 {
		return parts[0], "", nil
	}
	return parts[0], parts[1], nil
}

func parse(uri string) (bucket, object string, err error) {
	if !IsGCSURI(uri) {
		return "", "", fmt.Errorf("gcsfetch: not a gs:// URI: %s", uri)
	}
	rest := strings.TrimPrefix(uri, "gs://")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("gcsfetch: invalid GCS URI %q (want gs://bucket/object)", uri)
	}
	return parts[0], parts[1], nil
}
