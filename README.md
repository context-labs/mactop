# mactop

![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/context-labs/mactop/total) ![GitHub Release](https://img.shields.io/github/v/release/context-labs/mactop)

`mactop` is a terminal-based monitoring tool "top" designed to display real-time metrics for Apple Silicon chips written by Carsen Klock. It provides a simple and efficient way to monitor CPU and GPU usage, E-Cores and P-Cores, power consumption, GPU frequency, temperatures, and other system metrics directly from your terminal

![mactop](screenshotm.png)

## Compatibility

- Apple Silicon Only (ARM64)
- macOS Monterey 12.3+

## Features

- **No sudo required** - Uses native Apple APIs (IOReport, IOKit, IOHIDEventSystemClient)
- Apple Silicon Monitor Top written in Go Lang and CGO
- Real-time CPU, GPU, ANE, DRAM, and system power wattage usage display
- GPU frequency and usage percentage display
- CPU and GPU temperatures + Thermal State
- Detailed native metrics for CPU cores (E and P cores) via Apple's Mach Kernel API
- Memory usage and swap information
- Network usage information (upload/download speeds)
- Disk I/O activity (read/write speeds)
- Multiple volume display (shows Macintosh HD + mounted external volumes)
- Easy-to-read terminal UI
- **6 Layouts**: Default, Alternative, Vertical, Compact, Dashboard, and Gauges Only (L to cycle layouts)
- **Persistent Settings**: Remembers your Layout and Theme choice across restarts
- Customizable UI color (green, red, blue, cyan, magenta, yellow, and white) (C to cycle colors)
- Customizable update interval (default is 1000ms)
- Process list matching htop format (VIRT in GB, CPU normalized by core count)
- **Process Management**: Kill processes directly from the UI (F9). List pauses while selecting.
- **Headless Mode**: Output JSON metrics to stdout for scripting/logging (`--headless`)
- Party Mode (Randomly cycles through colors) (P to toggle)
- Optional Prometheus Metrics server (default is disabled)
- Support for all Apple Silicon models
- **Auto-detect Light/Dark Mode**: Automatically adjusts UI colors based on your terminal's background color or system theme.
- **Configurable Units**: Customize units for network, disk, and temperature display.

## Install via Homebrew

You can install [mactop](https://github.com/context-labs/mactop) via Homebrew! https://brew.sh

```bash
brew install mactop
```

```bash
mactop
```

## Updating via Homebrew

```bash
brew update
```

```bash
brew upgrade mactop
```

## Installation

To install `mactop`, follow these steps:

1. Ensure you have Go installed on your machine. If not, you can install it by following the instructions here: [Go Installation Guide](https://go.dev/doc/install).

2. Clone the repository:
   ```bash
   git clone https://github.com/context-labs/mactop.git
   cd mactop
   ```

3. Build the application:
   ```bash
   go build
   ```

4. Run the application:
   ```bash
   ./mactop
   ```

## Usage

After installation, you can start `mactop` by simply running:
```bash
./mactop
```

Example with flags:
```bash
mactop --interval 1000 --color green
```

## mactop Flags

- `--headless`: Run in headless mode (no TUI, output JSON to stdout).
- `--count`: Number of samples to collect in headless mode (0 = infinite).
- `--interval` or `-i`: Set the update interval in milliseconds. Default is 1000.
- `--color` or `-c`: Set the UI color. Default is white. 
Options are 'green', 'red', 'blue', 'cyan', 'magenta', 'yellow', and 'white'. (-c green)
- `--prometheus` or `-p`: Set and enable the local Prometheus metrics server on the given port. Default is disabled. (e.g. -p 2112 to enable Prometheus metrics on port 2112)
- `--unit-network`: Network unit: auto, byte, kb, mb, gb (default: auto)
- `--unit-disk`: Disk unit: auto, byte, kb, mb, gb (default: auto)
- `--unit-temp`: Temperature unit: celsius, fahrenheit (default: celsius)
- `--test` or `-t`: Test IOReport power metrics (no sudo required)
- `--version` or `-v`: Print the version of mactop.
- `--help` or `-h`: Show a help message about these flags and how to run mactop.

## mactop Commands
Use the following keys to interact with the application while its running:
- `q`: Quit the application.
- `r`: Refresh the UI data manually.
- `c`: Cycle through the color themes.
- `p`: Party Mode (Randomly cycles through colors)
- `l`: Cycle through the 6 available layouts.
- `+` or `=`: Increase update interval (slower updates).
- `-`: Decrease update interval (faster updates).
- `F9`: Kill the currently selected process (pauses updates while selecting).
- `Arrow Keys` or `h/j/k/l`: Navigate the process list and select columns.
- `Enter` or `Space`: Sort by the selected column.
- `h` or `?`: Toggle the help menu.

## Example Theme (Green) Screenshot (mactop -c green) on Advanced layout (Hit "l" key to toggle)

![mactop theme](screenshota.png)

## Confirmed tested working M series chips

- M1
- M1 Pro
- M1 Max
- M1 Ultra
- M2
- M2 Pro
- M2 Max
- M2 Ultra
- M3
- M3 Pro
- M3 Max
- M3 Ultra
- M4
- M4 Pro
- M4 Max
- M5

(If you have a confirmed working M series chip that is not listed, please open an issue, so we may add it here!)

## Contributing

Contributions are what make the open-source community such an amazing place to learn, inspire, and create. Any contributions you make are **greatly appreciated**.

1. Fork mactop
2. Create your Feature Branch (`git checkout -b feature/AmazingFeature`)
3. Commit your Changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the Branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

## What does mactop use to get real-time data?

- **Apple SMC**: For SoC temperature sensors and System Power (PSTR)
- **IOReport API**: For CPU, GPU, ANE, and DRAM power consumption (no sudo required)
- **IOKit**: For GPU frequency table from `pmgr` device
- **IOHIDEventSystemClient**: Fallback for SoC temperature sensors
- **NSProcessInfo.thermalState**: For system thermal state (Nominal/Fair/Serious/Critical)
- **Mach Kernel API** (`host_processor_info`): For CPU metrics (E and P cores) via CGO
- **gopsutil**: For memory, swap, network, and disk I/O metrics
- **ps**: For process list information
- `sysctl`: For CPU model information
- `system_profiler`: For GPU Core Count

## License

Distributed under the MIT License. See `LICENSE` for more information.

## Author and Contact

Carsen Klock - [@carsenklock](https://x.com/carsenklock)

Project Link: [https://github.com/context-labs/mactop](https://github.com/context-labs/mactop)

## Disclaimer

This tool is not officially supported by Apple. It is provided as is, and may not work as expected. Use at your own risk.

## Acknowledgements

- [termui](https://github.com/gizak/termui) for the terminal UI framework.
- [gopsutil](https://github.com/shirou/gopsutil) for system memory, network, and disk monitoring.
- [asitop](https://github.com/tlkh/asitop) for the original inspiration!
- [htop](https://github.com/htop-dev/htop) for the process list and CPU cores inspiration!