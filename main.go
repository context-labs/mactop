// Copyright (c) 2024-2025 Carsen Klock under MIT License
// mactop is a simple terminal based Apple Silicon power monitor written in Go Lang! github.com/context-labs/mactop
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	ui "github.com/gizak/termui/v3"
	w "github.com/gizak/termui/v3/widgets"
	"github.com/shirou/gopsutil/mem"
	"howett.net/plist"
)

type CPUMetrics struct {
	EClusterActive, EClusterFreqMHz, PClusterActive, PClusterFreqMHz int
	ECores, PCores                                                   []int
	CoreMetrics                                                      map[string]int
	CPUW, GPUW, PackageW                                             float64
}

func NewCPUMetrics() CPUMetrics {
	return CPUMetrics{
		CoreMetrics: make(map[string]int),
		ECores:      make([]int, 0),
		PCores:      make([]int, 0),
	}
}

type NetDiskMetrics struct {
	OutPacketsPerSec, OutBytesPerSec, InPacketsPerSec, InBytesPerSec, ReadOpsPerSec, WriteOpsPerSec, ReadKBytesPerSec, WriteKBytesPerSec float64
}

type GPUMetrics struct {
	FreqMHz, Active int
}
type ProcessMetrics struct {
	PID                                      int
	CPU, Memory                              float64
	VSZ, RSS                                 int64
	User, TTY, State, Started, Time, Command string
}

type MemoryMetrics struct {
	Total, Used, Available, SwapTotal, SwapUsed uint64
}

type EventThrottler struct {
	timer       *time.Timer
	gracePeriod time.Duration

	C chan struct{}
}

func NewEventThrottler(gracePeriod time.Duration) *EventThrottler {
	return &EventThrottler{
		timer:       nil,
		gracePeriod: gracePeriod,
		C:           make(chan struct{}, 1),
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

var (
	cpu1Gauge, cpu2Gauge, gpuGauge, memoryGauge  *w.Gauge
	TotalPowerChart                              *w.BarChart
	modelText, PowerChart, NetworkInfo, helpText *w.Paragraph
	grid                                         *ui.Grid
	processList                                  *w.List
	selectedProcess                              int
	powerValues                                  []float64
	lastUpdateTime                               time.Time
	stderrLogger                                 = log.New(os.Stderr, "", 0)
	currentGridLayout                            = "default"
	showHelp, partyMode                          = false, false
	updateInterval                               = 1000
	done                                         = make(chan struct{})
	currentColorIndex                            = 0
	colorOptions                                 = []ui.Color{ui.ColorWhite, ui.ColorGreen, ui.ColorBlue, ui.ColorCyan, ui.ColorMagenta, ui.ColorYellow, ui.ColorRed}
	partyTicker                                  *time.Ticker
)

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
	gpuCoreCount, ok := appleSiliconModel["gpu_core_count"].(string) // Assuming this is stored as a string
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
	helpText.Text = "mactop is open source monitoring tool for Apple Silicon authored by Carsen Klock in Go Lang!\n\nRepo: github.com/context-labs/mactop\n\nControls:\n- r: Refresh the UI data manually\n- c: Cycle through UI color themes\n- p: Toggle party mode (color cycling)\n- l: Toggle the main display's layout\n- h or ?: Toggle this help menu\n- q or <C-c>: Quit the application\n\nStart Flags:\n--help, -h: Show this help menu\n--version, -v: Show the version of mactop\n--interval, -i: Set the powermetrics update interval in milliseconds. Default is 1000.\n--color, -c: Set the UI color. Default is none. Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'."
	stderrLogger.Printf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %s",
		modelName,
		eCoreCount,
		pCoreCount,
		gpuCoreCount,
	)

	processList = w.NewList()
	processList.Title = "Process List (↑/↓ to scroll)"
	processList.TextStyle = ui.NewStyle(ui.ColorGreen)
	processList.WrapText = false
	processList.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, ui.ColorGreen)
	processList.Rows = []string{}
	processList.SelectedRow = 0

	gauges := []*w.Gauge{
		w.NewGauge(), w.NewGauge(), w.NewGauge(), w.NewGauge(),
	}
	titles := []string{"E-CPU Usage", "P-CPU Usage", "GPU Usage", "Memory Usage"}
	colors := []ui.Color{ui.ColorGreen, ui.ColorYellow, ui.ColorMagenta, ui.ColorBlue, ui.ColorCyan}
	for i, gauge := range gauges {
		gauge.Percent = 0
		gauge.Title = titles[i]
		gauge.BarColor = colors[i]
	}
	cpu1Gauge, cpu2Gauge, gpuGauge, memoryGauge = gauges[0], gauges[1], gauges[2], gauges[3]

	PowerChart, NetworkInfo = w.NewParagraph(), w.NewParagraph()
	PowerChart.Title, NetworkInfo.Title = "Power Usage", "Network & Disk Info"

	TotalPowerChart = w.NewBarChart()
	TotalPowerChart.Title = "~ W Total Power"
	TotalPowerChart.SetRect(50, 0, 75, 10)
	TotalPowerChart.BarWidth = 5 // Adjust the bar width to fill the available space
	TotalPowerChart.BarGap = 1   // Remove the gap between the bars
	TotalPowerChart.PaddingBottom = 0
	TotalPowerChart.PaddingTop = 1
	TotalPowerChart.NumFormatter = func(num float64) string {
		return ""
	}
	updateProcessList()
}

