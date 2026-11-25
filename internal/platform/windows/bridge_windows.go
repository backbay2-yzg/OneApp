//go:build windows

package windows

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"oneapp/internal/domain"
	"oneapp/internal/platform"
)

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modVersion  = windows.NewLazySystemDLL("version.dll")
	modCrypt32  = windows.NewLazySystemDLL("crypt32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procPrivateExtractIconsW = modUser32.NewProc("PrivateExtractIconsW")
	procGetIconInfo          = modUser32.NewProc("GetIconInfo")
	procDestroyIcon          = modUser32.NewProc("DestroyIcon")

	procCreateCompatibleDC = modGdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = modGdi32.NewProc("DeleteDC")
	procGetObjectW         = modGdi32.NewProc("GetObjectW")
	procGetDIBits          = modGdi32.NewProc("GetDIBits")
	procDeleteObject       = modGdi32.NewProc("DeleteObject")

	procExtractIconExW  = modShell32.NewProc("ExtractIconExW")
	procShellExecuteExW = modShell32.NewProc("ShellExecuteExW")

	procGetFileVersionInfoSizeW = modVersion.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfoW     = modVersion.NewProc("GetFileVersionInfoW")
	procVerQueryValueW          = modVersion.NewProc("VerQueryValueW")

	procCryptProtectData   = modCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")
	procLocalFree          = modKernel32.NewProc("LocalFree")
)

const (
	dibRGBColors        = 0
	biRGB               = 0
	seeMaskNoCloseProc  = 0x00000040
	swHide              = 0
	swShowNormal        = 1
	createNoWindow      = 0x08000000
	processQueryLimited = 0x1000
	processTerminate    = 0x0001
	stillActive         = 259
	autoStartRegKey     = `Software\Microsoft\Windows\CurrentVersion\Run`
)

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

type bitmap struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type shellExecuteInfoW struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr
	hProcess       windows.Handle
}

type vsFixedFileInfo struct {
	dwSignature        uint32
	dwStrucVersion     uint32
	dwFileVersionMS    uint32
	dwFileVersionLS    uint32
	dwProductVersionMS uint32
	dwProductVersionLS uint32
	dwFileFlagsMask    uint32
	dwFileFlags        uint32
	dwFileOS           uint32
	dwFileType         uint32
	dwFileSubtype      uint32
	dwFileDateMS       uint32
	dwFileDateLS       uint32
}

type dataBlob struct {
	cbData uint32
	pbData *byte
}

// Bridge implements platform.OSBridge for Windows 10/11.
type Bridge struct{}

// New returns a Windows OSBridge implementation.
func New() *Bridge {
	return &Bridge{}
}

