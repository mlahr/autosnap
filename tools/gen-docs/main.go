package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	autosnap "autosnap/internal/autosnap"

	"github.com/spf13/cobra/doc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gen-docs: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root := autosnap.NewRootCommand()
	root.DisableAutoGenTag = true

	if err := resetDir("docs/commands"); err != nil {
		return err
	}
	if err := doc.GenMarkdownTreeCustom(root, "docs/commands", filePrepender, linkHandler); err != nil {
		return err
	}

	if err := resetDir("docs/man/generated"); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "autosnap-man-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	manualDate := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	header := &doc.GenManHeader{
		Title:   "AUTOSNAP",
		Section: "1",
		Date:    &manualDate,
		Source:  "autosnap",
		Manual:  "autosnap manual",
	}
	if err := doc.GenManTree(root, header, tmp); err != nil {
		return err
	}
	if err := copyGeneratedSubcommandManPages(tmp, "docs/man/generated"); err != nil {
		return err
	}

	if err := resetDir("packaging/deb/usr/share/doc/autosnap"); err != nil {
		return err
	}
	if err := resetDir("packaging/deb/usr/share/man/man1"); err != nil {
		return err
	}
	if err := gzipMarkdownDocs("docs", "packaging/deb/usr/share/doc/autosnap"); err != nil {
		return err
	}
	if err := gzipManPages("docs/man", "packaging/deb/usr/share/man/man1"); err != nil {
		return err
	}
	return nil
}

func filePrepender(filename string) string {
	name := strings.TrimSuffix(filepath.Base(filename), ".md")
	name = strings.ReplaceAll(name, "_", " ")
	return "# " + name + "\n\n"
}

func linkHandler(name string) string {
	base := strings.TrimSuffix(name, ".md")
	return strings.ReplaceAll(base, " ", "_") + ".md"
}

func resetDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func copyGeneratedSubcommandManPages(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "autosnap.1" || !strings.HasSuffix(name, ".1") {
			continue
		}
		if err := copyFile(filepath.Join(srcDir, name), filepath.Join(dstDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func gzipMarkdownDocs(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name()+".gz")
		if err := gzipFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func gzipManPages(srcDir, dstDir string) error {
	rootMan := filepath.Join(srcDir, "autosnap.1")
	if err := gzipFile(rootMan, filepath.Join(dstDir, "autosnap.1.gz")); err != nil {
		return err
	}
	generatedDir := filepath.Join(srcDir, "generated")
	entries, err := os.ReadDir(generatedDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".1") {
			continue
		}
		src := filepath.Join(generatedDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name()+".gz")
		if err := gzipFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func gzipFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	zw.Name = filepath.Base(src)
	zw.ModTime = time.Unix(0, 0)
	if _, err := zw.Write(raw); err != nil {
		zw.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}