func setupGrid() {
	grid = ui.NewGrid()
	grid.Set(
		ui.NewRow(1.0/2, // This row now takes half the height of the grid
			ui.NewCol(1.0/2, ui.NewRow(1.0/2, cpu1Gauge), ui.NewCol(1.0, ui.NewRow(1.0, cpu2Gauge))),
			ui.NewCol(1.0/2, ui.NewRow(1.0/2, gpuGauge), ui.NewCol(1.0, ui.NewRow(1.0, processList))), // ui.NewCol(1.0/2, ui.NewRow(1.0, ProcessInfo)), // ProcessInfo spans this entire column
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/6, modelText),
			ui.NewCol(1.0/3, NetworkInfo),
			ui.NewCol(1.0/4, PowerChart),
			ui.NewCol(1.0/4, TotalPowerChart),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, memoryGauge),
		),
	)
}

func switchGridLayout() {
	if currentGridLayout == "default" {
		newGrid := ui.NewGrid()
		newGrid.Set(
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, ui.NewRow(1.0, cpu1Gauge)),
				ui.NewCol(1.0/2, ui.NewRow(1.0, cpu2Gauge)),
			),
			ui.NewRow(2.0/4,
				ui.NewCol(1.0/2,
					ui.NewRow(1.0/2, gpuGauge),
					ui.NewRow(1.0/2,
						ui.NewCol(1.0/2, PowerChart),
						ui.NewCol(1.0/2, TotalPowerChart),
					),
				),
				ui.NewCol(1.0/2, processList),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(3.0/6, memoryGauge),
				ui.NewCol(1.0/6, modelText),
				ui.NewCol(2.0/6, NetworkInfo),
			),
		)
		termWidth, termHeight := ui.TerminalDimensions()
		newGrid.SetRect(0, 0, termWidth, termHeight)
		grid = newGrid
		currentGridLayout = "alternative"
	} else {
		newGrid := ui.NewGrid()
		newGrid.Set(
			ui.NewRow(1.0/2,
				ui.NewCol(1.0/2, ui.NewRow(1.0/2, cpu1Gauge), ui.NewCol(1.0, ui.NewRow(1.0, cpu2Gauge))),
				ui.NewCol(1.0/2, ui.NewRow(1.0/2, gpuGauge), ui.NewCol(1.0, ui.NewRow(1.0, processList))),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/4, modelText),
				ui.NewCol(1.0/4, NetworkInfo),
				ui.NewCol(1.0/4, PowerChart),
				ui.NewCol(1.0/4, TotalPowerChart),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, memoryGauge),
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
		partyTicker = time.NewTicker(500 * time.Millisecond)
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

func updateProcessList() {
	processes := getProcessList()
	items := make([]string, len(processes))
	for i, p := range processes {
		items[i] = fmt.Sprintf("%5d %-8s %5.1f%% %5.1f%% %-50.50s",
			p.PID, p.User, p.CPU, p.Memory, p.Command)
	}
	processList.Rows = items
	if selectedProcess >= len(items) {
		selectedProcess = len(items) - 1
	}
	if selectedProcess < 0 {
		selectedProcess = 0
	}
	processList.SelectedRow = selectedProcess
}

func cycleColors() {
	currentColorIndex = (currentColorIndex + 1) % len(colorOptions)
	color := colorOptions[currentColorIndex]

	ui.Theme.Block.Title.Fg, ui.Theme.Block.Border.Fg, ui.Theme.Paragraph.Text.Fg, ui.Theme.Gauge.Label.Fg, ui.Theme.Gauge.Bar = color, color, color, color, color
	ui.Theme.BarChart.Bars = []ui.Color{color}

	cpu1Gauge.BarColor, cpu2Gauge.BarColor, gpuGauge.BarColor, memoryGauge.BarColor = color, color, color, color
	processList.TextStyle, NetworkInfo.TextStyle, PowerChart.TextStyle, TotalPowerChart.BarColors = ui.NewStyle(color), ui.NewStyle(color), ui.NewStyle(color), []ui.Color{color}
	processList.SelectedRowStyle, modelText.TextStyle, helpText.TextStyle = ui.NewStyle(ui.ColorBlack, color), ui.NewStyle(color), ui.NewStyle(color)

	cpu1Gauge.BorderStyle.Fg, cpu1Gauge.TitleStyle.Fg, cpu2Gauge.BorderStyle.Fg, cpu2Gauge.TitleStyle.Fg = color, color, color, color
	gpuGauge.BorderStyle.Fg, gpuGauge.TitleStyle.Fg, memoryGauge.BorderStyle.Fg, memoryGauge.TitleStyle.Fg = color, color, color, color
	processList.BorderStyle.Fg, processList.TitleStyle.Fg, NetworkInfo.BorderStyle.Fg, NetworkInfo.TitleStyle.Fg = color, color, color, color
	PowerChart.BorderStyle.Fg, PowerChart.TitleStyle.Fg, TotalPowerChart.BorderStyle.Fg, TotalPowerChart.TitleStyle.Fg = color, color, color, color
	modelText.BorderStyle.Fg, modelText.TitleStyle.Fg, helpText.BorderStyle.Fg, helpText.TitleStyle.Fg = color, color, color, color
}

func main() {
	var (
		colorName             string
		interval              int
		err                   error
		setColor, setInterval bool
	)
	version := "v0.2.0"
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
		cpu1Gauge.BarColor, cpu2Gauge.BarColor, gpuGauge.BarColor, memoryGauge.BarColor = color, color, color, color
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
		ticker := time.NewTicker(time.Duration(updateInterval/2) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case cpuMetrics := <-cpuMetricsChan:
				updateCPUUI(cpuMetrics)
				updateTotalPowerChart(cpuMetrics.PackageW)
			case gpuMetrics := <-gpuMetricsChan:
				updateGPUUI(gpuMetrics)
			case netdiskMetrics := <-netdiskMetricsChan:
				updateNetDiskUI(netdiskMetrics)
			case <-ticker.C:
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
		case cpumetricsChan <- parseCPUMetrics(plistData, NewCPUMetrics()):
		case gpumetricsChan <- parseGPUMetrics(data):
		case netdiskMetricsChan <- parseNetDiskMetrics(data):
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
	cmd := exec.Command("ps", "aux")
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

		process := ProcessMetrics{User: fields[0], PID: pid, CPU: cpu, Memory: mem, VSZ: vsz, RSS: rss, TTY: fields[6], State: fields[7], Started: fields[8], Time: fields[9], Command: strings.Join(fields[10:], " ")}
		processes = append(processes, process)
	}
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].CPU > processes[j].CPU
	})
	return processes
}

