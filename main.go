// Copyright (c) 2024 Carsen Klock under MIT License
// goasitop is a simple terminal based Apple Silicon power monitor written in Go Lang!

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

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

type GPUMetrics struct {
	FreqMHz int
	Active  float64
}

type MemoryMetrics struct {
	Total     uint64
	Used      uint64
	Available uint64
	SwapTotal uint64
	SwapUsed  uint64
}

var (
	cpu1Gauge, cpu2Gauge, gpuGauge, aneGauge *w.Gauge
	TotalPowerChart                          *w.Sparkline
	GroupPowerChart                          *w.SparklineGroup
	memoryGauge                              *w.Gauge
	modelText, PowerChart                    *w.Paragraph
	grid                                     *ui.Grid

	stderrLogger = log.New(os.Stderr, "", 0)

	totalPowerData []float64 = make([]float64, 0, 50)

	cpuMetricsChan = make(chan CPUMetrics, 1)
	gpuMetricsChan = make(chan GPUMetrics, 1)
)

func setupUI() {

	appleSiliconModel := getSOCInfo()
	modelText = w.NewParagraph()
	modelText.Title = "Apple Silicon Model Info"

	// Accessing map values with type assertion
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

	modelText.Text = fmt.Sprintf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %s", modelName, eCoreCount, pCoreCount, gpuCoreCount)

	stderrLogger.Printf("Model: %s\nE-Core Count: %d\nP-Core Count: %d\nGPU Core Count: %s", modelName, eCoreCount, pCoreCount, gpuCoreCount)

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

	TotalPowerChart = w.NewSparkline()
	TotalPowerChart.LineColor = ui.ColorCyan

	GroupPowerChart = w.NewSparklineGroup(TotalPowerChart)
	GroupPowerChart.Title = "Total Power Usage"
	GroupPowerChart.SetRect(0, 0, 20, 10)

	memoryGauge = w.NewGauge()
	memoryGauge.Title = "Memory Usage"
	memoryGauge.Percent = 0
	memoryGauge.BarColor = ui.ColorCyan

}

func setupGrid() {
	grid = ui.NewGrid()

	grid.Set(
		ui.NewRow(1.0/2,
			ui.NewCol(1.0/2, cpu1Gauge),
			ui.NewCol(1.0/2, cpu2Gauge),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/4, gpuGauge),
			ui.NewCol(1.0/4, aneGauge),
			ui.NewCol(1.0/4, PowerChart),
			ui.NewCol(1.0/4, GroupPowerChart), // TODO: fix this
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/2, modelText),
			ui.NewCol(1.0/2, memoryGauge),
		),
	)
}

func StderrToLogfile(logfile *os.File) {
	syscall.Dup2(int(logfile.Fd()), 2)
}

func main() {

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

	done := make(chan struct{})

	go collectMetrics(done)

	eventLoop(done)

	ui.Close()
	os.Exit(0)
}

