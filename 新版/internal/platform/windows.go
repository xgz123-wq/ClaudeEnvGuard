//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func init() {
	AnsiDecode = func(b []byte) string {
		if len(b) == 0 {
			return ""
		}
		n, err := windows.MultiByteToWideChar(0 /* CP_ACP */, 0, &b[0], int32(len(b)), nil, 0)
		if err != nil || n == 0 {
			return string(b)
		}
		u := make([]uint16, n)
		if _, err := windows.MultiByteToWideChar(0 /* CP_ACP */, 0, &b[0], int32(len(b)), &u[0], n); err != nil {
			return string(b)
		}
		return windows.UTF16ToString(u)
	}
}

// Windows 是真实系统实现。
type Windows struct {
	paths Paths
}

// NewWindows 读取一次已知文件夹路径并缓存。
func NewWindows() *Windows {
	w := &Windows{}
	kf := func(id *windows.KNOWNFOLDERID, fallback string) string {
		p, err := windows.KnownFolderPath(id, 0)
		if err != nil || p == "" {
			return fallback
		}
		return p
	}
	home, _ := os.UserHomeDir()
	w.paths = Paths{
		Home:            kf(windows.FOLDERID_Profile, home),
		LocalAppData:    kf(windows.FOLDERID_LocalAppData, os.Getenv("LOCALAPPDATA")),
		AppData:         kf(windows.FOLDERID_RoamingAppData, os.Getenv("APPDATA")),
		ProgramFiles:    kf(windows.FOLDERID_ProgramFiles, os.Getenv("ProgramFiles")),
		ProgramFilesX86: kf(windows.FOLDERID_ProgramFilesX86, os.Getenv("ProgramFiles(x86)")),
		Desktop:         kf(windows.FOLDERID_Desktop, filepath.Join(home, "Desktop")),
		PublicDesktop:   kf(windows.FOLDERID_PublicDesktop, ""),
		StartMenu:       kf(windows.FOLDERID_Programs, ""),
		CommonStartMenu: kf(windows.FOLDERID_CommonPrograms, ""),
	}
	return w
}

func (w *Windows) Paths() Paths             { return w.paths }
func (w *Windows) Getenv(key string) string { return os.Getenv(key) }

// ---------------------------------------------------------------- 注册表

func rootKey(r RegRoot) registry.Key {
	if r == HKLM {
		return registry.LOCAL_MACHINE
	}
	return registry.CURRENT_USER
}

func mapRegErr(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return ErrAccessDenied
	}
	return err
}

func (w *Windows) RegRead(root RegRoot, key, name string) (RegValue, error) {
	k, err := registry.OpenKey(rootKey(root), key, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return RegValue{}, nil
		}
		return RegValue{}, mapRegErr(err)
	}
	defer k.Close()
	_, typ, err := k.GetValue(name, nil)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return RegValue{}, nil
		}
		return RegValue{}, mapRegErr(err)
	}
	switch typ {
	case registry.SZ, registry.EXPAND_SZ:
		s, _, err := k.GetStringValue(name)
		if err != nil {
			return RegValue{}, err
		}
		t := "sz"
		if typ == registry.EXPAND_SZ {
			t = "expand_sz"
		}
		return RegValue{Exists: true, Type: t, Str: s}, nil
	case registry.DWORD, registry.QWORD:
		n, _, err := k.GetIntegerValue(name)
		if err != nil {
			return RegValue{}, err
		}
		return RegValue{Exists: true, Type: "dword", DWord: uint32(n)}, nil
	case registry.MULTI_SZ:
		m, _, err := k.GetStringsValue(name)
		if err != nil {
			return RegValue{}, err
		}
		return RegValue{Exists: true, Type: "multi_sz", Multi: m}, nil
	}
	return RegValue{Exists: true, Type: fmt.Sprintf("type%d", typ)}, nil
}

