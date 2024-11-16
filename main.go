// Copyright (c) 2024-2025 Carsen Klock under MIT License
// mactop is a simple terminal based Apple Silicon power monitor written in Go Lang! github.com/context-labs/mactop
package main

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <mach/mach_init.h>

extern kern_return_t vm_deallocate(vm_map_t target_task, vm_address_t address, vm_size_t size);
*/
import "C"
import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	ui "github.com/gizak/termui/v3"
	w "github.com/gizak/termui/v3/widgets"
	"github.com/shirou/gopsutil/mem"
	"howett.net/plist"
)

var (
	version                                      = "v0.2.2"
	cpuGauge, gpuGauge, memoryGauge              *w.Gauge
	modelText, PowerChart, NetworkInfo, helpText *w.Paragraph
	grid                                         *ui.Grid
	processList                                  *w.List
	sparkline, gpuSparkline                      *w.Sparkline
	sparklineGroup, gpuSparklineGroup            *w.SparklineGroup
	cpuCoreWidget                                *CPUCoreWidget
	selectedProcess                              int
	powerValues                                  = make([]float64, 35)
	lastUpdateTime                               time.Time
	stderrLogger                                 = log.New(os.Stderr, "", 0)
	currentGridLayout                            = "default"
	showHelp, partyMode                          = false, false
	updateInterval                               = 1000
	done                                         = make(chan struct{})
	currentColorIndex                            = 0
	colorOptions                                 = []ui.Color{ui.ColorWhite, ui.ColorGreen, ui.ColorBlue, ui.ColorCyan, ui.ColorMagenta, ui.ColorYellow, ui.ColorRed}
	partyTicker                                  *time.Ticker
	lastCPUTimes                                 []CPUUsage
	firstRun                                     = true
	processHistory                               = make(map[int]*ProcessMetrics)
	lastProcessUpdateTime                        = time.Now()
	currentSort                                  = "CPU" // Default sort by CPU
	sortReverse                                  = false // Toggle for reverse sorting
	columns                                      = []string{"PID", "USER", "VIRT", "RES", "CPU", "MEM", "TIME", "CMD"}
	selectedColumn                               = 4 // Default to CPU (0-based index)
	minPower                                     = math.MaxFloat64
	maxPowerSeen                                 = 0.1
	powerHistory                                 = make([]float64, 100)
	maxPower                                     = 0.0 // Track maximum power for better scaling
	gpuValues                                    = make([]float64, 65)
)

type CPUUsage struct {
	User   float64
	System float64
	Idle   float64
	Nice   float64
}

type CPUMetrics struct {
	EClusterActive, EClusterFreqMHz, PClusterActive, PClusterFreqMHz int
	ECores, PCores                                                   []int
	CoreMetrics                                                      map[string]int
	CPUW, GPUW, PackageW                                             float64
	CoreUsages                                                       []float64
	Throttled                                                        bool
}

type NetDiskMetrics struct {
	OutPacketsPerSec, OutBytesPerSec, InPacketsPerSec, InBytesPerSec, ReadOpsPerSec, WriteOpsPerSec, ReadKBytesPerSec, WriteKBytesPerSec float64
}

type GPUMetrics struct {
	FreqMHz, Active int
}
type ProcessMetrics struct {
	PID                                      int
	CPU, LastTime, Memory                    float64
	VSZ, RSS                                 int64
	User, TTY, State, Started, Time, Command string
	LastUpdated                              time.Time
}

type MemoryMetrics struct {
	Total, Used, Available, SwapTotal, SwapUsed uint64
}

type EventThrottler struct {
	timer       *time.Timer
	gracePeriod time.Duration

	C chan struct{}
}

type CPUCoreWidget struct {
	*ui.Block
	cores                  []float64
	labels                 []string
	eCoreCount, pCoreCount int
	modelName              string
}

func NewEventThrottler(gracePeriod time.Duration) *EventThrottler {
	return &EventThrottler{
		timer:       nil,
		gracePeriod: gracePeriod,
		C:           make(chan struct{}, 1),
	}
}

func NewCPUMetrics() CPUMetrics {
	return CPUMetrics{
		CoreMetrics: make(map[string]int),
		ECores:      make([]int, 0),
		PCores:      make([]int, 0),
	}
}

func (e *EventThrottler) Notify() {
	if e.timer != nil {
		return
	}

	e.timer = time.AfterFunc(e.gracePeriod, func() {
		e.timer = nil
		select {
		case e.C <- struct{}{}:
		default:
		}
	})
}

