package main

import (
	ui "github.com/gizak/termui/v3"
)

var colorMap = map[string]ui.Color{
	"green":   ui.ColorGreen,
	"red":     ui.ColorRed,
	"blue":    ui.ColorBlue,
	"cyan":    ui.ColorCyan,
	"magenta": ui.ColorMagenta,
	"yellow":  ui.ColorYellow,
	"white":   ui.ColorWhite,
}

var colorNames = []string{"green", "red", "blue", "cyan", "magenta", "yellow", "white"}

func applyTheme(colorName string) {
	color, ok := colorMap[colorName]
	if !ok {
		color = ui.ColorGreen // Default
		colorName = "green"
	}

	// Update global config
	currentConfig.Theme = colorName

	// Apply to UI components
	ui.Theme.Block.Title.Fg = color
	ui.Theme.Block.Border.Fg = color
	ui.Theme.Paragraph.Text.Fg = color
	ui.Theme.Gauge.Label.Fg = color
	ui.Theme.Gauge.Bar = color
	ui.Theme.BarChart.Bars = []ui.Color{color}

	if cpuGauge != nil {
		cpuGauge.BarColor = color
		cpuGauge.BorderStyle.Fg = color
		cpuGauge.TitleStyle.Fg = color

		gpuGauge.BarColor = color
		gpuGauge.BorderStyle.Fg = color
		gpuGauge.TitleStyle.Fg = color

		memoryGauge.BarColor = color
		memoryGauge.BorderStyle.Fg = color
		memoryGauge.TitleStyle.Fg = color

		aneGauge.BarColor = color
		aneGauge.BorderStyle.Fg = color
		aneGauge.TitleStyle.Fg = color
	}

	if processList != nil {
		processList.TextStyle = ui.NewStyle(color)
		processList.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, color)
		processList.BorderStyle.Fg = color
		processList.TitleStyle.Fg = color
	}

	if NetworkInfo != nil {
		NetworkInfo.TextStyle = ui.NewStyle(color)
		NetworkInfo.BorderStyle.Fg = color
		NetworkInfo.TitleStyle.Fg = color
	}

	if PowerChart != nil {
		PowerChart.TextStyle = ui.NewStyle(color)
		PowerChart.BorderStyle.Fg = color
		PowerChart.TitleStyle.Fg = color
	}

	if sparkline != nil {
		sparkline.LineColor = color
		sparkline.TitleStyle = ui.NewStyle(color)
	}

	if sparklineGroup != nil {
		sparklineGroup.BorderStyle.Fg = color
		sparklineGroup.TitleStyle.Fg = color
	}

	if gpuSparkline != nil {
		gpuSparkline.LineColor = color
		gpuSparkline.TitleStyle = ui.NewStyle(color)
	}

	if gpuSparklineGroup != nil {
		gpuSparklineGroup.BorderStyle.Fg = color
		gpuSparklineGroup.TitleStyle.Fg = color
	}

	if cpuCoreWidget != nil {
		cpuCoreWidget.BorderStyle.Fg = color
		cpuCoreWidget.TitleStyle.Fg = color
	}

	if modelText != nil {
		modelText.BorderStyle.Fg = color
		modelText.TitleStyle.Fg = color
		modelText.TextStyle = ui.NewStyle(color)
	}

	if helpText != nil {
		helpText.BorderStyle.Fg = color
		helpText.TitleStyle.Fg = color
		helpText.TextStyle = ui.NewStyle(color)
	}
}

func GetThemeColor(colorName string) ui.Color {
	color, ok := colorMap[colorName]
	if !ok {
		return ui.ColorGreen
	}
	return color
}

func cycleTheme() {
	currentIndex := 0
	for i, name := range colorNames {
		if name == currentConfig.Theme {
			currentIndex = i
			break
		}
	}
	nextIndex := (currentIndex + 1) % len(colorNames)
	applyTheme(colorNames[nextIndex])
}