func (w *Windows) RegWrite(root RegRoot, key, name string, v RegValue) error {
	k, _, err := registry.CreateKey(rootKey(root), key, registry.SET_VALUE)
	if err != nil {
		return mapRegErr(err)
	}
	defer k.Close()
	switch v.Type {
	case "dword":
		err = k.SetDWordValue(name, v.DWord)
	case "expand_sz":
		err = k.SetExpandStringValue(name, v.Str)
	case "multi_sz":
		err = k.SetStringsValue(name, v.Multi)
	default:
		err = k.SetStringValue(name, v.Str)
	}
	return mapRegErr(err)
}

func (w *Windows) RegDelete(root RegRoot, key, name string) error {
	k, err := registry.OpenKey(rootKey(root), key, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return mapRegErr(err)
	}
	defer k.Close()
	err = k.DeleteValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return mapRegErr(err)
}

func (w *Windows) RegValueNames(root RegRoot, key string) ([]string, error) {
	k, err := registry.OpenKey(rootKey(root), key, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil, nil
		}
		return nil, mapRegErr(err)
	}
	defer k.Close()
	return k.ReadValueNames(-1)
}

// ---------------------------------------------------------------- 快捷方式（COM）

// withCOM 在锁定的系统线程上初始化 COM 后执行 fn。
func withCOM(fn func(shell *ole.IDispatch) error) (err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); e != nil {
		var oe *ole.OleError
		// S_FALSE（已初始化）也需要配对 CoUninitialize；其他错误直接返回
		if errors.As(e, &oe) && oe.Code() != 1 {
			return fmt.Errorf("COM 初始化失败：%w", e)
		}
	}
	defer ole.CoUninitialize()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("快捷方式读写异常：%v", r)
		}
	}()
	unk, e := oleutil.CreateObject("WScript.Shell")
	if e != nil {
		return fmt.Errorf("创建 WScript.Shell 失败：%w", e)
	}
	defer unk.Release()
	shell, e := unk.QueryInterface(ole.IID_IDispatch)
	if e != nil {
		return e
	}
	defer shell.Release()
	return fn(shell)
}

func (w *Windows) ReadShortcut(path string) (Shortcut, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Shortcut{}, err
	}
	if s, perr := ParseLnk(b); perr == nil {
		s.Path = path
		s.Target = expandEnv(s.Target)
		return s, nil
	}
	return w.ReadShortcutCOM(path)
}

// ReadShortcutCOM 用 WScript.Shell 读快捷方式（原生解析失败时的兜底，也用于测试对照）。
func (w *Windows) ReadShortcutCOM(path string) (Shortcut, error) {
	var s Shortcut
	err := withCOM(func(shell *ole.IDispatch) error {
		v, err := oleutil.CallMethod(shell, "CreateShortcut", path)
		if err != nil {
			return err
		}
		sc := v.ToIDispatch()
		defer sc.Release()
		get := func(name string) string {
			p, err := oleutil.GetProperty(sc, name)
			if err != nil {
				return ""
			}
			return p.ToString()
		}
		s = Shortcut{Path: path, Target: get("TargetPath"), Args: get("Arguments"),
			WorkDir: get("WorkingDirectory"), Icon: get("IconLocation"), Description: get("Description")}
		return nil
	})
	return s, err
}

// expandEnv 展开 %VAR% 形式的环境变量。
func expandEnv(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	src, _ := windows.UTF16PtrFromString(s)
	buf := make([]uint16, 1024)
	n, err := windows.ExpandEnvironmentStrings(src, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return s
	}
	return windows.UTF16ToString(buf[:n])
}