func GetCPUPercentages() ([]float64, error) {
	currentTimes, err := GetCPUUsage()
	if err != nil {
		return nil, err
	}
	if firstRun {
		lastCPUTimes = currentTimes
		firstRun = false
		return make([]float64, len(currentTimes)), nil
	}
	percentages := make([]float64, len(currentTimes))
	for i := range currentTimes {
		totalDelta := (currentTimes[i].User - lastCPUTimes[i].User) +
			(currentTimes[i].System - lastCPUTimes[i].System) +
			(currentTimes[i].Idle - lastCPUTimes[i].Idle) +
			(currentTimes[i].Nice - lastCPUTimes[i].Nice)

		activeDelta := (currentTimes[i].User - lastCPUTimes[i].User) +
			(currentTimes[i].System - lastCPUTimes[i].System) +
			(currentTimes[i].Nice - lastCPUTimes[i].Nice)

		if totalDelta > 0 {
			percentages[i] = (activeDelta / totalDelta) * 100.0
		}
		if percentages[i] < 0 {
			percentages[i] = 0
		} else if percentages[i] > 100 {
			percentages[i] = 100
		}
	}
	lastCPUTimes = currentTimes
	return percentages, nil
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

func NewCPUCoreWidget(modelInfo map[string]interface{}) *CPUCoreWidget {
	eCoreCount, _ := modelInfo["e_core_count"].(int)
	pCoreCount, _ := modelInfo["p_core_count"].(int)
	modelName, _ := modelInfo["name"].(string)
	totalCores := eCoreCount + pCoreCount

	labels := make([]string, totalCores)
	for i := 0; i < eCoreCount; i++ {
		labels[i] = fmt.Sprintf("E%d", i)
	}
	for i := 0; i < pCoreCount; i++ {
		labels[i+eCoreCount] = fmt.Sprintf("P%d", i)
	}

	return &CPUCoreWidget{
		Block:      ui.NewBlock(),
		cores:      make([]float64, totalCores),
		labels:     labels,
		eCoreCount: eCoreCount,
		pCoreCount: pCoreCount,
		modelName:  modelName,
	}
}

func (w *CPUCoreWidget) UpdateUsage(usage []float64) {
	w.cores = make([]float64, len(usage))
	copy(w.cores, usage)
}

func (w *CPUCoreWidget) Draw(buf *ui.Buffer) {
	w.Block.Draw(buf)
	if len(w.cores) == 0 {
		return
	}
	themeColor := w.BorderStyle.Fg
	totalCores := len(w.cores)
	cols := 4 // default for <= 16 cores
	if totalCores > 16 {
		cols = 8 // switch to 8 columns for > 16 cores
	}
	availableWidth := w.Inner.Dx()
	availableHeight := w.Inner.Dy()
	minColWidth := 20 // minimum width needed for a readable core display
	if (availableWidth / cols) < minColWidth {
		cols = max(1, availableWidth/minColWidth)
	}
	rows := (totalCores + cols - 1) / cols
	if rows > availableHeight {
		rows = availableHeight
		cols = (totalCores + rows - 1) / rows // Recalculate columns
	}
	barWidth := availableWidth / cols
	labelWidth := 2 // Width for core labels

	for i := 0; i < totalCores; i++ {
		col := i % cols
		row := i / cols
		actualIndex := col*rows + row

		if actualIndex >= totalCores || row >= rows {
			continue
		}

		x := w.Inner.Min.X + (col * barWidth)
		y := w.Inner.Min.Y + row

		if y >= w.Inner.Max.Y {
			continue
		}

		usage := w.cores[actualIndex]

		label := fmt.Sprintf("%d", actualIndex)
		buf.SetString(label, ui.NewStyle(themeColor), image.Pt(x, y))

		availWidth := barWidth - labelWidth - 2 // -2 for brackets
		if x+labelWidth+availWidth > w.Inner.Max.X {
			availWidth = w.Inner.Max.X - x - labelWidth
		}

		if availWidth < 9 {
			continue
		}

		usedWidth := int((usage / 100.0) * float64(availWidth-7))

		buf.SetString("[", ui.NewStyle(ui.ColorWhite),
			image.Pt(x+labelWidth, y))

		for bx := 0; bx < availWidth-7; bx++ {
			char := " "
			var color ui.Color
			if bx < usedWidth {
				char = "❚"
				switch {
				case usage >= 60:
					color = ui.ColorRed
				case usage >= 40:
					color = ui.ColorYellow
				case usage >= 30:
					color = ui.ColorCyan
				default:
					color = themeColor
				}
			} else {
				color = themeColor
			}
			buf.SetString(char, ui.NewStyle(color),
				image.Pt(x+labelWidth+1+bx, y))
		}
		percentage := fmt.Sprintf("%5.1f%%", usage)
		buf.SetString(percentage, ui.NewStyle(245),
			image.Pt(x+labelWidth+availWidth-7, y))

		buf.SetString("]", ui.NewStyle(ui.ColorWhite),
			image.Pt(x+labelWidth+availWidth-1, y))
	}
}

func setupUI() {
	appleSiliconModel := getSOCInfo()
	modelText, helpText = w.NewParagraph(), w.NewParagraph()
	modelText.Title = "Apple Silicon"
	helpText.Title = "mactop help menu"
	modelName, ok := appleSiliconModel["name"].(string)
	if !ok {
		modelName = "Unknown Model"
	}
	eCoreCount, ok := appleSiliconModel["e_core_count"].(int)
	if !ok {
		eCoreCount = 0 // Default or error value
	}
	pCoreCount, ok := appleSiliconModel["p_core_count"].(int)
	if !ok {
		pCoreCount = 0
	}
	gpuCoreCount, ok := appleSiliconModel["gpu_core_count"].(string)
	if !ok {
		gpuCoreCount = "?"
	}
	modelText.Text = fmt.Sprintf("%s\nTotal Cores: %d\nE-Cores: %d\nP-Cores: %d\nGPU Cores: %s",
		modelName,
		eCoreCount+pCoreCount,
		eCoreCount,
		pCoreCount,
		gpuCoreCount,
	)
	helpText.Text = "mactop is open source monitoring tool for Apple Silicon authored by Carsen Klock in Go Lang!\n\nRepo: github.com/context-labs/mactop\n\nControls:\n- r: Refresh the UI data manually\n- c: Cycle through UI color themes\n- p: Toggle party mode (color cycling)\n- l: Toggle the main display's layout\n- h or ?: Toggle this help menu\n- q or <C-c>: Quit the application\n\nStart Flags:\n--help, -h: Show this help menu\n--version, -v: Show the version of mactop\n--interval, -i: Set the powermetrics update interval in milliseconds. Default is 1000.\n--color, -c: Set the UI color. Default is none. Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'.\n\nVersion: " + version
	stderrLogger.Printf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %s", modelName, eCoreCount, pCoreCount, gpuCoreCount)

	processList = w.NewList()
	processList.Title = "Process List"
	processList.TextStyle = ui.NewStyle(ui.ColorGreen)
	processList.WrapText = false
	processList.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, ui.ColorGreen)
	processList.Rows = []string{}
	processList.SelectedRow = 0

	gauges := []*w.Gauge{
		w.NewGauge(), w.NewGauge(), w.NewGauge(),
	}
	titles := []string{"E-CPU Usage", "P-CPU Usage", "GPU Usage", "Memory Usage"}
	colors := []ui.Color{ui.ColorGreen, ui.ColorYellow, ui.ColorMagenta, ui.ColorBlue, ui.ColorCyan}
	for i, gauge := range gauges {
		gauge.Percent = 0
		gauge.Title = titles[i]
		gauge.BarColor = colors[i]
	}
	cpuGauge, gpuGauge, memoryGauge = gauges[0], gauges[1], gauges[2]

	PowerChart, NetworkInfo = w.NewParagraph(), w.NewParagraph()
	PowerChart.Title, NetworkInfo.Title = "Power Usage", "Network & Disk Info"

	termWidth, _ := ui.TerminalDimensions()
	numPoints := (termWidth / 2) / 2
	powerValues = make([]float64, numPoints)
	gpuValues = make([]float64, numPoints)

	sparkline = w.NewSparkline()
	sparkline.LineColor = ui.ColorGreen
	sparkline.MaxHeight = 10
	sparkline.Data = powerValues

	sparklineGroup = w.NewSparklineGroup(sparkline)

	gpuSparkline = w.NewSparkline()
	gpuSparkline.LineColor = ui.ColorGreen
	gpuSparkline.MaxHeight = 10
	gpuSparkline.Data = gpuValues
	gpuSparklineGroup = w.NewSparklineGroup(gpuSparkline)
	gpuSparklineGroup.Title = "GPU Usage History"

	updateProcessList()

	cpuCoreWidget = NewCPUCoreWidget(appleSiliconModel)
	eCoreCount = appleSiliconModel["e_core_count"].(int)
	pCoreCount = appleSiliconModel["p_core_count"].(int)
	cpuCoreWidget.Title = fmt.Sprintf("mactop - %d Cores (%dE/%dP)",
		eCoreCount+pCoreCount,
		eCoreCount,
		pCoreCount,
	)
	cpuGauge.Title = fmt.Sprintf("mactop - %d Cores (%dE/%dP)",
		eCoreCount+pCoreCount,
		eCoreCount,
		pCoreCount,
	)
}

