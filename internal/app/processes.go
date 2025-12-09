package app

/*
#include <sys/sysctl.h>
#include <pwd.h>
#include <unistd.h>
#include <libproc.h>
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <mach/mach_init.h>
#include <mach/mach_time.h>

extern kern_return_t vm_deallocate(vm_map_t target_task, vm_address_t address, vm_size_t size);
*/
import "C"
import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unsafe"
)

// Global variables moved from app.go
var uidCache = make(map[uint32]string)
var uidCacheMutex sync.RWMutex

func getUsername(uid uint32) string {
	uidCacheMutex.RLock()
	name, ok := uidCache[uid]
	uidCacheMutex.RUnlock()
	if ok {
		return name
	}

	uidCacheMutex.Lock()
	defer uidCacheMutex.Unlock()

	// Double check
	if name, ok := uidCache[uid]; ok {
		return name
	}

	// Use C.getpwuid
	pwd := C.getpwuid(C.uid_t(uid))
	if pwd != nil {
		name = C.GoString(pwd.pw_name)
	} else {
		name = fmt.Sprintf("%d", uid)
	}
	uidCache[uid] = name
	return name
}

type ProcessTimeState struct {
	Time      uint64 // Total CPU Time (user + system) in nanoseconds
	Timestamp time.Time
}

var prevProcessTimes = make(map[int]ProcessTimeState)
var prevProcessTimesMutex sync.Mutex

// Timebase info for converting mach ticks to ns
var timebaseInfo C.mach_timebase_info_data_t
var timebaseOnce sync.Once

func getTimebase() {
	C.mach_timebase_info(&timebaseInfo)
}

