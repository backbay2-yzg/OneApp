package service

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"oneapp/internal/archive"
	"oneapp/internal/config"
	"oneapp/internal/domain"
	"oneapp/internal/platform"
	"oneapp/internal/storage"
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9\-_]+`)

// IngestService analyzes dropped/picked paths and ingests them into OneApp.
type IngestService struct {
	paths     *config.Paths
	settings  *config.SettingsStore
	repo      *storage.Repo
	bridge    platform.OSBridge
	onChanged func()
}

// NewIngestService constructs an IngestService.
func NewIngestService(
	paths *config.Paths,
	settings *config.SettingsStore,
	repo *storage.Repo,
	bridge platform.OSBridge,
	onChanged func(),
) *IngestService {
	return &IngestService{
		paths:     paths,
		settings:  settings,
		repo:      repo,
		bridge:    bridge,
		onChanged: onChanged,
	}
}

// AnalyzePaths inspects a list of dropped or selected local files/folders/archives.
func (s *IngestService) AnalyzePaths(rawPaths []string) ([]domain.IngestAnalysis, error) {
	cfg := s.settings.Get()
	defaultAction := domain.IngestAction(cfg.DefaultIngestMode)
	if defaultAction != domain.IngestCopy && defaultAction != domain.IngestMove && defaultAction != domain.IngestLink {
		defaultAction = domain.IngestCopy
	}

	analyses := make([]domain.IngestAnalysis, 0, len(rawPaths))
	for _, raw := range rawPaths {
		clean := filepath.Clean(strings.TrimSpace(raw))
		if clean == "" {
			continue
		}
		info, err := os.Stat(clean)
		if err != nil {
			analyses = append(analyses, domain.IngestAnalysis{
				SourcePath: clean,
				Error:      fmt.Sprintf("无法访问路径: %v", err),
			})
			continue
		}

		if info.IsDir() {
			analyses = append(analyses, s.analyzeDirectory(clean, defaultAction))
			continue
		}
		if archive.IsArchive(clean) {
			analyses = append(analyses, s.analyzeArchive(clean, info.Size()))
			continue
		}
		if strings.EqualFold(filepath.Ext(clean), ".lnk") {
			analyses = append(analyses, s.analyzeShortcut(clean, info.Size(), defaultAction))
			continue
		}
		analyses = append(analyses, s.analyzeSingleFile(clean, info.Size(), defaultAction))
	}
	return analyses, nil
}

func (s *IngestService) analyzeSingleFile(filePath string, sizeBytes int64, defaultAction domain.IngestAction) domain.IngestAnalysis {
	entryType, ok := DetectEntryType(filePath)
	if !ok {
		return domain.IngestAnalysis{
			SourcePath: filePath,
			Error:      fmt.Sprintf("不支持的文件类型: %s", filepath.Ext(filePath)),
		}
	}

	baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	suggestedName := baseName
	var version, desc string

	if entryType == domain.EntryExe {
		if meta, err := s.bridge.ReadExecutableMeta(filePath); err == nil {
			version = meta.Version
			desc = meta.Description
			if meta.ProductName != "" && !looksGenericProductName(meta.ProductName) {
				suggestedName = meta.ProductName
			}
		}
	}

	return domain.IngestAnalysis{
		SourcePath:      filePath,
		SourceKind:      "file",
		SuggestedName:   suggestedName,
		Version:         version,
		Description:     desc,
		SuggestedAction: defaultAction,
		CanCopy:         true,
		CanLink:         true,
		CanMove:         true,
		SizeBytes:       sizeBytes,
		Candidates: []domain.IngestCandidateEntry{
			{
				RelPath:   filepath.Base(filePath),
				Name:      "主程序",
				EntryType: entryType,
				Score:     100,
				IsPrimary: true,
			},
		},
	}
}

func (s *IngestService) analyzeShortcut(lnkPath string, sizeBytes int64, defaultAction domain.IngestAction) domain.IngestAnalysis {
	baseName := strings.TrimSuffix(filepath.Base(lnkPath), filepath.Ext(lnkPath))
	sc, err := s.bridge.ResolveShortcut(lnkPath)
	var version, desc, args, workDir string
	if err == nil && sc.TargetPath != "" {
		args = sc.Arguments
		workDir = sc.WorkingDir
		if meta, mErr := s.bridge.ReadExecutableMeta(sc.TargetPath); mErr == nil {
			version = meta.Version
			desc = meta.Description
		}
	}

	return domain.IngestAnalysis{
		SourcePath:      lnkPath,
		SourceKind:      "shortcut",
		SuggestedName:   baseName,
		Version:         version,
		Description:     desc,
		SuggestedAction: defaultAction,
		CanCopy:         true,
		CanLink:         true,
		CanMove:         true,
		SizeBytes:       sizeBytes,
		Candidates: []domain.IngestCandidateEntry{
			{
				RelPath:      filepath.Base(lnkPath),
				Name:         "主程序",
				EntryType:    domain.EntryLnk,
				ArgsTemplate: args,
				WorkingDir:   workDir,
				Score:        100,
				IsPrimary:    true,
			},
		},
	}
}

func (s *IngestService) analyzeArchive(archivePath string, sizeBytes int64) domain.IngestAnalysis {
	baseName := strings.TrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath))
	entries, uncompressedSize, err := archive.InspectArchive(archivePath)
	if err != nil {
		return domain.IngestAnalysis{
			SourcePath: archivePath,
			SourceKind: "archive",
			Error:      fmt.Sprintf("压缩包解析失败: %v", err),
		}
	}

	candidates := make([]domain.IngestCandidateEntry, 0)
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		depth := strings.Count(entry.RelPath, "/")
		if depth > 2 {
			continue
		}
		entryType, ok := DetectEntryType(entry.RelPath)
		if !ok {
			continue
		}
		score := ScoreCandidate(entry.RelPath, baseName, depth == 0)
		candidates = append(candidates, domain.IngestCandidateEntry{
			RelPath:   entry.RelPath,
			Name:      strings.TrimSuffix(filepath.Base(entry.RelPath), filepath.Ext(entry.RelPath)),
			EntryType: entryType,
			Score:     score,
		})
	}

	sortAndMarkPrimary(candidates)

	if uncompressedSize > 0 {
		sizeBytes = uncompressedSize
	}

	return domain.IngestAnalysis{
		SourcePath:      archivePath,
		SourceKind:      "archive",
		SuggestedName:   baseName,
		SuggestedAction: domain.IngestCopy,
		CanCopy:         true,
		CanLink:         false,
		CanMove:         false,
		SizeBytes:       sizeBytes,
		Candidates:      candidates,
	}
}

func (s *IngestService) analyzeDirectory(dirPath string, defaultAction domain.IngestAction) domain.IngestAnalysis {
	if proj, ok := analyzeNodeOrElectronProject(dirPath); ok {
		return proj
	}

	isSrcProj := isSourceProjectDir(dirPath)
	baseName := filepath.Base(dirPath)
	candidates := make([]domain.IngestCandidateEntry, 0)
	visited := 0
	var totalSize int64

	_ = filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dirPath, path)
		if err != nil || rel == "." {
			return nil
		}
		slashed := filepath.ToSlash(rel)
		depth := strings.Count(slashed, "/")

		if d.IsDir() {
			if skippedWalkDirs[strings.ToLower(d.Name())] || depth >= 2 || visited > 500 {
				return filepath.SkipDir
			}
			return nil
		}

		visited++
		if info, infoErr := d.Info(); infoErr == nil {
			totalSize += infoErrSize(info)
		}
		if depth > 2 {
			return nil
		}
		entryType, ok := DetectEntryType(slashed)
		if !ok {
			return nil
		}
		score := ScoreCandidate(slashed, baseName, depth == 0)
		candidates = append(candidates, domain.IngestCandidateEntry{
			RelPath:   slashed,
			Name:      strings.TrimSuffix(filepath.Base(slashed), filepath.Ext(slashed)),
			EntryType: entryType,
			Score:     score,
		})
		return nil
	})

	sortAndMarkPrimary(candidates)

	suggestedName := baseName
	var version, desc string
	if len(candidates) > 0 && candidates[0].EntryType == domain.EntryExe {
		primaryFull := filepath.Join(dirPath, filepath.FromSlash(candidates[0].RelPath))
		if meta, err := s.bridge.ReadExecutableMeta(primaryFull); err == nil {
			version = meta.Version
			desc = meta.Description
			if meta.ProductName != "" && !looksGenericProductName(meta.ProductName) {
				suggestedName = meta.ProductName
			}
		}
	}

	sourceKind := "dir"
	suggestedAction := defaultAction
	canCopy := true
	canMove := true
	projectKind := ""
	if isSrcProj {
		sourceKind = "project"
		suggestedAction = domain.IngestLink
		canCopy = false
		canMove = false
		projectKind = "custom"
	}

	return domain.IngestAnalysis{
		SourcePath:      dirPath,
		SourceKind:      sourceKind,
		SuggestedName:   suggestedName,
		Version:         version,
		Description:     desc,
		SuggestedAction: suggestedAction,
		CanCopy:         canCopy,
		CanLink:         true,
		CanMove:         canMove,
		SizeBytes:       totalSize,
		Candidates:      candidates,
		ProjectKind:     projectKind,
	}
}

func infoErrSize(info fs.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}

// CommitIngest persists one or more confirmed items into OneApp's library and SQLite database.
func (s *IngestService) CommitIngest(items []domain.IngestCommitItem) domain.IngestResult {
	res := domain.IngestResult{
		Apps:   make([]domain.AppOverview, 0, len(items)),
		Errors: make([]string, 0),
	}

	for _, item := range items {
		ov, err := s.commitSingle(item)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", filepath.Base(item.SourcePath), err))
			continue
		}
		res.Apps = append(res.Apps, *ov)
	}

	if len(res.Apps) > 0 && s.onChanged != nil {
		s.onChanged()
	}
	return res
}

func (s *IngestService) commitSingle(item domain.IngestCommitItem) (*domain.AppOverview, error) {
	src := filepath.Clean(strings.TrimSpace(item.SourcePath))
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}

	cfg := s.settings.Get()
	if err := s.paths.EnsureDirs(cfg); err != nil {
		return nil, err
	}

	appID := uuid.NewString()
	displayName := strings.TrimSpace(item.Name)
	if displayName == "" {
		displayName = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	slug := makeSlug(displayName)
	folderName := fmt.Sprintf("%s-%s", slug, appID[:6])

	action := item.Action
	if action == "" {
		action = domain.IngestAction(cfg.DefaultIngestMode)
	}
	if info.IsDir() && isSourceProjectDir(src) {
		if action != domain.IngestLink {
			return nil, fmt.Errorf("源码项目目录仅支持「仅链接原路径」模式，禁止复制或移动")
		}
	}

	var storageMode domain.StorageMode
	var rootPath string
	var absRoot string
	var manifest []domain.InstallManifestFile
	primaryRel := strings.TrimSpace(item.PrimaryPath)
	hasStructuredEntries := len(item.Entries) > 0

	isArch := !info.IsDir() && archive.IsArchive(src)

	switch {
	case isArch:
		storageMode = domain.StorageManaged
		rootPath = "apps/" + folderName
		absRoot = filepath.Join(s.paths.LibraryAppsDir(cfg), folderName)
		if err := archive.Extract(src, absRoot); err != nil {
			_ = os.RemoveAll(absRoot)
			return nil, fmt.Errorf("解压失败: %w", err)
		}
		if err := archive.UnwrapSingleRoot(absRoot); err != nil {
			_ = os.RemoveAll(absRoot)
			return nil, fmt.Errorf("目录规范化失败: %w", err)
		}
		manifest, err = archive.ComputeManifest(appID, absRoot)
		if err != nil {
			return nil, err
		}
		if primaryRel == "" && !hasStructuredEntries {
			analysis := s.analyzeDirectory(absRoot, domain.IngestCopy)
			if len(analysis.Candidates) > 0 {
				primaryRel = analysis.Candidates[0].RelPath
			}
		}

	case info.IsDir():
		if action == domain.IngestLink {
			storageMode = domain.StorageLinked
			rootPath = src
			absRoot = src
		} else {
			storageMode = domain.StorageManaged
			rootPath = "apps/" + folderName
			absRoot = filepath.Join(s.paths.LibraryAppsDir(cfg), folderName)
			if action == domain.IngestMove {
				if err := archive.MovePath(src, absRoot); err != nil {
					return nil, err
				}
			} else {
				if err := archive.CopyDir(src, absRoot); err != nil {
					_ = os.RemoveAll(absRoot)
					return nil, err
				}
			}
			_ = archive.UnwrapSingleRoot(absRoot)
			manifest, err = archive.ComputeManifest(appID, absRoot)
			if err != nil {
				return nil, err
			}
		}
		if primaryRel == "" && !hasStructuredEntries {
			analysis := s.analyzeDirectory(absRoot, domain.IngestCopy)
			if len(analysis.Candidates) > 0 {
				primaryRel = analysis.Candidates[0].RelPath
			}
		}

	default:
		// Single file (.exe / script / .lnk)
		fileName := filepath.Base(src)
		if primaryRel == "" && !hasStructuredEntries {
			primaryRel = fileName
		}
		if action == domain.IngestLink {
			storageMode = domain.StorageLinked
			rootPath = filepath.Dir(src)
			absRoot = rootPath
		} else {
			storageMode = domain.StorageManaged
			rootPath = "apps/" + folderName
			absRoot = filepath.Join(s.paths.LibraryAppsDir(cfg), folderName)
			dstFile := filepath.Join(absRoot, fileName)
			if action == domain.IngestMove {
				if err := archive.MovePath(src, dstFile); err != nil {
					return nil, err
				}
			} else {
				if err := archive.CopyFile(src, dstFile); err != nil {
					_ = os.RemoveAll(absRoot)
					return nil, err
				}
			}
			manifest, err = archive.ComputeManifest(appID, absRoot)
			if err != nil {
				return nil, err
			}
		}
	}

	var entries []domain.AppEntry
	if hasStructuredEntries {
		entries = buildEntriesFromInputs(appID, item.Entries)
	} else {
		if primaryRel == "" {
			return nil, fmt.Errorf("未能确定主启动入口")
		}
		primarySlashed := filepath.ToSlash(primaryRel)
		primaryType, ok := DetectEntryType(primarySlashed)
		if !ok {
			primaryType = domain.EntryExe
		}
		entries = make([]domain.AppEntry, 0, 1+len(item.ExtraPaths))
		entries = append(entries, domain.AppEntry{
			ID:           uuid.NewString(),
			AppID:        appID,
			Name:         "主程序",
			EntryType:    primaryType,
			RelPath:      primarySlashed,
			ArgsTemplate: strings.TrimSpace(item.ArgsTemplate),
			WorkingDir:   strings.TrimSpace(item.WorkingDir),
			EnvVars:      map[string]string{},
			RunAsAdmin:   item.RunAsAdmin,
			HideConsole:  item.HideConsole,
			IsPrimary:    true,
			SortOrder:    0,
		})

		seenRel := map[string]bool{strings.ToLower(primarySlashed): true}
		for _, extraRel := range item.ExtraPaths {
			slashed := filepath.ToSlash(strings.TrimSpace(extraRel))
			if slashed == "" || seenRel[strings.ToLower(slashed)] {
				continue
			}
			et, valid := DetectEntryType(slashed)
			if !valid {
				continue
			}
			seenRel[strings.ToLower(slashed)] = true
			entries = append(entries, domain.AppEntry{
				ID:        uuid.NewString(),
				AppID:     appID,
				Name:      strings.TrimSuffix(filepath.Base(slashed), filepath.Ext(slashed)),
				EntryType: et,
				RelPath:   slashed,
				EnvVars:   map[string]string{},
				IsPrimary: false,
				SortOrder: len(entries),
			})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("未能确定主启动入口")
	}

	var primaryEntry domain.AppEntry
	for _, e := range entries {
		if e.IsPrimary {
			primaryEntry = e
			break
		}
	}
	if primaryEntry.ID == "" {
		primaryEntry = entries[0]
	}

	// Resolve version/description and extract icon.
	version := strings.TrimSpace(item.Version)
	desc := strings.TrimSpace(item.Description)
	iconSource := strings.TrimSpace(item.IconSourcePath)

	if primaryEntry.EntryType != domain.EntryCommand {
		primaryFull := filepath.Join(absRoot, filepath.FromSlash(primaryEntry.RelPath))
		if iconSource == "" {
			iconSource = primaryFull
		}
		if primaryEntry.EntryType == domain.EntryLnk {
			if sc, scErr := s.bridge.ResolveShortcut(primaryFull); scErr == nil && sc.TargetPath != "" {
				if strings.TrimSpace(item.IconSourcePath) == "" {
					iconSource = sc.TargetPath
				}
				if version == "" || desc == "" {
					if meta, mErr := s.bridge.ReadExecutableMeta(sc.TargetPath); mErr == nil {
						if version == "" {
							version = meta.Version
						}
						if desc == "" {
							desc = meta.Description
						}
					}
				}
			}
		} else if primaryEntry.EntryType == domain.EntryExe && (version == "" || desc == "") {
			if meta, mErr := s.bridge.ReadExecutableMeta(primaryFull); mErr == nil {
				if version == "" {
					version = meta.Version
				}
				if desc == "" {
					desc = meta.Description
				}
			}
		}
	}

	iconRelPath := s.extractAppIcon(appID, iconSource)
	iconOut := filepath.Join(s.paths.IconsDir, appID+".png")

	now := time.Now().UnixMilli()
	app := &domain.App{
		ID:             appID,
		Slug:           slug,
		Name:           displayName,
		Aliases:        item.Aliases,
		Description:    desc,
		IconRelPath:    iconRelPath,
		CategoryID:     strings.TrimSpace(item.CategoryID),
		StorageMode:    storageMode,
		Status:         domain.StatusReady,
		RootPath:       rootPath,
		SourceType:     domain.SourceLocal,
		CurrentVersion: version,
		PersistRules:   []string{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.repo.CreateApp(app, entries, item.Tags, manifest); err != nil {
		if storageMode == domain.StorageManaged {
			_ = os.RemoveAll(absRoot)
		}
		if iconRelPath != "" {
			_ = os.Remove(iconOut)
		}
		return nil, err
	}

	return s.repo.GetApp(appID)
}

func buildEntriesFromInputs(appID string, inputs []domain.EntryInput) []domain.AppEntry {
	entries := make([]domain.AppEntry, 0, len(inputs))
	hasPrimary := false
	for _, in := range inputs {
		et := in.EntryType
		rawPath := strings.TrimSpace(in.RelPath)
		if rawPath == "" {
			continue
		}
		if et == "" {
			if detected, ok := DetectEntryType(rawPath); ok {
				et = detected
			} else {
				et = domain.EntryExe
			}
		}
		relPath := rawPath
		if et != domain.EntryCommand {
			relPath = filepath.ToSlash(rawPath)
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			if et == domain.EntryCommand {
				name = relPath
			} else {
				name = strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
			}
		}
		envVars := in.EnvVars
		if envVars == nil {
			envVars = map[string]string{}
		}
		if in.IsPrimary {
			hasPrimary = true
		}
		entries = append(entries, domain.AppEntry{
			ID:                 uuid.NewString(),
			AppID:              appID,
			Name:               name,
			EntryType:          et,
			RelPath:            relPath,
			ArgsTemplate:       strings.TrimSpace(in.ArgsTemplate),
			WorkingDir:         strings.TrimSpace(in.WorkingDir),
			EnvVars:            envVars,
			RunAsAdmin:         in.RunAsAdmin,
			HideConsole:        in.HideConsole,
			PromptArgs:         in.PromptArgs,
			IsPrimary:          in.IsPrimary,
			SortOrder:          len(entries),
			PreLaunchCommand:   strings.TrimSpace(in.PreLaunchCommand),
			PreLaunchCondition: normalizePreLaunchCondition(in.PreLaunchCondition),
			ReadyCheckPath:     strings.TrimSpace(in.ReadyCheckPath),
		})
	}
	if len(entries) > 0 && !hasPrimary {
		entries[0].IsPrimary = true
	}
	return entries
}

func normalizePreLaunchCondition(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "if_missing", "always":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func (s *IngestService) extractAppIcon(appID, iconSource string) string {
	iconSource = strings.TrimSpace(iconSource)
	if iconSource == "" {
		return ""
	}
	iconOut := filepath.Join(s.paths.IconsDir, appID+".png")
	if strings.EqualFold(filepath.Ext(iconSource), ".png") {
		if err := archive.CopyFile(iconSource, iconOut); err == nil {
			if _, statErr := os.Stat(iconOut); statErr == nil {
				return appID + ".png"
			}
		}
		return ""
	}
	if err := s.bridge.ExtractIcon(iconSource, iconOut); err == nil {
		if _, statErr := os.Stat(iconOut); statErr == nil {
			return appID + ".png"
		}
	}
	return ""
}

// CreateCommandApp directly creates a linked project/command application without scanning executables.
func (s *IngestService) CreateCommandApp(req domain.CreateCommandAppRequest) (*domain.AppOverview, error) {
	cmdText := strings.TrimSpace(req.Command)
	if cmdText == "" {
		return nil, fmt.Errorf("启动命令不能为空")
	}

	rootPath := strings.TrimSpace(req.RootPath)
	workDir := strings.TrimSpace(req.WorkingDir)
	if rootPath == "" {
		rootPath = workDir
	}
	if rootPath == "" {
		return nil, fmt.Errorf("请选择项目目录或工作目录")
	}
	cleanRoot := filepath.Clean(rootPath)
	info, err := os.Stat(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("无法访问项目目录: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("项目路径必须是文件夹")
	}

	cfg := s.settings.Get()
	if err := s.paths.EnsureDirs(cfg); err != nil {
		return nil, err
	}

	displayName := strings.TrimSpace(req.Name)
	if displayName == "" {
		displayName = filepath.Base(cleanRoot)
	}
	if displayName == "" {
		displayName = "未命名项目"
	}

	appID := uuid.NewString()
	slug := makeSlug(displayName)
	if workDir == "" {
		workDir = "{{APP_DIR}}"
	}

	var iconSource string
	if proj, ok := analyzeNodeOrElectronProject(cleanRoot); ok {
		iconSource = proj.IconSourcePath
	} else {
		iconSource = discoverProjectIcon(cleanRoot, packageJSON{}, nil, "", false)
	}
	iconRelPath := s.extractAppIcon(appID, iconSource)

	entry := domain.AppEntry{
		ID:                 uuid.NewString(),
		AppID:              appID,
		Name:               "启动命令",
		EntryType:          domain.EntryCommand,
		RelPath:            cmdText,
		ArgsTemplate:       strings.TrimSpace(req.ArgsTemplate),
		WorkingDir:         workDir,
		EnvVars:            map[string]string{},
		RunAsAdmin:         req.RunAsAdmin,
		HideConsole:        req.HideConsole,
		IsPrimary:          true,
		SortOrder:          0,
		PreLaunchCommand:   strings.TrimSpace(req.PreLaunchCommand),
		PreLaunchCondition: normalizePreLaunchCondition(req.PreLaunchCondition),
		ReadyCheckPath:     strings.TrimSpace(req.ReadyCheckPath),
	}

	now := time.Now().UnixMilli()
	app := &domain.App{
		ID:           appID,
		Slug:         slug,
		Name:         displayName,
		Aliases:      req.Aliases,
		Description:  strings.TrimSpace(req.Description),
		IconRelPath:  iconRelPath,
		CategoryID:   strings.TrimSpace(req.CategoryID),
		StorageMode:  domain.StorageLinked,
		Status:       domain.StatusReady,
		RootPath:     cleanRoot,
		SourceType:   domain.SourceLocal,
		PersistRules: []string{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.repo.CreateApp(app, []domain.AppEntry{entry}, req.Tags, nil); err != nil {
		if iconRelPath != "" {
			_ = os.Remove(filepath.Join(s.paths.IconsDir, iconRelPath))
		}
		return nil, err
	}

	if s.onChanged != nil {
		s.onChanged()
	}
	return s.repo.GetApp(appID)
}

// DetectEntryType maps file extension to a supported EntryType.
func DetectEntryType(path string) (domain.EntryType, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe":
		return domain.EntryExe, true
	case ".bat":
		return domain.EntryBat, true
	case ".cmd":
		return domain.EntryCmd, true
	case ".ps1":
		return domain.EntryPs1, true
	case ".lnk":
		return domain.EntryLnk, true
	default:
		return "", false
	}
}

// ScoreCandidate assigns a heuristic priority score to potential entry executables/scripts.
func ScoreCandidate(relPath string, appBaseName string, isRoot bool) int {
	base := filepath.Base(relPath)
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.ToLower(strings.TrimSuffix(base, ext))
	targetStem := strings.ToLower(strings.TrimSpace(appBaseName))
	lowerRel := strings.ToLower(relPath)

	score := 0
	switch ext {
	case ".exe":
		score += 40
	case ".lnk":
		score += 35
	case ".ps1", ".bat", ".cmd":
		score += 20
	}

	if isRoot {
		score += 30
	} else {
		depth := strings.Count(lowerRel, "/")
		score -= depth * 12
	}

	if targetStem != "" {
		switch {
		case stem == targetStem:
			score += 60
		case strings.HasPrefix(targetStem, stem) || strings.HasPrefix(stem, targetStem):
			score += 35
		case strings.Contains(targetStem, stem) || strings.Contains(stem, targetStem):
			score += 20
		}
	}

	if strings.Contains(stem, "gui") || strings.Contains(stem, "launcher") {
		score += 10
	}

	strongBad := []string{"unins", "uninstall", "updater", "crash", "reporter", "vcredist", "vc_redist", "dxsetup", "dotnetfx", "createdump"}
	for _, bad := range strongBad {
		if strings.Contains(stem, bad) {
			score -= 500
			break
		}
	}

	mildBad := []string{"helper", "service", "daemon", "update", "setup", "elevate"}
	for _, bad := range mildBad {
		if strings.Contains(stem, bad) {
			score -= 150
			break
		}
	}

	return score
}

func sortAndMarkPrimary(candidates []domain.IngestCandidateEntry) {
	if len(candidates) == 0 {
		return
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].RelPath < candidates[j].RelPath
	})
	candidates[0].IsPrimary = true
}

func makeSlug(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	var ascii strings.Builder
	for _, r := range lower {
		if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			ascii.WriteRune(r)
		} else if unicode.IsSpace(r) {
			ascii.WriteRune('-')
		}
	}
	cleaned := nonSlugChars.ReplaceAllString(ascii.String(), "-")
	cleaned = strings.Trim(cleaned, "-_")
	if cleaned == "" {
		return "app"
	}
	if len(cleaned) > 28 {
		cleaned = cleaned[:28]
	}
	return cleaned
}

func looksGenericProductName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "" || lower == "application" || lower == "todo" || lower == "my application" || lower == "microsoft"
}