func setupGrid() {
	grid = ui.NewGrid()

	grid.Set(
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, cpuGauge),
			// ui.NewCol(1.0/2, gpuSparklineGroup),
		),
		ui.NewRow(2.0/4,
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, gpuGauge),
				ui.NewRow(1.0/2,
					ui.NewCol(1.0/2, PowerChart),
					ui.NewCol(1.0/2, sparklineGroup),
				),
			),
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, memoryGauge),
				ui.NewRow(1.0/2,
					ui.NewCol(1.0/3, modelText),
					ui.NewCol(2.0/3, NetworkInfo),
				),
			),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func switchGridLayout() {
	if currentGridLayout == "default" {
		newGrid := ui.NewGrid()
		newGrid.Set(
			ui.NewRow(1.0/2, // This row now takes half the height of the grid
				ui.NewCol(1.0/2, cpuCoreWidget), ui.NewCol(1.0/2, ui.NewRow(1.0/2, gpuGauge), ui.NewCol(1.0, ui.NewRow(1.0, memoryGauge))), // ui.NewCol(1.0/2, ui.NewRow(1.0, ProcessInfo)), // ProcessInfo spans this entire column
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/6, modelText), ui.NewCol(1.0/3, NetworkInfo), ui.NewCol(1.0/4, PowerChart), ui.NewCol(1.0/4, sparklineGroup),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, processList),
			),
		)
		termWidth, termHeight := ui.TerminalDimensions()
		newGrid.SetRect(0, 0, termWidth, termHeight)
		grid = newGrid
		currentGridLayout = "alternative"
	} else {
		newGrid := ui.NewGrid()
		newGrid.Set(
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, cpuGauge),
			),
			ui.NewRow(2.0/4,
				ui.NewCol(1.0/2,
					ui.NewRow(1.0/2, gpuGauge),
					ui.NewRow(1.0/2,
						ui.NewCol(1.0/2, PowerChart),
						ui.NewCol(1.0/2, sparklineGroup),
					),
				),
				ui.NewCol(1.0/2,
					ui.NewRow(1.0/2, memoryGauge),
					ui.NewRow(1.0/2,
						ui.NewCol(1.0/3, modelText),
						ui.NewCol(2.0/3, NetworkInfo),
					),
				),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, processList),
			),
		)
		termWidth, termHeight := ui.TerminalDimensions()
		newGrid.SetRect(0, 0, termWidth, termHeight)
		grid = newGrid
		currentGridLayout = "default"
	}
}

func toggleHelpMenu() {
	showHelp = !showHelp
	if showHelp {
		newGrid := ui.NewGrid()
		newGrid.Set(
			ui.NewRow(1.0,
				ui.NewCol(1.0, helpText),
			),
		)
		termWidth, termHeight := ui.TerminalDimensions()
		helpTextGridWidth := termWidth
		helpTextGridHeight := termHeight
		x := (termWidth - helpTextGridWidth) / 2
		y := (termHeight - helpTextGridHeight) / 2
		newGrid.SetRect(x, y, x+helpTextGridWidth, y+helpTextGridHeight)
		grid = newGrid
	} else {
		currentGridLayout = map[bool]string{
			true:  "alternative",
			false: "default",
		}[currentGridLayout == "default"]
		switchGridLayout()
	}
	ui.Clear()
	ui.Render(grid)
}

func togglePartyMode() {
	partyMode = !partyMode
	if partyMode {
		partyTicker = time.NewTicker(time.Duration(updateInterval/2) * time.Millisecond)
		go func() {
			for range partyTicker.C {
				if !partyMode {
					partyTicker.Stop()
					return
				}
				cycleColors()
				ui.Clear()
				ui.Render(grid)
			}
		}()
	} else if partyTicker != nil {
		partyTicker.Stop()
	}
}

func StderrToLogfile(logfile *os.File) {
	syscall.Dup2(int(logfile.Fd()), 2)
}

func parseTimeString(timeStr string) float64 {
	var hours, minutes int
	var seconds float64
	if strings.Contains(timeStr, "h") {
		parts := strings.Split(timeStr, "h")
		fmt.Sscanf(parts[0], "%d", &hours)
		fmt.Sscanf(parts[1], "%d:%f", &minutes, &seconds)
	} else {
		fmt.Sscanf(timeStr, "%d:%f", &minutes, &seconds)
	}
	return float64(hours*3600) + float64(minutes*60) + seconds
}