func getProcessList() ([]ProcessMetrics, error) {
	mib := []C.int{C.CTL_KERN, C.KERN_PROC, C.KERN_PROC_ALL}
	var size C.size_t

	// Get buffer size
	if _, err := C.sysctl(&mib[0], 3, nil, &size, nil, 0); err != nil {
		return nil, fmt.Errorf("sysctl size check failed: %v", err)
	}

	// Allocate buffer
	buf := make([]byte, size)
	if _, err := C.sysctl(&mib[0], 3, unsafe.Pointer(&buf[0]), &size, nil, 0); err != nil {
		return nil, fmt.Errorf("sysctl fetch failed: %v", err)
	}

	count := int(size) / int(C.sizeof_struct_kinfo_proc)
	kprocs := (*[1 << 30]C.struct_kinfo_proc)(unsafe.Pointer(&buf[0]))[:count:count]

	var processes []ProcessMetrics
	now := time.Now()

	// Capture previous times for delta calc
	prevProcessTimesMutex.Lock()
	defer prevProcessTimesMutex.Unlock()

	// New cache to replace the old one (handling process termination cleanup implicitly)
	// Efficient way: create Next map.
	nextProcessTimes := make(map[int]ProcessTimeState)

	// Get Total Memory for % Calculation via sysctl
	// CTL_HW = 6, HW_MEMSIZE = 24
	// usage: sysctl([CTL_HW, HW_MEMSIZE])
	mibMem := []C.int{6, 24}
	var memSize C.uint64_t
	memLen := C.size_t(unsafe.Sizeof(memSize))
	totalMem := uint64(0)
	if _, err := C.sysctl(&mibMem[0], 2, unsafe.Pointer(&memSize), &memLen, nil, 0); err == nil {
		totalMem = uint64(memSize)
	}

	// Init timebase once
	timebaseOnce.Do(getTimebase)
	numer := uint64(timebaseInfo.numer)
	denom := uint64(timebaseInfo.denom)
	if denom == 0 {
		denom = 1
	} // safety

	for _, kp := range kprocs {
		pid := int(kp.kp_proc.p_pid)
		if pid == 0 {
			continue
		}

		comm := C.GoString(&kp.kp_proc.p_comm[0])

		// Try to get full command name via proc_pidpath
		var pathBuf [C.PROC_PIDPATHINFO_MAXSIZE]C.char
		if C.proc_pidpath(C.int(pid), unsafe.Pointer(&pathBuf), C.PROC_PIDPATHINFO_MAXSIZE) > 0 {
			fullPath := C.GoString(&pathBuf[0])
			comm = filepath.Base(fullPath)
		}

		// Get Task Info (Memory & Time) via libproc
		rssBytes := int64(0)
		vszBytes := int64(0)
		totalTimeNs := uint64(0)

		var taskInfo C.struct_proc_taskinfo
		ret := C.proc_pidinfo(C.int(pid), C.PROC_PIDTASKINFO, 0, unsafe.Pointer(&taskInfo), C.int(C.sizeof_struct_proc_taskinfo))
		if ret == C.int(C.sizeof_struct_proc_taskinfo) {
			rssBytes = int64(taskInfo.pti_resident_size)
			vszBytes = int64(taskInfo.pti_virtual_size)

			// Convert Mach Ticks to Nanoseconds
			// time_ns = ticks * (numer / denom)
			rawTime := uint64(taskInfo.pti_total_user) + uint64(taskInfo.pti_total_system)
			totalTimeNs = (rawTime * numer) / denom
		}

		// CPU Calculation (Delta)
		cpuPercent := 0.0
		if prevState, ok := prevProcessTimes[pid]; ok {
			timeDelta := totalTimeNs - prevState.Time
			wallDelta := now.Sub(prevState.Timestamp).Nanoseconds()

			if wallDelta > 0 && timeDelta > 0 { // Avoid divide by zero or negative
				// cpu = (cpu_delta / wall_delta) * 100
				cpuPercent = (float64(timeDelta) / float64(wallDelta)) * 100.0
			}
		}

		// Update state for next run
		nextProcessTimes[pid] = ProcessTimeState{
			Time:      totalTimeNs,
			Timestamp: now,
		}

		// Memory Calculation
		memPercent := 0.0
		if totalMem > 0 {
			memPercent = (float64(rssBytes) / float64(totalMem)) * 100.0
		}

		state := ""
		switch kp.kp_proc.p_stat {
		case C.SIDL:
			state = "I"
		case C.SRUN:
			state = "R"
		case C.SSLEEP:
			state = "S"
		case C.SSTOP:
			state = "T"
		case C.SZOMB:
			state = "Z"
		default:
			state = "?"
		}

		uid := uint32(kp.kp_eproc.e_ucred.cr_uid)
		user := getUsername(uid)

		// Format time string
		// totalTimeNs is nanoseconds.
		totalSeconds := float64(totalTimeNs) / 1e9
		timeStr := formatTime(totalSeconds)

		processes = append(processes, ProcessMetrics{
			PID:         pid,
			User:        user,
			CPU:         cpuPercent,
			Memory:      memPercent,
			VSZ:         vszBytes / 1024, // KB
			RSS:         rssBytes / 1024, // KB
			Command:     comm,
			State:       state,
			Started:     "",
			Time:        timeStr,
			LastUpdated: now,
		})
	}

	// Swap map
	prevProcessTimes = nextProcessTimes

	sort.Slice(processes, func(i, j int) bool {
		return processes[i].CPU > processes[j].CPU
	})

	if len(processes) > 500 {
		processes = processes[:500]
	}

	return processes, nil
}

func GetCPUUsage() ([]CPUUsage, error) {
	var numCPUs C.natural_t
	var cpuLoad *C.processor_cpu_load_info_data_t
	var cpuMsgCount C.mach_msg_type_number_t
	host := C.mach_host_self()
	kernReturn := C.host_processor_info(
		host,
		C.PROCESSOR_CPU_LOAD_INFO,
		&numCPUs,
		(*C.processor_info_array_t)(unsafe.Pointer(&cpuLoad)),
		&cpuMsgCount,
	)
	if kernReturn != C.KERN_SUCCESS {
		return nil, fmt.Errorf("error getting CPU info: %d", kernReturn)
	}
	defer C.vm_deallocate(
		C.mach_task_self_,
		(C.vm_address_t)(uintptr(unsafe.Pointer(cpuLoad))),
		C.vm_size_t(cpuMsgCount)*C.sizeof_processor_cpu_load_info_data_t,
	)
	cpuLoadInfo := (*[1 << 30]C.processor_cpu_load_info_data_t)(unsafe.Pointer(cpuLoad))[:numCPUs:numCPUs]
	cpuUsage := make([]CPUUsage, numCPUs)
	for i := 0; i < int(numCPUs); i++ {
		cpuUsage[i] = CPUUsage{
			User:   float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_USER]),
			System: float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_SYSTEM]),
			Idle:   float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_IDLE]),
			Nice:   float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_NICE]),
		}
	}
	return cpuUsage, nil
}
