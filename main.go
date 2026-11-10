package main

import "errors"
import "flag"
import "log"
import "fmt"
import "time"
import "os/exec"
import "os"
import "path/filepath"
import "sync"
import "fyne.io/systray"
import "kevinboeckler.com/mouse-monitor/battery"

const(
	ASSETS_DIR_ENV_NAME = "ASSETS_DIR"
	APP_NAME = "Mouse Monitor"
	NOTIFY_LOW = iota
	NOTIFY_CRIT
	NOTIFY_FULL
)

var (
	assetsDir string
	infoBtn *systray.MenuItem
	refreshBtn *systray.MenuItem
	icons map[string]icon
	alreadyNotified = make(map[int]bool)
	// updateMu serializes battery updates (ticker, refresh button, startup)
	// and guards alreadyNotified.
	updateMu sync.Mutex
	// lastStatus/lastSeen remember the last good reading for the asleep text.
	lastStatus *battery.Status
	lastSeen time.Time
	sendNotificationScript string
	sendCriticalNotificationScript string
)


func main() {
	debugBattery := flag.Bool("debug-battery", false, "query the mouse once, print raw and decoded battery replies, and exit")
	flag.Parse()
	if *debugBattery {
		os.Exit(runDebugBattery())
	}
	loadAssetsDir()
	readIcons()
	setScripts()
	systray.Run(onReady, onExit)
}

func loadAssetsDir() {
	assetsDir = "assets"
	val, present := os.LookupEnv(ASSETS_DIR_ENV_NAME)
	if present {
		assetsDir = filepath.Clean(val)
	}
}

func setScripts() {
	sendNotificationScript = fmt.Sprintf("%s/scripts/%s", assetsDir, "send_notification.sh")
	sendCriticalNotificationScript = fmt.Sprintf("%s/scripts/%s", assetsDir, "send_critical_notification.sh")
}

func onReady() {
	systray.SetIcon(icons["base"].data)
	systray.SetTitle(APP_NAME)
	systray.SetTooltip(APP_NAME)
	infoBtn = systray.AddMenuItem(APP_NAME, APP_NAME)
	infoBtn.SetIcon(icons["base"].data)
	infoBtn.Disable()
	refreshBtn = systray.AddMenuItem("Refresh", "Refreshes the watcher")
	mQuit := systray.AddMenuItem("Quit", "Quit the watcher instance")
	go func() {
		for {
			select {
			case <- mQuit.ClickedCh:
				systray.Quit()
				return
			case <- refreshBtn.ClickedCh:
				refresh()
			}
		}
	}()
	go refresh()
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		for {
			_ = <- ticker.C
			updateBatteryLevel()
		}
	}()
}

func onExit() {
	// clean up here
}

// refresh resets the notification state and reads the battery level again.
func refresh() {
	updateMu.Lock()
	defer updateMu.Unlock()
	resetAlreadyNotified()
	doUpdateBatteryLevel()
}

// resetAlreadyNotified must be called with updateMu held.
func resetAlreadyNotified() {
	alreadyNotified[NOTIFY_LOW] = false
	alreadyNotified[NOTIFY_CRIT] = false
	alreadyNotified[NOTIFY_FULL] = false
}

func updateBatteryLevel() {
	updateMu.Lock()
	defer updateMu.Unlock()
	doUpdateBatteryLevel()
}

// doUpdateBatteryLevel must be called with updateMu held.
func doUpdateBatteryLevel() {
	status, err := battery.Read(battery.DefaultTimeout)
	switch {
	case errors.Is(err, battery.ErrAsleep):
		showUnavailable("Asleep or out of range")
	case errors.Is(err, battery.ErrNotFound):
		showUnavailable("Not connected")
	case err != nil:
		log.Printf("Error reading battery level: %v\n", err)
	default:
		lastStatus = &status
		lastSeen = time.Now()
		handleNewBatteryLevel(status)
	}
}

// showUnavailable shows the neutral icon while no reading is possible, with
// the last known level so the menu still tells how full the battery was.
func showUnavailable(reason string) {
	text := fmt.Sprintf("%s | %s", APP_NAME, reason)
	if lastStatus != nil {
		text += fmt.Sprintf(" (last: %d%% at %s)", lastStatus.Level, lastSeen.Format("15:04"))
	}
	systray.SetIcon(icons["base"].data)
	systray.SetTooltip(text)
	infoBtn.SetTitle(text)
}