func formatTime(seconds float64) string {
	hours := int(seconds) / 3600
	minutes := (int(seconds) / 60) % 60
	secs := int(seconds) % 60
	centisecs := int((seconds - float64(int(seconds))) * 100)

	if hours > 0 {
		return fmt.Sprintf("%dh%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%02d:%02d.%02d", minutes, secs, centisecs)
}

func formatMemorySize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%dM", bytes/MB)
	case bytes >= KB:
		return fmt.Sprintf("%dK", bytes/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func formatResMemorySize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	if bytes < MB { // If value seems too small, assume it's in KB
		bytes *= KB
	}
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%dM", bytes/MB)
	case bytes >= KB:
		return fmt.Sprintf("%dK", bytes/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func truncateWithEllipsis(s string, maxLen int) string {
	if maxLen <= 3 {
		return "..."
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func updateProcessList() {
	processes := getProcessList()
	themeColor := processList.TextStyle.Fg
	themeColorStr := "white" // Default color in case theme color isn't recognized
	switch themeColor {
	case ui.ColorRed:
		themeColorStr = "red"
	case ui.ColorGreen:
		themeColorStr = "green"
	case ui.ColorYellow:
		themeColorStr = "yellow"
	case ui.ColorBlue:
		themeColorStr = "blue"
	case ui.ColorMagenta:
		themeColorStr = "magenta"
	case ui.ColorCyan:
		themeColorStr = "cyan"
	case ui.ColorWhite:
		themeColorStr = "white"
	}
	termWidth, _ := 200, 200 // Fixed for calling repeatedly
	minWidth := 40           // Set a minimum width to prevent crashes
	availableWidth := max(termWidth-2, minWidth)
	maxWidths := map[string]int{
		"PID":  5,  // Minimum for PID
		"USER": 12, // Fixed maximum width for USER
		"VIRT": 6,  // For memory format
		"RES":  6,  // For memory format
		"CPU":  6,  // For "XX.X%"
		"MEM":  5,  // For "X.X%"
		"TIME": 8,  // For time format
		"CMD":  13, // Minimum for command
	}
	usedWidth := 0
	for col, width := range maxWidths {
		if col != "CMD" {
			usedWidth += width + 1 // +1 for separator
		}
	}
	maxWidths["CMD"] = availableWidth - usedWidth

	header := ""
	for i, col := range columns {
		width := maxWidths[col]
		format := ""
		switch col {
		case "PID":
			format = fmt.Sprintf("%%%ds", width) // Right-align
		case "USER":
			format = fmt.Sprintf("%%-%ds", width) // Left-align
		case "VIRT", "RES":
			format = fmt.Sprintf("%%%ds", width) // Right-align
		case "CPU", "MEM":
			format = fmt.Sprintf("%%%ds", width) // Right-align
		case "TIME":
			format = fmt.Sprintf("%%%ds", width) // Right-align
		case "CMD":
			format = fmt.Sprintf("%%-%ds", width) // Left-align
		}

		colText := fmt.Sprintf(format, col)
		if i == selectedColumn {
			if sortReverse {
				header += fmt.Sprintf("[%s↑](fg:black,bg:%s)", colText, themeColorStr)
			} else {
				header += fmt.Sprintf("[%s↓](fg:black,bg:%s)", colText, themeColorStr)
			}
		} else {
			header += fmt.Sprintf("[%s](fg:%s)", colText, themeColorStr)
		}

		if i < len(columns)-1 {
			header += "|"
		}
	}

	sort.Slice(processes, func(i, j int) bool {
		var result bool

		switch columns[selectedColumn] {
		case "PID":
			result = processes[i].PID < processes[j].PID
		case "USER":
			result = strings.ToLower(processes[i].User) < strings.ToLower(processes[j].User)
		case "VIRT":
			result = processes[i].VSZ > processes[j].VSZ
		case "RES":
			result = processes[i].RSS > processes[j].RSS
		case "CPU":
			result = processes[i].CPU > processes[j].CPU
		case "MEM":
			result = processes[i].Memory > processes[j].Memory
		case "TIME":
			iTime := parseTimeString(processes[i].Time)
			jTime := parseTimeString(processes[j].Time)
			result = iTime > jTime
		case "CMD":
			result = strings.ToLower(processes[i].Command) < strings.ToLower(processes[j].Command)
		default:
			result = processes[i].CPU > processes[j].CPU
		}

		if sortReverse {
			return !result
		}
		return result
	})

	items := make([]string, len(processes)+1) // +1 for header
	items[0] = header

	for i, p := range processes {
		seconds := parseTimeString(p.Time)
		timeStr := formatTime(seconds)
		virtStr := formatMemorySize(p.VSZ)
		resStr := formatResMemorySize(p.RSS)
		username := truncateWithEllipsis(p.User, maxWidths["USER"])

		items[i+1] = fmt.Sprintf("%*d %-*s %*s %*s %*.1f%% %*.1f%% %*s %-s",
			maxWidths["PID"], p.PID,
			maxWidths["USER"], username,
			maxWidths["VIRT"], virtStr,
			maxWidths["RES"], resStr,
			maxWidths["CPU"]-1, p.CPU, // -1 for % symbol
			maxWidths["MEM"]-1, p.Memory, // -1 for % symbol
			maxWidths["TIME"], timeStr,
			truncateWithEllipsis(p.Command, maxWidths["CMD"]),
		)
	}

	processList.Title = "Process List (↑/↓ scroll, ←/→ select column, Enter/Space to sort)"
	processList.Rows = items
}

func handleProcessListEvents(e ui.Event) {
	switch e.ID {
	case "<Up>":
		if processList.SelectedRow > 0 {
			processList.SelectedRow--
		}
	case "<Down>":
		if processList.SelectedRow < len(processList.Rows)-1 {
			processList.SelectedRow++
		}
	case "<Left>":
		if selectedColumn > 0 {
			selectedColumn--
			updateProcessList()
		}
	case "<Right>":
		if selectedColumn < len(columns)-1 {
			selectedColumn++
			updateProcessList()
		}
	case "<Enter>", "<Space>":
		sortReverse = !sortReverse
		updateProcessList()
	}
	ui.Render(processList, grid)
}

func cycleColors() {
	currentColorIndex = (currentColorIndex + 1) % len(colorOptions)
	color := colorOptions[currentColorIndex]

	ui.Theme.Block.Title.Fg, ui.Theme.Block.Border.Fg, ui.Theme.Paragraph.Text.Fg, ui.Theme.Gauge.Label.Fg, ui.Theme.Gauge.Bar = color, color, color, color, color
	ui.Theme.BarChart.Bars = []ui.Color{color}

	cpuGauge.BarColor, gpuGauge.BarColor, memoryGauge.BarColor = color, color, color
	processList.TextStyle, NetworkInfo.TextStyle, PowerChart.TextStyle = ui.NewStyle(color), ui.NewStyle(color), ui.NewStyle(color)
	processList.SelectedRowStyle, modelText.TextStyle, helpText.TextStyle = ui.NewStyle(ui.ColorBlack, color), ui.NewStyle(color), ui.NewStyle(color)

	cpuGauge.BorderStyle.Fg, cpuGauge.TitleStyle.Fg = color, color
	gpuGauge.BorderStyle.Fg, gpuGauge.TitleStyle.Fg, memoryGauge.BorderStyle.Fg, memoryGauge.TitleStyle.Fg = color, color, color, color
	processList.BorderStyle.Fg, processList.TitleStyle.Fg, NetworkInfo.BorderStyle.Fg, NetworkInfo.TitleStyle.Fg = color, color, color, color
	PowerChart.BorderStyle.Fg, PowerChart.TitleStyle.Fg = color, color
	modelText.BorderStyle.Fg, modelText.TitleStyle.Fg, helpText.BorderStyle.Fg, helpText.TitleStyle.Fg = color, color, color, color

	if sparkline != nil {
		sparkline.LineColor = color
		sparkline.TitleStyle = ui.NewStyle(color)
	}
	if sparklineGroup != nil {
		sparklineGroup.BorderStyle = ui.NewStyle(color)
		sparklineGroup.TitleStyle = ui.NewStyle(color)
	}

	cpuCoreWidget.BorderStyle.Fg, cpuCoreWidget.TitleStyle.Fg = color, color
	processList.TextStyle = ui.NewStyle(color)
	processList.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, color)
	processList.BorderStyle.Fg = color
	processList.TitleStyle.Fg = color
	updateProcessList()
	ui.Render(processList)
}

func main() {
	var (
		colorName             string
		interval              int
		err                   error
		setColor, setInterval bool
	)
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--help", "-h":
			fmt.Print("Usage: mactop [--help] [--version] [--interval] [--color]\n--help: Show this help message\n--version: Show the version of mactop\n--interval: Set the powermetrics update interval in milliseconds. Default is 1000.\n--color: Set the UI color. Default is none. Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'. (-c green)\n\nYou must use sudo to run mactop, as powermetrics requires root privileges.\n\nFor more information, see https://github.com/context-labs/mactop written by Carsen Klock.\n")
			os.Exit(0)
		case "--version", "-v":
			fmt.Println("mactop version:", version)
			os.Exit(0)
		case "--test", "-t":
			if i+1 < len(os.Args) {
				testInput := os.Args[i+1]
				fmt.Printf("Test input received: %s\n", testInput)
				os.Exit(0)
			}
		case "--color", "-c":
			if i+1 < len(os.Args) {
				colorName = strings.ToLower(os.Args[i+1])
				setColor = true
				i++
			} else {
				fmt.Println("Error: --color flag requires a color value")
				os.Exit(1)
			}
		case "--interval", "-i":
			if i+1 < len(os.Args) {
				interval, err = strconv.Atoi(os.Args[i+1])
				if err != nil {
					fmt.Println("Invalid interval:", err)
					os.Exit(1)
				}
				setInterval = true
				i++
			} else {
				fmt.Println("Error: --interval flag requires an interval value")
				os.Exit(1)
			}
		}
	}
	if os.Geteuid() != 0 {
		fmt.Println("Welcome to mactop! Please try again and run mactop with sudo privileges!")
		fmt.Println("Usage: sudo mactop")
		os.Exit(1)
	}
	logfile, err := setupLogfile()
	if err != nil {
		stderrLogger.Fatalf("failed to setup log file: %v", err)
	}
	defer logfile.Close()

	if err := ui.Init(); err != nil {
		stderrLogger.Fatalf("failed to initialize termui: %v", err)
	}
	defer ui.Close()
	StderrToLogfile(logfile)
	if setColor {
		var color ui.Color
		switch colorName {
		case "green":
			color = ui.ColorGreen
		case "red":
			color = ui.ColorRed
		case "blue":
			color = ui.ColorBlue
		case "cyan":
			color = ui.ColorCyan
		case "magenta":
			color = ui.ColorMagenta
		case "yellow":
			color = ui.ColorYellow
		case "white":
			color = ui.ColorWhite
		default:
			stderrLogger.Printf("Unsupported color: %s. Using default color.\n", colorName)
			color = ui.ColorWhite
		}
		ui.Theme.Block.Title.Fg, ui.Theme.Block.Border.Fg, ui.Theme.Paragraph.Text.Fg, ui.Theme.Gauge.Label.Fg, ui.Theme.Gauge.Bar = color, color, color, color, color
		ui.Theme.BarChart.Bars = []ui.Color{color}
		setupUI()
		cpuGauge.BarColor, gpuGauge.BarColor, memoryGauge.BarColor = color, color, color
		processList.TextStyle = ui.NewStyle(color)
		processList.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, color)
	} else {
		setupUI()
	}
	if setInterval {
		updateInterval = interval
	}
	setupGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	grid.SetRect(0, 0, termWidth, termHeight)
	cpuMetricsChan := make(chan CPUMetrics, 1)
	gpuMetricsChan := make(chan GPUMetrics, 1)
	netdiskMetricsChan := make(chan NetDiskMetrics, 1)
	go collectMetrics(done, cpuMetricsChan, gpuMetricsChan, netdiskMetricsChan)
	go func() {
		ticker := time.NewTicker(time.Duration(updateInterval) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case cpuMetrics := <-cpuMetricsChan:
				updateCPUUI(cpuMetrics)
				updateTotalPowerChart(cpuMetrics.PackageW)
				ui.Render(grid)
			case gpuMetrics := <-gpuMetricsChan:
				updateGPUUI(gpuMetrics)
				ui.Render(grid)
			case netdiskMetrics := <-netdiskMetricsChan:
				updateNetDiskUI(netdiskMetrics)
				ui.Render(grid)
			case <-ticker.C:
				percentages, err := GetCPUPercentages()
				if err != nil {
					stderrLogger.Printf("Error getting CPU percentages: %v\n", err)
					continue
				}
				cpuCoreWidget.UpdateUsage(percentages)
				var totalUsage float64
				for _, usage := range percentages {
					totalUsage += usage
				}
				totalUsage /= float64(len(percentages))

				cpuCoreWidget.Title = fmt.Sprintf("mactop - %d Cores (%dE/%dP) %.2f%%",
					cpuCoreWidget.eCoreCount+cpuCoreWidget.pCoreCount,
					cpuCoreWidget.eCoreCount,
					cpuCoreWidget.pCoreCount,
					totalUsage,
				)
				updateProcessList()
				ui.Render(grid)
			case <-done:
				return
			}
		}
	}()
	ui.Render(grid)
	done := make(chan struct{})
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer func() {
		if partyTicker != nil {
			partyTicker.Stop()
		}
	}()
	lastUpdateTime = time.Now()
	uiEvents := ui.PollEvents()
	for {
		select {
		case e := <-uiEvents:
			handleProcessListEvents(e)
			switch e.ID {
			case "q", "<C-c>":
				close(done)
				ui.Close()
				os.Exit(0)
				return
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				grid.SetRect(0, 0, payload.Width, payload.Height)
				ui.Render(grid)
			case "r":
				termWidth, termHeight := ui.TerminalDimensions()
				grid.SetRect(0, 0, termWidth, termHeight)
				ui.Clear()
				ui.Render(grid)
			case "p":
				togglePartyMode()
			case "c":
				termWidth, termHeight := ui.TerminalDimensions()
				grid.SetRect(0, 0, termWidth, termHeight)
				cycleColors()
				ui.Clear()
				ui.Render(grid)
			case "l":
				termWidth, termHeight := ui.TerminalDimensions()
				grid.SetRect(0, 0, termWidth, termHeight)
				ui.Clear()
				switchGridLayout()
				ui.Render(grid)
			case "h", "?":
				termWidth, termHeight := ui.TerminalDimensions()
				grid.SetRect(0, 0, termWidth, termHeight)
				ui.Clear()
				toggleHelpMenu()
				ui.Render(grid)
			case "j", "<Down>":
				if selectedProcess < len(processList.Rows)-1 {
					selectedProcess++
					ui.Render(processList)
				}
			case "k", "<Up>":
				if selectedProcess > 0 {
					selectedProcess--
					ui.Render(processList)
				}
			}
		case <-done:
			ui.Close()
			os.Exit(0)
			return
		}
	}
}