func (w *Windows) WriteShortcut(s Shortcut) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	return withCOM(func(shell *ole.IDispatch) error {
		v, err := oleutil.CallMethod(shell, "CreateShortcut", s.Path)
		if err != nil {
			return err
		}
		sc := v.ToIDispatch()
		defer sc.Release()
		for _, kv := range [][2]string{{"TargetPath", s.Target}, {"Arguments", s.Args},
			{"WorkingDirectory", s.WorkDir}, {"IconLocation", s.Icon}, {"Description", s.Description}} {
			if kv[1] == "" && kv[0] != "Arguments" {
				continue
			}
			if _, err := oleutil.PutProperty(sc, kv[0], kv[1]); err != nil {
				return fmt.Errorf("写入 %s 失败：%w", kv[0], err)
			}
		}
		_, err = oleutil.CallMethod(sc, "Save")
		return err
	})
}

// ---------------------------------------------------------------- 进程

var (
	modkernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procGetPackagesByPackageFamily = modkernel32.NewProc("GetPackagesByPackageFamily")
	procGetPackagePathByFullName   = modkernel32.NewProc("GetPackagePathByFullName")
	procGetUserDefaultUILanguage   = modkernel32.NewProc("GetUserDefaultUILanguage")
	procLCIDToLocaleName           = modkernel32.NewProc("LCIDToLocaleName")
	modiphlpapi                    = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable        = modiphlpapi.NewProc("GetExtendedTcpTable")
	moduser32                      = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW        = moduser32.NewProc("SendMessageTimeoutW")
)

const processCommandLineInformation = 60

func (w *Windows) Processes(withCmdLine ...string) ([]Process, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	want := map[string]bool{}
	for _, n := range withCmdLine {
		want[strings.ToLower(n)] = true
	}
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []Process
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		p := Process{PID: int(pe.ProcessID), Name: windows.UTF16ToString(pe.ExeFile[:])}
		if want[strings.ToLower(p.Name)] {
			p.Exe, p.CmdLine = processDetail(pe.ProcessID)
		}
		out = append(out, p)
	}
	return out, nil
}

// processDetail 读进程路径和命令行；无权限时返回空，不报错。
func processDetail(pid uint32) (exe, cmd string) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		exe = windows.UTF16ToString(buf[:n])
	}
	raw := make([]byte, 64*1024)
	var ret uint32
	if windows.NtQueryInformationProcess(h, processCommandLineInformation, unsafe.Pointer(&raw[0]), uint32(len(raw)), &ret) == nil {
		us := (*windows.NTUnicodeString)(unsafe.Pointer(&raw[0]))
		cmd = us.String()
	}
	runtime.KeepAlive(raw)
	return exe, cmd
}

func (w *Windows) PortOwner(port int) (int, error) {
	const afInet = 2
	const tcpTableOwnerPidListener = 3
	var size uint32
	r, _, _ := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidListener, 0)
	if size == 0 {
		return 0, fmt.Errorf("GetExtendedTcpTable 失败：%d", r)
	}
	buf := make([]byte, size+1024)
	size = uint32(len(buf))
	r, _, _ = procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidListener, 0)
	if r != 0 {
		return 0, fmt.Errorf("GetExtendedTcpTable 返回 %d", r)
	}
	count := *(*uint32)(unsafe.Pointer(&buf[0]))
	type row struct{ State, LocalAddr, LocalPort, RemoteAddr, RemotePort, PID uint32 }
	rowSize := unsafe.Sizeof(row{})
	for i := uint32(0); i < count; i++ {
		off := 4 + uintptr(i)*rowSize
		if off+rowSize > uintptr(len(buf)) {
			break
		}
		rw := (*row)(unsafe.Pointer(&buf[off]))
		p := int(uint16(rw.LocalPort>>8) | uint16(rw.LocalPort&0xff)<<8)
		addr := rw.LocalAddr // 网络字节序
		if p == port && (addr == 0 || addr&0xff == 127) {
			return int(rw.PID), nil
		}
	}
	return 0, nil
}

