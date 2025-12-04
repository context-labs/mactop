// Copyright (c) 2024-2026 Carsen Klock under MIT License
// mactop is a simple terminal based Apple Silicon power monitor written in Go Lang! github.com/context-labs/mactop
package app

/*
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <mach/mach_init.h>

extern kern_return_t vm_deallocate(vm_map_t target_task, vm_address_t address, vm_size_t size);
*/
import "C"
import (
	"flag"
	"fmt"
	"log"
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

	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	ui "github.com/gizak/termui/v3"
	w "github.com/gizak/termui/v3/widgets"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
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
	registry.MustRegister(gpuTemp)
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

func setupUI() {
	appleSiliconModel := getSOCInfo()
	modelText, helpText = w.NewParagraph(), w.NewParagraph()
	modelText.Title = "Apple Silicon"
	helpText.Title = "mactop help menu"
	modelName := appleSiliconModel.Name
	if modelName == "" {
		modelName = "Unknown Model"
	}
	eCoreCount := appleSiliconModel.ECoreCount
	pCoreCount := appleSiliconModel.PCoreCount
	gpuCoreCount := appleSiliconModel.GPUCoreCount
	updateModelText()
	updateHelpText()
	stderrLogger.Printf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %d", modelName, eCoreCount, pCoreCount, gpuCoreCount)
	stderrLogger.Printf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %d", modelName, eCoreCount, pCoreCount, gpuCoreCount)

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
	for i, gauge := range gauges {
		gauge.Percent = 0
		gauge.Title = titles[i]
		gauge.Percent = 0
		gauge.Title = titles[i]
	}
	cpuGauge, gpuGauge, memoryGauge, aneGauge = gauges[0], gauges[1], gauges[2], gauges[3]

	PowerChart, NetworkInfo = w.NewParagraph(), w.NewParagraph()
	PowerChart.Title, NetworkInfo.Title = "Power Usage", "Network & Disk"

	termWidth, _ := ui.TerminalDimensions()
	numPoints := termWidth / 2
	numPointsGPU := termWidth / 2
	powerValues = make([]float64, numPoints)
	gpuValues = make([]float64, numPointsGPU)

	sparkline = w.NewSparkline()
	sparkline.MaxHeight = 100
	sparkline.Data = powerValues

	sparklineGroup = w.NewSparklineGroup(sparkline)

	gpuSparkline = w.NewSparkline()
	gpuSparkline.MaxHeight = 100
	gpuSparkline.Data = gpuValues
	gpuSparklineGroup = w.NewSparklineGroup(gpuSparkline)
	gpuSparklineGroup.Title = "GPU Usage History"

	updateProcessList()

	cpuCoreWidget = NewCPUCoreWidget(appleSiliconModel)
	eCoreCount = appleSiliconModel.ECoreCount
	pCoreCount = appleSiliconModel.PCoreCount
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

func updateModelText() {
	appleSiliconModel := getSOCInfo()
	modelName := appleSiliconModel.Name
	if modelName == "" {
		modelName = "Unknown Model"
	}
	eCoreCount := appleSiliconModel.ECoreCount
	pCoreCount := appleSiliconModel.PCoreCount
	gpuCoreCount := appleSiliconModel.GPUCoreCount

	gpuCoreCountStr := "?"
	if gpuCoreCount > 0 {
		gpuCoreCountStr = fmt.Sprintf("%d", gpuCoreCount)
	}

	modelText.Text = fmt.Sprintf("%s\n%d Cores\n%d E-Cores\n%d P-Cores\n%s GPU Cores",
		modelName,
		eCoreCount+pCoreCount,
		eCoreCount,
		pCoreCount,
		gpuCoreCountStr,
	)
}

func updateHelpText() {
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
			"- l: Cycle through the 6 available layouts\n"+
			"- + or -: Adjust update interval (faster/slower)\n"+
			"- F9: Kill selected process\n"+
			"- h or ?: Toggle this help menu\n"+
			"- q or <C-c>: Quit the application\n\n"+
			"Start Flags:\n"+
			"--help, -h: Show this help menu\n"+
			"--version, -v: Show the version of mactop\n"+
			"--interval, -i: Set the update interval in milliseconds. Default is 1000.\n"+
			"--prometheus, -p: Set and enable a Prometheus metrics port. Default is none. (e.g. --prometheus=9090)\n"+
			"--color, -c: Set the UI color. Default is none. Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'.\n\n"+
			"Version: %s\n\n"+
			"Current Settings:\n"+
			"Layout: %s\n"+
			"Theme: %s\n"+
			"Update Interval: %dms",
		prometheusStatus,
		version,
		currentConfig.DefaultLayout,
		currentConfig.Theme,
		updateInterval,
	)
}

func toggleHelpMenu() {
	updateHelpText()
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
		applyLayout(currentConfig.DefaultLayout)
	}
	ui.Clear()
	renderUI()
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
				cycleTheme()
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
					case 50: // 2
						if n >= 5 && buf[3] == 48 && buf[4] == 126 { // 0, ~
							ch <- "<F9>"
						} else {
							ch <- "<Escape>"
						}
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
	processes := lastProcesses
	if processes == nil {
		return
	}
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
	termWidth, _ := ui.TerminalDimensions()
	minWidth := 40 // Set a minimum width to prevent crashes
	availableWidth := max(termWidth-2, minWidth)
	maxWidths := map[string]int{
		"PID":  5,  // Minimum for PID
		"USER": 8,  // Fixed maximum width for USER
		"VIRT": 6,  // For memory format
		"RES":  6,  // For memory format
		"CPU":  6,  // For "XX.X%"
		"MEM":  5,  // For "X.X%"
		"TIME": 8,  // For time format
		"CMD":  15, // Minimum for command
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

		cmdName := p.Command // Already simplified by ps -c

		line := fmt.Sprintf("%*d %-*s %*s %*s %*.1f%% %*.1f%% %*s %-s",
			maxWidths["PID"], p.PID,
			maxWidths["USER"], username,
			maxWidths["VIRT"], virtStr,
			maxWidths["RES"], resStr,
			maxWidths["CPU"]-1, p.CPU, // -1 for % symbol
			maxWidths["MEM"]-1, p.Memory, // -1 for % symbol
			maxWidths["TIME"], timeStr,
			truncateWithEllipsis(cmdName, maxWidths["CMD"]),
		)

		if p.User != currentUser {
			items[i+1] = fmt.Sprintf("[%s](fg:white)", line)
		} else {
			colorName := currentConfig.Theme
			if colorName == "" {
				colorName = "green"
			}
			items[i+1] = fmt.Sprintf("[%s](fg:%s)", line, colorName)
		}
	}

	if killPending {
		processList.Title = fmt.Sprintf("CONFIRM KILL PID %d? (y/n)", killPID)
		processList.TitleStyle = ui.NewStyle(ui.ColorRed, ui.ColorClear, ui.ModifierBold)
	} else {
		processList.Title = "Process List (↑/↓ scroll, ←/→ select column, Enter/Space to sort, F9 to kill process)"
		processList.TitleStyle = ui.NewStyle(GetThemeColor(currentConfig.Theme))
	}
	processList.Rows = items
}