func setupLogfile() (*os.File, error) {
	if err := os.MkdirAll("/var/log", 0755); err != nil {
		return nil, fmt.Errorf("failed to make the log directory: %v", err)
	}
	logfile, err := os.OpenFile("/var/log/mactop.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}
	log.SetFlags(log.Ltime | log.Lshortfile)
	log.SetOutput(logfile)
	return logfile, nil
}

func collectMetrics(done chan struct{}, cpumetricsChan chan CPUMetrics, gpumetricsChan chan GPUMetrics, netdiskMetricsChan chan NetDiskMetrics) {
	cpumetricsChan <- CPUMetrics{}
	gpumetricsChan <- GPUMetrics{}
	netdiskMetricsChan <- NetDiskMetrics{}
	cmd := exec.Command("sudo", "powermetrics", "--samplers", "cpu_power,gpu_power,thermal,network,disk", "--show-initial-usage", "-f", "plist", "-i", strconv.Itoa(updateInterval))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		start := bytes.Index(data, []byte("<?xml"))
		if start == -1 {
			start = bytes.Index(data, []byte("<plist"))
		}
		if start >= 0 {
			if end := bytes.Index(data[start:], []byte("</plist>")); end >= 0 {
				return start + end + 8, data[start : start+end+8], nil
			}
		}
		if atEOF {
			return len(data), nil, nil
		}
		return 0, nil, nil
	})
	for scanner.Scan() {
		plistData := scanner.Text()
		if !strings.Contains(plistData, "<?xml") || !strings.Contains(plistData, "</plist>") {
			continue
		}
		var data map[string]interface{}
		err := plist.NewDecoder(strings.NewReader(plistData)).Decode(&data)
		if err != nil {
			log.Printf("Error decoding plist: %v", err)
			continue
		}
		select {
		case <-done:
			cmd.Process.Kill()
			return
		default:
			// Send all metrics at once
			cpuMetrics := parseCPUMetrics(data, NewCPUMetrics())
			gpuMetrics := parseGPUMetrics(data)
			netdiskMetrics := parseNetDiskMetrics(data)

			// Non-blocking sends
			select {
			case cpumetricsChan <- cpuMetrics:
			default:
			}
			select {
			case gpumetricsChan <- gpuMetrics:
			default:
			}
			select {
			case netdiskMetricsChan <- netdiskMetrics:
			default:
			}
		}
	}
}

