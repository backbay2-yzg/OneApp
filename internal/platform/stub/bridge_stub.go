//go:build !windows

package stub

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"oneapp/internal/platform"
)

// Bridge provides a cross-platform fallback stub on non-Windows hosts.
type Bridge struct{}

func New() *Bridge { return &Bridge{} }

func (b *Bridge) ExtractIcon(string, string) error { return nil }

func (b *Bridge) ReadExecutableMeta(string) (platform.ExecMeta, error) {
	return platform.ExecMeta{}, nil
}

func (b *Bridge) ResolveShortcut(string) (platform.ShortcutInfo, error) {
	return platform.ShortcutInfo{}, fmt.Errorf("shortcut resolution not supported on this OS")
}

func (b *Bridge) Launch(spec platform.LaunchSpec) (int, error) {
	cmd := exec.Command(spec.TargetPath, spec.Args...)
	cmd.Dir = spec.WorkingDir
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

func (b *Bridge) RunCommand(ctx context.Context, spec platform.LaunchSpec, onOutput func(line string)) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", spec.TargetPath)
	cmd.Dir = spec.WorkingDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	if onOutput != nil {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			onOutput(sc.Text())
		}
	}
	return cmd.Wait()
}

func (b *Bridge) IsProcessAlive(int, string) bool { return false }

func (b *Bridge) TerminateProcess(int) error { return fmt.Errorf("unsupported on this OS") }

func (b *Bridge) RevealInFileManager(targetPath string) error {
	return exec.Command("xdg-open", filepath.Dir(targetPath)).Start()
}

func (b *Bridge) OpenTerminal(string) error { return fmt.Errorf("unsupported on this OS") }

func (b *Bridge) SetAutoStart(string, string, bool) error {
	return fmt.Errorf("unsupported on this OS")
}

func (b *Bridge) ProtectSecret(plain string) (string, error)   { return plain, nil }
func (b *Bridge) UnprotectSecret(cipher string) (string, error) { return cipher, nil }
