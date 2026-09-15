//go:build windows

package sandbox

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	disableMaxPrivilege = 0x1

	createNewProcessGroup = 0x00000200

	processSetQuota  = 0x0100
	processTerminate = 0x0001

	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x00002000
)

var (
	advapi32                  = syscall.NewLazyDLL("advapi32.dll")
	procCreateRestrictedToken = advapi32.NewProc("CreateRestrictedToken")

	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
)

// These structures mirror JOBOBJECT_EXTENDED_LIMIT_INFORMATION and the nested
// JOBOBJECT_BASIC_LIMIT_INFORMATION/IO_COUNTERS definitions from WinNT.h.
type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func probeNativeCapability() Capability {
	return Capability{
		Platform: "windows", Backend: "restricted-token+job-object", Available: true,
		// Restricted Token/Job Object 能降低 token privilege 并约束进程树，但它们本身
		// 不能表达 per-root 文件系统边界或可靠网络 namespace。受限 Profile 的本地进程
		// 会在 Runner 层 fail-closed，绝不把 Filesystem=false 当成“可以降级运行”。
		Filesystem:  false,
		ProcessTree: true,
		Network:     false,
		Reason:      "Windows 当前使用 Restricted Token + Job Object：可降低进程权限并约束进程树，但尚不能可靠实现 Workspace 文件边界或网络隔离；受限 Profile 的本地程序默认拒绝启动，除非用户显式关闭 Native Sandbox",
	}
}

func prepareNativeCommand(policy EffectivePolicy, executable string, rawArgs []string, _ string) (string, []string, func(), bool, error) {
	if policy.NativeMode == NativeOff {
		return executable, append([]string(nil), rawArgs...), nil, false, nil
	}
	return executable, append([]string(nil), rawArgs...), nil, true, nil
}

func configurePlatformProcess(cmd *exec.Cmd, policy EffectivePolicy) (func(), error) {
	if policy.NativeMode == NativeOff {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
		return nil, nil
	}

	current, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return nil, fmt.Errorf("打开当前进程 Token 失败: %w", err)
	}
	defer current.Close()

	// DISABLE_MAX_PRIVILEGE 会禁用除 SeChangeNotifyPrivilege 之外的 token privileges。
	// 这不是文件系统 namespace，因此 Capability.Filesystem 仍然保持 false；这里的目的
	// 是降低 FullAccess/显式 unsafe 进程的权限上限，而不是伪装成 Workspace 隔离。
	var restricted syscall.Token
	r1, _, callErr := procCreateRestrictedToken.Call(
		uintptr(current), uintptr(disableMaxPrivilege),
		0, 0,
		0, 0,
		0, 0,
		uintptr(unsafe.Pointer(&restricted)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("CreateRestrictedToken 失败: %w", callErr)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Token:         restricted,
		CreationFlags: createNewProcessGroup,
	}
	return func() { _ = restricted.Close() }, nil
}

func attachPlatformProcess(cmd *exec.Cmd, policy EffectivePolicy) (func() error, func(), error) {
	if policy.NativeMode == NativeOff {
		return func() error { return cmd.Process.Kill() }, nil, nil
	}

	job, _, callErr := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return nil, nil, fmt.Errorf("CreateJobObjectW 失败: %w", callErr)
	}
	closeJob := func() { _, _, _ = procCloseHandle.Call(job) }

	// KILL_ON_JOB_CLOSE 很重要：即使 Humbert 在正常收尾路径之外退出，只要最后一个
	// Job handle 被 OS 关闭，Job 中仍存活的后代进程也会被终止。显式 timeout/cancel
	// 仍通过 TerminateJobObject 立即结束整个进程树。
	limits := jobObjectExtendedLimitInformation{}
	limits.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r1, _, callErr := procSetInformationJobObject.Call(
		job,
		uintptr(jobObjectExtendedLimitInformationClass),
		uintptr(unsafe.Pointer(&limits)),
		unsafe.Sizeof(limits),
	)
	if r1 == 0 {
		closeJob()
		return nil, nil, fmt.Errorf("SetInformationJobObject(KILL_ON_JOB_CLOSE) 失败: %w", callErr)
	}

	process, _, callErr := procOpenProcess.Call(
		uintptr(processSetQuota|processTerminate),
		0,
		uintptr(uint32(cmd.Process.Pid)),
	)
	if process == 0 {
		closeJob()
		return nil, nil, fmt.Errorf("OpenProcess 失败: %w", callErr)
	}
	r1, _, callErr = procAssignProcessToJobObject.Call(job, process)
	_, _, _ = procCloseHandle.Call(process)
	if r1 == 0 {
		closeJob()
		return nil, nil, fmt.Errorf("AssignProcessToJobObject 失败: %w", callErr)
	}

	kill := func() error {
		r1, _, err := procTerminateJobObject.Call(job, 1)
		if r1 == 0 {
			return err
		}
		return nil
	}
	cleanup := func() {
		// 正常主进程退出后，关闭 Job handle。KILL_ON_JOB_CLOSE 会清理仍在运行的
		// detached descendants；不再依赖一次额外的 best-effort Terminate 调用。
		closeJob()
	}
	return kill, cleanup, nil
}
