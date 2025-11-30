// Copyright (c) 2024-2026 Carsen Klock under MIT License
// mactop is a simple terminal based Apple Silicon power monitor written in Go Lang! github.com/context-labs/mactop
package main

/*
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <mach/mach_init.h>

extern kern_return_t vm_deallocate(vm_map_t target_task, vm_address_t address, vm_size_t size);
*/
import "C"
import (
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	ui "github.com/gizak/termui/v3"
	w "github.com/gizak/termui/v3/widgets"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

var (
	version                                      = "v0.2.5"
	cpuGauge, gpuGauge, memoryGauge, aneGauge    *w.Gauge
	modelText, PowerChart, NetworkInfo, helpText *w.Paragraph
	grid                                         *ui.Grid
	processList                                  *w.List
	sparkline, gpuSparkline                      *w.Sparkline
	sparklineGroup, gpuSparklineGroup            *w.SparklineGroup
	cpuCoreWidget                                *CPUCoreWidget
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
	sortReverse                                  = false
	columns                                      = []string{"PID", "USER", "VIRT", "RES", "CPU", "MEM", "TIME", "CMD"}
	selectedColumn                               = 4
	maxPowerSeen                                 = 0.1
	gpuValues                                    = make([]float64, 100)
	prometheusPort                               string
	ttyFile                                      *os.File
	lastNetStats                                 net.IOCountersStat
	lastDiskStats                                disk.IOCountersStat
	lastNetDiskTime                              time.Time
	netDiskMutex                                 sync.Mutex
)

var (
	cpuUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mactop_cpu_usage_percent",
			Help: "Current total CPU usage percentage",
		},
	)

	ecoreUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mactop_ecore_usage_percent",
			Help: "Current E-core CPU usage percentage",
		},
	)

	pcoreUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mactop_pcore_usage_percent",
			Help: "Current P-core CPU usage percentage",
		},
	)

	gpuUsage = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mactop_gpu_usage_percent",
			Help: "Current GPU usage percentage",
		},
	)

	gpuFreqMHz = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mactop_gpu_freq_mhz",
			Help: "Current GPU frequency in MHz",
		},
	)

	powerUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mactop_power_watts",
			Help: "Current power usage in watts",
		},
		[]string{"component"},
	)

	socTemp = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mactop_soc_temp_celsius",
			Help: "Current SoC temperature in Celsius",
		},
	)

	thermalState = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "mactop_thermal_state",
			Help: "Current thermal state (0=Nominal, 1=Fair, 2=Serious, 3=Critical)",
		},
	)

	memoryUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mactop_memory_gb",
			Help: "Memory usage in GB",
		},
		[]string{"type"},
	)

	networkSpeed = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mactop_network_kbytes_per_sec",
			Help: "Network speed in KB/s",
		},
		[]string{"direction"},
	)

	diskIOSpeed = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mactop_disk_kbytes_per_sec",
			Help: "Disk I/O speed in KB/s",
		},
		[]string{"operation"},
	)

	diskIOPS = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mactop_disk_iops",
			Help: "Disk I/O operations per second",
		},
		[]string{"operation"},
	)
)

func startPrometheusServer(port string) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(cpuUsage)
	registry.MustRegister(ecoreUsage)
	registry.MustRegister(pcoreUsage)
	registry.MustRegister(gpuUsage)
	registry.MustRegister(gpuFreqMHz)
	registry.MustRegister(powerUsage)
	registry.MustRegister(socTemp)
	registry.MustRegister(thermalState)
	registry.MustRegister(memoryUsage)
	registry.MustRegister(networkSpeed)
	registry.MustRegister(diskIOSpeed)
	registry.MustRegister(diskIOPS)

	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	http.Handle("/metrics", handler)
	go func() {
		err := http.ListenAndServe(":"+port, nil)
		if err != nil {
			stderrLogger.Printf("Failed to start Prometheus metrics server: %v\n", err)
		}
	}()
}

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
	ANEW, CPUW, GPUW, DRAMW, PackageW                                float64
	CoreUsages                                                       []float64
	Throttled                                                        bool
	SocTemp                                                          float64
}

