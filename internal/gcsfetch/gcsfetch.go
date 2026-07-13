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
