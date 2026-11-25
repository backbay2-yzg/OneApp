package domain

// StorageMode determines whether OneApp manages files inside its library or links to an external path.
type StorageMode string

const (
	StorageManaged StorageMode = "managed"
	StorageLinked  StorageMode = "linked"
)

// AppStatus represents the file availability state of an application.
type AppStatus string

const (
	StatusReady        AppStatus = "ready"
	StatusPendingFiles AppStatus = "pending_files"
	StatusMissing      AppStatus = "missing"
)

// SourceType identifies how an application was obtained.
type SourceType string

const (
	SourceLocal     SourceType = "local"
	SourceGitHub    SourceType = "github"
	SourceDirectURL SourceType = "direct_url"
)

// EntryType identifies how a launchable entry is executed.
type EntryType string

const (
	EntryExe     EntryType = "exe"
	EntryBat     EntryType = "bat"
	EntryCmd     EntryType = "cmd"
	EntryPs1     EntryType = "ps1"
	EntryLnk     EntryType = "lnk"
	EntryCommand EntryType = "command"
)

// IngestAction defines how an ingested item's files are stored.
type IngestAction string

const (
	IngestCopy IngestAction = "copy"
	IngestMove IngestAction = "move"
	IngestLink IngestAction = "link"
)

// App represents a stored portable software record.
type App struct {
	ID                string      `json:"id"`
	Slug              string      `json:"slug"`
	Name              string      `json:"name"`
	Aliases           []string    `json:"aliases"`
	Description       string      `json:"description"`
	NotesMD           string      `json:"notesMd"`
	IconRelPath       string      `json:"iconRelPath"`
	CategoryID        string      `json:"categoryId"`
	IsPinned          bool        `json:"isPinned"`
	Hotkey            string      `json:"hotkey"`
	StorageMode       StorageMode `json:"storageMode"`
	Status            AppStatus   `json:"status"`
	RootPath          string      `json:"rootPath"`
	SourceType        SourceType  `json:"sourceType"`
	SourceURL         string      `json:"sourceUrl"`
	AssetPattern      string      `json:"assetPattern"`
	IncludePrerelease bool        `json:"includePrerelease"`
	CurrentVersion    string      `json:"currentVersion"`
	LatestVersion     string      `json:"latestVersion"`
	RemoteETag        string      `json:"remoteEtag"`
	PersistRules      []string    `json:"persistRules"`
	LaunchCount       int64       `json:"launchCount"`
	LastLaunchedAt    int64       `json:"lastLaunchedAt"`
	LastCheckedAt     int64       `json:"lastCheckedAt"`
	CreatedAt         int64       `json:"createdAt"`
	UpdatedAt         int64       `json:"updatedAt"`
}

// AppEntry represents a single executable, script, or command entry inside an App.
type AppEntry struct {
	ID                 string            `json:"id"`
	AppID              string            `json:"appId"`
	Name               string            `json:"name"`
	EntryType          EntryType         `json:"entryType"`
	RelPath            string            `json:"relPath"`
	ArgsTemplate       string            `json:"argsTemplate"`
	WorkingDir         string            `json:"workingDir"`
	EnvVars            map[string]string `json:"envVars"`
	RunAsAdmin         bool              `json:"runAsAdmin"`
	HideConsole        bool              `json:"hideConsole"`
	PromptArgs         bool              `json:"promptArgs"`
	IsPrimary          bool              `json:"isPrimary"`
	SortOrder          int               `json:"sortOrder"`
	PreLaunchCommand   string            `json:"preLaunchCommand"`
	PreLaunchCondition string            `json:"preLaunchCondition"` // "" | "if_missing" | "always"
	ReadyCheckPath     string            `json:"readyCheckPath"`
}

// Category groups applications in the sidebar.
type Category struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
}

// Tag labels applications across categories.
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// InstallManifestFile records a packaged file's baseline hash for safe upgrades.
type InstallManifestFile struct {
	AppID    string `json:"appId"`
	RelPath  string `json:"relPath"`
	SHA256   string `json:"sha256"`
	FileSize int64  `json:"fileSize"`
}

