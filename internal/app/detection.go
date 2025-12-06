package app

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

func detectLightMode() bool {
	if isLight, err := checkTerminalColorOSC11(); err == nil {
		return isLight
	}

	if isLight, err := checkCOLORFGBG(); err == nil {
		return isLight
	}

	if isLight, err := checkSystemTheme(); err == nil {
		return isLight
	}

	return false
}

func checkTerminalColorOSC11() (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf("stdin is not a terminal")
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return false, err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	query := "\033]11;?\007"
	if _, err := os.Stdout.Write([]byte(query)); err != nil {
		return false, err
	}

	responseChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		reader := bufio.NewReader(os.Stdin)
		var response []byte
		for {
			b, err := reader.ReadByte()
			if err != nil {
				errChan <- err
				return
			}
			response = append(response, b)
			if b == 0x07 {
				break
			}
			if len(response) >= 2 && response[len(response)-2] == 0x1b && response[len(response)-1] == 0x5c {
				break
			}
			if len(response) > 100 {
				break
			}
		}
		responseChan <- string(response)
	}()

	select {
	case resp := <-responseChan:
		return parseOSC11Response(resp)
	case <-errChan:
		return false, fmt.Errorf("error reading response")
	case <-time.After(100 * time.Millisecond):
		return false, fmt.Errorf("timeout waiting for OSC 11 response")
	}
}

func parseOSC11Response(resp string) (bool, error) {
	start := strings.Index(resp, "rgb:")
	if start == -1 {
		return false, fmt.Errorf("invalid response format")
	}

	colorStr := resp[start+4:]
	parts := strings.Split(colorStr, "/")
	if len(parts) < 3 {
		return false, fmt.Errorf("invalid color format")
	}

	r, err1 := strconv.ParseUint(cleanHex(parts[0]), 16, 16)
	g, err2 := strconv.ParseUint(cleanHex(parts[1]), 16, 16)
	b, err3 := strconv.ParseUint(cleanHex(parts[2]), 16, 16)

	if err1 != nil || err2 != nil || err3 != nil {
		return false, fmt.Errorf("error parsing hex")
	}

	luminance := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	maxLum := 65535.0

	if luminance > (maxLum * 0.5) {
		return true, nil
	}
	return false, nil
}

func cleanHex(s string) string {
	var sb strings.Builder
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			sb.WriteRune(c)
		} else {
			break
		}
	}
	return sb.String()
}

func checkCOLORFGBG() (bool, error) {
	colorFGBG := os.Getenv("COLORFGBG")
	if colorFGBG == "" {
		return false, fmt.Errorf("COLORFGBG not set")
	}

	parts := strings.Split(colorFGBG, ";")
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid COLORFGBG format")
	}

	bgStr := parts[1]
	bg, err := strconv.Atoi(bgStr)
	if err != nil {
		return false, err
	}

	if bg == 7 || bg == 15 || bg == 11 || bg == 14 || bg == 231 || bg == 255 {
		return true, nil
	}

	return false, nil
}

func checkSystemTheme() (bool, error) {
	cmd := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle")
	out, err := cmd.Output()

	if err != nil {
		return true, nil
	}

	output := strings.TrimSpace(string(out))
	if output == "Dark" {
		return false, nil
	}

	return true, nil
}