func parseGPUMetrics(data map[string]interface{}) GPUMetrics {
	var gpuMetrics GPUMetrics
	if gpu, ok := data["gpu"].(map[string]interface{}); ok {
		if freqHz, ok := gpu["freq_hz"].(float64); ok {
			gpuMetrics.FreqMHz = int(freqHz)
		}
		if idleRatio, ok := gpu["idle_ratio"].(float64); ok {
			gpuMetrics.Active = int((1 - idleRatio) * 100)
		}
	}
	return gpuMetrics
}

func parseNetDiskMetrics(data map[string]interface{}) NetDiskMetrics {
	var metrics NetDiskMetrics
	if network, ok := data["network"].(map[string]interface{}); ok {
		if rate, ok := network["ibyte_rate"].(float64); ok {
			metrics.InBytesPerSec = rate / 1000
		}
		if rate, ok := network["obyte_rate"].(float64); ok {
			metrics.OutBytesPerSec = rate / 1000
		}
		if rate, ok := network["ipacket_rate"].(float64); ok {
			metrics.InPacketsPerSec = rate
		}
		if rate, ok := network["opacket_rate"].(float64); ok {
			metrics.OutPacketsPerSec = rate
		}
	}
	if disk, ok := data["disk"].(map[string]interface{}); ok {
		if rate, ok := disk["rbytes_per_s"].(float64); ok {
			metrics.ReadKBytesPerSec = rate / 1000
		}
		if rate, ok := disk["wbytes_per_s"].(float64); ok {
			metrics.WriteKBytesPerSec = rate / 1000
		}
		if rate, ok := disk["rops_per_s"].(float64); ok {
			metrics.ReadOpsPerSec = rate
		}
		if rate, ok := disk["wops_per_s"].(float64); ok {
			metrics.WriteOpsPerSec = rate
		}
	}
	return metrics
}

