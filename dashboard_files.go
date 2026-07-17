package skillmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const dashboardFilePreviewLimit int64 = 512 * 1024

type DashboardFileEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size,omitempty"`
}

type DashboardSkillFiles struct {
	Files []DashboardFileEntry `json:"files"`
}

type DashboardFileContent struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Language    string `json:"language"`
	Preview     bool   `json:"preview"`
	Content     string `json:"content,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

func listDashboardSkillFiles(repo, id string) (DashboardSkillFiles, error) {
	root, err := dashboardSkillRoot(repo, id)
	if err != nil {
		return DashboardSkillFiles{}, err
	}
	result := DashboardSkillFiles{Files: []DashboardFileEntry{}}
	err = filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		item := DashboardFileEntry{Path: filepath.ToSlash(relative), Directory: entry.IsDir()}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			item.Size = info.Size()
		}
		result.Files = append(result.Files, item)
		return nil
	})
	if err != nil {
		return DashboardSkillFiles{}, err
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	return result, nil
}

func readDashboardSkillFile(repo, id, relative string) (DashboardFileContent, error) {
	root, err := dashboardSkillRoot(repo, id)
	if err != nil {
		return DashboardFileContent{}, err
	}
	name, clean, err := dashboardSkillFilePath(root, relative)
	if err != nil {
		return DashboardFileContent{}, err
	}
	info, err := os.Stat(name)
	if err != nil {
		return DashboardFileContent{}, fmt.Errorf("read skill file %q: %w", clean, err)
	}
	if !info.Mode().IsRegular() {
		return DashboardFileContent{}, fmt.Errorf("skill path %q is not a regular file", clean)
	}
	result := DashboardFileContent{Path: clean, Size: info.Size(), Language: dashboardFileLanguage(clean), Preview: true}
	if info.Size() > dashboardFilePreviewLimit {
		result.Preview = false
		result.Unavailable = "文件超过 512 KB，无法在线预览"
		return result, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return DashboardFileContent{}, fmt.Errorf("read skill file %q: %w", clean, err)
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		result.Preview = false
		result.Unavailable = "二进制文件无法在线预览"
		return result, nil
	}
	result.Content = string(data)
	return result, nil
}

func dashboardSkillRoot(repo, id string) (string, error) {
	if err := validateID(id); err != nil {
		return "", fmt.Errorf("invalid skill: %w", err)
	}
	root := filepath.Join(repo, "skills", id)
	if err := validateCanonicalSkill(root); err != nil {
		return "", fmt.Errorf("skill %s: %w", id, err)
	}
	return root, nil
}

func dashboardSkillFilePath(root, relative string) (string, string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("skill file path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("skill file path escapes skill root")
	}
	name := filepath.Join(root, clean)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", "", fmt.Errorf("resolve skill file %q: %w", filepath.ToSlash(clean), err)
	}
	if !within(resolvedRoot, resolved) {
		return "", "", fmt.Errorf("skill file path escapes skill root")
	}
	return resolved, filepath.ToSlash(clean), nil
}

func dashboardFileLanguage(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".mdx":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".css":
		return "css"
	case ".html", ".xml", ".svg":
		return "xml"
	case ".py":
		return "python"
	case ".toml":
		return "ini"
	default:
		return "text"
	}
}