func setupLogfile() (*os.File, error) {
	// create the log directory
	if err := os.MkdirAll("logs", 0755); err != nil {
		return nil, fmt.Errorf("failed to make the log directory: %v", err)
	}
	// open the log file
	logfile, err := os.OpenFile("logs/goasitop.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}

	// log time, filename, and line number
	log.SetFlags(log.Ltime | log.Lshortfile)
	// log to file
	log.SetOutput(logfile)

	return logfile, nil
}

func eventLoop(done chan struct{}) {
	updateInterval := 2000 * time.Millisecond
	drawTicker := time.NewTicker(updateInterval)
	defer drawTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// collectMetrics()
	var lastCpuMetrics CPUMetrics
	var lastGpuMetrics GPUMetrics
	var cpuUpdated, gpuUpdated bool

	for {
		select {
		case newCpuMetrics := <-cpuMetricsChan:
			lastCpuMetrics = newCpuMetrics
			cpuUpdated = true
			if gpuUpdated {
				updateUI(lastCpuMetrics, lastGpuMetrics)
				cpuUpdated = false
				gpuUpdated = false
			}
		case newGpuMetrics := <-gpuMetricsChan:
			lastGpuMetrics = newGpuMetrics
			gpuUpdated = true
			if cpuUpdated {
				updateUI(lastCpuMetrics, lastGpuMetrics)
				cpuUpdated = false
				gpuUpdated = false
			}
		case <-drawTicker.C:
			collectMetrics(done)
			ui.Render(grid) // Regularly render the grid
		case e := <-ui.PollEvents():
			if e.ID == "q" || e.ID == "<C-c>" {
				close(done)
				ui.Close()
				os.Exit(0)
				return
			}
			switch e.ID {
			case "q", "<C-c>":
				os.Exit(0)
				close(done)
				ui.Close()
				return
			case "<Resize>":
				payload := e.Payload.(ui.Resize)
				grid.SetRect(0, 0, payload.Width, payload.Height)
				ui.Clear()
				ui.Render(grid)
			}
		case <-sigCh:
			close(done) // Signal to terminate collectMetrics
			ui.Close()
			os.Exit(0)
			return

		}
	}
}

func collectMetrics(done chan struct{}) {
	var cpuMetrics CPUMetrics
	var gpuMetrics GPUMetrics
	cmd := exec.Command("powermetrics", "--samplers", "cpu_power,gpu_power,thermal", "--show-process-gpu", "--show-process-energy", "--show-initial-usage")
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

					updateUI(cpuMetrics, gpuMetrics) // removing this causes UI to not update at all
					// Process line here
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

func updateUI(cpuMetrics CPUMetrics, gpuMetrics GPUMetrics) {
	cpu1Gauge.Title = fmt.Sprintf("E-CPU Usage: %d%% @ %d MHz", cpuMetrics.EClusterActive, cpuMetrics.EClusterFreqMHz)
	cpu1Gauge.Percent = cpuMetrics.EClusterActive
	cpu2Gauge.Title = fmt.Sprintf("P-CPU Usage: %d%% @ %d MHz", cpuMetrics.PClusterActive, cpuMetrics.PClusterFreqMHz)
	cpu2Gauge.Percent = cpuMetrics.PClusterActive
	gpuGauge.Title = fmt.Sprintf("GPU Usage: %d%% @ %d MHz (%.1f W)", int(gpuMetrics.Active), gpuMetrics.FreqMHz, cpuMetrics.GPUW)
	gpuGauge.Percent = int(gpuMetrics.Active)
	aneUtil := int(cpuMetrics.ANEW * 100 / 8.0)
	aneGauge.Title = fmt.Sprintf("ANE Usage: %d%% @ %.1f W", aneUtil, cpuMetrics.ANEW)
	aneGauge.Percent = aneUtil

	PowerChart.Title = fmt.Sprintf("%.1f W CPU - %.1f W GPU - %.1f W Total", cpuMetrics.CPUW, cpuMetrics.GPUW, cpuMetrics.PackageW)
	PowerChart.Text = fmt.Sprintf("\n\nCPU Power: %.1f W\nGPU Power: %.1f W\nANE Power: %.1f W\nTotal Power: %.1f W", cpuMetrics.CPUW, cpuMetrics.GPUW, cpuMetrics.ANEW, cpuMetrics.PackageW)

	totalPowerData = append(totalPowerData, cpuMetrics.PackageW)
	// Ensure the slice does not grow indefinitely
	if len(totalPowerData) > 50 { // Limit to last 50 readings
		totalPowerData = totalPowerData[1:]
	}

	TotalPowerChart.Data = totalPowerData
	// modelText.Text += fmt.Sprintf("\nCPU Power: %.1f W - GPU Power: %.1f W\nTotal Power: %.1f W", cpuMetrics.CPUW, cpuMetrics.GPUW, cpuMetrics.PackageW)

	memoryMetrics := getMemoryMetrics()

	memoryGauge.Title = fmt.Sprintf("Memory Usage: %.2f GB / %.2f GB (Swap: %.2f/%.2f GB)", float64(memoryMetrics.Used)/1024/1024/1024, float64(memoryMetrics.Total)/1024/1024/1024, float64(memoryMetrics.SwapUsed)/1024/1024/1024, float64(memoryMetrics.SwapTotal)/1024/1024/1024)
	memoryGauge.Percent = int((float64(memoryMetrics.Used) / float64(memoryMetrics.Total)) * 100)

	ui.Render(grid)
	ui.Render(cpu1Gauge, cpu2Gauge, gpuGauge, aneGauge, memoryGauge, modelText, PowerChart)
}

func parseCPUMetrics(powermetricsOutput string, cpuMetrics CPUMetrics) CPUMetrics {
	lines := strings.Split(powermetricsOutput, "\n")
	eCores := []int{}
	pCores := []int{}
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

	// Additional calculations for M1 Ultra or other logic as needed
	// M1 Ultra calculation example:
	if cpuMetrics.E0ClusterActive != 0 && cpuMetrics.E1ClusterActive != 0 {
		cpuMetrics.EClusterActive = (cpuMetrics.E0ClusterActive + cpuMetrics.E1ClusterActive) / 2
		cpuMetrics.EClusterFreqMHz = max(cpuMetrics.E0ClusterFreqMHz, cpuMetrics.E1ClusterFreqMHz)
	} else {
		cpuMetrics.EClusterActive = cpuMetrics.E0ClusterActive
		cpuMetrics.EClusterFreqMHz = cpuMetrics.E0ClusterFreqMHz
	}

	if cpuMetrics.P2ClusterActive != 0 && cpuMetrics.P3ClusterActive != 0 {
		cpuMetrics.PClusterActive = (cpuMetrics.P0ClusterActive + cpuMetrics.P1ClusterActive + cpuMetrics.P2ClusterActive + cpuMetrics.P3ClusterActive) / 4
		freqs := []int{cpuMetrics.P0ClusterFreqMHz, cpuMetrics.P1ClusterFreqMHz, cpuMetrics.P2ClusterFreqMHz, cpuMetrics.P3ClusterFreqMHz}
		cpuMetrics.PClusterFreqMHz = max(freqs...)
	} else {
		cpuMetrics.PClusterActive = (cpuMetrics.P0ClusterActive + cpuMetrics.P1ClusterActive) / 2
		cpuMetrics.PClusterFreqMHz = max(cpuMetrics.P0ClusterFreqMHz, cpuMetrics.P1ClusterFreqMHz)
	}

	// stderrLogger.Printf("CPU Metrics: %+v\n", cpuMetrics)

	return cpuMetrics
}

func parseGPUMetrics(powermetricsOutput string, gpuMetrics GPUMetrics) GPUMetrics {
	re := regexp.MustCompile(`GPU HW active residency:\s+(\d+\.\d+)%`) // Regex to capture the floating-point number followed by '%'
	lines := strings.Split(powermetricsOutput, "\n")

	for _, line := range lines {
		if strings.Contains(line, "GPU HW active residency") {
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				gpuMetrics.Active, _ = strconv.ParseFloat(matches[1], 64) // matches[1] contains the first captured group, the percentage
			}
		} else if strings.Contains(line, "GPU HW active frequency") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				gpuMetrics.FreqMHz, _ = strconv.Atoi(strings.TrimSuffix(fields[4], "MHz"))
			}
		}
	}

	// stderrLogger.Printf("GPU Metrics: %+v\n", gpuMetrics)
	return gpuMetrics
}

func max(values ...int) int {
	maxVal := values[0]
	for _, val := range values {
		if val > maxVal {
			maxVal = val
		}
	}
	return maxVal
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

	// Parse the output looking for the line containing "Total Number of Cores"
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
