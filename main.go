// Copyright (c) 2024 Carsen Klock under MIT License
// mactop is a simple terminal based Apple Silicon power monitor written in Go Lang!

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	ui "github.com/gizak/termui/v3"
	w "github.com/gizak/termui/v3/widgets"
	"github.com/shirou/gopsutil/mem"
)

type CPUMetrics struct {
	EClusterActive   int
	EClusterFreqMHz  int
	PClusterActive   int
	PClusterFreqMHz  int
	ECores           []int
	PCores           []int
	ANEW             float64
	CPUW             float64
	GPUW             float64
	PackageW         float64
	E0ClusterActive  int
	E0ClusterFreqMHz int
	E1ClusterActive  int
	E1ClusterFreqMHz int
	P0ClusterActive  int
	P0ClusterFreqMHz int
	P1ClusterActive  int
	P1ClusterFreqMHz int
	P2ClusterActive  int
	P2ClusterFreqMHz int
	P3ClusterActive  int
	P3ClusterFreqMHz int
}
type NetDiskMetrics struct {
	OutPacketsPerSec float64
	OutBytesPerSec   float64
	InPacketsPerSec  float64
	InBytesPerSec    float64

	ReadOpsPerSec     float64
	WriteOpsPerSec    float64
	ReadKBytesPerSec  float64
	WriteKBytesPerSec float64
}

type GPUMetrics struct {
	FreqMHz int
	Active  float64
}

type ProcessMetrics struct {
	ID       int
	Name     string
	CPUUsage float64
}

type MemoryMetrics struct {
	Total     uint64
	Used      uint64
	Available uint64
	SwapTotal uint64
	SwapUsed  uint64
}

var (
	cpu1Gauge, cpu2Gauge, gpuGauge, aneGauge        *w.Gauge
	TotalPowerChart                                 *w.Plot
	memoryGauge                                     *w.Gauge
	modelText, PowerChart, NetworkInfo, ProcessInfo *w.Paragraph
	grid                                            *ui.Grid

	stderrLogger      = log.New(os.Stderr, "", 0)
	currentGridLayout = "default"
	updateInterval    = 1000
)

func setupUI() {

	appleSiliconModel := getSOCInfo()
	modelText = w.NewParagraph()
	modelText.Title = "Apple Silicon"

	modelName, ok := appleSiliconModel["name"].(string)
	if !ok {
		modelName = "Unknown Model"
	}
	eCoreCount, ok := appleSiliconModel["e_core_count"].(int) // Ensure the type assertion is correct as per what getSOCInfo() stores
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
	stderrLogger.Printf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %s",
		modelName,
		eCoreCount,
		pCoreCount,
		gpuCoreCount,
	)

	cpu1Gauge = w.NewGauge()
	cpu1Gauge.Title = "E-CPU Usage"
	cpu1Gauge.Percent = 0
	cpu1Gauge.BarColor = ui.ColorGreen

	cpu2Gauge = w.NewGauge()
	cpu2Gauge.Title = "P-CPU Usage"
	cpu2Gauge.Percent = 0
	cpu2Gauge.BarColor = ui.ColorYellow

	gpuGauge = w.NewGauge()
	gpuGauge.Title = "GPU Usage"
	gpuGauge.Percent = 0
	gpuGauge.BarColor = ui.ColorMagenta

	aneGauge = w.NewGauge()
	aneGauge.Title = "ANE"
	aneGauge.Percent = 0
	aneGauge.BarColor = ui.ColorBlue

	PowerChart = w.NewParagraph()
	PowerChart.Title = "Power Usage"

	NetworkInfo = w.NewParagraph()
	NetworkInfo.Title = "Network & Disk Info"

	ProcessInfo = w.NewParagraph()
	ProcessInfo.Title = "Process Info"

	TotalPowerChart = w.NewPlot()
	TotalPowerChart.Title = "Total Power Usage (W)"
	TotalPowerChart.Data = make([][]float64, 1)
	TotalPowerChart.Data[0] = []float64{1, 2, 3, 4, 5}
	TotalPowerChart.AxesColor = ui.ColorGreen
	TotalPowerChart.LineColors = []ui.Color{ui.ColorCyan}

	memoryGauge = w.NewGauge()
	memoryGauge.Title = "Memory Usage"
	memoryGauge.Percent = 0
	memoryGauge.BarColor = ui.ColorCyan

}

