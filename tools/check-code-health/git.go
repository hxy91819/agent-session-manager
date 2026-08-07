package main

import (
	"archive/tar"
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func repositoryRoot() (string, error) {
	output, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("find repository root: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func resolveRevision(root, revision string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve %q: %s", revision, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func analyzeGitRevision(root, revision string, cfg config) (snapshot, error) {
	resolved, err := resolveRevision(root, revision)
	if err != nil {
		return snapshot{}, err
	}
	tempRoot, err := os.MkdirTemp("", "asm-code-health-source-")
	if err != nil {
		return snapshot{}, fmt.Errorf("create source snapshot: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempRoot) }()
	if err := extractGitArchive(root, resolved, tempRoot); err != nil {
		return snapshot{}, err
	}
	return analyzeSnapshot(tempRoot, resolved, cfg)
}

func extractGitArchive(root, revision, destination string) error {
	archive, err := os.CreateTemp("", "asm-code-health-archive-*.tar")
	if err != nil {
		return fmt.Errorf("create git archive: %w", err)
	}
	archivePath := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archivePath)
	}()

	cmd := exec.Command("git", "archive", "--format=tar", revision)
	cmd.Dir = root
	// A real file avoids the reader/wait deadlock that Git for Windows can hit
	// when its archive stream is connected to an inherited stdout pipe.
	cmd.Stdout = archive
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git archive %s: %s", revision, strings.TrimSpace(stderr.String()))
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind git archive: %w", err)
	}
	return extractTar(archive, destination)
}

func extractTar(reader io.Reader, destination string) error {
	archive := tar.NewReader(reader)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read git archive: %w", err)
		}
		relative := filepath.Clean(filepath.FromSlash(header.Name))
		if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("git archive contains unsafe path %q", header.Name)
		}
		target := filepath.Join(destination, relative)
		if err := extractTarEntry(archive, header, target); err != nil {
			return err
		}
	}
}

func extractTarEntry(archive io.Reader, header *tar.Header, target string) error {
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, archive)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	default:
		return nil
	}
}

func gitLines(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func gitText(root string, args ...string) (string, error) {
	lines, err := gitLines(root, args...)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}
