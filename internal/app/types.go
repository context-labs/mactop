package app

import (
	"fmt"
	"image"
	"time"

	ui "github.com/gizak/termui/v3"
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
	ANEW, CPUW, GPUW, DRAMW, GPUSRAMW, PackageW                      float64
	CoreUsages                                                       []float64
	Throttled                                                        bool
	CPUTemp                                                          float64
	GPUTemp                                                          float64
}

type SystemInfo struct {
	Name         string `json:"name"`
	CoreCount    int    `json:"core_count"`
	ECoreCount   int    `json:"e_core_count"`
	PCoreCount   int    `json:"p_core_count"`
	GPUCoreCount int    `json:"gpu_core_count"`
}

type NetDiskMetrics struct {
	OutPacketsPerSec  float64 `json:"out_packets_per_sec"`
	OutBytesPerSec    float64 `json:"out_bytes_per_sec"`
	InPacketsPerSec   float64 `json:"in_packets_per_sec"`
	InBytesPerSec     float64 `json:"in_bytes_per_sec"`
	ReadOpsPerSec     float64 `json:"read_ops_per_sec"`
	WriteOpsPerSec    float64 `json:"write_ops_per_sec"`
	ReadKBytesPerSec  float64 `json:"read_kbytes_per_sec"`
	WriteKBytesPerSec float64 `json:"write_kbytes_per_sec"`
}

type GPUMetrics struct {
	FreqMHz       int
	ActivePercent float64
	Power         float64
	Temp          float32
}

type ProcessMetrics struct {
	PID                                      int
	CPU, LastTime, Memory                    float64
	VSZ, RSS                                 int64
	User, TTY, State, Started, Time, Command string
	LastUpdated                              time.Time
}

type MemoryMetrics struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
	SwapTotal uint64 `json:"swap_total"`
	SwapUsed  uint64 `json:"swap_used"`
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

func NewCPUCoreWidget(modelInfo SystemInfo) *CPUCoreWidget {
	eCoreCount := modelInfo.ECoreCount
	pCoreCount := modelInfo.PCoreCount
	modelName := modelInfo.Name
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
	labelWidth := 3 // Width for core labels

	colWidths := make([]int, cols)
	colXs := make([]int, cols)
	baseWidth := availableWidth / cols
	remainder := availableWidth % cols
	currentX := 0
	for c := 0; c < cols; c++ {
		colXs[c] = currentX
		w := baseWidth
		if c < remainder {
			w++
		}
		colWidths[c] = w
		currentX += w
	}

	for i := 0; i < totalCores; i++ {
		col := i % cols
		row := i / cols
		actualIndex := col*rows + row

		if actualIndex >= totalCores || row >= rows {
			continue
		}

		x := w.Inner.Min.X + colXs[col]
		y := w.Inner.Min.Y + row

		barWidth := colWidths[col]

		if y >= w.Inner.Max.Y {
			continue
		}

		usage := w.cores[actualIndex]

		label := fmt.Sprintf("%-2d", actualIndex)
		buf.SetString(label, ui.NewStyle(themeColor), image.Pt(x, y))

		availWidth := barWidth - labelWidth

		if x+labelWidth+availWidth > w.Inner.Max.X {
			availWidth = w.Inner.Max.X - x - labelWidth
		}

		if availWidth < 9 { // 2 brackets + 7 for text/min bar
			continue
		}

		textWidth := 7

		innerBarWidth := availWidth - 2 - textWidth
		if innerBarWidth < 0 {
			innerBarWidth = 0
		}

		usedWidth := int((usage / 100.0) * float64(innerBarWidth))

		buf.SetString("[", ui.NewStyle(BracketColor),
			image.Pt(x+labelWidth, y))

		for bx := 0; bx < innerBarWidth; bx++ {
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
		buf.SetString(percentage, ui.NewStyle(SecondaryTextColor),
			image.Pt(x+labelWidth+1+innerBarWidth, y))

		buf.SetString("]", ui.NewStyle(BracketColor),
			image.Pt(x+labelWidth+availWidth-1, y))
	}
}
