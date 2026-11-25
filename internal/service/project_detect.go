package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"oneapp/internal/domain"
)

type builderConfig struct {
	ProductName string `json:"productName" yaml:"productName"`
	Icon        string `json:"icon" yaml:"icon"`
	Directories struct {
		BuildResources string `json:"buildResources" yaml:"buildResources"`
	} `json:"directories" yaml:"directories"`
	Win struct {
		Icon string `json:"icon" yaml:"icon"`
	} `json:"win" yaml:"win"`
}

type packageJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description"`
	Main            string            `json:"main"`
	PackageManager  string            `json:"packageManager"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Build           *builderConfig    `json:"build"`
}

var sourceProjectMarkers = []string{
	"package.json",
	"node_modules",
	".git",
	"pyproject.toml",
	"go.mod",
	"Cargo.toml",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"composer.json",
}

var skippedWalkDirs = map[string]bool{
	"node_modules":   true,
	".git":           true,
	".venv":          true,
	"venv":           true,
	"target":         true,
	"__pycache__":    true,
	".next":          true,
	".nuxt":          true,
	"coverage":       true,
	"coverage-integ": true,
	".vite":          true,
	".turbo":         true,
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isSourceProjectDir(dirPath string) bool {
	for _, marker := range sourceProjectMarkers {
		if _, err := os.Stat(filepath.Join(dirPath, marker)); err == nil {
			return true
		}
	}
	return false
}

func readBuilderConfig(dirPath string) *builderConfig {
	for _, name := range []string{"electron-builder.yml", "electron-builder.yaml"} {
		data, err := os.ReadFile(filepath.Join(dirPath, name))
		if err == nil {
			var cfg builderConfig
			if yaml.Unmarshal(data, &cfg) == nil {
				return &cfg
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(dirPath, "electron-builder.json"))
	if err == nil {
		var cfg builderConfig
		if json.Unmarshal(data, &cfg) == nil {
			return &cfg
		}
	}
	return nil
}

func detectPackageManager(dirPath string, pkg packageJSON) string {
	pmField := strings.ToLower(strings.TrimSpace(pkg.PackageManager))
	switch {
	case strings.HasPrefix(pmField, "pnpm@") || pmField == "pnpm":
		return "pnpm"
	case strings.HasPrefix(pmField, "yarn@") || pmField == "yarn":
		return "yarn"
	case strings.HasPrefix(pmField, "bun@") || pmField == "bun":
		return "bun"
	case strings.HasPrefix(pmField, "npm@") || pmField == "npm":
		return "npm"
	}

	searchDirs := []string{dirPath}
	if parent := filepath.Dir(dirPath); parent != "" && parent != dirPath {
		searchDirs = append(searchDirs, parent)
	}
	for _, dir := range searchDirs {
		if fileExists(filepath.Join(dir, "pnpm-lock.yaml")) {
			return "pnpm"
		}
		if fileExists(filepath.Join(dir, "yarn.lock")) {
			return "yarn"
		}
		if fileExists(filepath.Join(dir, "bun.lock")) || fileExists(filepath.Join(dir, "bun.lockb")) {
			return "bun"
		}
		if fileExists(filepath.Join(dir, "package-lock.json")) {
			return "npm"
		}
	}
	return "npm"
}

func pmRunCmd(pm, script string) string {
	return fmt.Sprintf("%s run %s", pm, script)
}

func hasAnyDep(pkg packageJSON, names ...string) bool {
	for _, n := range names {
		if _, ok := pkg.Dependencies[n]; ok {
			return true
		}
		if _, ok := pkg.DevDependencies[n]; ok {
			return true
		}
	}
	return false
}

func locateElectronExe(dirPath string) (relPath string, absPath string, exists bool) {
	localAbs := filepath.Join(dirPath, "node_modules", "electron", "dist", "electron.exe")
	if fileExists(localAbs) {
		return "node_modules/electron/dist/electron.exe", localAbs, true
	}
	parent := filepath.Dir(dirPath)
	if parent != "" && parent != dirPath {
		parentAbs := filepath.Join(parent, "node_modules", "electron", "dist", "electron.exe")
		if fileExists(parentAbs) {
			return "../node_modules/electron/dist/electron.exe", parentAbs, true
		}
	}
	return "node_modules/electron/dist/electron.exe", localAbs, false
}

func isElectronProject(dirPath string, pkg packageJSON, builder *builderConfig) bool {
	if builder != nil || pkg.Build != nil {
		return true
	}
	if hasAnyDep(pkg, "electron", "electron-vite", "electron-builder", "@electron-forge/cli", "electron-forge") {
		return true
	}
	_, _, exists := locateElectronExe(dirPath)
	return exists
}

func isGenericProjectFolderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "app", "client", "desktop", "frontend", "web", "src", "packages":
		return true
	default:
		return false
	}
}

func suggestProjectName(dirPath string, pkg packageJSON, builder *builderConfig) string {
	if builder != nil && strings.TrimSpace(builder.ProductName) != "" {
		return strings.TrimSpace(builder.ProductName)
	}
	if pkg.Build != nil && strings.TrimSpace(pkg.Build.ProductName) != "" {
		return strings.TrimSpace(pkg.Build.ProductName)
	}
	if name := strings.TrimSpace(pkg.Name); name != "" {
		if strings.HasPrefix(name, "@") {
			if _, rest, ok := strings.Cut(name, "/"); ok && strings.TrimSpace(rest) != "" {
				return strings.TrimSpace(rest)
			}
		}
		return name
	}
	base := filepath.Base(dirPath)
	if isGenericProjectFolderName(base) {
		parent := filepath.Base(filepath.Dir(dirPath))
		if parent != "" && parent != "." && parent != string(filepath.Separator) && !strings.HasSuffix(parent, ":") {
			return parent
		}
	}
	return base
}

func normalizeProjectRelPath(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	cleaned = strings.ReplaceAll(cleaned, `\`, `/`)
	cleaned = strings.TrimPrefix(cleaned, "./")
	return cleaned
}

func discoverProjectIcon(dirPath string, pkg packageJSON, builder *builderConfig, electronAbs string, electronExists bool) string {
	var explicit []string
	var buildResDir string
	if builder != nil {
		buildResDir = strings.TrimSpace(builder.Directories.BuildResources)
		explicit = append(explicit, builder.Win.Icon, builder.Icon)
	}
	if pkg.Build != nil {
		if buildResDir == "" {
			buildResDir = strings.TrimSpace(pkg.Build.Directories.BuildResources)
		}
		explicit = append(explicit, pkg.Build.Win.Icon, pkg.Build.Icon)
	}

	for _, rel := range explicit {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		candidates := []string{
			filepath.Join(dirPath, filepath.FromSlash(rel)),
		}
		if buildResDir != "" {
			candidates = append(candidates, filepath.Join(dirPath, filepath.FromSlash(buildResDir), filepath.FromSlash(rel)))
		}
		for _, c := range candidates {
			if fileExists(c) && isSupportedIconFile(c) {
				return c
			}
		}
	}

	dirs := []string{}
	if buildResDir != "" {
		dirs = append(dirs, filepath.FromSlash(buildResDir))
	}
	dirs = append(dirs, "build", "resources", "assets", "public", filepath.Join("src", "assets"), ".")
	names := []string{"icon.ico", "icon.png", "app.ico", "app.png", "logo.ico", "logo.png"}
	seenDir := make(map[string]bool)
	for _, d := range dirs {
		if seenDir[d] {
			continue
		}
		seenDir[d] = true
		for _, n := range names {
			full := filepath.Join(dirPath, d, n)
			if fileExists(full) {
				return full
			}
		}
	}

	if electronExists && fileExists(electronAbs) {
		return electronAbs
	}
	return ""
}

func isSupportedIconFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".ico" || ext == ".png" || ext == ".exe"
}

func analyzeNodeOrElectronProject(dirPath string) (domain.IngestAnalysis, bool) {
	pkgPath := filepath.Join(dirPath, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return domain.IngestAnalysis{}, false
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return domain.IngestAnalysis{}, false
	}

	builder := readBuilderConfig(dirPath)
	pm := detectPackageManager(dirPath, pkg)
	isElectron := isElectronProject(dirPath, pkg, builder)
	suggestedName := suggestProjectName(dirPath, pkg, builder)

	electronRel, electronAbs, electronExists := locateElectronExe(dirPath)
	iconSource := discoverProjectIcon(dirPath, pkg, builder, electronAbs, electronExists)

	if isElectron {
		candidates := make([]domain.IngestCandidateEntry, 0, 5)
		mainEntry := normalizeProjectRelPath(pkg.Main)
		if mainEntry == "" && fileExists(filepath.Join(dirPath, "out", "main", "index.js")) {
			mainEntry = "out/main/index.js"
		}

		_, hasBuild := pkg.Scripts["build"]
		_, hasDev := pkg.Scripts["dev"]
		_, hasPreview := pkg.Scripts["preview"]
		_, hasStart := pkg.Scripts["start"]

		buildCmd := ""
		if hasBuild {
			buildCmd = pmRunCmd(pm, "build")
		}

		primary := domain.IngestCandidateEntry{
			RelPath:         electronRel,
			Name:            "直接启动 (Electron)",
			EntryType:       domain.EntryExe,
			ArgsTemplate:    ".",
			WorkingDir:      "{{APP_DIR}}",
			Score:           200,
			IsPrimary:       true,
			HideConsole:     true,
			DefaultSelected: true,
			Note:            "免打包直接运行本地 Electron 构建产物",
		}
		if buildCmd != "" && mainEntry != "" {
			primary.PreLaunchCommand = buildCmd
			primary.PreLaunchCondition = "if_missing"
			primary.ReadyCheckPath = mainEntry
			primary.Note = fmt.Sprintf("免打包秒开；若 %s 缺失则自动执行 %s", mainEntry, buildCmd)
		}
		candidates = append(candidates, primary)

		if buildCmd != "" {
			candidates = append(candidates, domain.IngestCandidateEntry{
				RelPath:            electronRel,
				Name:               "重新构建并启动",
				EntryType:          domain.EntryExe,
				ArgsTemplate:       ".",
				WorkingDir:         "{{APP_DIR}}",
				Score:              160,
				HideConsole:        true,
				DefaultSelected:    true,
				PreLaunchCommand:   buildCmd,
				PreLaunchCondition: "always",
				ReadyCheckPath:     mainEntry,
				Note:               fmt.Sprintf("先执行 %s，成功后重启运行新版", buildCmd),
			})
		}

		if hasDev {
			candidates = append(candidates, domain.IngestCandidateEntry{
				RelPath:         pmRunCmd(pm, "dev"),
				Name:            "开发模式 (dev)",
				EntryType:       domain.EntryCommand,
				WorkingDir:      "{{APP_DIR}}",
				Score:           150,
				HideConsole:     true,
				DefaultSelected: true,
				Note:            "热更新开发模式",
			})
		}

		if hasPreview {
			candidates = append(candidates, domain.IngestCandidateEntry{
				RelPath:         pmRunCmd(pm, "preview"),
				Name:            "预览模式 (preview)",
				EntryType:       domain.EntryCommand,
				WorkingDir:      "{{APP_DIR}}",
				Score:           120,
				HideConsole:     true,
				DefaultSelected: false,
				Note:            "通过包管理器预览构建产物",
			})
		}

		if hasStart {
			candidates = append(candidates, domain.IngestCandidateEntry{
				RelPath:         pmRunCmd(pm, "start"),
				Name:            "标准启动 (start)",
				EntryType:       domain.EntryCommand,
				WorkingDir:      "{{APP_DIR}}",
				Score:           110,
				HideConsole:     true,
				DefaultSelected: false,
				Note:            "通过包管理器运行 start 脚本",
			})
		}

		warning := ""
		if !electronExists {
			warning = fmt.Sprintf("未在本地找到 %s，请先在项目目录执行 %s install", electronRel, pm)
		}

		return domain.IngestAnalysis{
			SourcePath:      dirPath,
			SourceKind:      "project",
			SuggestedName:   suggestedName,
			Version:         strings.TrimSpace(pkg.Version),
			Description:     strings.TrimSpace(pkg.Description),
			SuggestedAction: domain.IngestLink,
			CanCopy:         false,
			CanLink:         true,
			CanMove:         false,
			SizeBytes:       0,
			Candidates:      candidates,
			ProjectKind:     "electron",
			ProjectWarning:  warning,
			IconSourcePath:  iconSource,
		}, true
	}

	// General Node project: extract common dev/start/preview/serve scripts.
	preferredScripts := []struct {
		key   string
		label string
		score int
	}{
		{"dev", "开发模式 (dev)", 180},
		{"start", "启动服务 (start)", 170},
		{"preview", "预览模式 (preview)", 140},
		{"serve", "本地服务 (serve)", 130},
	}

	candidates := make([]domain.IngestCandidateEntry, 0, 4)
	seenScript := make(map[string]bool)
	for _, spec := range preferredScripts {
		if _, ok := pkg.Scripts[spec.key]; !ok {
			continue
		}
		seenScript[spec.key] = true
		candidates = append(candidates, domain.IngestCandidateEntry{
			RelPath:         pmRunCmd(pm, spec.key),
			Name:            spec.label,
			EntryType:       domain.EntryCommand,
			WorkingDir:      "{{APP_DIR}}",
			Score:           spec.score,
			IsPrimary:       len(candidates) == 0,
			HideConsole:     true,
			DefaultSelected: true,
			Note:            fmt.Sprintf("执行 %s", pmRunCmd(pm, spec.key)),
		})
	}

	if len(candidates) == 0 && len(pkg.Scripts) > 0 {
		otherKeys := make([]string, 0, len(pkg.Scripts))
		for k := range pkg.Scripts {
			if seenScript[k] {
				continue
			}
			lk := strings.ToLower(k)
			if strings.HasPrefix(lk, "test") || strings.HasPrefix(lk, "lint") || lk == "prepare" || lk == "typecheck" || lk == "format" {
				continue
			}
			otherKeys = append(otherKeys, k)
		}
		sort.Strings(otherKeys)
		for i, k := range otherKeys {
			if i >= 4 {
				break
			}
			candidates = append(candidates, domain.IngestCandidateEntry{
				RelPath:         pmRunCmd(pm, k),
				Name:            fmt.Sprintf("脚本 (%s)", k),
				EntryType:       domain.EntryCommand,
				WorkingDir:      "{{APP_DIR}}",
				Score:           100 - i*5,
				IsPrimary:       i == 0,
				HideConsole:     true,
				DefaultSelected: i == 0,
				Note:            fmt.Sprintf("执行 %s", pmRunCmd(pm, k)),
			})
		}
	}

	return domain.IngestAnalysis{
		SourcePath:      dirPath,
		SourceKind:      "project",
		SuggestedName:   suggestedName,
		Version:         strings.TrimSpace(pkg.Version),
		Description:     strings.TrimSpace(pkg.Description),
		SuggestedAction: domain.IngestLink,
		CanCopy:         false,
		CanLink:         true,
		CanMove:         false,
		SizeBytes:       0,
		Candidates:      candidates,
		ProjectKind:     "node",
		IconSourcePath:  iconSource,
	}, true
}