type NetDiskMetrics struct {
	OutPacketsPerSec, OutBytesPerSec, InPacketsPerSec, InBytesPerSec, ReadOpsPerSec, WriteOpsPerSec, ReadKBytesPerSec, WriteKBytesPerSec float64
}

type GPUMetrics struct {
	FreqMHz, Active int
	Temp            float64
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
	C           chan struct{}
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
	prometheusStatus := "Disabled"
	if prometheusPort != "" {
		prometheusStatus = fmt.Sprintf("Enabled (Port: %s)", prometheusPort)
	}
	helpText.Text = fmt.Sprintf(
		"mactop is open source monitoring tool for Apple Silicon authored by Carsen Klock in Go Lang!\n\n"+
			"Repo: github.com/context-labs/mactop\n\n"+
			"Prometheus Metrics: %s\n\n"+
			"Controls:\n"+
			"- r: Refresh the UI data manually\n"+
			"- c: Cycle through UI color themes\n"+
			"- p: Toggle party mode (color cycling)\n"+
			"- l: Toggle the main display's layout\n"+
			"- h or ?: Toggle this help menu\n"+
			"- q or <C-c>: Quit the application\n\n"+
			"Start Flags:\n"+
			"--help, -h: Show this help menu\n"+
			"--version, -v: Show the version of mactop\n"+
			"--interval, -i: Set the update interval in milliseconds. Default is 1000.\n"+
			"--prometheus, -p: Set and enable a Prometheus metrics port. Default is none. (e.g. --prometheus=9090)\n"+
			"--color, -c: Set the UI color. Default is none. Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'.\n\n"+
			"Version: %s",
		prometheusStatus,
		version,
	)
	stderrLogger.Printf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %s", modelName, eCoreCount, pCoreCount, gpuCoreCount)

	processList = w.NewList()
	processList.Title = "Process List"
	processList.TextStyle = ui.NewStyle(ui.ColorGreen)
	processList.WrapText = false
	processList.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, ui.ColorGreen)
	processList.Rows = []string{}
	processList.SelectedRow = 0

	gauges := []*w.Gauge{
		w.NewGauge(), w.NewGauge(), w.NewGauge(), w.NewGauge(),
	}
	titles := []string{"E-CPU Usage", "P-CPU Usage", "GPU Usage", "Memory Usage", "ANE Usage"}
	colors := []ui.Color{ui.ColorGreen, ui.ColorYellow, ui.ColorMagenta, ui.ColorBlue, ui.ColorCyan}
	for i, gauge := range gauges {
		gauge.Percent = 0
		gauge.Title = titles[i]
		gauge.BarColor = colors[i]
	}
	cpuGauge, gpuGauge, memoryGauge, aneGauge = gauges[0], gauges[1], gauges[2], gauges[3]

	PowerChart, NetworkInfo = w.NewParagraph(), w.NewParagraph()
	PowerChart.Title, NetworkInfo.Title = "Power Usage", "Network & Disk Info"

	termWidth, _ := ui.TerminalDimensions()
	numPoints := (termWidth / 2) / 2
	numPointsGPU := (termWidth / 2)
	powerValues = make([]float64, numPoints)
	gpuValues = make([]float64, numPointsGPU)

	sparkline = w.NewSparkline()
	sparkline.LineColor = ui.ColorGreen
	sparkline.MaxHeight = 10
	sparkline.Data = powerValues

	sparklineGroup = w.NewSparklineGroup(sparkline)

	gpuSparkline = w.NewSparkline()
	gpuSparkline.LineColor = ui.ColorGreen
	gpuSparkline.MaxHeight = 100
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
			ui.NewCol(1.0/2, cpuGauge),
			ui.NewCol(1.0/2, gpuGauge),
		),
		ui.NewRow(2.0/4,
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, aneGauge),
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
				ui.NewCol(1.0/2, cpuGauge),
				ui.NewCol(1.0/2, aneGauge),
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

