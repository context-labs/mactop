package app

import (
	"log"
	"os"
	"sync"
	"time"

	ui "github.com/gizak/termui/v3"
	w "github.com/gizak/termui/v3/widgets"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shirou/gopsutil/v4/disk"
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
	showHelp, partyMode                          = false, false
	updateInterval                               = 1000
	done                                         = make(chan struct{})
	partyTicker                                  *time.Ticker
	lastCPUTimes                                 []CPUUsage
	firstRun                                     = true
	sortReverse                                  = false
	columns                                      = []string{"PID", "USER", "VIRT", "RES", "CPU", "MEM", "TIME", "CMD"}
	selectedColumn                               = 4
	maxPowerSeen                                 = 0.1
	gpuValues                                    = make([]float64, 100)
	prometheusPort                               string
	headless                                     bool
	headlessCount                                int
	interruptChan                                = make(chan struct{}, 10)
	lastNetStats                                 net.IOCountersStat
	lastDiskStats                                disk.IOCountersStat
	lastNetDiskTime                              time.Time
	netDiskMutex                                 sync.Mutex
	killPending                                  bool
	killPID                                      int
	currentUser                                  string
	lastProcesses                                []ProcessMetrics
	networkUnit                                  string
	diskUnit                                     string
	tempUnit                                     string
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
	gpuTemp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mactop_gpu_temperature_celsius",
		Help: "Current GPU temperature in Celsius",
	})
	thermalState = prometheus.NewGauge(prometheus.GaugeOpts{
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
