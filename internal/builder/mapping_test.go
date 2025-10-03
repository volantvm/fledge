package builder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockFileInfo implements os.FileInfo for testing
type mockFileInfo struct {
	name  string
	mode  os.FileMode
	isDir bool
}

func (m mockFileInfo) Name() string       { return m.name }
func (m mockFileInfo) Size() int64        { return 0 }
func (m mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m mockFileInfo) IsDir() bool        { return m.isDir }
func (m mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m mockFileInfo) Sys() interface{}   { return nil }

// TestDetermineFileMode_Directory tests permission detection for directories
func TestDetermineFileMode_Directory(t *testing.T) {
	info := mockFileInfo{name: "testdir", mode: 0755, isDir: true}
	mode := DetermineFileMode("/any/path", info)
	if mode != 0755 {
		t.Errorf("Expected directory mode 0755, got %04o", mode)
	}
}

// TestDetermineFileMode_FHSExecutable tests FHS executable path detection
func TestDetermineFileMode_FHSExecutable(t *testing.T) {
	testCases := []struct {
		path     string
		baseMode os.FileMode
		expected os.FileMode
	}{
		{"/bin/myapp", 0644, 0755},
		{"/sbin/daemon", 0644, 0755},
		{"/usr/bin/tool", 0644, 0755},
		{"/usr/sbin/admin", 0644, 0755},
		{"/usr/local/bin/custom", 0644, 0755},
		{"/usr/local/sbin/service", 0644, 0755},
		{"/opt/bin/vendor-tool", 0644, 0755},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			info := mockFileInfo{name: filepath.Base(tc.path), mode: tc.baseMode, isDir: false}
			mode := DetermineFileMode(tc.path, info)
			if mode != tc.expected {
				t.Errorf("Path %s: expected mode %04o, got %04o", tc.path, tc.expected, mode)
			}
		})
	}
}

// TestDetermineFileMode_NonExecutable tests non-executable paths
func TestDetermineFileMode_NonExecutable(t *testing.T) {
	testCases := []string{
		"/etc/config.yml",
		"/var/log/app.log",
		"/usr/share/doc/README.md",
		"/home/user/data.txt",
	}

	for _, path := range testCases {
		t.Run(path, func(t *testing.T) {
			info := mockFileInfo{name: filepath.Base(path), mode: 0644, isDir: false}
			mode := DetermineFileMode(path, info)
			if mode != 0644 {
				t.Errorf("Path %s: expected mode 0644, got %04o", path, mode)
			}
		})
	}
}

// TestDetermineFileMode_LibraryFiles tests library file detection
func TestDetermineFileMode_LibraryFiles(t *testing.T) {
	testCases := []string{
		"/lib/libc.so",
		"/lib/libc.so.6",
		"/lib64/libm.so.6",
		"/usr/lib/libssl.so",
		"/usr/lib64/libcrypto.so.1.1",
		"/usr/local/lib/libcustom.so",
	}

	for _, path := range testCases {
		t.Run(path, func(t *testing.T) {
			info := mockFileInfo{name: filepath.Base(path), mode: 0644, isDir: false}
			mode := DetermineFileMode(path, info)
			if mode != 0755 {
				t.Errorf("Library %s: expected mode 0755, got %04o", path, mode)
			}
		})
	}
}

// TestDetermineFileMode_PreserveExecutable tests that already-executable files remain executable
func TestDetermineFileMode_PreserveExecutable(t *testing.T) {
	info := mockFileInfo{name: "script.sh", mode: 0755, isDir: false}
	mode := DetermineFileMode("/home/user/script.sh", info)
	if mode&0111 == 0 {
		t.Errorf("Executable file lost execute permission: got %04o", mode)
	}
}

// TestIsInFHSExecutablePath tests FHS path detection
func TestIsInFHSExecutablePath(t *testing.T) {
	testCases := []struct {
		path     string
		expected bool
	}{
		{"/bin/ls", true},
		{"/sbin/init", true},
		{"/usr/bin/vim", true},
		{"/usr/sbin/sshd", true},
		{"/usr/local/bin/app", true},
		{"/opt/bin/tool", true},
		{"/etc/config", false},
		{"/var/log/file", false},
		{"/home/user/bin/script", false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			result := isInFHSExecutablePath(tc.path)
			if result != tc.expected {
				t.Errorf("Path %s: expected %v, got %v", tc.path, tc.expected, result)
			}
		})
	}
}

// TestIsInFHSLibraryPath tests FHS library path detection
func TestIsInFHSLibraryPath(t *testing.T) {
	testCases := []struct {
		path     string
		expected bool
	}{
		{"/lib/libc.so", true},
		{"/lib/libc.so.6", true},
		{"/lib64/libm.so", true},
		{"/usr/lib/libssl.so.1.1", true},
		{"/lib/notso.txt", false},
		{"/etc/lib/config", false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			result := isInFHSLibraryPath(tc.path)
			if result != tc.expected {
				t.Errorf("Path %s: expected %v, got %v", tc.path, tc.expected, result)
			}
		})
	}
}