// ExtractIcon extracts the primary high-resolution icon from an executable or .ico file into a PNG.
func (b *Bridge) ExtractIcon(sourcePath string, outPNGPath string) error {
	if strings.TrimSpace(sourcePath) == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(sourcePath))
	if ext != ".exe" && ext != ".ico" && ext != ".dll" {
		return nil
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return err
	}

	pathPtr, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return err
	}

	var hIcon uintptr
	var iconID uint32
	ret, _, _ := procPrivateExtractIconsW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		64,
		64,
		uintptr(unsafe.Pointer(&hIcon)),
		uintptr(unsafe.Pointer(&iconID)),
		1,
		0,
	)
	if ret == 0 || hIcon == 0 {
		var largeIcon uintptr
		r2, _, _ := procExtractIconExW.Call(
			uintptr(unsafe.Pointer(pathPtr)),
			0,
			uintptr(unsafe.Pointer(&largeIcon)),
			0,
			1,
		)
		if r2 == 0 || largeIcon == 0 {
			return nil
		}
		hIcon = largeIcon
	}
	defer procDestroyIcon.Call(hIcon)

	var ii iconInfo
	ok, _, _ := procGetIconInfo.Call(hIcon, uintptr(unsafe.Pointer(&ii)))
	if ok == 0 {
		return fmt.Errorf("GetIconInfo failed")
	}
	if ii.hbmColor != 0 {
		defer procDeleteObject.Call(ii.hbmColor)
	}
	if ii.hbmMask != 0 {
		defer procDeleteObject.Call(ii.hbmMask)
	}
	if ii.hbmColor == 0 {
		return nil
	}

	var bmp bitmap
	objRet, _, _ := procGetObjectW.Call(ii.hbmColor, unsafe.Sizeof(bmp), uintptr(unsafe.Pointer(&bmp)))
	if objRet == 0 || bmp.bmWidth <= 0 || bmp.bmHeight <= 0 {
		return fmt.Errorf("GetObjectW failed for icon bitmap")
	}

	w := int(bmp.bmWidth)
	h := int(bmp.bmHeight)

	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(hdc)

	bmi := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(w),
		biHeight:      -int32(h),
		biPlanes:      1,
		biBitCount:    32,
		biCompression: biRGB,
	}

	pixels := make([]byte, w*h*4)
	scanlines, _, _ := procGetDIBits.Call(
		hdc,
		ii.hbmColor,
		0,
		uintptr(h),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bmi)),
		dibRGBColors,
	)
	if scanlines == 0 {
		return fmt.Errorf("GetDIBits failed for color bitmap")
	}

	hasAlpha := false
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			hasAlpha = true
			break
		}
	}

	if !hasAlpha && ii.hbmMask != 0 {
		maskPixels := make([]byte, w*h*4)
		mRet, _, _ := procGetDIBits.Call(
			hdc,
			ii.hbmMask,
			0,
			uintptr(h),
			uintptr(unsafe.Pointer(&maskPixels[0])),
			uintptr(unsafe.Pointer(&bmi)),
			dibRGBColors,
		)
		if mRet != 0 {
			for i := 0; i < len(pixels); i += 4 {
				// Mask pixel is 0 where icon is opaque.
				if maskPixels[i] == 0 && maskPixels[i+1] == 0 && maskPixels[i+2] == 0 {
					pixels[i+3] = 255
				} else {
					pixels[i+3] = 0
				}
			}
		} else {
			for i := 3; i < len(pixels); i += 4 {
				pixels[i] = 255
			}
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(pixels); i += 4 {
		// DIB is BGRA -> convert to RGBA
		img.Pix[i+0] = pixels[i+2]
		img.Pix[i+1] = pixels[i+1]
		img.Pix[i+2] = pixels[i+0]
		img.Pix[i+3] = pixels[i+3]
	}

	if err := os.MkdirAll(filepath.Dir(outPNGPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPNGPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// ReadExecutableMeta reads PE VersionInfo metadata from a Windows executable.
func (b *Bridge) ReadExecutableMeta(exePath string) (platform.ExecMeta, error) {
	var meta platform.ExecMeta
	ext := strings.ToLower(filepath.Ext(exePath))
	if ext != ".exe" && ext != ".dll" {
		return meta, nil
	}

	pathPtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return meta, err
	}

	var handle uint32
	size, _, _ := procGetFileVersionInfoSizeW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if size == 0 {
		return meta, nil
	}

	buf := make([]byte, size)
	ok, _, _ := procGetFileVersionInfoW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		size,
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if ok == 0 {
		return meta, nil
	}

	// Read numeric fixed file version as fallback.
	var fixedVersion string
	if ptr, length, ok := verQueryValue(buf, `\`); ok && length >= unsafe.Sizeof(vsFixedFileInfo{}) {
		ffi := (*vsFixedFileInfo)(ptr)
		major := (ffi.dwFileVersionMS >> 16) & 0xffff
		minor := ffi.dwFileVersionMS & 0xffff
		patch := (ffi.dwFileVersionLS >> 16) & 0xffff
		build := ffi.dwFileVersionLS & 0xffff
		if major != 0 || minor != 0 || patch != 0 || build != 0 {
			if build == 0 {
				fixedVersion = fmt.Sprintf("%d.%d.%d", major, minor, patch)
			} else {
				fixedVersion = fmt.Sprintf("%d.%d.%d.%d", major, minor, patch, build)
			}
		}
	}

	var codepages []string
	if ptr, length, ok := verQueryValue(buf, `\VarFileInfo\Translation`); ok && length >= 4 {
		count := int(length / 4)
		for i := 0; i < count; i++ {
			pair := (*[2]uint16)(unsafe.Add(ptr, i*4))
			codepages = append(codepages, fmt.Sprintf("%04x%04x", pair[0], pair[1]))
		}
	}
	codepages = append(codepages, "040904b0", "080404b0", "040904e4")

	for _, cp := range codepages {
		if meta.ProductName == "" {
			meta.ProductName = queryVersionString(buf, cp, "ProductName")
		}
		if meta.Description == "" {
			meta.Description = queryVersionString(buf, cp, "FileDescription")
		}
		if meta.Version == "" {
			meta.Version = queryVersionString(buf, cp, "ProductVersion")
			if meta.Version == "" {
				meta.Version = queryVersionString(buf, cp, "FileVersion")
			}
		}
		if meta.ProductName != "" && meta.Description != "" && meta.Version != "" {
			break
		}
	}

	if meta.Version == "" {
		meta.Version = fixedVersion
	}
	return meta, nil
}

func verQueryValue(buf []byte, subBlock string) (unsafe.Pointer, uintptr, bool) {
	sbPtr, err := windows.UTF16PtrFromString(subBlock)
	if err != nil {
		return nil, 0, false
	}
	var ptr unsafe.Pointer
	var length uint32
	ok, _, _ := procVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(sbPtr)),
		uintptr(unsafe.Pointer(&ptr)),
		uintptr(unsafe.Pointer(&length)),
	)
	return ptr, uintptr(length), ok != 0 && ptr != nil && length > 0
}

func queryVersionString(buf []byte, codepage, key string) string {
	sub := fmt.Sprintf(`\StringFileInfo\%s\%s`, codepage, key)
	ptr, length, ok := verQueryValue(buf, sub)
	if !ok || length == 0 {
		return ""
	}
	slice := unsafe.Slice((*uint16)(ptr), length)
	return strings.TrimSpace(windows.UTF16ToString(slice))
}

// ResolveShortcut resolves a Windows .lnk shortcut using WScript.Shell COM.
func (b *Bridge) ResolveShortcut(lnkPath string) (platform.ShortcutInfo, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var info platform.ShortcutInfo
	initErr := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED)
	if initErr == nil {
		defer ole.CoUninitialize()
	}

	unknown, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return info, fmt.Errorf("create WScript.Shell: %w", err)
	}
	defer unknown.Release()

	wshell, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return info, err
	}
	defer wshell.Release()

	scVar, err := oleutil.CallMethod(wshell, "CreateShortcut", lnkPath)
	if err != nil {
		return info, err
	}
	defer scVar.Clear()

	sc := scVar.ToIDispatch()
	if sc == nil {
		return info, fmt.Errorf("nil shortcut dispatch")
	}

	if v, err := oleutil.GetProperty(sc, "TargetPath"); err == nil {
		info.TargetPath = strings.TrimSpace(v.ToString())
		_ = v.Clear()
	}
	if v, err := oleutil.GetProperty(sc, "Arguments"); err == nil {
		info.Arguments = strings.TrimSpace(v.ToString())
		_ = v.Clear()
	}
	if v, err := oleutil.GetProperty(sc, "WorkingDirectory"); err == nil {
		info.WorkingDir = strings.TrimSpace(v.ToString())
		_ = v.Clear()
	}
	if v, err := oleutil.GetProperty(sc, "IconLocation"); err == nil {
		info.IconLocation = strings.TrimSpace(v.ToString())
		_ = v.Clear()
	}
	return info, nil
}

// Launch starts an entry on Windows, supporting executables, scripts, shortcuts, shell commands, hidden console, and UAC elevation.
func (b *Bridge) Launch(spec platform.LaunchSpec) (int, error) {
	target := spec.TargetPath
	argsLine := strings.TrimSpace(spec.RawArgsLine)
	workDir := spec.WorkingDir

	if spec.EntryType == domain.EntryLnk {
		if sc, err := b.ResolveShortcut(target); err == nil && sc.TargetPath != "" {
			target = sc.TargetPath
			if argsLine == "" {
				argsLine = sc.Arguments
			}
			if workDir == "" {
				workDir = sc.WorkingDir
			}
		}
	}

	if workDir == "" && target != "" && spec.EntryType != domain.EntryCommand {
		workDir = filepath.Dir(target)
	}

	if spec.RunAsAdmin {
		return b.launchElevated(spec.EntryType, target, argsLine, workDir, spec.HideConsole)
	}
	return b.launchNormal(spec, target, argsLine, workDir)
}

func buildEnv(envVars map[string]string, unsetVars []string) []string {
	if len(envVars) == 0 && len(unsetVars) == 0 {
		return nil
	}
	drop := make(map[string]bool, len(envVars)+len(unsetVars))
	for _, k := range unsetVars {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			drop[strings.ToLower(trimmed)] = true
		}
	}
	for k := range envVars {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			drop[strings.ToLower(trimmed)] = true
		}
	}
	base := os.Environ()
	out := make([]string, 0, len(base)+len(envVars))
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if ok && drop[strings.ToLower(key)] {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range envVars {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			out = append(out, fmt.Sprintf("%s=%s", trimmed, v))
		}
	}
	return out
}

func (b *Bridge) launchNormal(
	spec platform.LaunchSpec,
	target string,
	rawArgsLine string,
	workDir string,
) (int, error) {
	var bin string
	var cmdArgs []string
	var customCmdLine string

	switch spec.EntryType {
	case domain.EntryCommand:
		bin = "cmd.exe"
		fullCmd := strings.TrimSpace(target)
		if rawArgsLine != "" {
			fullCmd = strings.TrimSpace(fullCmd + " " + rawArgsLine)
		}
		if fullCmd == "" {
			return 0, fmt.Errorf("命令不能为空")
		}
		customCmdLine = fmt.Sprintf(`cmd.exe /d /s /c "%s"`, fullCmd)
	case domain.EntryBat, domain.EntryCmd:
		bin = "cmd.exe"
		cmdArgs = append([]string{"/c", target}, spec.Args...)
	case domain.EntryPs1:
		bin = "powershell.exe"
		cmdArgs = append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", target}, spec.Args...)
	default:
		bin = target
		cmdArgs = spec.Args
		if rawArgsLine != "" && len(spec.Args) == 0 {
			customCmdLine = fmt.Sprintf(`"%s" %s`, target, rawArgsLine)
		}
	}

	cmd := exec.Command(bin, cmdArgs...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if env := buildEnv(spec.EnvVars, spec.UnsetEnvVars); env != nil {
		cmd.Env = env
	}

	attr := &syscall.SysProcAttr{}
	if spec.HideConsole {
		attr.HideWindow = true
		attr.CreationFlags = createNoWindow
	}
	if customCmdLine != "" {
		attr.CmdLine = customCmdLine
	}
	cmd.SysProcAttr = attr

	var logFile *os.File
	if spec.LogFilePath != "" && spec.HideConsole {
		if mkErr := os.MkdirAll(filepath.Dir(spec.LogFilePath), 0o755); mkErr == nil {
			flags := os.O_CREATE | os.O_WRONLY
			if spec.LogAppend {
				flags |= os.O_APPEND
			} else {
				flags |= os.O_TRUNC
			}
			if f, openErr := os.OpenFile(spec.LogFilePath, flags, 0o644); openErr == nil {
				logFile = f
				cmd.Stdout = f
				cmd.Stderr = f
			}
		}
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return 0, err
	}
	if logFile != nil {
		_ = logFile.Close()
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// RunCommand executes a blocking command synchronously until completion or context cancellation,
// streaming combined stdout/stderr lines to onOutput and killing the process tree on cancellation.
func (b *Bridge) RunCommand(ctx context.Context, spec platform.LaunchSpec, onOutput func(line string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	fullCmd := strings.TrimSpace(spec.TargetPath)
	if spec.EntryType != "" && spec.EntryType != domain.EntryCommand {
		fullCmd = fmt.Sprintf(`"%s"`, fullCmd)
	}
	if raw := strings.TrimSpace(spec.RawArgsLine); raw != "" {
		fullCmd = strings.TrimSpace(fullCmd + " " + raw)
	}
	if fullCmd == "" {
		return fmt.Errorf("命令不能为空")
	}

	cmd := exec.Command("cmd.exe")
	if strings.TrimSpace(spec.WorkingDir) != "" {
		cmd.Dir = spec.WorkingDir
	}
	if env := buildEnv(spec.EnvVars, spec.UnsetEnvVars); env != nil {
		cmd.Env = env
	}
	attr := &syscall.SysProcAttr{
		CmdLine: fmt.Sprintf(`cmd.exe /d /s /c "%s"`, fullCmd),
	}
	if spec.HideConsole {
		attr.HideWindow = true
		attr.CreationFlags = createNoWindow
	}
	cmd.SysProcAttr = attr

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return err
	}
	_ = pw.Close()

	pid := cmd.Process.Pid
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer pr.Close()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if onOutput == nil {
				continue
			}
			line := sc.Text()
			if idx := strings.LastIndexByte(line, '\r'); idx >= 0 {
				if tail := strings.TrimSpace(line[idx+1:]); tail != "" {
					line = tail
				} else {
					line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
				}
			}
			onOutput(line)
		}
	}()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = b.TerminateProcess(pid)
		_ = pr.Close()
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
		select {
		case <-readerDone:
		case <-time.After(500 * time.Millisecond):
		}
		return ctx.Err()
	case waitErr := <-waitCh:
		select {
		case <-readerDone:
		case <-time.After(500 * time.Millisecond):
			_ = pr.Close()
		}
		return waitErr
	}
}

func (b *Bridge) launchElevated(
	entryType domain.EntryType,
	target string,
	argsLine string,
	workDir string,
	hideConsole bool,
) (int, error) {
	file := target
	params := argsLine
	switch entryType {
	case domain.EntryCommand:
		file = "cmd.exe"
		fullCmd := strings.TrimSpace(target)
		if params != "" {
			fullCmd = strings.TrimSpace(fullCmd + " " + params)
		}
		params = fmt.Sprintf(`/d /s /c "%s"`, fullCmd)
	case domain.EntryBat, domain.EntryCmd:
		file = "cmd.exe"
		if params != "" {
			params = fmt.Sprintf(`/c "%s" %s`, target, params)
		} else {
			params = fmt.Sprintf(`/c "%s"`, target)
		}
	case domain.EntryPs1:
		file = "powershell.exe"
		if params != "" {
			params = fmt.Sprintf(`-NoProfile -ExecutionPolicy Bypass -File "%s" %s`, target, params)
		} else {
			params = fmt.Sprintf(`-NoProfile -ExecutionPolicy Bypass -File "%s"`, target)
		}
	}

	verbPtr, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 0, err
	}
	filePtr, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return 0, err
	}
	var paramsPtr *uint16
	if params != "" {
		paramsPtr, err = windows.UTF16PtrFromString(params)
		if err != nil {
			return 0, err
		}
	}
	var dirPtr *uint16
	if workDir != "" {
		dirPtr, err = windows.UTF16PtrFromString(workDir)
		if err != nil {
			return 0, err
		}
	}

	nShow := int32(swShowNormal)
	if hideConsole {
		nShow = swHide
	}

	sei := shellExecuteInfoW{
		cbSize:       uint32(unsafe.Sizeof(shellExecuteInfoW{})),
		fMask:        seeMaskNoCloseProc,
		lpVerb:       verbPtr,
		lpFile:       filePtr,
		lpParameters: paramsPtr,
		lpDirectory:  dirPtr,
		nShow:        nShow,
	}

	ret, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if ret == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return 0, fmt.Errorf("用户取消了管理员提权请求")
		}
		return 0, fmt.Errorf("ShellExecuteExW runas failed: %w", callErr)
	}
	if sei.hProcess == 0 {
		return 0, nil
	}
	defer windows.CloseHandle(sei.hProcess)
	pid, err := windows.GetProcessId(sei.hProcess)
	if err != nil {
		return 0, nil
	}
	return int(pid), nil
}

// IsProcessAlive checks whether pid is running and matches expectedExe (if provided).
func (b *Bridge) IsProcessAlive(pid int, expectedExe string) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(processQueryLimited, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil || exitCode != stillActive {
		return false
	}

	if strings.TrimSpace(expectedExe) != "" && strings.EqualFold(filepath.Ext(expectedExe), ".exe") {
		buf := make([]uint16, 1024)
		sz := uint32(len(buf))
		if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &sz); err == nil && sz > 0 {
			actual := windows.UTF16ToString(buf[:sz])
			if !strings.EqualFold(filepath.Base(actual), filepath.Base(expectedExe)) {
				return false
			}
		}
	}
	return true
}

// TerminateProcess gracefully closes a Windows process tree (taskkill /T), then falls back to forced termination.
func (b *Bridge) TerminateProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if !b.IsProcessAlive(pid, "") {
		return nil
	}

	// 1. Try graceful close across the process tree so GUI/Electron apps can run before-quit handlers.
	graceCtx, cancelGrace := context.WithTimeout(context.Background(), 3*time.Second)
	graceCmd := exec.CommandContext(graceCtx, "taskkill.exe", "/T", "/PID", strconv.Itoa(pid))
	graceCmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	graceErr := graceCmd.Run()
	cancelGrace()

	if graceErr == nil {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if !b.IsProcessAlive(pid, "") {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// 2. Force-kill the process tree if still alive.
	forceCtx, cancelForce := context.WithTimeout(context.Background(), 4*time.Second)
	forceCmd := exec.CommandContext(forceCtx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(pid))
	forceCmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	_ = forceCmd.Run()
	cancelForce()

	if !b.IsProcessAlive(pid, "") {
		return nil
	}

	// 3. Final direct handle termination fallback.
	h, err := windows.OpenProcess(processTerminate, false, uint32(pid))
	if err != nil {
		if !b.IsProcessAlive(pid, "") {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// RevealInFileManager opens Explorer highlighting the target path.
func (b *Bridge) RevealInFileManager(targetPath string) error {
	if strings.TrimSpace(targetPath) == "" {
		return fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(targetPath)
	if err == nil {
		targetPath = abs
	}
	info, statErr := os.Stat(targetPath)
	if statErr == nil && info.IsDir() {
		cmd := exec.Command("explorer.exe", targetPath)
		return cmd.Start()
	}
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: fmt.Sprintf(`explorer.exe /select,"%s"`, targetPath),
	}
	return cmd.Start()
}

// OpenTerminal opens Windows Terminal or PowerShell in dirPath.
func (b *Bridge) OpenTerminal(dirPath string) error {
	if strings.TrimSpace(dirPath) == "" {
		return fmt.Errorf("empty directory path")
	}
	if wt, err := exec.LookPath("wt.exe"); err == nil {
		cmd := exec.Command(wt, "-d", dirPath)
		return cmd.Start()
	}
	cmd := exec.Command("powershell.exe", "-NoExit", "-Command", fmt.Sprintf("Set-Location -LiteralPath '%s'", strings.ReplaceAll(dirPath, "'", "''")))
	cmd.Dir = dirPath
	return cmd.Start()
}

// SetAutoStart manages the HKCU Run registry key for OneApp.
func (b *Bridge) SetAutoStart(appName string, exePath string, enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autoStartRegKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if !enabled {
		err := key.DeleteValue(appName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	return key.SetStringValue(appName, fmt.Sprintf(`"%s" --silent`, exePath))
}

// ProtectSecret encrypts sensitive text using Windows DPAPI (CurrentUser scope).
func (b *Bridge) ProtectSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	inBytes := []byte(plain)
	inBlob := dataBlob{cbData: uint32(len(inBytes)), pbData: &inBytes[0]}
	var outBlob dataBlob

	ret, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if ret == 0 {
		return "", fmt.Errorf("CryptProtectData failed: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))

	cipher := unsafe.Slice(outBlob.pbData, outBlob.cbData)
	return base64.StdEncoding.EncodeToString(cipher), nil
}

// UnprotectSecret decrypts DPAPI-protected cipher text.
func (b *Bridge) UnprotectSecret(cipherText string) (string, error) {
	if cipherText == "" {
		return "", nil
	}
	inBytes, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil || len(inBytes) == 0 {
		return "", err
	}
	inBlob := dataBlob{cbData: uint32(len(inBytes)), pbData: &inBytes[0]}
	var outBlob dataBlob

	ret, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if ret == 0 {
		return "", fmt.Errorf("CryptUnprotectData failed: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))

	plain := unsafe.Slice(outBlob.pbData, outBlob.cbData)
	return string(plain), nil
}