func setupGrid() {
	grid = ui.NewGrid()
	grid.Set(
		ui.NewRow(1.0/2, // This row now takes half the height of the grid
			ui.NewCol(1.0/2, ui.NewRow(1.0/2, cpu1Gauge), ui.NewCol(1.0, ui.NewRow(1.0, cpu2Gauge))),
			ui.NewCol(1.0/2, ui.NewRow(1.0/2, gpuGauge), ui.NewCol(1.0, ui.NewRow(1.0, aneGauge))), // ui.NewCol(1.0/2, ui.NewRow(1.0, ProcessInfo)), // ProcessInfo spans this entire column
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
		ui.Clear()
		newGrid := ui.NewGrid()
		newGrid.Set(
			ui.NewRow(1.0/2, // This row now takes half the height of the grid
				ui.NewCol(1.0/2, ui.NewRow(1.0, cpu1Gauge)), // ui.NewCol(1.0, ui.NewRow(1.0, cpu2Gauge))),
				ui.NewCol(1.0/2, ui.NewRow(1.0, cpu2Gauge)), // ProcessInfo spans this entire column
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/4, gpuGauge),
				ui.NewCol(1.0/4, aneGauge),
				ui.NewCol(1.0/4, PowerChart),
				ui.NewCol(1.0/4, TotalPowerChart),
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
		ui.Render(grid)
	} else {
		ui.Clear()
		newGrid := ui.NewGrid()

		newGrid.Set(
			ui.NewRow(1.0/2, // This row now takes half the height of the grid
				ui.NewCol(1.0/2, ui.NewRow(1.0/2, cpu1Gauge), ui.NewCol(1.0, ui.NewRow(1.0, cpu2Gauge))),
				ui.NewCol(1.0/2, ui.NewRow(1.0/2, gpuGauge), ui.NewCol(1.0, ui.NewRow(1.0, aneGauge))), // ui.NewCol(1.0/2, ui.NewRow(1.0, ProcessInfo)), // ProcessInfo spans this entire column
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
		ui.Render(grid)
	}
}

func StderrToLogfile(logfile *os.File) {
	syscall.Dup2(int(logfile.Fd()), 2)
}

func main() {

	if len(os.Args) > 1 && os.Args[1] == "--help" || len(os.Args) > 1 && os.Args[1] == "-h" {
		fmt.Println("Usage: mactop [--help] [--version] [--interval]")
		fmt.Println("--help: Show this help message")
		fmt.Println("--version: Show the version of mactop")
		fmt.Println("--interval: Set the powermetrics update interval in milliseconds. Default is 1000.")
		fmt.Println("You must use sudo to run mactop, as powermetrics requires root privileges.")
		fmt.Println("For more information, see https://github.com/context-labs/mactop")
		os.Exit(0)
	}

	version := "v0.1.5"
	if len(os.Args) > 1 && os.Args[1] == "--version" || len(os.Args) > 1 && os.Args[1] == "-v" {
		fmt.Println("mactop version:", version)
		os.Exit(0)
	}

	if len(os.Args) > 2 && os.Args[1] == "--test" {
		testInput := os.Args[2]
		fmt.Printf("Test input received: %s\n", testInput)
		os.Exit(0)
	}

	if os.Geteuid() != 0 {
		fmt.Println("Welcome to mactop! Please try again and run mactop with sudo privileges!")
		fmt.Println("Usage: sudo mactop")
		os.Exit(1)
	}

	if len(os.Args) > 1 && os.Args[1] == "--interval" || len(os.Args) > 1 && os.Args[1] == "-i" {
		interval, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Println("Invalid interval:", err)
			os.Exit(1)
		}
		updateInterval = interval
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
	setupUI() // Initialize UI components and layout
	setupGrid()

	termWidth, termHeight := ui.TerminalDimensions()
	grid.SetRect(0, 0, termWidth, termHeight)
	ui.Render(grid)

	cpuMetricsChan := make(chan CPUMetrics)
	gpuMetricsChan := make(chan GPUMetrics)
	netdiskMetricsChan := make(chan NetDiskMetrics)
	processMetricsChan := make(chan []ProcessMetrics)

	done := make(chan struct{})
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go collectMetrics(done, cpuMetricsChan, gpuMetricsChan, netdiskMetricsChan, processMetricsChan)

	go func() {
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
			case processMetrics := <-processMetricsChan:
				updateProcessUI(processMetrics)
				ui.Render(grid)
			case <-quit:
				close(done)
				ui.Close()
				os.Exit(0)
				return
			}
		}
	}()

	uiEvents := ui.PollEvents()
	for {
		select {
		case e := <-uiEvents:
			switch e.ID {
			case "q", "<C-c>": // "q" or Ctrl+C to quit
				close(done)
				ui.Close()
				os.Exit(0)
				return
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				grid.SetRect(0, 0, payload.Width, payload.Height)
				ui.Render(grid)
			case "r":
				// refresh ui data
				termWidth, termHeight := ui.TerminalDimensions()
				grid.SetRect(0, 0, termWidth, termHeight)
				ui.Clear()
				ui.Render(grid)
			case "l":
				// Set the new grid's dimensions to match the terminal size
				termWidth, termHeight := ui.TerminalDimensions()
				grid.SetRect(0, 0, termWidth, termHeight)
				ui.Clear()
				switchGridLayout()
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
	// create the log directory
	if err := os.MkdirAll("logs", 0755); err != nil {
		return nil, fmt.Errorf("failed to make the log directory: %v", err)
	}
	// open the log file
	logfile, err := os.OpenFile("logs/mactop.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}

	// log time, filename, and line number
	log.SetFlags(log.Ltime | log.Lshortfile)
	// log to file
	log.SetOutput(logfile)

	return logfile, nil
}

func collectMetrics(done chan struct{}, cpumetricsChan chan CPUMetrics, gpumetricsChan chan GPUMetrics, netdiskMetricsChan chan NetDiskMetrics, processMetricsChan chan []ProcessMetrics) {
	var cpuMetrics CPUMetrics
	var gpuMetrics GPUMetrics
	var netdiskMetrics NetDiskMetrics
	var processMetrics []ProcessMetrics
	cmd := exec.Command("powermetrics", "--samplers", "cpu_power,gpu_power,thermal,network,disk", "--show-process-gpu", "--show-process-energy", "--show-initial-usage", "--show-process-netstats", "-i", strconv.Itoa(updateInterval))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stderrLogger.Fatalf("failed to get stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		stderrLogger.Fatalf("failed to start command: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	go func() {
		for {
			select {
			case <-done: // Check if we need to exit
				cmd.Process.Kill() // Ensure subprocess is terminated
				os.Exit(0)
				return
			default:
				if scanner.Scan() {
					line := scanner.Text()
					cpuMetrics = parseCPUMetrics(line, cpuMetrics)
					gpuMetrics = parseGPUMetrics(line, gpuMetrics)
					netdiskMetrics = parseActivityMetrics(line, netdiskMetrics)
					processMetrics = parseProcessMetrics(line, processMetrics)

					cpumetricsChan <- cpuMetrics
					gpumetricsChan <- gpuMetrics
					netdiskMetricsChan <- netdiskMetrics
					processMetricsChan <- processMetrics

				} else {
					if err := scanner.Err(); err != nil {
						stderrLogger.Printf("error during scan: %v", err)
					}
					return // Exit loop if Scan() returns false
				}
			}
		}
	}()

	if err := cmd.Wait(); err != nil {
		stderrLogger.Fatalf("command failed: %v", err)
	}
}

func updateTotalPowerChart(newPowerValue float64) {
	// stderrLogger.Printf("Rendering TotalPowerChart with data: %v\n", TotalPowerChart.Data)
	if len(TotalPowerChart.Data[0]) == 0 {
		TotalPowerChart.Data[0] = append(TotalPowerChart.Data[0], 0) // Ensure there's at least one data point
	}

	TotalPowerChart.Data[0] = append(TotalPowerChart.Data[0], newPowerValue)

	if len(TotalPowerChart.Data[0]) > 250 {
		TotalPowerChart.Data[0] = TotalPowerChart.Data[0][1:]
	}

	if len(TotalPowerChart.Data[0]) > 0 {
		ui.Render(TotalPowerChart)
	} else {
		log.Println("No data to render for TotalPowerChart")
	}
}

func updateCPUUI(cpuMetrics CPUMetrics) {
	cpu1Gauge.Title = fmt.Sprintf("E-CPU Usage: %d%% @ %d MHz", cpuMetrics.EClusterActive, cpuMetrics.EClusterFreqMHz)
	cpu1Gauge.Percent = cpuMetrics.EClusterActive
	cpu2Gauge.Title = fmt.Sprintf("P-CPU Usage: %d%% @ %d MHz", cpuMetrics.PClusterActive, cpuMetrics.PClusterFreqMHz)
	cpu2Gauge.Percent = cpuMetrics.PClusterActive
	aneUtil := int(cpuMetrics.ANEW * 100 / 8.0)
	aneGauge.Title = fmt.Sprintf("ANE Usage: %d%% @ %.1f W", aneUtil, cpuMetrics.ANEW)
	aneGauge.Percent = aneUtil

	TotalPowerChart.Title = fmt.Sprintf("%.1f W Total Power", cpuMetrics.PackageW)
	PowerChart.Title = fmt.Sprintf("%.1f W CPU - %.1f W GPU", cpuMetrics.CPUW, cpuMetrics.GPUW)
	PowerChart.Text = fmt.Sprintf("CPU Power: %.1f W\nGPU Power: %.1f W\nANE Power: %.1f W\nTotal Power: %.1f W", cpuMetrics.CPUW, cpuMetrics.GPUW, cpuMetrics.ANEW, cpuMetrics.PackageW)

	memoryMetrics := getMemoryMetrics()

	memoryGauge.Title = fmt.Sprintf("Memory Usage: %.2f GB / %.2f GB (Swap: %.2f/%.2f GB)", float64(memoryMetrics.Used)/1024/1024/1024, float64(memoryMetrics.Total)/1024/1024/1024, float64(memoryMetrics.SwapUsed)/1024/1024/1024, float64(memoryMetrics.SwapTotal)/1024/1024/1024)
	memoryGauge.Percent = int((float64(memoryMetrics.Used) / float64(memoryMetrics.Total)) * 100)

	ui.Render(grid)
	ui.Render(cpu1Gauge, cpu2Gauge, gpuGauge, aneGauge, memoryGauge, modelText, PowerChart)
}

func updateGPUUI(gpuMetrics GPUMetrics) {
	gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz", int(gpuMetrics.Active), gpuMetrics.FreqMHz)
	gpuGauge.Percent = int(gpuMetrics.Active)
}

func updateNetDiskUI(netdiskMetrics NetDiskMetrics) {
	NetworkInfo.Text = fmt.Sprintf("Out: %.1f packets/s, %.1f bytes/s\nIn: %.1f packets/s, %.1f bytes/s\nRead: %.1f ops/s, %.1f KBytes/s\nWrite: %.1f ops/s, %.1f KBytes/s", netdiskMetrics.OutPacketsPerSec, netdiskMetrics.OutBytesPerSec, netdiskMetrics.InPacketsPerSec, netdiskMetrics.InBytesPerSec, netdiskMetrics.ReadOpsPerSec, netdiskMetrics.ReadKBytesPerSec, netdiskMetrics.WriteOpsPerSec, netdiskMetrics.WriteKBytesPerSec)
}

func updateProcessUI(processMetrics []ProcessMetrics) {
	ProcessInfo.Text = ""
	sort.Slice(processMetrics, func(i, j int) bool {
		return processMetrics[i].CPUUsage > processMetrics[j].CPUUsage
	})
	maxEntries := 15
	if len(processMetrics) > maxEntries {
		processMetrics = processMetrics[:maxEntries]
	}
	for _, pm := range processMetrics {
		ProcessInfo.Text += fmt.Sprintf("%d - %s: %.2f ms/s\n", pm.ID, pm.Name, pm.CPUUsage)
	}
	ui.Render(ProcessInfo)
}

func parseProcessMetrics(powermetricsOutput string, processMetrics []ProcessMetrics) []ProcessMetrics {
	lines := strings.Split(powermetricsOutput, "\n")
	dataRegex := regexp.MustCompile(`(?m)^\s*(\S.*?)\s+(\d+)\s+(\d+\.\d+)\s+\d+\.\d+\s+`)
	seen := make(map[int]bool) // Map to track seen process IDs
	for _, line := range lines {
		matches := dataRegex.FindStringSubmatch(line)
		if len(matches) > 3 {
			processName := matches[1]
			if processName == "mactop" || processName == "main" || processName == "powermetrics" {
				continue // Skip this process
			}
			id, _ := strconv.Atoi(matches[2])
			if !seen[id] {
				seen[id] = true
				cpuMsPerS, _ := strconv.ParseFloat(matches[3], 64)
				processMetrics = append(processMetrics, ProcessMetrics{
					Name:     matches[1],
					ID:       id,
					CPUUsage: cpuMsPerS,
				})
			}
		}
	}

	sort.Slice(processMetrics, func(i, j int) bool {
		return processMetrics[i].CPUUsage > processMetrics[j].CPUUsage
	})

	return processMetrics
}

func parseActivityMetrics(powermetricsOutput string, netdiskMetrics NetDiskMetrics) NetDiskMetrics {
	outRegex := regexp.MustCompile(`out:\s*([\d.]+)\s*packets/s,\s*([\d.]+)\s*bytes/s`)
	inRegex := regexp.MustCompile(`in:\s*([\d.]+)\s*packets/s,\s*([\d.]+)\s*bytes/s`)
	outMatches := outRegex.FindStringSubmatch(powermetricsOutput)
	inMatches := inRegex.FindStringSubmatch(powermetricsOutput)

	if len(outMatches) == 3 {
		netdiskMetrics.OutPacketsPerSec, _ = strconv.ParseFloat(outMatches[1], 64)
		netdiskMetrics.OutBytesPerSec, _ = strconv.ParseFloat(outMatches[2], 64)
	}

	if len(inMatches) == 3 {
		netdiskMetrics.InPacketsPerSec, _ = strconv.ParseFloat(inMatches[1], 64)
		netdiskMetrics.InBytesPerSec, _ = strconv.ParseFloat(inMatches[2], 64)
	}

	readRegex := regexp.MustCompile(`read:\s*([\d.]+)\s*ops/s\s*([\d.]+)\s*KBytes/s`)
	writeRegex := regexp.MustCompile(`write:\s*([\d.]+)\s*ops/s\s*([\d.]+)\s*KBytes/s`)
	readMatches := readRegex.FindStringSubmatch(powermetricsOutput)
	writeMatches := writeRegex.FindStringSubmatch(powermetricsOutput)

	if len(readMatches) == 3 {
		netdiskMetrics.ReadOpsPerSec, _ = strconv.ParseFloat(readMatches[1], 64)
		netdiskMetrics.ReadKBytesPerSec, _ = strconv.ParseFloat(readMatches[2], 64)
	}

	if len(writeMatches) == 3 {
		netdiskMetrics.WriteOpsPerSec, _ = strconv.ParseFloat(writeMatches[1], 64)
		netdiskMetrics.WriteKBytesPerSec, _ = strconv.ParseFloat(writeMatches[2], 64)
	}

	return netdiskMetrics
}

func parseCPUMetrics(powermetricsOutput string, cpuMetrics CPUMetrics) CPUMetrics {
	lines := strings.Split(powermetricsOutput, "\n")
	eCores := []int{}
	pCores := []int{}
	eClusterActiveTotal := 0
	eClusterCount := 0
	pClusterActiveTotal := 0
	pClusterCount := 0
	eClusterFreqTotal := 0
	pClusterFreqTotal := 0
	residencyRe := regexp.MustCompile(`(\w+-Cluster)\s+HW active residency:\s+(\d+\.\d+)%`)
	frequencyRe := regexp.MustCompile(`(\w+-Cluster)\s+HW active frequency:\s+(\d+)\s+MHz`)

	for _, line := range lines {
		residencyMatches := residencyRe.FindStringSubmatch(line)
		frequencyMatches := frequencyRe.FindStringSubmatch(line)

		if residencyMatches != nil {
			cluster := residencyMatches[1]
			percent, _ := strconv.ParseFloat(residencyMatches[2], 64)
			switch cluster {
			case "E0-Cluster":
				cpuMetrics.E0ClusterActive = int(percent)
			case "E1-Cluster":
				cpuMetrics.E1ClusterActive = int(percent)
			case "P0-Cluster":
				cpuMetrics.P0ClusterActive = int(percent)
			case "P1-Cluster":
				cpuMetrics.P1ClusterActive = int(percent)
			case "P2-Cluster":
				cpuMetrics.P2ClusterActive = int(percent)
			case "P3-Cluster":
				cpuMetrics.P3ClusterActive = int(percent)
			}
			if strings.HasPrefix(cluster, "E") {
				eClusterActiveTotal += int(percent)
				eClusterCount++
			} else if strings.HasPrefix(cluster, "P") {
				pClusterActiveTotal += int(percent)
				pClusterCount++
				cpuMetrics.PClusterActive = pClusterActiveTotal / pClusterCount
			}
		}

		if frequencyMatches != nil {
			cluster := frequencyMatches[1]
			freqMHz, _ := strconv.Atoi(frequencyMatches[2])
			switch cluster {
			case "E0-Cluster":
				cpuMetrics.E0ClusterFreqMHz = freqMHz
			case "E1-Cluster":
				cpuMetrics.E1ClusterFreqMHz = freqMHz
			case "P0-Cluster":
				cpuMetrics.P0ClusterFreqMHz = freqMHz
			case "P1-Cluster":
				cpuMetrics.P1ClusterFreqMHz = freqMHz
			case "P2-Cluster":
				cpuMetrics.P2ClusterFreqMHz = freqMHz
			case "P3-Cluster":
				cpuMetrics.P3ClusterFreqMHz = freqMHz
			}
			if strings.HasPrefix(cluster, "E") {
				eClusterFreqTotal += int(freqMHz)
				cpuMetrics.EClusterFreqMHz = eClusterFreqTotal
			} else if strings.HasPrefix(cluster, "P") {
				pClusterFreqTotal += int(freqMHz)
				cpuMetrics.PClusterFreqMHz = pClusterFreqTotal
			}
		}

		if strings.Contains(line, "CPU ") && strings.Contains(line, "frequency") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				core, _ := strconv.Atoi(strings.TrimPrefix(fields[1], "CPU"))
				if strings.Contains(line, "E-Cluster") {
					eCores = append(eCores, core)
				} else if strings.Contains(line, "P-Cluster") {
					pCores = append(pCores, core)
				}
			}
		} else if strings.Contains(line, "ANE Power") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				cpuMetrics.ANEW, _ = strconv.ParseFloat(strings.TrimSuffix(fields[2], "mW"), 64)
				cpuMetrics.ANEW /= 1000 // Convert mW to W
			}
		} else if strings.Contains(line, "CPU Power") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				cpuMetrics.CPUW, _ = strconv.ParseFloat(strings.TrimSuffix(fields[2], "mW"), 64)
				cpuMetrics.CPUW /= 1000 // Convert mW to W
			}
		} else if strings.Contains(line, "GPU Power") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				cpuMetrics.GPUW, _ = strconv.ParseFloat(strings.TrimSuffix(fields[2], "mW"), 64)
				cpuMetrics.GPUW /= 1000 // Convert mW to W
			}
		} else if strings.Contains(line, "Combined Power (CPU + GPU + ANE)") {
			fields := strings.Fields(line)
			if len(fields) >= 8 {
				cpuMetrics.PackageW, _ = strconv.ParseFloat(strings.TrimSuffix(fields[7], "mW"), 64)
				cpuMetrics.PackageW /= 1000 // Convert mW to W
			}
		}
	}

	cpuMetrics.ECores = eCores
	cpuMetrics.PCores = pCores

	if cpuMetrics.E1ClusterActive != 0 {
		// M1 Ultra
		cpuMetrics.EClusterActive = (cpuMetrics.E0ClusterActive + cpuMetrics.E1ClusterActive) / 2
		cpuMetrics.EClusterFreqMHz = max(cpuMetrics.E0ClusterFreqMHz, cpuMetrics.E1ClusterFreqMHz)
	}

	if cpuMetrics.P3ClusterActive != 0 {
		// M1 Ultra
		cpuMetrics.PClusterActive = (cpuMetrics.P0ClusterActive + cpuMetrics.P1ClusterActive + cpuMetrics.P2ClusterActive + cpuMetrics.P3ClusterActive) / 4
		cpuMetrics.PClusterFreqMHz = max(cpuMetrics.P0ClusterFreqMHz, cpuMetrics.P1ClusterFreqMHz, cpuMetrics.P2ClusterFreqMHz, cpuMetrics.P3ClusterFreqMHz)
	} else if cpuMetrics.P1ClusterActive != 0 {
		// M1/M2/M3 Max/Pro
		cpuMetrics.PClusterActive = (cpuMetrics.P0ClusterActive + cpuMetrics.P1ClusterActive) / 2
		cpuMetrics.PClusterFreqMHz = max(cpuMetrics.P0ClusterFreqMHz, cpuMetrics.P1ClusterFreqMHz)
	} else {
		// M1
		cpuMetrics.PClusterActive = cpuMetrics.PClusterActive + cpuMetrics.P0ClusterActive
	}

	// Calculate average active residency and frequency for E and P clusters
	if eClusterCount > 0 {
		cpuMetrics.EClusterActive = eClusterActiveTotal / eClusterCount
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

func parseGPUMetrics(powermetricsOutput string, gpuMetrics GPUMetrics) GPUMetrics {
	re := regexp.MustCompile(`GPU\s*(HW)?\s*active\s*(residency|frequency):\s+(\d+\.\d+)%?`)
	freqRe := regexp.MustCompile(`(\d+)\s*MHz:\s*(\d+)%`)
	lines := strings.Split(powermetricsOutput, "\n")

	for _, line := range lines {
		if strings.Contains(line, "GPU active") || strings.Contains(line, "GPU HW active") {
			matches := re.FindStringSubmatch(line)
			if len(matches) > 3 {
				if strings.Contains(matches[2], "residency") {
					gpuMetrics.Active, _ = strconv.ParseFloat(matches[3], 64)
				} else if strings.Contains(matches[2], "frequency") {
					gpuMetrics.FreqMHz, _ = strconv.Atoi(strings.TrimSuffix(matches[3], "MHz"))
				}
			}

			freqMatches := freqRe.FindAllStringSubmatch(line, -1)
			for _, match := range freqMatches {
				if len(match) == 3 {
					freq, _ := strconv.Atoi(match[1])
					residency, _ := strconv.ParseFloat(match[2], 64)
					if residency > 0 {
						gpuMetrics.FreqMHz = freq
						break
					}
				}
			}
		}
	}

	return gpuMetrics
}

func getSOCInfo() map[string]interface{} {
	cpuInfoDict := getCPUInfo()
	coreCountsDict := getCoreCounts()

	var eCoreCounts int
	var pCoreCounts int

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

	// TDP (power)
	switch socInfo["name"] {
	case "Apple M1 Max":
		socInfo["cpu_max_power"] = 30
		socInfo["gpu_max_power"] = 60
	case "Apple M1 Pro":
		socInfo["cpu_max_power"] = 30
		socInfo["gpu_max_power"] = 30
	case "Apple M1":
		socInfo["cpu_max_power"] = 20
		socInfo["gpu_max_power"] = 20
	case "Apple M1 Ultra":
		socInfo["cpu_max_power"] = 60
		socInfo["gpu_max_power"] = 120
	case "Apple M2":
		socInfo["cpu_max_power"] = 25
		socInfo["gpu_max_power"] = 15
	default:
		socInfo["cpu_max_power"] = 20
		socInfo["gpu_max_power"] = 20
	}

	// Bandwidth
	switch socInfo["name"] {
	case "Apple M1 Max":
		socInfo["cpu_max_bw"] = 250
		socInfo["gpu_max_bw"] = 400
	case "Apple M1 Pro":
		socInfo["cpu_max_bw"] = 200
		socInfo["gpu_max_bw"] = 200
	case "Apple M1":
		socInfo["cpu_max_bw"] = 70
		socInfo["gpu_max_bw"] = 70
	case "Apple M1 Ultra":
		socInfo["cpu_max_bw"] = 500
		socInfo["gpu_max_bw"] = 800
	case "Apple M2":
		socInfo["cpu_max_bw"] = 100
		socInfo["gpu_max_bw"] = 100
	default:
		socInfo["cpu_max_bw"] = 70
		socInfo["gpu_max_bw"] = 70
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
	out, err := exec.Command("sysctl", "hw.perflevel0.logicalcpu", "hw.perflevel1.logicalcpu").Output()
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