func handleNewBatteryLevel(batteryLevel battery.Status) {
	// Re-arm the alerts of the opposite state so they fire again on every
	// charge/discharge cycle, not only once per app run.
	if batteryLevel.Charging {
		alreadyNotified[NOTIFY_LOW] = false
		alreadyNotified[NOTIFY_CRIT] = false
	} else {
		alreadyNotified[NOTIFY_FULL] = false
	}
	if (batteryLevel.Charging) {
		if (batteryLevel.Level == 100) {
			if !alreadyNotified[NOTIFY_FULL] {
				alreadyNotified[NOTIFY_FULL] = true
				sendNotification("Battery fully charged", "The Battery Level is at 100%. You can unplug the mouse.", icons["charging-full"].path)
			}
			systray.SetIcon(icons["charging-full"].data)
		} else if (batteryLevel.Level >= 80) {
			systray.SetIcon(icons["charging-full"].data)
		} else if (batteryLevel.Level >= 40) {
			systray.SetIcon(icons["charging-34"].data)
		} else if (batteryLevel.Level >= 20) {
			systray.SetIcon(icons["charging-12"].data)
		} else {
			systray.SetIcon(icons["charging-14"].data)
		}
	} else {
		if (batteryLevel.Level <= 5) {
			if !alreadyNotified[NOTIFY_CRIT] {
				alreadyNotified[NOTIFY_CRIT] = true
				sendCriticalNotification(fmt.Sprintf("Battery Level critical (%d%%)", batteryLevel.Level), fmt.Sprintf("The Battery Level is decreasing (currently at %d%%). Please connect it soon.", batteryLevel.Level), icons["14"].path)
			}
			systray.SetIcon(icons["14"].data)
		} else if (batteryLevel.Level <= 10) {
			if !alreadyNotified[NOTIFY_LOW] {
				alreadyNotified[NOTIFY_LOW] = true
				sendNotification(fmt.Sprintf("Battery Level low (%d%%)", batteryLevel.Level), fmt.Sprintf("The Battery Level is decreasing (currently at %d%%). Please connect it soon.", batteryLevel.Level), icons["14"].path)
			}
			systray.SetIcon(icons["14"].data)
		} else if (batteryLevel.Level <= 30) {
			systray.SetIcon(icons["12"].data)
		} else if (batteryLevel.Level <= 70) {
			systray.SetIcon(icons["34"].data)
		} else {
			systray.SetIcon(icons["full"].data)
		}

	}
	chargingStr := ""
	if batteryLevel.Charging {
		chargingStr = " (Charging)"
	}
	text := fmt.Sprintf("%s | Current level: %d%s", APP_NAME, batteryLevel.Level, chargingStr)
	systray.SetTooltip(text)
	infoBtn.SetTitle(text)
}

func sendNotification(title string, body string, icon string) {
	cmd := exec.Command("/bin/bash", sendNotificationScript, title, body, icon)
	_, err := cmd.Output()
	if err != nil {
		log.Printf("Error executing bash to send a notification: %v\n", err)
	}
}

func sendCriticalNotification(title string, body string, icon string) {
	cmd := exec.Command("/bin/bash", sendCriticalNotificationScript, title, body, icon)
	_, err := cmd.Output()
	if err != nil {
		log.Printf("Error executing bash to send a critical notification: %v\n", err)
	}
}

type icon struct {
	data []byte
	path string
}

func readIcons() {
	icons = make(map[string]icon)
	icons["base"] = icon{}
	icons["12"] = icon{}
	icons["14"] = icon{}
	icons["34"] = icon{}
	icons["full"] = icon{}
	icons["charging-12"] = icon{}
	icons["charging-14"] = icon{}
	icons["charging-34"] = icon{}
	icons["charging-full"] = icon{}
	for key := range icons {
		path := fmt.Sprintf("%s/icons/mouse-monitor-%s.png", assetsDir, key)
		icons[key] = icon{data: readFile(path), path: path}
	}
}

func readFile(file string) []byte {
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatal("Error reading file", err)
	}
	return data
}

// runDebugBattery queries every matching device once and prints what it
// answered. It replaces the former cmd/probe tool.
func runDebugBattery() int {
	devs, err := battery.FindDevices(battery.SysfsRoot)
	if err != nil {
		fmt.Printf("Scanning %s failed: %v\n", battery.SysfsRoot, err)
		return 1
	}
	if len(devs) == 0 {
		fmt.Println("No Aerox 5 Wireless found (dongle or cable, USB interface 3).")
		return 1
	}
	code := 0
	for _, d := range devs {
		status, raw, err := battery.Query(d, battery.DefaultTimeout)
		fmt.Printf("%s  1038:%04x  %s  command 0x%02x\n", d.Node, d.Product, d.Name, d.Command())
		if raw != nil {
			fmt.Printf("  raw:    % x ...\n", raw[:min(len(raw), 16)])
		}
		if err != nil {
			fmt.Printf("  result: %v\n", err)
			code = 1
			continue
		}
		state := "Discharging"
		if status.Charging {
			state = "Charging"
		}
		fmt.Printf("  result: %s %d %%\n", state, status.Level)
	}
	return code
}
