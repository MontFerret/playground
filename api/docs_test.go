package api_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLocalDocumentationLinks(t *testing.T) {
	link := regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	for _, root := range []string{"../README.md", "../AGENTS.md", "../docs"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}

			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			for _, match := range link.FindAllStringSubmatch(string(body), -1) {
				target, _, _ := strings.Cut(match[1], "#")
				if target == "" || strings.Contains(target, "://") {
					continue
				}

				if _, err := os.Stat(filepath.Join(filepath.Dir(path), target)); err != nil {
					t.Errorf("%s: broken local link %s: %v", path, match[1], err)
				}
			}

			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}
}