func getProcessList() []ProcessMetrics {
	cmd := exec.Command("ps", "aux") // ps aux has the same results as htop, TODO: better method in Cgo
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error getting process list: %v", err)
		return nil
	}
	processes := []ProcessMetrics{}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[2], 64)
		mem, _ := strconv.ParseFloat(fields[3], 64)
		vsz, _ := strconv.ParseInt(fields[4], 10, 64)
		rss, _ := strconv.ParseInt(fields[5], 10, 64)
		pid, _ := strconv.Atoi(fields[1])
		command := filepath.Base(fields[10])
		process := ProcessMetrics{User: fields[0], PID: pid, CPU: cpu, Memory: mem, VSZ: vsz, RSS: rss, TTY: fields[6], State: fields[7], Started: fields[8], Time: fields[9], Command: command}
		processes = append(processes, process)

	}
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].CPU > processes[j].CPU
	})
	return processes
}

func updateTotalPowerChart(watts float64) {
	if watts > maxPowerSeen {
		maxPowerSeen = watts * 1.1
	}
	scaledValue := int((watts / maxPowerSeen) * 8)
	if watts > 0 && scaledValue == 0 {
		scaledValue = 1 // Ensure non-zero values are visible
	}
	for i := 0; i < len(powerValues)-1; i++ {
		powerValues[i] = powerValues[i+1]
	}
	powerValues[len(powerValues)-1] = float64(scaledValue)
	var sum float64
	count := 0
	for _, v := range powerValues {
		if v > 0 { // Only count non-zero values
			actualWatts := (v / 8) * maxPowerSeen
			sum += actualWatts
			count++
		}
	}
	avgWatts := 0.0
	if count > 0 {
		avgWatts = sum / float64(count)
	}
	sparkline.Data = powerValues
	sparkline.MaxVal = 8 // Match MaxHeight
	sparklineGroup.Title = fmt.Sprintf("%.2f W Total (Max: %.2f W)", watts, maxPowerSeen)
	sparkline.Title = fmt.Sprintf("Avg: %.2f W", avgWatts)
}

func updateCPUUI(cpuMetrics CPUMetrics) {
	coreUsages, err := GetCPUPercentages()
	if err != nil {
		stderrLogger.Printf("Error getting CPU percentages: %v\n", err)
		return
	}
	cpuCoreWidget.UpdateUsage(coreUsages)
	var totalUsage float64
	for _, usage := range coreUsages {
		totalUsage += usage
	}
	totalUsage /= float64(len(coreUsages))
	cpuGauge.Percent = int(totalUsage)
	cpuGauge.Title = fmt.Sprintf("mactop - %d Cores (%dE/%dP) - CPU Usage: %.2f%%",
		cpuCoreWidget.eCoreCount+cpuCoreWidget.pCoreCount,
		cpuCoreWidget.eCoreCount,
		cpuCoreWidget.pCoreCount,
		totalUsage,
	)
	cpuCoreWidget.Title = fmt.Sprintf("mactop - %d Cores (%dE/%dP) %.2f%%",
		cpuCoreWidget.eCoreCount+cpuCoreWidget.pCoreCount,
		cpuCoreWidget.eCoreCount,
		cpuCoreWidget.pCoreCount,
		totalUsage,
	)
	PowerChart.Title = fmt.Sprintf("%.2f W CPU - %.2f W GPU", cpuMetrics.CPUW, cpuMetrics.GPUW)
	PowerChart.Text = fmt.Sprintf("CPU Power: %.2f W\nGPU Power: %.2f W\nTotal Power: %.2f W\nThermals: %s",
		cpuMetrics.CPUW,
		cpuMetrics.GPUW,
		cpuMetrics.PackageW,
		map[bool]string{
			true:  "Throttled!",
			false: "Nominal",
		}[cpuMetrics.Throttled],
	)
	memoryMetrics := getMemoryMetrics()
	memoryGauge.Title = fmt.Sprintf("Memory Usage: %.2f GB / %.2f GB (Swap: %.2f/%.2f GB)", float64(memoryMetrics.Used)/1024/1024/1024, float64(memoryMetrics.Total)/1024/1024/1024, float64(memoryMetrics.SwapUsed)/1024/1024/1024, float64(memoryMetrics.SwapTotal)/1024/1024/1024)
	memoryGauge.Percent = int((float64(memoryMetrics.Used) / float64(memoryMetrics.Total)) * 100)
}

func updateGPUUI(gpuMetrics GPUMetrics) {
	gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz", int(gpuMetrics.Active), gpuMetrics.FreqMHz)
	gpuGauge.Percent = int(gpuMetrics.Active)

	// Add GPU history tracking
	for i := 0; i < len(gpuValues)-1; i++ {
		gpuValues[i] = gpuValues[i+1]
	}
	gpuValues[len(gpuValues)-1] = float64(gpuMetrics.Active)

	// Calculate average GPU usage
	var sum float64
	count := 0
	for _, v := range gpuValues {
		if v > 0 {
			sum += v
			count++
		}
	}
	avgGPU := 0.0
	if count > 0 {
		avgGPU = sum / float64(count)
	}

	gpuSparkline.Data = gpuValues
	gpuSparkline.MaxVal = 100 // GPU usage is 0-100%
	gpuSparklineGroup.Title = fmt.Sprintf("GPU: %d%% (Avg: %.1f%%)", gpuMetrics.Active, avgGPU)
}

func getDiskStorage() (total, used, available string) {
	cmd := exec.Command("df", "-h", "/")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.Output()
	if err != nil {
		return "N/A", "N/A", "N/A"
	}
	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return "N/A", "N/A", "N/A"
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 6 {
		return "N/A", "N/A", "N/A"
	}
	totalBytes := parseSize(fields[1])
	availBytes := parseSize(fields[3])
	usedBytes := totalBytes - availBytes
	return formatGigabytes(totalBytes), formatGigabytes(usedBytes), formatGigabytes(availBytes)
}