func (w *Windows) PackageByFamily(family string) (string, string, error) {
	if err := procGetPackagesByPackageFamily.Find(); err != nil {
		return "", "", err
	}
	fam, _ := windows.UTF16PtrFromString(family)
	var count, bufLen uint32
	r, _, _ := procGetPackagesByPackageFamily.Call(uintptr(unsafe.Pointer(fam)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&bufLen)), 0)
	if count == 0 {
		return "", "", nil
	}
	if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && r != 0 {
		return "", "", fmt.Errorf("GetPackagesByPackageFamily 返回 %d", r)
	}
	names := make([]*uint16, count)
	buf := make([]uint16, bufLen)
	r, _, _ = procGetPackagesByPackageFamily.Call(uintptr(unsafe.Pointer(fam)), uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&names[0])), uintptr(unsafe.Pointer(&bufLen)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return "", "", fmt.Errorf("GetPackagesByPackageFamily 返回 %d", r)
	}
	full := windows.UTF16PtrToString(names[count-1])
	var plen uint32
	fn, _ := windows.UTF16PtrFromString(full)
	procGetPackagePathByFullName.Call(uintptr(unsafe.Pointer(fn)), uintptr(unsafe.Pointer(&plen)), 0)
	path := ""
	if plen > 0 {
		pb := make([]uint16, plen)
		if r, _, _ := procGetPackagePathByFullName.Call(uintptr(unsafe.Pointer(fn)), uintptr(unsafe.Pointer(&plen)), uintptr(unsafe.Pointer(&pb[0]))); r == 0 {
			path = windows.UTF16ToString(pb)
		}
	}
	runtime.KeepAlive(buf)
	return full, path, nil
}

// ---------------------------------------------------------------- 其他

func (w *Windows) Run(ctx context.Context, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		err = nil
	}
	return string(out), code, err
}

var (
	modshell32          = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = modshell32.NewProc("ShellExecuteExW")
)

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIconOrMon   uintptr
	hProcess     windows.Handle
}

// quoteArg 按 Windows 命令行规则给参数加引号。
func quoteArg(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"") {
		return a
	}
	return syscall.EscapeArg(a)
}

func (w *Windows) RunElevated(exe string, args []string) (int, error) {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = quoteArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(q, " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	info := shellExecuteInfo{fMask: 0x00000040 | 0x00000400, lpVerb: verb, lpFile: file, lpParameters: params, lpDirectory: dir, nShow: 0} // NOCLOSEPROCESS | NOASYNC，窗口隐藏
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		if errors.Is(e, windows.ERROR_CANCELLED) {
			return 0, ErrElevationCancelled
		}
		return 0, fmt.Errorf("提权启动失败：%v", e)
	}
	if info.hProcess == 0 {
		return 0, errors.New("提权启动失败：拿不到进程句柄")
	}
	defer windows.CloseHandle(info.hProcess)
	if _, err := windows.WaitForSingleObject(info.hProcess, 10*60*1000); err != nil {
		return 0, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, err
	}
	return int(code), nil
}

func (w *Windows) StartDetached(exe, rawArgs string) error {
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(exe) + " " + rawArgs}
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (w *Windows) OpenPath(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return exec.Command(filepath.Join(os.Getenv("SystemRoot"), "explorer.exe"), path).Start()
	}
	return exec.Command(filepath.Join(os.Getenv("SystemRoot"), "explorer.exe"), "/select,", path).Start()
}

func (w *Windows) IsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func (w *Windows) UILanguage() string {
	r, _, _ := procGetUserDefaultUILanguage.Call()
	buf := make([]uint16, 85)
	n, _, _ := procLCIDToLocaleName.Call(r, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

func (w *Windows) BroadcastEnvChange() {
	env, _ := windows.UTF16PtrFromString("Environment")
	var res uintptr
	procSendMessageTimeoutW.Call(0xffff, 0x001A, 0, uintptr(unsafe.Pointer(env)), 0x0002, 3000, uintptr(unsafe.Pointer(&res)))
}

func (w *Windows) GlobalIPv6() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, itf := range ifs {
		if itf.Flags&net.FlagUp == 0 || itf.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := itf.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() != nil {
				continue
			}
			ip := ipn.IP
			// 只算真正的公网 IPv6：排除链路本地、ULA（fc00::/7，TUN 常用）
			if !ip.IsGlobalUnicast() || ip[0]&0xfe == 0xfc {
				continue
			}
			out = append(out, ip.String()+"（"+itf.Name+"）")
		}
	}
	return out
}

