package app

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

var (
	BracketColor       ui.Color = ui.ColorWhite
	SecondaryTextColor ui.Color = 245
	IsLightMode        bool     = false
)

func applyTheme(colorName string, lightMode bool) {
	color, ok := colorMap[colorName]
	if !ok {
		color = ui.ColorGreen // Default
		colorName = "green"
	}

	currentConfig.Theme = colorName

	// Adjust for Light Mode
	if lightMode {
		BracketColor = ui.ColorBlack
		SecondaryTextColor = ui.ColorBlack // Or a dark gray like 235

		// In light mode, if the user picks "white", it will be invisible.
		// Force black if theme is white in light mode?
		if color == ui.ColorWhite {
			color = ui.ColorBlack
		}
	} else {
		BracketColor = ui.ColorWhite
		SecondaryTextColor = 245
	}

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
		cpuGauge.LabelStyle = ui.NewStyle(SecondaryTextColor)

		gpuGauge.BarColor = color
		gpuGauge.BorderStyle.Fg = color
		gpuGauge.TitleStyle.Fg = color
		gpuGauge.LabelStyle = ui.NewStyle(SecondaryTextColor)

		memoryGauge.BarColor = color
		memoryGauge.BorderStyle.Fg = color
		memoryGauge.TitleStyle.Fg = color
		memoryGauge.LabelStyle = ui.NewStyle(SecondaryTextColor)

		aneGauge.BarColor = color
		aneGauge.BorderStyle.Fg = color
		aneGauge.TitleStyle.Fg = color
		aneGauge.LabelStyle = ui.NewStyle(SecondaryTextColor)
	}

	if processList != nil {
		processList.TextStyle = ui.NewStyle(color)

		// Determine selected row foreground color
		selectedFg := ui.ColorBlack
		if lightMode && color == ui.ColorBlack {
			selectedFg = ui.ColorWhite
		}

		processList.SelectedRowStyle = ui.NewStyle(selectedFg, color)
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

func GetThemeColorWithLightMode(colorName string, lightMode bool) ui.Color {
	color := GetThemeColor(colorName)
	if lightMode && color == ui.ColorWhite {
		return ui.ColorBlack
	}
	return color
}

func GetProcessTextColor(isCurrentUser bool) string {
	if IsLightMode {
		if isCurrentUser {
			color := GetThemeColorWithLightMode(currentConfig.Theme, true)
			if color == ui.ColorBlack {
				return "black"
			}
			return currentConfig.Theme
		}
		return "black"
	}

	if isCurrentUser {
		return currentConfig.Theme
	}
	return "white"
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
	applyTheme(colorNames[nextIndex], IsLightMode)
}
