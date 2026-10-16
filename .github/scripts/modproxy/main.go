// Command modproxy publishes one module of this repository into a local file
// module proxy, exactly as the public module proxy would serve it for a tag.
//
// The release workflow uses it to compute go.sum entries for internal module
// versions before those versions are tagged: the zip is built from the files
// of the module directory (tracked and not ignored, so rewritten go.mod and
// go.sum files are included) with the same rules the public proxy applies to a
// subdirectory module, including the repository LICENSE fallback.
//
// It is built from a temporary module so the repository itself needs no tool
// dependency; see seal-module-sums.sh.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/zip"
)

func main() {
	repo := flag.String("repo", "", "repository root")
	dir := flag.String("dir", "", "module directory relative to the repository root (\".\" for the root module)")
	path := flag.String("path", "", "module path")
	version := flag.String("version", "", "module version, for example v1.2.3")
	proxy := flag.String("proxy", "", "proxy directory to write to")
	flag.Parse()
	if *repo == "" || *dir == "" || *path == "" || *version == "" || *proxy == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*repo, *dir, *path, *version, *proxy); err != nil {
		fmt.Fprintf(os.Stderr, "modproxy: %s@%s: %v\n", *path, *version, err)
		os.Exit(1)
	}
}

func run(repo, dir, path, version, proxy string) error {
	mv := module.Version{Path: path, Version: version}
	if err := module.Check(path, version); err != nil {
		return err
	}
	escPath, err := module.EscapePath(path)
	if err != nil {
		return err
	}
	escVersion, err := module.EscapeVersion(version)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "modproxy-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := stageModule(repo, dir, stage); err != nil {
		return err
	}
	goMod, err := os.ReadFile(filepath.Join(stage, "go.mod"))
	if err != nil {
		return err
	}
	versionDir := filepath.Join(proxy, filepath.FromSlash(escPath), "@v")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return err
	}
	zipFile, err := os.Create(filepath.Join(versionDir, escVersion+".zip"))
	if err != nil {
		return err
	}
	if err := zip.CreateFromDir(zipFile, mv, stage); err != nil {
		zipFile.Close()
		return err
	}
	if err := zipFile.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(versionDir, escVersion+".mod"), goMod, 0o644); err != nil {
		return err
	}
	info := fmt.Sprintf("{\"Version\":%q,\"Time\":%q}\n", version, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(versionDir, escVersion+".info"), []byte(info), 0o644); err != nil {
		return err
	}
	return appendVersion(filepath.Join(versionDir, "list"), version)
}

// stageModule copies the files that belong to the module into stage.
func stageModule(repo, dir, stage string) error {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", dir)
	cmd.Dir = repo
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	prefix := ""
	if dir != "." {
		prefix = strings.TrimSuffix(filepath.ToSlash(dir), "/") + "/"
	}
	for _, name := range strings.Split(out.String(), "\x00") {
		if name == "" {
			continue
		}
		src := filepath.Join(repo, filepath.FromSlash(name))
		info, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue // deleted in the working tree
			}
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if err := copyFile(src, filepath.Join(stage, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	// The public proxy adds the repository LICENSE to a subdirectory module
	// that has none of its own.
	if dir != "." {
		license := filepath.Join(stage, "LICENSE")
		if _, err := os.Stat(license); os.IsNotExist(err) {
			if err := copyFile(filepath.Join(repo, "LICENSE"), license); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func appendVersion(listFile, version string) error {
	var versions []string
	if data, err := os.ReadFile(listFile); err == nil {
		versions = strings.Fields(string(data))
	}
	for _, v := range versions {
		if v == version {
			return nil
		}
	}
	versions = append(versions, version)
	sort.Strings(versions)
	return os.WriteFile(listFile, []byte(strings.Join(versions, "\n")+"\n"), 0o644)
}
