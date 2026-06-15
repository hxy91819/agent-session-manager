package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	providerRoot           = "internal/provider"
	sessionCacheImport     = "internal/sessioncache"
	cwdStatusImport        = "internal/cwdstatus"
	sessionCacheExemption  = "sessioncache: not required -"
	providerDiscoverMarker = "func (p Provider) Discover("
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	entries, err := os.ReadDir(providerRoot)
	if err != nil {
		return err
	}

	var failures []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		body, err := packageBody(filepath.Join(providerRoot, name))
		if err != nil {
			return err
		}
		if !strings.Contains(body, providerDiscoverMarker) {
			continue
		}
		if !strings.Contains(body, cwdStatusImport) {
			failures = append(failures, fmt.Sprintf("%s: provider discovery must use internal/cwdstatus for cwd metadata", name))
		}
		if !strings.Contains(body, sessionCacheImport) && !strings.Contains(body, sessionCacheExemption) {
			failures = append(failures, fmt.Sprintf("%s: provider discovery must use internal/sessioncache or declare %q with a reason", name, sessionCacheExemption))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("provider performance contract failed:\n- %s", strings.Join(failures, "\n- "))
	}
	return nil
}

func packageBody(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
