package secretfiles

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectedSecretRotationReadsFreshExactBytes(t *testing.T) {
	base := t.TempDir()
	wid := strings.Repeat("1", 32)
	root := filepath.Join(base, wid)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for dir, value := range map[string]string{"..first": "FIRST_VALUE\n", "..second": "SECOND_VALUE\n"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "token"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("..first", filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..data/token", filepath.Join(root, "checkout-token")); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(base, wid)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	value, err := reader.Read("checkout-token")
	if err != nil || string(value) != "FIRST_VALUE\n" {
		t.Fatalf("initial read: %v", err)
	}
	if err = os.Symlink("..second", filepath.Join(root, "..next")); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(root, "..next"), filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	value, err = reader.Read("checkout-token")
	if err != nil || string(value) != "SECOND_VALUE\n" {
		t.Fatalf("rotated read: %v", err)
	}
}
func TestSecretReaderRejectsEscapeAndNonValuesWithoutPathDisclosure(t *testing.T) {
	base := t.TempDir()
	wid := strings.Repeat("1", 32)
	root := filepath.Join(base, wid)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "PRIVATE_OUTSIDE_FILE")
	if err := os.WriteFile(outside, []byte("PRIVATE_OUTSIDE_VALUE"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escaped")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "oversize"), bytes.Repeat([]byte("x"), MaxBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(base, wid)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, key := range []string{"escaped", "directory", "empty", "oversize", "missing", "../PRIVATE_OUTSIDE_FILE", "/etc/passwd", "..data", "nested/token", "token\\file"} {
		value, err := reader.Read(key)
		if !errors.Is(err, ErrUnavailable) || len(value) != 0 {
			t.Fatal("invalid source returned a value")
		}
		for _, private := range []string{root, outside, key, "PRIVATE_OUTSIDE_VALUE"} {
			if strings.Contains(err.Error(), private) {
				t.Fatal("error exposed provider details")
			}
		}
	}
	if _, err := Open("relative", wid); !errors.Is(err, ErrUnavailable) {
		t.Fatal("relative root accepted")
	}
}

func TestMountedSecretsStayInsideSelectedWorkspace(t *testing.T) {
	base := t.TempDir()
	a, b := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for id, value := range map[string]string{a: "WORKSPACE_A", b: "WORKSPACE_B"} {
		if err := os.Mkdir(filepath.Join(base, id), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, id, "token"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../"+b+"/token", filepath.Join(base, a, "other")); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(base, a)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	value, err := reader.Read("token")
	if err != nil || string(value) != "WORKSPACE_A" {
		t.Fatal("wrong workspace secret returned")
	}
	if value, err = reader.Read("other"); !errors.Is(err, ErrUnavailable) || len(value) != 0 {
		t.Fatal("source crossed workspace directory")
	}
	if _, err = Open(base, "../"+b); !errors.Is(err, ErrUnavailable) {
		t.Fatal("workspace traversal accepted")
	}
	alias := strings.Repeat("c", 32)
	if err = os.Symlink(b, filepath.Join(base, alias)); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(base, alias); !errors.Is(err, ErrUnavailable) {
		t.Fatal("workspace directory symlink retargeted secrets")
	}
}