// ---------------------------------------------------------------- 签名

var (
	modwintrust                        = windows.NewLazySystemDLL("wintrust.dll")
	procWTHelperProvDataFromStateData  = modwintrust.NewProc("WTHelperProvDataFromStateData")
	procWTHelperGetProvSignerFromChain = modwintrust.NewProc("WTHelperGetProvSignerFromChain")
	procWTHelperGetProvCertFromChain   = modwintrust.NewProc("WTHelperGetProvCertFromChain")
)

const (
	wtdStateActionVerify  = 1
	certNameAttrType      = 3          // CERT_NAME_ATTR_TYPE
	certX500NameStr       = 3          // CERT_X500_NAME_STR
	certNameStrReverseFlg = 0x02000000 // CERT_NAME_STR_REVERSE_FLAG：CN 在前，和 PowerShell 的 Subject 一致
)

// cryptProviderCert 只取 CRYPT_PROVIDER_CERT 开头用到的字段。
type cryptProviderCert struct {
	Size uint32
	Cert *windows.CertContext
}

// FileSignature 用 WinVerifyTrust 校验签名，再从校验状态里取签名证书（链上第 0 张）的主题。
// 不查吊销（WTD_REVOKE_NONE + 只用缓存），检查不联网也不卡。
func (w *Windows) FileSignature(path string) (Signature, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Signature{}, err
	}
	fi := windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: p}
	data := windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&fi),
		StateAction:                     wtdStateActionVerify,
		ProvFlags:                       windows.WTD_CACHE_ONLY_URL_RETRIEVAL,
	}
	verr := windows.WinVerifyTrustEx(0, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	defer func() {
		data.StateAction = windows.WTD_STATEACTION_CLOSE
		windows.WinVerifyTrustEx(0, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	}()
	sig := Signature{Valid: verr == nil}
	if verr != nil {
		sig.Status = verr.Error()
	}
	if data.StateData == 0 {
		return sig, nil
	}
	prov, _, _ := procWTHelperProvDataFromStateData.Call(uintptr(data.StateData))
	if prov == 0 {
		return sig, nil
	}
	sgnr, _, _ := procWTHelperGetProvSignerFromChain.Call(prov, 0, 0, 0)
	if sgnr == 0 {
		return sig, nil
	}
	pc, _, _ := procWTHelperGetProvCertFromChain.Call(sgnr, 0)
	if pc == 0 {
		return sig, nil
	}
	cert := (*(**cryptProviderCert)(unsafe.Pointer(&pc))).Cert
	if cert == nil {
		return sig, nil
	}
	strType := uint32(certX500NameStr | certNameStrReverseFlg)
	sig.Subject = certName(cert, windows.CERT_NAME_RDN_TYPE, unsafe.Pointer(&strType))
	oid := append([]byte("2.5.4.10"), 0) // szOID_ORGANIZATION_NAME
	sig.Org = certName(cert, certNameAttrType, unsafe.Pointer(&oid[0]))
	return sig, nil
}

func certName(cert *windows.CertContext, nameType uint32, para unsafe.Pointer) string {
	n := windows.CertGetNameString(cert, nameType, 0, para, nil, 0)
	if n <= 1 {
		return ""
	}
	buf := make([]uint16, n)
	windows.CertGetNameString(cert, nameType, 0, para, &buf[0], n)
	return windows.UTF16ToString(buf)
}