func pollKeyboardInput(tty *os.File) <-chan string {
	ch := make(chan string)
	go func() {
		buf := make([]byte, 16)
		for {
			n, err := tty.Read(buf)
			if err != nil {
				close(ch)
				return
			}
			if n > 0 {
				if n >= 3 && buf[0] == 27 && (buf[1] == 91 || buf[1] == 79) {
					switch buf[2] {
					case 65:
						ch <- "<Up>"
					case 66:
						ch <- "<Down>"
					case 67:
						ch <- "<Right>"
					case 68:
						ch <- "<Left>"
					default:
						ch <- "<Escape>"
					}
				} else if n == 1 {
					b := buf[0]
					switch b {
					case 3:
						ch <- "<C-c>"
					case 27:
						ch <- "<Escape>"
					case 13, 10:
						ch <- "<Enter>"
					case 32:
						ch <- "<Space>"
					default:
						ch <- string(b)
					}
				} else if n == 2 && buf[0] == 27 {
					ch <- "<Escape>"
				}
			}
		}
	}()
	return ch
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

func formatMemorySize(kb int64) string {
	const (
		MB = 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case kb >= TB:
		return fmt.Sprintf("%.1fT", float64(kb)/float64(TB))
	case kb >= GB:
		return fmt.Sprintf("%.1fG", float64(kb)/float64(GB))
	case kb >= MB:
		return fmt.Sprintf("%dM", kb/MB)
	default:
		return fmt.Sprintf("%dK", kb)
	}
}

func formatResMemorySize(kb int64) string {
	const (
		MB = 1024
		GB = MB * 1024
	)
	switch {
	case kb >= GB:
		return fmt.Sprintf("%.1fG", float64(kb)/float64(GB))
	case kb >= MB:
		return fmt.Sprintf("%dM", kb/MB)
	default:
		return fmt.Sprintf("%dK", kb)
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
	case "<Up>", "k":
		if processList.SelectedRow > 0 {
			processList.SelectedRow--
		}
	case "<Down>", "j":
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

	cpuGauge.BarColor, gpuGauge.BarColor, memoryGauge.BarColor, aneGauge.BarColor = color, color, color, color
	processList.TextStyle, NetworkInfo.TextStyle, PowerChart.TextStyle = ui.NewStyle(color), ui.NewStyle(color), ui.NewStyle(color)
	processList.SelectedRowStyle, modelText.TextStyle, helpText.TextStyle = ui.NewStyle(ui.ColorBlack, color), ui.NewStyle(color), ui.NewStyle(color)

	cpuGauge.BorderStyle.Fg, cpuGauge.TitleStyle.Fg = color, color
	aneGauge.BorderStyle.Fg, aneGauge.TitleStyle.Fg = color, color
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
	if gpuSparkline != nil {
		gpuSparkline.LineColor = color
		gpuSparkline.TitleStyle = ui.NewStyle(color)
	}
	if gpuSparklineGroup != nil {
		gpuSparklineGroup.BorderStyle = ui.NewStyle(color)
		gpuSparklineGroup.TitleStyle = ui.NewStyle(color)
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
			fmt.Print("Usage: mactop [--help] [--version] [--interval] [--color]\n--help: Show this help message\n--version: Show the version of mactop\n--interval: Set the update interval in milliseconds. Default is 1000.\n--color: Set the UI color. Default is none. Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'. (-c green)\n\nFor more information, see https://github.com/context-labs/mactop written by Carsen Klock.\n")
			os.Exit(0)
		case "--version", "-v":
			fmt.Println("mactop version:", version)
			os.Exit(0)
		case "--test", "-t":
			fmt.Println("Testing IOReport power metrics...")
			initSocMetrics()
			for i := 0; i < 3; i++ {
				m := sampleSocMetrics(500)
				thermalStr, _ := getThermalStateString()
				fmt.Printf("Sample %d:\n", i+1)
				fmt.Printf("  SoC Temp: %.1f°C\n", m.SocTemp)
				fmt.Printf("  CPU: %.2fW | GPU: %.2fW (%d MHz, %.0f%% active)\n",
					m.CPUPower, m.GPUPower, m.GPUFreqMHz, m.GPUActive)
				fmt.Printf("  ANE: %.2fW | DRAM: %.2fW | Total: %.2fW | %s\n",
					m.ANEPower, m.DRAMPower, m.TotalPower, thermalStr)
				fmt.Println()
			}
			cleanupSocMetrics()
			os.Exit(0)
		case "--color", "-c":
			if i+1 < len(os.Args) {
				colorName = strings.ToLower(os.Args[i+1])
				setColor = true
				i++
			} else {
				fmt.Println("Error: --color flag requires a color value")
				os.Exit(1)
			}
		case "--prometheus", "-p":
			if i+1 < len(os.Args) {
				prometheusPort = os.Args[i+1]
				i++
			} else {
				fmt.Println("Error: --prometheus flag requires a port number")
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

	ttyFile, err = os.Open("/dev/tty")
	if err != nil {
		ui.Close()
		stderrLogger.Fatalf("failed to open /dev/tty: %v", err)
	}
	defer ttyFile.Close()

	if prometheusPort != "" {
		startPrometheusServer(prometheusPort)
		stderrLogger.Printf("Prometheus metrics available at http://localhost:%s/metrics\n", prometheusPort)
	}
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
		cpuGauge.BarColor, gpuGauge.BarColor, memoryGauge.BarColor, aneGauge.BarColor = color, color, color, color
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
	keyboardInput := pollKeyboardInput(ttyFile)
	uiEvents := ui.PollEvents()
	for {
		select {
		case key := <-keyboardInput:
			fakeEvent := ui.Event{Type: ui.KeyboardEvent, ID: key}
			handleProcessListEvents(fakeEvent)
			switch key {
			case "q", "<C-c>":
				close(done)
				ui.Close()
				os.Exit(0)
				return
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
			}
		case e := <-uiEvents:
			if e.ID == "<Resize>" {
				payload := e.Payload.(ui.Resize)
				grid.SetRect(0, 0, payload.Width, payload.Height)
				ui.Render(grid)
			}
		case <-done:
			ui.Close()
			os.Exit(0)
			return
		}
	}
}

func setupLogfile() (*os.File, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.TempDir()
	}
	logDir := filepath.Join(homeDir, ".mactop")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to make the log directory: %v", err)
	}
	logPath := filepath.Join(logDir, "mactop.log")
	logfile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}
	log.SetFlags(log.Ltime | log.Lshortfile)
	log.SetOutput(logfile)
	return logfile, nil
}

func getThermalStateString() (string, bool) {
	state := getSocThermalState()
	states := []string{"Nominal", "Fair", "Serious", "Critical"}
	if state >= 0 && state < len(states) {
		return states[state], state > 0
	}
	return "Unknown", false
}

func getNetDiskMetrics() NetDiskMetrics {
	var metrics NetDiskMetrics

	netDiskMutex.Lock()
	defer netDiskMutex.Unlock()

	now := time.Now()
	elapsed := now.Sub(lastNetDiskTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	netStats, err := net.IOCounters(false)
	if err == nil && len(netStats) > 0 {
		current := netStats[0]
		if lastNetDiskTime.IsZero() {
			lastNetStats = current
		} else {
			metrics.InBytesPerSec = float64(current.BytesRecv-lastNetStats.BytesRecv) / elapsed / 1000
			metrics.OutBytesPerSec = float64(current.BytesSent-lastNetStats.BytesSent) / elapsed / 1000
			metrics.InPacketsPerSec = float64(current.PacketsRecv-lastNetStats.PacketsRecv) / elapsed
			metrics.OutPacketsPerSec = float64(current.PacketsSent-lastNetStats.PacketsSent) / elapsed
		}
		lastNetStats = current
	}

	diskStats, err := disk.IOCounters()
	if err == nil {
		var totalReadBytes, totalWriteBytes, totalReadOps, totalWriteOps uint64
		for _, d := range diskStats {
			totalReadBytes += d.ReadBytes
			totalWriteBytes += d.WriteBytes
			totalReadOps += d.ReadCount
			totalWriteOps += d.WriteCount
		}
		if !lastNetDiskTime.IsZero() {
			metrics.ReadKBytesPerSec = float64(totalReadBytes-lastDiskStats.ReadBytes) / elapsed / 1000
			metrics.WriteKBytesPerSec = float64(totalWriteBytes-lastDiskStats.WriteBytes) / elapsed / 1000
			metrics.ReadOpsPerSec = float64(totalReadOps-lastDiskStats.ReadCount) / elapsed
			metrics.WriteOpsPerSec = float64(totalWriteOps-lastDiskStats.WriteCount) / elapsed
		}
		lastDiskStats = disk.IOCountersStat{
			ReadBytes:  totalReadBytes,
			WriteBytes: totalWriteBytes,
			ReadCount:  totalReadOps,
			WriteCount: totalWriteOps,
		}
	}

	lastNetDiskTime = now
	return metrics
}

func collectMetrics(done chan struct{}, cpumetricsChan chan CPUMetrics, gpumetricsChan chan GPUMetrics, netdiskMetricsChan chan NetDiskMetrics) {
	cpumetricsChan <- CPUMetrics{}
	gpumetricsChan <- GPUMetrics{}
	netdiskMetricsChan <- NetDiskMetrics{}

	if err := initSocMetrics(); err != nil {
		stderrLogger.Printf("Warning: Failed to initialize IOReport, metrics may be unavailable\n")
	}
	defer cleanupSocMetrics()

	getNetDiskMetrics()

	ticker := time.NewTicker(time.Duration(updateInterval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			sampleDuration := updateInterval
			if sampleDuration < 100 {
				sampleDuration = 100
			}

			m := sampleSocMetrics(sampleDuration / 2)

			_, throttled := getThermalStateString()

			cpuMetrics := CPUMetrics{
				CPUW:      m.CPUPower,
				GPUW:      m.GPUPower,
				ANEW:      m.ANEPower,
				DRAMW:     m.DRAMPower,
				PackageW:  m.TotalPower,
				Throttled: throttled,
				SocTemp:   m.SocTemp,
			}

			gpuMetrics := GPUMetrics{
				FreqMHz: m.GPUFreqMHz,
				Active:  int(m.GPUActive),
				Temp:    m.SocTemp,
			}

			netdiskMetrics := getNetDiskMetrics()

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

func getProcessList() []ProcessMetrics {
	cmd := exec.Command("ps", "aux")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error getting process list: %v", err)
		return nil
	}
	numCPU := float64(runtime.NumCPU())
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
		cpu, _ := strconv.ParseFloat(replaceCommas(fields[2]), 64)
		cpu = cpu / numCPU
		mem, _ := strconv.ParseFloat(replaceCommas(fields[3]), 64)
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

func replaceCommas(s string) string {
	return strings.Replace(s, ",", ".", -1)
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
	aneUtil := float64(cpuMetrics.ANEW / 1 / 8.0 * 100)
	aneGauge.Title = fmt.Sprintf("ANE Usage: %.2f%% @ %.2f W", aneUtil, cpuMetrics.ANEW)
	aneGauge.Percent = int(aneUtil)

	thermalStr, _ := getThermalStateString()
	tempStr := ""
	if cpuMetrics.SocTemp > 0 {
		tempStr = fmt.Sprintf(" @ %.0f°C", cpuMetrics.SocTemp)
	}
	PowerChart.Title = fmt.Sprintf("%.1fW Total%s", cpuMetrics.PackageW, tempStr)
	PowerChart.Text = fmt.Sprintf("CPU: %.2f W | GPU: %.2f W\nANE: %.2f W | DRAM: %.2f W\nTotal: %.2f W | %s",
		cpuMetrics.CPUW,
		cpuMetrics.GPUW,
		cpuMetrics.ANEW,
		cpuMetrics.DRAMW,
		cpuMetrics.PackageW,
		thermalStr,
	)
	memoryMetrics := getMemoryMetrics()
	memoryGauge.Title = fmt.Sprintf("Memory Usage: %.2f GB / %.2f GB (Swap: %.2f/%.2f GB)", float64(memoryMetrics.Used)/1024/1024/1024, float64(memoryMetrics.Total)/1024/1024/1024, float64(memoryMetrics.SwapUsed)/1024/1024/1024, float64(memoryMetrics.SwapTotal)/1024/1024/1024)
	memoryGauge.Percent = int((float64(memoryMetrics.Used) / float64(memoryMetrics.Total)) * 100)

	var ecoreAvg, pcoreAvg float64
	if cpuCoreWidget.eCoreCount > 0 && len(coreUsages) >= cpuCoreWidget.eCoreCount {
		for i := 0; i < cpuCoreWidget.eCoreCount; i++ {
			ecoreAvg += coreUsages[i]
		}
		ecoreAvg /= float64(cpuCoreWidget.eCoreCount)
	}
	if cpuCoreWidget.pCoreCount > 0 && len(coreUsages) >= cpuCoreWidget.eCoreCount+cpuCoreWidget.pCoreCount {
		for i := cpuCoreWidget.eCoreCount; i < cpuCoreWidget.eCoreCount+cpuCoreWidget.pCoreCount; i++ {
			pcoreAvg += coreUsages[i]
		}
		pcoreAvg /= float64(cpuCoreWidget.pCoreCount)
	}

	thermalStateVal, _ := getThermalStateString()
	thermalStateNum := 0
	switch thermalStateVal {
	case "Fair":
		thermalStateNum = 1
	case "Serious":
		thermalStateNum = 2
	case "Critical":
		thermalStateNum = 3
	}

	cpuUsage.Set(totalUsage)
	ecoreUsage.Set(ecoreAvg)
	pcoreUsage.Set(pcoreAvg)
	powerUsage.With(prometheus.Labels{"component": "cpu"}).Set(cpuMetrics.CPUW)
	powerUsage.With(prometheus.Labels{"component": "gpu"}).Set(cpuMetrics.GPUW)
	powerUsage.With(prometheus.Labels{"component": "ane"}).Set(cpuMetrics.ANEW)
	powerUsage.With(prometheus.Labels{"component": "dram"}).Set(cpuMetrics.DRAMW)
	powerUsage.With(prometheus.Labels{"component": "total"}).Set(cpuMetrics.PackageW)
	socTemp.Set(cpuMetrics.SocTemp)
	thermalState.Set(float64(thermalStateNum))

	memoryUsage.With(prometheus.Labels{"type": "used"}).Set(float64(memoryMetrics.Used) / 1024 / 1024 / 1024)
	memoryUsage.With(prometheus.Labels{"type": "total"}).Set(float64(memoryMetrics.Total) / 1024 / 1024 / 1024)
	memoryUsage.With(prometheus.Labels{"type": "swap_used"}).Set(float64(memoryMetrics.SwapUsed) / 1024 / 1024 / 1024)
	memoryUsage.With(prometheus.Labels{"type": "swap_total"}).Set(float64(memoryMetrics.SwapTotal) / 1024 / 1024 / 1024)
}

func updateGPUUI(gpuMetrics GPUMetrics) {
	if gpuMetrics.Temp > 0 {
		gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz (%.0f°C)", int(gpuMetrics.Active), gpuMetrics.FreqMHz, gpuMetrics.Temp)
	} else {
		gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz", int(gpuMetrics.Active), gpuMetrics.FreqMHz)
	}
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
	gpuSparklineGroup.Title = fmt.Sprintf("GPU History: %d%% (Avg: %.1f%%)", gpuMetrics.Active, avgGPU)

	gpuUsage.Set(float64(gpuMetrics.Active))
	gpuFreqMHz.Set(float64(gpuMetrics.FreqMHz))
}

type VolumeInfo struct {
	Name      string
	Total     float64
	Used      float64
	Available float64
	UsedPct   float64
}

func getVolumes() []VolumeInfo {
	var volumes []VolumeInfo
	partitions, err := disk.Partitions(false)
	if err != nil {
		return volumes
	}

	seen := make(map[string]bool)
	for _, p := range partitions {
		if seen[p.Device] {
			continue
		}
		if !strings.HasPrefix(p.Mountpoint, "/Volumes/") && p.Mountpoint != "/" {
			continue
		}
		if strings.Contains(p.Mountpoint, "/Volumes/Recovery") ||
			strings.Contains(p.Mountpoint, "/Volumes/Preboot") ||
			strings.Contains(p.Mountpoint, "/Volumes/VM") ||
			strings.Contains(p.Mountpoint, "/Volumes/Update") ||
			strings.Contains(p.Mountpoint, "/Volumes/xarts") ||
			strings.Contains(p.Mountpoint, "/Volumes/iSCPreboot") ||
			strings.Contains(p.Mountpoint, "/Volumes/Hardware") {
			continue
		}
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil || usage.Total == 0 {
			continue
		}
		seen[p.Device] = true
		var name string
		if p.Mountpoint == "/" {
			name = "Macintosh HD"
		} else {
			name = strings.TrimPrefix(p.Mountpoint, "/Volumes/")
		}
		if len(name) > 12 {
			name = name[:12]
		}
		volumes = append(volumes, VolumeInfo{
			Name:      name,
			Total:     float64(usage.Total) / 1e9,
			Used:      float64(usage.Used) / 1e9,
			Available: float64(usage.Free) / 1e9,
			UsedPct:   usage.UsedPercent,
		})
	}
	return volumes
}

func updateNetDiskUI(netdiskMetrics NetDiskMetrics) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Net: ↑ %.0fKB/s ↓ %.0fKB/s\n",
		netdiskMetrics.OutBytesPerSec, netdiskMetrics.InBytesPerSec))
	sb.WriteString(fmt.Sprintf("I/O: R %.0fKB/s W %.0fKB/s\n",
		netdiskMetrics.ReadKBytesPerSec, netdiskMetrics.WriteKBytesPerSec))

	volumes := getVolumes()
	for i, v := range volumes {
		if i >= 3 {
			break
		}
		sb.WriteString(fmt.Sprintf("%s: %.0f/%.0fGB (%.0fGB free)\n",
			v.Name, v.Used, v.Total, v.Available))
	}
	NetworkInfo.Text = strings.TrimSuffix(sb.String(), "\n")

	networkSpeed.With(prometheus.Labels{"direction": "upload"}).Set(netdiskMetrics.OutBytesPerSec)
	networkSpeed.With(prometheus.Labels{"direction": "download"}).Set(netdiskMetrics.InBytesPerSec)
	diskIOSpeed.With(prometheus.Labels{"operation": "read"}).Set(netdiskMetrics.ReadKBytesPerSec)
	diskIOSpeed.With(prometheus.Labels{"operation": "write"}).Set(netdiskMetrics.WriteKBytesPerSec)
	diskIOPS.With(prometheus.Labels{"operation": "read"}).Set(netdiskMetrics.ReadOpsPerSec)
	diskIOPS.With(prometheus.Labels{"operation": "write"}).Set(netdiskMetrics.WriteOpsPerSec)
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