// LaunchHistory tracks individual app launches.
type LaunchHistory struct {
	ID         string `json:"id"`
	AppID      string `json:"appId"`
	EntryID    string `json:"entryId"`
	LaunchedAt int64  `json:"launchedAt"`
	Elevated   bool   `json:"elevated"`
}

// AppOverview is the hydrated application view model consumed by the UI.
type AppOverview struct {
	App
	CategoryName  string     `json:"categoryName"`
	Tags          []string   `json:"tags"`
	Entries       []AppEntry `json:"entries"`
	IconURL       string     `json:"iconUrl"`
	ResolvedRoot  string     `json:"resolvedRoot"`
	PrimaryExe    string     `json:"primaryExe"`
	IsRunning     bool       `json:"isRunning"`
	RunningPIDs   []int      `json:"runningPids"`
	IsBuilding    bool       `json:"isBuilding"`
	BuildMessage  string     `json:"buildMessage"`
	DiskSizeBytes int64      `json:"diskSizeBytes"`
}

// AppLogSnapshot represents the captured build and runtime log state for an app.
type AppLogSnapshot struct {
	AppID       string   `json:"appId"`
	Status      string   `json:"status"` // "idle" | "building" | "running" | "failed" | "exited"
	LastCommand string   `json:"lastCommand"`
	UpdatedAt   int64    `json:"updatedAt"`
	Lines       []string `json:"lines"`
	LastError   string   `json:"lastError"`
}

// IngestCandidateEntry is a discovered executable/script/command candidate from analysis.
type IngestCandidateEntry struct {
	RelPath            string    `json:"relPath"`
	Name               string    `json:"name"`
	EntryType          EntryType `json:"entryType"`
	ArgsTemplate       string    `json:"argsTemplate"`
	WorkingDir         string    `json:"workingDir"`
	Score              int       `json:"score"`
	IsPrimary          bool      `json:"isPrimary"`
	PreLaunchCommand   string    `json:"preLaunchCommand"`
	PreLaunchCondition string    `json:"preLaunchCondition"`
	ReadyCheckPath     string    `json:"readyCheckPath"`
	HideConsole        bool      `json:"hideConsole"`
	DefaultSelected    bool      `json:"defaultSelected"`
	Note               string    `json:"note"`
}

// IngestAnalysis holds pre-commit inspection details for a dropped/picked item.
type IngestAnalysis struct {
	SourcePath      string                 `json:"sourcePath"`
	SourceKind      string                 `json:"sourceKind"` // "file" | "dir" | "archive" | "shortcut" | "project"
	SuggestedName   string                 `json:"suggestedName"`
	Version         string                 `json:"version"`
	Description     string                 `json:"description"`
	SuggestedAction IngestAction           `json:"suggestedAction"` // "copy" | "move" | "link"
	CanCopy         bool                   `json:"canCopy"`
	CanLink         bool                   `json:"canLink"`
	CanMove         bool                   `json:"canMove"`
	SizeBytes       int64                  `json:"sizeBytes"`
	Candidates      []IngestCandidateEntry `json:"candidates"`
	ProjectKind     string                 `json:"projectKind"` // "" | "electron" | "node" | "custom"
	ProjectWarning  string                 `json:"projectWarning"`
	IconSourcePath  string                 `json:"iconSourcePath"`
	Error           string                 `json:"error"`
}

// IngestCommitItem specifies user-confirmed choices for ingesting one item.
type IngestCommitItem struct {
	SourcePath     string       `json:"sourcePath"`
	Name           string       `json:"name"`
	Version        string       `json:"version"`
	Description    string       `json:"description"`
	Action         IngestAction `json:"action"` // "copy" | "move" | "link"
	CategoryID     string       `json:"categoryId"`
	Tags           []string     `json:"tags"`
	Aliases        []string     `json:"aliases"`
	PrimaryPath    string       `json:"primaryPath"`
	ExtraPaths     []string     `json:"extraPaths"`
	Entries        []EntryInput `json:"entries"`
	IconSourcePath string       `json:"iconSourcePath"`
	ArgsTemplate   string       `json:"argsTemplate"`
	WorkingDir     string       `json:"workingDir"`
	RunAsAdmin     bool         `json:"runAsAdmin"`
	HideConsole    bool         `json:"hideConsole"`
}

