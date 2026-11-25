package archive

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func createTestZip(t *testing.T, zipPath string, files map[string]string) {
	t.Helper()
	f, err := os.Create(zipPath)
	require.NoError(t, err)
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range files {
		w, wErr := zw.Create(name)
		require.NoError(t, wErr)
		_, wErr = w.Write([]byte(content))
		require.NoError(t, wErr)
	}
	require.NoError(t, zw.Close())
}

func TestExtractAndUnwrapSingleRoot(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "tool.zip")
	createTestZip(t, zipPath, map[string]string{
		"MyApp/MyApp/tool.exe":   "MZ-binary",
		"MyApp/MyApp/sub/a.txt":  "hello",
		"MyApp/__MACOSX/._junk":  "junk",
	})

	dest := filepath.Join(tmp, "out")
	require.NoError(t, Extract(zipPath, dest))
	require.NoError(t, UnwrapSingleRoot(dest))

	// Single nested MyApp dir should be unwrapped cleanly without collision.
	exeData, err := os.ReadFile(filepath.Join(dest, "tool.exe"))
	require.NoError(t, err)
	require.Equal(t, "MZ-binary", string(exeData))

	subData, err := os.ReadFile(filepath.Join(dest, "sub", "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(subData))

	_, junkErr := os.Stat(filepath.Join(dest, "__MACOSX"))
	require.True(t, os.IsNotExist(junkErr))
}

func TestExtractZipSlipRejected(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "evil.zip")
	createTestZip(t, zipPath, map[string]string{
		"../../escaped.txt": "bad",
	})

	dest := filepath.Join(tmp, "sandbox", "app")
	err := Extract(zipPath, dest)
	require.Error(t, err)

	_, statErr := os.Stat(filepath.Join(tmp, "escaped.txt"))
	require.True(t, os.IsNotExist(statErr))
}

func TestComputeManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tool.exe"), []byte("abc"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "data.txt"), []byte("12345"), 0o644))

	manifest, err := ComputeManifest("app-1", dir)
	require.NoError(t, err)
	require.Len(t, manifest, 2)
	require.Equal(t, "sub/data.txt", manifest[0].RelPath)
	require.Equal(t, int64(5), manifest[0].FileSize)
	require.Len(t, manifest[0].SHA256, 64)
	require.Equal(t, "tool.exe", manifest[1].RelPath)
	require.Equal(t, int64(3), manifest[1].FileSize)
}
