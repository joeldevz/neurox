package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// createTestTarGz creates a .tar.gz archive at archivePath containing a single
// file with the given entryName and content.
func createTestTarGz(t *testing.T, archivePath, entryName string, content []byte) {
	t.Helper()

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create tar.gz file: %v", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	hdr := &tar.Header{
		Name: entryName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
}

// createTestZip creates a .zip archive at archivePath containing a single
// file with the given entryName and content.
func createTestZip(t *testing.T, archivePath, entryName string, content []byte) {
	t.Helper()

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create zip file: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	w, err := zw.Create(entryName)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("write zip body: %v", err)
	}
}

func TestExtractBinaryMatchesPlatformName(t *testing.T) {
	sentinelContent := []byte("fake-neurox-binary-content")

	tarTests := []struct {
		name      string
		entryName string
		wantErr   bool
	}{
		{name: "plain neurox", entryName: "neurox", wantErr: false},
		{name: "neurox.exe", entryName: "neurox.exe", wantErr: false},
		{name: "linux amd64", entryName: "neurox_linux_amd64", wantErr: false},
		{name: "darwin arm64", entryName: "neurox_darwin_arm64", wantErr: false},
		{name: "windows amd64 exe", entryName: "neurox_windows_amd64.exe", wantErr: false},
		{name: "nested path", entryName: "dist/neurox_linux_amd64", wantErr: false},
		{name: "unrelated binary", entryName: "other-tool", wantErr: true},
		{name: "readme only", entryName: "README.md", wantErr: true},
	}

	for _, tc := range tarTests {
		t.Run("tar_"+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "test.tar.gz")
			dstPath := filepath.Join(dir, "extracted")

			createTestTarGz(t, archivePath, tc.entryName, sentinelContent)

			err := extractBinary(archivePath, dstPath)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := os.ReadFile(dstPath)
			if err != nil {
				t.Fatalf("read extracted file: %v", err)
			}
			if string(got) != string(sentinelContent) {
				t.Fatalf("content mismatch: got %q, want %q", got, sentinelContent)
			}
		})
	}

	zipTests := []struct {
		name      string
		entryName string
		wantErr   bool
	}{
		{name: "plain neurox", entryName: "neurox", wantErr: false},
		{name: "neurox.exe", entryName: "neurox.exe", wantErr: false},
		{name: "windows amd64 exe", entryName: "neurox_windows_amd64.exe", wantErr: false},
		{name: "linux amd64", entryName: "neurox_linux_amd64", wantErr: false},
		{name: "nested path", entryName: "dist/neurox_darwin_arm64", wantErr: false},
		{name: "unrelated binary", entryName: "other-tool.exe", wantErr: true},
	}

	for _, tc := range zipTests {
		t.Run("zip_"+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "test.zip")
			dstPath := filepath.Join(dir, "extracted")

			createTestZip(t, archivePath, tc.entryName, sentinelContent)

			err := extractBinaryFromZip(archivePath, dstPath)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := os.ReadFile(dstPath)
			if err != nil {
				t.Fatalf("read extracted file: %v", err)
			}
			if string(got) != string(sentinelContent) {
				t.Fatalf("content mismatch: got %q, want %q", got, sentinelContent)
			}
		})
	}
}