// TestPrepareFileMappings tests file mapping preparation
func TestPrepareFileMappings(t *testing.T) {
	// Create a temporary directory with test files
	tmpDir := t.TempDir()

	// Create test files
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	execFile := filepath.Join(tmpDir, "executable")
	if err := os.WriteFile(execFile, []byte("#!/bin/sh\necho test"), 0755); err != nil {
		t.Fatalf("Failed to create executable: %v", err)
	}

	testDir := filepath.Join(tmpDir, "testdir")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Test mappings
	mappings := map[string]string{
		"test.txt":   "/etc/config.txt",
		"executable": "/bin/myapp",
		"testdir":    "/opt/data",
	}

	results, err := PrepareFileMappings(mappings, tmpDir)
	if err != nil {
		t.Fatalf("PrepareFileMappings failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 mappings, got %d", len(results))
	}

	// Verify each mapping
	for _, mapping := range results {
		if mapping.Source == "" {
			t.Error("Source path is empty")
		}
		if mapping.Destination == "" {
			t.Error("Destination path is empty")
		}
		if mapping.Mode == 0 {
			t.Error("Mode is not set")
		}
	}
}

// TestPrepareFileMappings_NonExistent tests error handling for non-existent sources
func TestPrepareFileMappings_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()

	mappings := map[string]string{
		"nonexistent.txt": "/etc/file.txt",
	}

	_, err := PrepareFileMappings(mappings, tmpDir)
	if err == nil {
		t.Fatal("Expected error for non-existent file, got nil")
	}
}

// TestPrepareFileMappings_EmptyMappings tests handling of empty mappings
func TestPrepareFileMappings_EmptyMappings(t *testing.T) {
	tmpDir := t.TempDir()

	mappings := map[string]string{}

	results, err := PrepareFileMappings(mappings, tmpDir)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 mappings, got %d", len(results))
	}
}

// TestCopyFile tests file copying
func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source file
	srcFile := filepath.Join(tmpDir, "source.txt")
	content := []byte("test content")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	// Copy to destination
	dstFile := filepath.Join(tmpDir, "dest", "target.txt")
	if err := CopyFile(srcFile, dstFile, 0755); err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	// Verify destination exists
	if _, err := os.Stat(dstFile); err != nil {
		t.Errorf("Destination file not created: %v", err)
	}

	// Verify content
	dstContent, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("Failed to read destination: %v", err)
	}
	if string(dstContent) != string(content) {
		t.Error("Content mismatch")
	}

	// Verify permissions
	info, err := os.Stat(dstFile)
	if err != nil {
		t.Fatalf("Failed to stat destination: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("Expected mode 0755, got %04o", info.Mode().Perm())
	}
}

// TestCopyDirectory tests directory copying
func TestCopyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source directory structure
	srcDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatalf("Failed to create source directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "subdir", "file2.txt"), []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}

	// Copy directory
	dstDir := filepath.Join(tmpDir, "dest")
	if err := CopyDirectory(srcDir, dstDir, 0755); err != nil {
		t.Fatalf("CopyDirectory failed: %v", err)
	}

	// Verify structure
	checkFile := func(path, expectedContent string) {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("Failed to read %s: %v", path, err)
		}
		if string(content) != expectedContent {
			t.Errorf("Content mismatch in %s", path)
		}
	}

	checkFile(filepath.Join(dstDir, "file1.txt"), "content1")
	checkFile(filepath.Join(dstDir, "subdir", "file2.txt"), "content2")
}

// TestApplyFileMappings tests applying multiple file mappings
func TestApplyFileMappings(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source files
	srcDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	srcFile := filepath.Join(srcDir, "app")
	if err := os.WriteFile(srcFile, []byte("app content"), 0755); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	// Prepare mappings
	mappings := []FileMapping{
		{
			Source:      srcFile,
			Destination: "/bin/app",
			IsDirectory: false,
			Mode:        0755,
		},
	}

	// Apply mappings to target
	targetDir := filepath.Join(tmpDir, "target")
	if err := ApplyFileMappings(mappings, targetDir); err != nil {
		t.Fatalf("ApplyFileMappings failed: %v", err)
	}

	// Verify file was copied
	dstFile := filepath.Join(targetDir, "bin", "app")
	content, err := os.ReadFile(dstFile)
	if err != nil {
		t.Errorf("Failed to read destination: %v", err)
	}
	if string(content) != "app content" {
		t.Error("Content mismatch")
	}
}

// TestNormalizeExecutableMode tests executable mode normalization
func TestNormalizeExecutableMode(t *testing.T) {
	testCases := []struct {
		input    os.FileMode
		expected os.FileMode
	}{
		{0755, 0755}, // rwxr-xr-x stays the same
		{0744, 0755}, // rwxr--r-- becomes rwxr-xr-x
		{0700, 0700}, // rwx------ stays the same (only owner has read)
		{0644, 0644}, // rw-r--r-- stays (no execute bit)
	}

	for _, tc := range testCases {
		t.Run(tc.input.String(), func(t *testing.T) {
			result := normalizeExecutableMode(tc.input)
			if result != tc.expected {
				t.Errorf("Input %04o: expected %04o, got %04o", tc.input, tc.expected, result)
			}
		})
	}
}