func updateTotalPowerChart(newPowerValue float64) {
	currentTime := time.Now()
	powerValues = append(powerValues, newPowerValue)
	if currentTime.Sub(lastUpdateTime) >= 2*time.Second {
		var sum float64
		for _, value := range powerValues {
			sum += value
		}
		averagePower := sum / float64(len(powerValues))
		averagePower = math.Round(averagePower)
		TotalPowerChart.Data = append([]float64{averagePower}, TotalPowerChart.Data...)
		if len(TotalPowerChart.Data) > 25 {
			TotalPowerChart.Data = TotalPowerChart.Data[:25]
		}
		powerValues = nil
		lastUpdateTime = currentTime
	}
}

func updateCPUUI(cpuMetrics CPUMetrics) {
	cpu1Gauge.Title = fmt.Sprintf("E-CPU Usage: %d%% @ %d MHz", cpuMetrics.EClusterActive, cpuMetrics.EClusterFreqMHz)
	cpu1Gauge.Percent = cpuMetrics.EClusterActive
	cpu2Gauge.Title = fmt.Sprintf("P-CPU Usage: %d%% @ %d MHz", cpuMetrics.PClusterActive, cpuMetrics.PClusterFreqMHz)
	cpu2Gauge.Percent = cpuMetrics.PClusterActive
	TotalPowerChart.Title = fmt.Sprintf("%.2f W Total Power", cpuMetrics.PackageW)
	PowerChart.Title = fmt.Sprintf("%.2f W CPU - %.2f W GPU", cpuMetrics.CPUW, cpuMetrics.GPUW)
	PowerChart.Text = fmt.Sprintf("CPU Power: %.2f W\nGPU Power: %.2f W\nTotal Power: %.2f W", cpuMetrics.CPUW, cpuMetrics.GPUW, cpuMetrics.PackageW)
	memoryMetrics := getMemoryMetrics()
	memoryGauge.Title = fmt.Sprintf("Memory Usage: %.2f GB / %.2f GB (Swap: %.2f/%.2f GB)", float64(memoryMetrics.Used)/1024/1024/1024, float64(memoryMetrics.Total)/1024/1024/1024, float64(memoryMetrics.SwapUsed)/1024/1024/1024, float64(memoryMetrics.SwapTotal)/1024/1024/1024)
	memoryGauge.Percent = int((float64(memoryMetrics.Used) / float64(memoryMetrics.Total)) * 100)
}