// IngestResult summarizes a batch commit operation.
type IngestResult struct {
	Apps   []AppOverview `json:"apps"`
	Errors []string      `json:"errors"`
}

// EntryInput defines editable entry fields when saving entries.
type EntryInput struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	EntryType          EntryType         `json:"entryType"`
	RelPath            string            `json:"relPath"`
	ArgsTemplate       string            `json:"argsTemplate"`
	WorkingDir         string            `json:"workingDir"`
	EnvVars            map[string]string `json:"envVars"`
	RunAsAdmin         bool              `json:"runAsAdmin"`
	HideConsole        bool              `json:"hideConsole"`
	PromptArgs         bool              `json:"promptArgs"`
	IsPrimary          bool              `json:"isPrimary"`
	PreLaunchCommand   string            `json:"preLaunchCommand"`
	PreLaunchCondition string            `json:"preLaunchCondition"`
	ReadyCheckPath     string            `json:"readyCheckPath"`
}

// CreateCommandAppRequest creates a linked project/command app directly without scanning executables.
type CreateCommandAppRequest struct {
	Name               string   `json:"name"`
	RootPath           string   `json:"rootPath"`
	Command            string   `json:"command"`
	ArgsTemplate       string   `json:"argsTemplate"`
	WorkingDir         string   `json:"workingDir"`
	PreLaunchCommand   string   `json:"preLaunchCommand"`
	PreLaunchCondition string   `json:"preLaunchCondition"`
	ReadyCheckPath     string   `json:"readyCheckPath"`
	CategoryID         string   `json:"categoryId"`
	Tags               []string `json:"tags"`
	Aliases            []string `json:"aliases"`
	Description        string   `json:"description"`
	HideConsole        bool     `json:"hideConsole"`
	RunAsAdmin         bool     `json:"runAsAdmin"`
}

// UpdateAppRequest updates editable metadata fields on an App.
type UpdateAppRequest struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases"`
	Description    string   `json:"description"`
	NotesMD        string   `json:"notesMd"`
	CategoryID     string   `json:"categoryId"`
	Tags           []string `json:"tags"`
	IsPinned       bool     `json:"isPinned"`
	Hotkey         string   `json:"hotkey"`
	CurrentVersion string   `json:"currentVersion"`
	PersistRules   []string `json:"persistRules"`
}

// SearchHit pairs a matched app with match metadata for the launcher.
type SearchHit struct {
	App         AppOverview `json:"app"`
	Score       float64     `json:"score"`
	MatchReason string      `json:"matchReason"`
}

// LaunchRequest describes how to execute an app entry.
type LaunchRequest struct {
	AppID       string `json:"appId"`
	EntryID     string `json:"entryId"`
	ExtraArgs   string `json:"extraArgs"`
	ForceAdmin  bool   `json:"forceAdmin"`
}

// RunningAppState reports live PIDs and build status for an active application.
type RunningAppState struct {
	AppID        string `json:"appId"`
	PIDs         []int  `json:"pids"`
	IsBuilding   bool   `json:"isBuilding"`
	BuildMessage string `json:"buildMessage"`
}

// HotkeyStatus reports whether the global launcher hotkey succeeded or conflicted.
type HotkeyStatus struct {
	Accelerator string `json:"accelerator"`
	Registered  bool   `json:"registered"`
	Error       string `json:"error"`
}

// AppBootstrapData provides initial hydration data for frontend startup.
type AppBootstrapData struct {
	Apps         []AppOverview     `json:"apps"`
	Categories   []Category        `json:"categories"`
	Tags         []Tag             `json:"tags"`
	Running      []RunningAppState `json:"running"`
	HotkeyStatus HotkeyStatus      `json:"hotkeyStatus"`
	PortableRoot string            `json:"portableRoot"`
	LibraryRoot  string            `json:"libraryRoot"`
}
