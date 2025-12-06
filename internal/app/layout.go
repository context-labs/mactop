package app

import (
	ui "github.com/gizak/termui/v3"
)

const (
	LayoutDefault         = "default"
	LayoutAlternative     = "alternative"
	LayoutAlternativeFull = "alternative_full"
	LayoutVertical        = "vertical"
	LayoutCompact         = "compact"
	LayoutDashboard       = "dashboard"
	LayoutGaugesOnly      = "gauges_only"
)

var layoutOrder = []string{LayoutDefault, LayoutAlternative, LayoutAlternativeFull, LayoutVertical, LayoutCompact, LayoutDashboard, LayoutGaugesOnly}

func setupGrid() {
	applyLayout(currentConfig.DefaultLayout)
}

func cycleLayout() {
	currentIndex := 0
	for i, layout := range layoutOrder {
		if layout == currentConfig.DefaultLayout {
			currentIndex = i
			break
		}
	}
	nextIndex := (currentIndex + 1) % len(layoutOrder)
	currentConfig.DefaultLayout = layoutOrder[nextIndex]
	applyLayout(currentConfig.DefaultLayout)
	updateHelpText()
}

func applyLayout(layoutName string) {
	termWidth, termHeight := ui.TerminalDimensions()
	grid = ui.NewGrid()

	switch layoutName {
	case LayoutAlternative:
		grid.Set(
			ui.NewRow(1.0/2,
				ui.NewCol(1.0/2, cpuCoreWidget),
				ui.NewCol(1.0/2,
					ui.NewRow(1.0/2, gpuGauge),
					ui.NewCol(1.0, ui.NewRow(1.0, memoryGauge)),
				),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/6, modelText),
				ui.NewCol(1.0/3, NetworkInfo),
				ui.NewCol(1.0/4, PowerChart),
				ui.NewCol(1.0/4, sparklineGroup),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, processList),
			),
		)
	case LayoutAlternativeFull:
		grid.Set(
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, cpuCoreWidget),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, gpuGauge),
				ui.NewCol(1.0/2, memoryGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/6, modelText),
				ui.NewCol(1.0/3, NetworkInfo),
				ui.NewCol(1.0/4, PowerChart),
				ui.NewCol(1.0/4, sparklineGroup),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, processList),
			),
		)
	case LayoutVertical:
		grid.Set(
			ui.NewRow(1.0,
				ui.NewCol(0.4,
					ui.NewRow(1.0/8, cpuGauge),
					ui.NewRow(1.0/8, gpuGauge),
					ui.NewRow(1.0/8, aneGauge),
					ui.NewRow(1.5/8, memoryGauge),
					ui.NewRow(1.5/8, NetworkInfo),
					ui.NewRow(2.0/8, modelText),
				),
				ui.NewCol(0.6,
					ui.NewRow(3.0/4, processList),
					ui.NewRow(1.0/4,
						ui.NewCol(1.0/2, PowerChart),
						ui.NewCol(1.0/2, sparklineGroup),
					),
				),
			),
		)
	case LayoutCompact:
		grid.Set(
			ui.NewRow(2.0/8,
				ui.NewCol(1.0/4, cpuGauge),
				ui.NewCol(1.0/4, gpuGauge),
				ui.NewCol(1.0/4, memoryGauge),
				ui.NewCol(1.0/4, aneGauge),
			),
			ui.NewRow(2.0/8,
				ui.NewCol(1.0/3, modelText),
				ui.NewCol(1.0/3, NetworkInfo),
				ui.NewCol(1.0/3, PowerChart),
			),
			ui.NewRow(2.0/4,
				ui.NewCol(1.0, processList),
			),
		)
	case LayoutDashboard:
		grid.Set(
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/4, cpuGauge),
				ui.NewCol(1.0/4, gpuGauge),
				ui.NewCol(1.0/4, memoryGauge),
				ui.NewCol(1.0/4, aneGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, sparklineGroup),
				ui.NewCol(1.0/2, gpuSparklineGroup),
			),
			ui.NewRow(2.0/4,
				ui.NewCol(1.0, processList),
			),
		)
	case LayoutGaugesOnly:
		grid.Set(
			ui.NewRow(1.0/3,
				ui.NewCol(1.0/2, cpuGauge),
				ui.NewCol(1.0/2, memoryGauge),
			),
			ui.NewRow(1.0/3,
				ui.NewCol(1.0/2, gpuGauge),
				ui.NewCol(1.0/2, aneGauge),
			),
			ui.NewRow(1.0/3,
				ui.NewCol(1.0/2, gpuSparklineGroup),
				ui.NewCol(1.0/2, sparklineGroup),
			),
		)
	default: // LayoutDefault
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
	grid.SetRect(0, 0, termWidth, termHeight)
}