func handleProcessListEvents(e ui.Event) {
	if killPending {
		switch e.ID {
		case "y", "Y":
			if err := syscall.Kill(killPID, syscall.SIGTERM); err == nil {
				stderrLogger.Printf("Sent SIGTERM to PID %d\n", killPID)
			} else {
				stderrLogger.Printf("Failed to kill PID %d: %v\n", killPID, err)
			}
			killPending = false
			updateProcessList()
		case "n", "N", "<Escape>":
			killPending = false
			updateProcessList()
		}
		ui.Render(processList, grid)
		return
	}

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
	case "<F9>":
		if len(processList.Rows) > 0 && processList.SelectedRow > 0 {
			processIndex := processList.SelectedRow - 1
			if processIndex < len(lastProcesses) {
				pid := lastProcesses[processIndex].PID
				killPending = true
				killPID = pid
				updateProcessList()
			}
		}
	case "c": // Cycle colors
		cycleTheme()
		saveConfig()
		updateProcessList()
		renderUI()
	}
	renderUI()
}

func renderUI() {
	ui.Render(grid)
}

func Run() {
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
				fmt.Printf("  ANE: %.2fW | DRAM: %.2fW | GPU SRAM: %.2fW | Total: %.2fW | %s\n",
					m.ANEPower, m.DRAMPower, m.GPUSRAMPower, m.TotalPower, thermalStr)
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

	flag.StringVar(&prometheusPort, "prometheus", "", "Port to run Prometheus metrics server on (e.g. :9090)")
	flag.BoolVar(&headless, "headless", false, "Run in headless mode (no TUI, output JSON to stdout)")
	flag.IntVar(&headlessCount, "count", 0, "Number of samples to collect in headless mode (0 = infinite)")
	flag.IntVar(&updateInterval, "interval", 1000, "Update interval in milliseconds")
	flag.StringVar(&colorName, "color", "", "Set the UI color. Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'.")
	flag.BoolVar(&setColor, "set-color", false, "Internal flag to indicate if color was set via CLI") // Used to differentiate default from CLI set
	flag.StringVar(&networkUnit, "unit-network", "auto", "Network unit: auto, byte, kb, mb, gb")
	flag.StringVar(&diskUnit, "unit-disk", "auto", "Disk unit: auto, byte, kb, mb, gb")
	flag.StringVar(&tempUnit, "unit-temp", "celsius", "Temperature unit: celsius, fahrenheit")
	flag.Parse()

	currentUser = os.Getenv("USER")

	if headless {
		runHeadless(headlessCount)
		return
	}

	// TUI Mode
	if err := ui.Init(); err != nil {
		stderrLogger.Fatalf("failed to initialize termui: %v", err)
	}
	defer ui.Close()

	if err := initSocMetrics(); err != nil {
		stderrLogger.Fatalf("failed to initialize metrics: %v", err)
	}
	defer cleanupSocMetrics()

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
		applyTheme(colorName)
		loadConfig()
		setupUI()
		applyTheme(colorName)
	} else {
		loadConfig()
		if currentConfig.Theme == "" {
			currentConfig.Theme = "green"
		}
		setupUI()
		applyTheme(currentConfig.Theme)
	}
	if setInterval {
		updateInterval = interval
	}
	setupGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	grid.SetRect(0, 0, termWidth, termHeight)
	renderUI()

	cpuMetricsChan := make(chan CPUMetrics, 1)
	gpuMetricsChan := make(chan GPUMetrics, 1)
	netdiskMetricsChan := make(chan NetDiskMetrics, 1)
	processMetricsChan := make(chan []ProcessMetrics, 1)

	initialSocMetrics := sampleSocMetrics(100)
	_, throttled := getThermalStateString()
	totalPower := initialSocMetrics.TotalPower
	if initialSocMetrics.SystemPower > totalPower {
		totalPower = initialSocMetrics.SystemPower
	}
	cpuMetrics := CPUMetrics{
		CPUW:      initialSocMetrics.CPUPower,
		GPUW:      initialSocMetrics.GPUPower,
		ANEW:      initialSocMetrics.ANEPower,
		DRAMW:     initialSocMetrics.DRAMPower,
		GPUSRAMW:  initialSocMetrics.GPUSRAMPower,
		PackageW:  totalPower,
		Throttled: throttled,
		CPUTemp:   float64(initialSocMetrics.CPUTemp),
		GPUTemp:   float64(initialSocMetrics.GPUTemp),
	}
	gpuMetrics := GPUMetrics{
		FreqMHz:       int(initialSocMetrics.GPUFreqMHz),
		ActivePercent: initialSocMetrics.GPUActive,
		Power:         initialSocMetrics.GPUPower + initialSocMetrics.GPUSRAMPower,
		Temp:          initialSocMetrics.GPUTemp,
	}

	// Send initial data to channels (buffered, so won't block)
	cpuMetricsChan <- cpuMetrics
	gpuMetricsChan <- gpuMetrics

	if processes, err := getProcessList(); err == nil {
		processMetricsChan <- processes
	}

	netdiskMetricsChan <- getNetDiskMetrics()

	go collectMetrics(done, cpuMetricsChan, gpuMetricsChan)
	go collectProcessMetrics(done, processMetricsChan)
	go collectNetDiskMetrics(done, netdiskMetricsChan)

	uiEvents := ui.PollEvents()
	ticker := time.NewTicker(time.Duration(updateInterval) * time.Millisecond)

	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				select {
				case cpuMetrics := <-cpuMetricsChan:
					updateCPUUI(cpuMetrics)
					updateTotalPowerChart(cpuMetrics.PackageW)
					ui.Render(grid)
				default:
				}
				select {
				case gpuMetrics := <-gpuMetricsChan:
					updateGPUUI(gpuMetrics)
					ui.Render(grid)
				default:
				}
				select {
				case netdiskMetrics := <-netdiskMetricsChan:
					updateNetDiskUI(netdiskMetrics)
					ui.Render(grid)
				default:
				}
				select {
				case processes := <-processMetricsChan:
					if processList.SelectedRow == 0 {
						lastProcesses = processes
						updateProcessList()
					}
					renderUI()
				default:
				}

			}
		}
	}()
	renderUI()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer func() {
		if partyTicker != nil {
			partyTicker.Stop()
		}
	}()
	lastUpdateTime = time.Now()
	keyboardInput := pollKeyboardInput(ttyFile)
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
				renderUI()
			case "p":
				togglePartyMode()
			case "c":
				termWidth, termHeight := ui.TerminalDimensions()
				grid.SetRect(0, 0, termWidth, termHeight)
				cycleTheme()
				saveConfig()
				ui.Clear()
				renderUI()
			case "l":
				cycleLayout()
				saveConfig()
				ui.Clear()
				renderUI()
			case "h", "?":
				toggleHelpMenu()
			case "-", "_":
				updateInterval -= 100
				if updateInterval > 5000 {
					updateInterval = 5000
				}
				updateHelpText()
				updateModelText()
				select {
				case interruptChan <- struct{}{}:
				default:
				}
				select {
				case interruptChan <- struct{}{}:
				default:
				}
				renderUI()
			case "+", "=":
				updateInterval += 100
				if updateInterval < 100 {
					updateInterval = 100
				}
				updateHelpText()
				updateModelText()
				select {
				case interruptChan <- struct{}{}:
				default:
				}
				select {
				case interruptChan <- struct{}{}:
				default:
				}
				renderUI()
			}
		case e := <-uiEvents:
			if e.ID == "<Resize>" {
				payload := e.Payload.(ui.Resize)
				grid.SetRect(0, 0, payload.Width, payload.Height)
				renderUI()
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
	// NSProcessInfoThermalState: 0=Nominal, 1=Fair, 2=Serious, 3=Critical
	// powermetrics terminology: Nominal, Moderate, Heavy, Critical (or Trapping)
	states := []string{"Nominal", "Moderate", "Heavy", "Critical"}
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

func collectNetDiskMetrics(done chan struct{}, netdiskMetricsChan chan NetDiskMetrics) {
	time.Sleep(time.Duration(updateInterval) * time.Millisecond)

	for {
		start := time.Now()

		select {
		case <-done:
			return
		default:
			netdiskMetrics := getNetDiskMetrics()
			select {
			case netdiskMetricsChan <- netdiskMetrics:
			default:
			}
		}

		elapsed := time.Since(start)
		sleepTime := time.Duration(updateInterval)*time.Millisecond - elapsed
		if sleepTime > 0 {
			select {
			case <-time.After(sleepTime):
			case <-interruptChan:
			}
		}
	}
}

func collectMetrics(done chan struct{}, cpumetricsChan chan CPUMetrics, gpumetricsChan chan GPUMetrics) {
	time.Sleep(time.Duration(updateInterval) * time.Millisecond)

	for {
		start := time.Now()

		sampleDuration := updateInterval
		if sampleDuration < 100 {
			sampleDuration = 100
		}

		m := sampleSocMetrics(sampleDuration / 2)

		_, throttled := getThermalStateString()

		totalPower := m.TotalPower
		if m.SystemPower > totalPower {
			totalPower = m.SystemPower
		}

		cpuMetrics := CPUMetrics{
			CPUW:      m.CPUPower,
			GPUW:      m.GPUPower,
			ANEW:      m.ANEPower,
			DRAMW:     m.DRAMPower,
			GPUSRAMW:  m.GPUSRAMPower,
			PackageW:  totalPower,
			Throttled: throttled,
			CPUTemp:   float64(m.CPUTemp),
			GPUTemp:   float64(m.GPUTemp),
		}

		gpuMetrics := GPUMetrics{
			FreqMHz:       int(m.GPUFreqMHz),
			ActivePercent: m.GPUActive,
			Power:         m.GPUPower + m.GPUSRAMPower,
			Temp:          m.GPUTemp,
		}

		select {
		case <-done:
			return
		case cpumetricsChan <- cpuMetrics:
		default:
		}
		select {
		case gpumetricsChan <- gpuMetrics:
		default:
		}

		elapsed := time.Since(start)
		sleepTime := time.Duration(updateInterval)*time.Millisecond - elapsed
		if sleepTime > 0 {
			select {
			case <-time.After(sleepTime):
			case <-interruptChan:
			}
		}
	}
}

func collectProcessMetrics(done chan struct{}, processMetricsChan chan []ProcessMetrics) {
	time.Sleep(time.Duration(updateInterval) * time.Millisecond)

	for {
		start := time.Now()

		select {
		case <-done:
			return
		default:
			if processes, err := getProcessList(); err == nil {
				processMetricsChan <- processes
			} else {
				stderrLogger.Printf("Error getting process list: %v\n", err)
			}
		}

		elapsed := time.Since(start)
		sleepTime := time.Duration(updateInterval)*time.Millisecond - elapsed
		if sleepTime > 0 {
			time.Sleep(sleepTime)
		}
	}
}

func getProcessList() ([]ProcessMetrics, error) {
	// Use -c to get just the executable name (not full path)
	// Put comm at the end to handle spaces in app names (e.g. "Google Chrome")
	// Removed 'start' field as it can contain spaces and break parsing, and we don't display it.
	cmd := exec.Command("ps", "-c", "-Ao", "pid,user,%cpu,%mem,vsz,rss,state,time,comm")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\n")
	var processes []ProcessMetrics

	if len(lines) > 0 {
		lines = lines[1:]
	}

	for _, line := range lines {
		if len(strings.TrimSpace(line)) == 0 {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		cpu, _ := strconv.ParseFloat(fields[2], 64)
		mem, _ := strconv.ParseFloat(fields[3], 64)
		vsz, _ := strconv.ParseInt(fields[4], 10, 64)
		rss, _ := strconv.ParseInt(fields[5], 10, 64)

		state := fields[6]
		timeStr := fields[7]
		// Join all remaining fields as the command name
		command := strings.Join(fields[8:], " ")

		processes = append(processes, ProcessMetrics{
			PID:         pid,
			User:        fields[1],
			CPU:         cpu,
			Memory:      mem,
			VSZ:         vsz,
			RSS:         rss,
			Command:     command,
			State:       state,
			Started:     "", // Removed from ps command
			Time:        timeStr,
			LastUpdated: time.Now(),
		})
	}

	sort.Slice(processes, func(i, j int) bool {
		return processes[i].CPU > processes[j].CPU
	})

	if len(processes) > 100 {
		processes = processes[:100]
	}

	return processes, nil
}

func updateTotalPowerChart(watts float64) {
	if watts > maxPowerSeen {
		maxPowerSeen = watts * 1.1
	}
	scaledValue := int((watts / maxPowerSeen) * 8)
	if watts > 0 && scaledValue == 0 {
		scaledValue = 1
	}
	for i := 0; i < len(powerValues)-1; i++ {
		powerValues[i] = powerValues[i+1]
	}
	powerValues[len(powerValues)-1] = float64(scaledValue)
	var sum float64
	count := 0
	for _, v := range powerValues {
		if v > 0 {
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
	sparkline.MaxVal = 8
	sparklineGroup.Title = fmt.Sprintf("%.2f W Total (Max: %.2f W)", watts, maxPowerSeen)
	thermalStr, _ := getThermalStateString()
	sparkline.Title = fmt.Sprintf("Avg: %.2f W | %s", avgWatts, thermalStr)
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
	cpuGauge.Title = fmt.Sprintf("mactop - %d Cores (%dE/%dP) %.2f%% (%s)",
		cpuCoreWidget.eCoreCount+cpuCoreWidget.pCoreCount,
		cpuCoreWidget.eCoreCount,
		cpuCoreWidget.pCoreCount,
		totalUsage,
		formatTemp(cpuMetrics.CPUTemp),
	)
	cpuCoreWidget.Title = fmt.Sprintf("mactop - %d Cores (%dE/%dP) %.2f%% (%s)",
		cpuCoreWidget.eCoreCount+cpuCoreWidget.pCoreCount,
		cpuCoreWidget.eCoreCount,
		cpuCoreWidget.pCoreCount,
		totalUsage,
		formatTemp(cpuMetrics.CPUTemp),
	)
	aneUtil := float64(cpuMetrics.ANEW / 1 / 8.0 * 100)
	aneGauge.Title = fmt.Sprintf("ANE Usage: %.2f%% @ %.2f W", aneUtil, cpuMetrics.ANEW)
	aneGauge.Percent = int(aneUtil)

	thermalStr, _ := getThermalStateString()

	PowerChart.Title = fmt.Sprintf("Power Usage")
	PowerChart.Text = fmt.Sprintf("CPU: %.2f W | GPU: %.2f W\nANE: %.2f W | DRAM: %.2f W\nTotal: %.2f W\nThermals: %s",
		cpuMetrics.CPUW,
		cpuMetrics.GPUW+cpuMetrics.GPUSRAMW,
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
	case "Moderate":
		thermalStateNum = 1
	case "Heavy":
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
	socTemp.Set(cpuMetrics.CPUTemp)
	gpuTemp.Set(cpuMetrics.GPUTemp)
	thermalState.Set(float64(thermalStateNum))

	memoryUsage.With(prometheus.Labels{"type": "used"}).Set(float64(memoryMetrics.Used) / 1024 / 1024 / 1024)
	memoryUsage.With(prometheus.Labels{"type": "total"}).Set(float64(memoryMetrics.Total) / 1024 / 1024 / 1024)
	memoryUsage.With(prometheus.Labels{"type": "swap_used"}).Set(float64(memoryMetrics.SwapUsed) / 1024 / 1024 / 1024)
	memoryUsage.With(prometheus.Labels{"type": "swap_total"}).Set(float64(memoryMetrics.SwapTotal) / 1024 / 1024 / 1024)
}

func updateGPUUI(gpuMetrics GPUMetrics) {
	if gpuMetrics.Temp > 0 {
		gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz (%s)", int(gpuMetrics.ActivePercent), gpuMetrics.FreqMHz, formatTemp(float64(gpuMetrics.Temp)))
	} else {
		gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz", int(gpuMetrics.ActivePercent), gpuMetrics.FreqMHz)
	}
	gpuGauge.Percent = int(gpuMetrics.ActivePercent)

	for i := 0; i < len(gpuValues)-1; i++ {
		gpuValues[i] = gpuValues[i+1]
	}
	gpuValues[len(gpuValues)-1] = gpuMetrics.ActivePercent

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
	gpuSparklineGroup.Title = fmt.Sprintf("GPU History: %d%% (Avg: %.1f%%)", int(gpuMetrics.ActivePercent), avgGPU)

	if gpuMetrics.ActivePercent > 0 {
		gpuUsage.Set(gpuMetrics.ActivePercent)
	} else {
		gpuUsage.Set(0)
	}
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
			name = "Mac HD"
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

	// Network metrics are in Bytes/sec
	netOut := formatBytes(netdiskMetrics.OutBytesPerSec, networkUnit)
	netIn := formatBytes(netdiskMetrics.InBytesPerSec, networkUnit)
	sb.WriteString(fmt.Sprintf("Net: ↑ %s/s ↓ %s/s\n", netOut, netIn))

	// Disk metrics are in KB/s, convert to Bytes for formatBytes
	diskRead := formatBytes(netdiskMetrics.ReadKBytesPerSec*1024, diskUnit)
	diskWrite := formatBytes(netdiskMetrics.WriteKBytesPerSec*1024, diskUnit)
	sb.WriteString(fmt.Sprintf("I/O: R %s/s W %s/s\n", diskRead, diskWrite))

	volumes := getVolumes()
	for i, v := range volumes {
		if i >= 3 {
			break
		}
		// Volume info is in GB. Convert to Bytes for formatBytes
		used := formatBytes(v.Used*1024*1024*1024, diskUnit)
		total := formatBytes(v.Total*1024*1024*1024, diskUnit)
		avail := formatBytes(v.Available*1024*1024*1024, diskUnit)

		sb.WriteString(fmt.Sprintf("%s: %s/%s (%s free)\n",
			v.Name, used, total, avail))
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

func getSOCInfo() SystemInfo {
	cpuInfoDict := getCPUInfo()
	coreCountsDict := getCoreCounts()
	var eCoreCounts, pCoreCounts int
	if val, ok := coreCountsDict["hw.perflevel1.logicalcpu"]; ok {
		eCoreCounts = val
	}
	if val, ok := coreCountsDict["hw.perflevel0.logicalcpu"]; ok {
		pCoreCounts = val
	}

	coreCount, _ := strconv.Atoi(cpuInfoDict["machdep.cpu.core_count"])
	gpuCoreCountStr := getGPUCores()
	gpuCoreCount, _ := strconv.Atoi(gpuCoreCountStr)
	if gpuCoreCount == 0 && gpuCoreCountStr != "?" {
		// Try to parse if it's not "?" and Atoi failed (though getGPUCores returns clean string usually)
		// If it is "?", it stays 0
	}

	return SystemInfo{
		Name:         cpuInfoDict["machdep.cpu.brand_string"],
		CoreCount:    coreCount,
		ECoreCount:   eCoreCounts,
		PCoreCount:   pCoreCounts,
		GPUCoreCount: gpuCoreCount,
	}
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
func formatBytes(val float64, unitType string) string {
	// val is expected to be in bytes for "byte" unit, or KB for "kb", etc.
	// But wait, the input `val` from metrics might be in different units.
	// NetDiskMetrics has BytesPerSec (bytes) and KBytesPerSec (KB).
	// Let's assume input `val` is always in BYTES for consistency if possible,
	// or we handle it based on context.
	// Actually, let's make this function take bytes and convert.

	// However, updateNetDiskUI passes:
	// OutBytesPerSec (bytes)
	// ReadKBytesPerSec (KB)
	// So we need to be careful.

	// Let's define formatBytes taking bytes.

	units := []string{"B", "KB", "MB", "GB", "TB"}

	targetUnit := strings.ToLower(unitType)
	if targetUnit == "" {
		targetUnit = "auto"
	}

	value := val
	suffix := ""

	switch targetUnit {
	case "byte":
		suffix = "B"
	case "kb":
		value /= 1024
		suffix = "KB"
	case "mb":
		value /= 1024 * 1024
		suffix = "MB"
	case "gb":
		value /= 1024 * 1024 * 1024
		suffix = "GB"
	case "auto":
		i := 0
		for value >= 1000 && i < len(units)-1 {
			value /= 1024
			i++
		}
		suffix = units[i]
	default:
		// Default to auto behavior if unknown
		i := 0
		for value >= 1000 && i < len(units)-1 {
			value /= 1024
			i++
		}
		suffix = units[i]
	}

	return fmt.Sprintf("%.1f%s", value, suffix)
}

func formatTemp(celsius float64) string {
	if strings.ToLower(tempUnit) == "fahrenheit" {
		f := (celsius * 9 / 5) + 32
		return fmt.Sprintf("%d°F", int(f))
	}
	return fmt.Sprintf("%d°C", int(celsius))
}