func parseSize(size string) float64 {
	var value float64
	var unit string
	fmt.Sscanf(size, "%f%s", &value, &unit)
	multiplier := 1.0
	switch strings.ToLower(strings.TrimSuffix(unit, "i")) {
	case "k", "kb":
		multiplier = 1000
	case "m", "mb":
		multiplier = 1000 * 1000
	case "g", "gb":
		multiplier = 1000 * 1000 * 1000
	case "t", "tb":
		multiplier = 1000 * 1000 * 1000 * 1000
	}
	return value * multiplier
}

func formatGigabytes(bytes float64) string {
	gb := bytes / (1000 * 1000 * 1000)
	return fmt.Sprintf("%.0fGB", gb)
}

func updateNetDiskUI(netdiskMetrics NetDiskMetrics) {
	total, used, available := getDiskStorage()
	NetworkInfo.Text = fmt.Sprintf("Out: %.1f p/s, %.1f KB/s\n"+"In: %.1f p/s, %.1f KB/s\n"+"Read: %.1f ops/s, %.1f KB/s\n"+"Write: %.1f ops/s, %.1f KB/s\n"+"%s U / %s T / %s A", netdiskMetrics.OutPacketsPerSec, netdiskMetrics.OutBytesPerSec, netdiskMetrics.InPacketsPerSec, netdiskMetrics.InBytesPerSec, netdiskMetrics.ReadOpsPerSec, netdiskMetrics.ReadKBytesPerSec, netdiskMetrics.WriteOpsPerSec, netdiskMetrics.WriteKBytesPerSec, used, total, available)
}

func parseCPUMetrics(data map[string]interface{}, cpuMetrics CPUMetrics) CPUMetrics {
	processor, ok := data["processor"].(map[string]interface{})
	if !ok {
		stderrLogger.Fatalf("Failed to get processor data\n")
		return cpuMetrics
	}

	thermal, ok := data["thermal_pressure"].(string)
	if !ok {
		stderrLogger.Fatalf("Failed to get thermal data\n")
	}

	cpuMetrics.Throttled = thermal != "Nominal"

	eCores := []int{}
	pCores := []int{}
	cpuMetrics.ECores = eCores
	cpuMetrics.PCores = pCores

	if cpuEnergy, ok := processor["cpu_power"].(float64); ok {
		cpuMetrics.CPUW = float64(cpuEnergy) / 1000
	}
	if gpuEnergy, ok := processor["gpu_power"].(float64); ok {
		cpuMetrics.GPUW = float64(gpuEnergy) / 1000
	}
	if combinedPower, ok := processor["combined_power"].(float64); ok {
		cpuMetrics.PackageW = float64(combinedPower) / 1000
	}

	return cpuMetrics
}

func max(nums ...int) int {
	maxVal := nums[0]
	for _, num := range nums[1:] {
		if num > maxVal {
			maxVal = num
		}
	}
	return maxVal
}

func getSOCInfo() map[string]interface{} {
	cpuInfoDict := getCPUInfo()
	coreCountsDict := getCoreCounts()
	var eCoreCounts, pCoreCounts int
	if val, ok := coreCountsDict["hw.perflevel1.logicalcpu"]; ok {
		eCoreCounts = val
	}
	if val, ok := coreCountsDict["hw.perflevel0.logicalcpu"]; ok {
		pCoreCounts = val
	}
	socInfo := map[string]interface{}{
		"name":           cpuInfoDict["machdep.cpu.brand_string"],
		"core_count":     cpuInfoDict["machdep.cpu.core_count"],
		"cpu_max_power":  nil,
		"gpu_max_power":  nil,
		"cpu_max_bw":     nil,
		"gpu_max_bw":     nil,
		"e_core_count":   eCoreCounts,
		"p_core_count":   pCoreCounts,
		"gpu_core_count": getGPUCores(),
	}
	return socInfo
}

func getMemoryMetrics() MemoryMetrics {
	v, _ := mem.VirtualMemory()
	s, _ := mem.SwapMemory()
	totalMemory := v.Total
	usedMemory := v.Used
	availableMemory := v.Available
	swapTotal := s.Total
	swapUsed := s.Used
	return MemoryMetrics{
		Total:     totalMemory,
		Used:      usedMemory,
		Available: availableMemory,
		SwapTotal: swapTotal,
		SwapUsed:  swapUsed,
	}
}

func getCPUInfo() map[string]string {
	out, err := exec.Command("sysctl", "machdep.cpu").Output()
	if err != nil {
		stderrLogger.Fatalf("failed to execute getCPUInfo() sysctl command: %v", err)
	}
	cpuInfo := string(out)
	cpuInfoLines := strings.Split(cpuInfo, "\n")
	dataFields := []string{"machdep.cpu.brand_string", "machdep.cpu.core_count"}
	cpuInfoDict := make(map[string]string)
	for _, line := range cpuInfoLines {
		for _, field := range dataFields {
			if strings.Contains(line, field) {
				value := strings.TrimSpace(strings.Split(line, ":")[1])
				cpuInfoDict[field] = value
			}
		}
	}
	return cpuInfoDict
}

func getCoreCounts() map[string]int {
	cmd := exec.Command("sysctl", "hw.perflevel0.logicalcpu", "hw.perflevel1.logicalcpu")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.Output()
	if err != nil {
		stderrLogger.Fatalf("failed to execute getCoreCounts() sysctl command: %v", err)
	}
	coresInfo := string(out)
	coresInfoLines := strings.Split(coresInfo, "\n")
	dataFields := []string{"hw.perflevel0.logicalcpu", "hw.perflevel1.logicalcpu"}
	coresInfoDict := make(map[string]int)
	for _, line := range coresInfoLines {
		for _, field := range dataFields {
			if strings.Contains(line, field) {
				value, _ := strconv.Atoi(strings.TrimSpace(strings.Split(line, ":")[1]))
				coresInfoDict[field] = value
			}
		}
	}
	return coresInfoDict
}

func getGPUCores() string {
	cmd, err := exec.Command("system_profiler", "-detailLevel", "basic", "SPDisplaysDataType").Output()
	if err != nil {
		stderrLogger.Fatalf("failed to execute system_profiler command: %v", err)
	}
	output := string(cmd)
	stderrLogger.Printf("Output: %s\n", output)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Total Number of Cores") {
			parts := strings.Split(line, ": ")
			if len(parts) > 1 {
				cores := strings.TrimSpace(parts[1])
				return cores
			}
			break
		}
	}
	return "?"
}