func updateGPUUI(gpuMetrics GPUMetrics) {
	gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz", int(gpuMetrics.Active), gpuMetrics.FreqMHz)
	gpuGauge.Percent = int(gpuMetrics.Active)
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

func parseCPUMetrics(powermetricsOutput string, cpuMetrics CPUMetrics) CPUMetrics {
	var data map[string]interface{}
	err := plist.NewDecoder(strings.NewReader(powermetricsOutput)).Decode(&data)
	if err != nil {
		stderrLogger.Fatalf("Error decoding plist: %v\n", err)
		return cpuMetrics
	}
	processor, ok := data["processor"].(map[string]interface{})
	if !ok {
		stderrLogger.Fatalf("Failed to get processor data\n")
		return cpuMetrics
	}
	clusters, ok := processor["clusters"].([]interface{})
	if !ok {
		stderrLogger.Fatalf("Failed to get clusters data\n")
		return cpuMetrics
	}
	cpuMetricDict := make(map[string]interface{})
	eCores := []int{}
	pCores := []int{}
	for _, c := range clusters {
		cluster := c.(map[string]interface{})
		name := cluster["name"].(string)
		var freqHz float64
		switch v := cluster["freq_hz"].(type) {
		case int64:
			freqHz = float64(v)
		case float64:
			freqHz = v
		}
		idleRatio := cluster["idle_ratio"].(float64)
		cpuMetricDict[name+"_freq_Mhz"] = int(freqHz / 1e6)
		cpuMetricDict[name+"_active"] = int((1 - idleRatio) * 100)
		cpus := cluster["cpus"].([]interface{})
		for _, c := range cpus {
			cpu := c.(map[string]interface{})
			var cpuNum int
			switch v := cpu["cpu"].(type) {
			case int64:
				cpuNum = int(v)
			case uint64:
				cpuNum = int(v)
			case float64:
				cpuNum = int(v)
			}
			var cpuFreqHz float64
			switch v := cpu["freq_hz"].(type) {
			case int64:
				cpuFreqHz = float64(v)
			case float64:
				cpuFreqHz = v
			}
			cpuIdleRatio := cpu["idle_ratio"].(float64)
			clusterName := "E-Cluster"
			if !strings.HasPrefix(name, "E") {
				clusterName = "P-Cluster"
			}
			if clusterName == "E-Cluster" {
				eCores = append(eCores, cpuNum)
			} else {
				pCores = append(pCores, cpuNum)
			}
			cpuMetricDict[clusterName+strconv.Itoa(cpuNum)+"_freq_Mhz"] = int(cpuFreqHz / 1e6)
			cpuMetricDict[clusterName+strconv.Itoa(cpuNum)+"_active"] = int((1 - cpuIdleRatio) * 100)
		}
	}
	cpuMetrics.ECores = eCores
	cpuMetrics.PCores = pCores
	if _, exists := cpuMetricDict["E-Cluster_active"]; !exists {
		if e0Active, ok := cpuMetricDict["E0-Cluster_active"].(int); ok {
			if e1Active, ok := cpuMetricDict["E1-Cluster_active"].(int); ok {
				cpuMetrics.EClusterActive = (e0Active + e1Active) / 2
			}
		}
		if e0Freq, ok := cpuMetricDict["E0-Cluster_freq_Mhz"].(int); ok {
			if e1Freq, ok := cpuMetricDict["E1-Cluster_freq_Mhz"].(int); ok {
				cpuMetrics.EClusterFreqMHz = max(e0Freq, e1Freq)
			}
		}
	} else {
		if active, ok := cpuMetricDict["E-Cluster_active"].(int); ok {
			cpuMetrics.EClusterActive = active
		}
		if freq, ok := cpuMetricDict["E-Cluster_freq_Mhz"].(int); ok {
			cpuMetrics.EClusterFreqMHz = freq
		}
	}
	if _, exists := cpuMetricDict["P-Cluster_active"]; !exists {
		if _, hasP2 := cpuMetricDict["P2-Cluster_active"]; hasP2 {
			if p0Active, ok := cpuMetricDict["P0-Cluster_active"].(int); ok {
				if p1Active, ok := cpuMetricDict["P1-Cluster_active"].(int); ok {
					if p2Active, ok := cpuMetricDict["P2-Cluster_active"].(int); ok {
						if p3Active, ok := cpuMetricDict["P3-Cluster_active"].(int); ok {
							cpuMetrics.PClusterActive = (p0Active + p1Active + p2Active + p3Active) / 4
						}
					}
				}
			}
		} else {
			if p0Active, ok := cpuMetricDict["P0-Cluster_active"].(int); ok {
				if p1Active, ok := cpuMetricDict["P1-Cluster_active"].(int); ok {
					cpuMetrics.PClusterActive = (p0Active + p1Active) / 2
				}
			}
		}
	} else {
		if active, ok := cpuMetricDict["P-Cluster_active"].(int); ok {
			cpuMetrics.PClusterActive = active
		}
		if freq, ok := cpuMetricDict["P-Cluster_freq_Mhz"].(int); ok {
			cpuMetrics.PClusterFreqMHz = freq
		}
	}
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
