package platform

import (
	"context"

	"oneapp/internal/domain"
)

// ExecMeta contains version and product metadata extracted from an executable.
type ExecMeta struct {
	ProductName string
	Description string
	Version     string
}

// ShortcutInfo contains resolved properties from an OS shortcut (.lnk).
type ShortcutInfo struct {
	TargetPath   string
	Arguments    string
	WorkingDir   string
	IconLocation string
}

// LaunchSpec contains all parameters needed to spawn an application entry.
type LaunchSpec struct {
	EntryType    domain.EntryType
	TargetPath   string
	Args         []string
	RawArgsLine  string
	WorkingDir   string
	EnvVars      map[string]string
	UnsetEnvVars []string
	RunAsAdmin   bool
	HideConsole  bool
	LogFilePath  string
	LogAppend    bool
}

// OSBridge abstracts platform-specific desktop capabilities so core services remain cross-platform.
type OSBridge interface {
	ExtractIcon(sourcePath string, outPNGPath string) error
	ReadExecutableMeta(exePath string) (ExecMeta, error)
	ResolveShortcut(lnkPath string) (ShortcutInfo, error)
	Launch(spec LaunchSpec) (int, error)
	RunCommand(ctx context.Context, spec LaunchSpec, onOutput func(line string)) error
	IsProcessAlive(pid int, expectedExe string) bool
	TerminateProcess(pid int) error
	RevealInFileManager(targetPath string) error
	OpenTerminal(dirPath string) error
	SetAutoStart(appName string, exePath string, enabled bool) error
	ProtectSecret(plain string) (string, error)
	UnprotectSecret(cipher string) (string, error)
}
