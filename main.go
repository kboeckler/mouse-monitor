package main

import "log"
import "strings"
import "strconv"
import "fmt"
import "time"
import "os/exec"
import "os"
import "path/filepath"
import "fyne.io/systray"

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
	getBatteryLevelScript string
	sendNotificationScript string
	sendCriticalNotificationScript string
)


func main() {
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
	getBatteryLevelScript = fmt.Sprintf("%s/scripts/%s", assetsDir, "get_rivalcfg_batterylevel.sh")
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
				resetAlreadyNotified()
				updateBatteryLevel()
			}
		}
	}()
	resetAlreadyNotified()
	go updateBatteryLevel()
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

func resetAlreadyNotified() {
	alreadyNotified[NOTIFY_LOW] = false
	alreadyNotified[NOTIFY_CRIT] = false
	alreadyNotified[NOTIFY_FULL] = false
}

func updateBatteryLevel() {
	batteryLevelRaw, err := readBatterylevel()
	if err != nil {
		log.Printf("Error reading battery output: %v\n", err)
		return
	}
	batteryLevel, err := parseBatteryOutput(batteryLevelRaw)
	if err != nil {
		log.Printf("Error converting battery output: %v\n", err)
		return
	}
	handleNewBatteryLevel(batteryLevel)
}

func handleNewBatteryLevel(batteryLevel batteryLevel) {
	if (batteryLevel.charging) {
		if (batteryLevel.level == 100) {
			if !alreadyNotified[NOTIFY_FULL] {
				alreadyNotified[NOTIFY_FULL] = true
				sendNotification("Battery fully charged", "The Battery Level is at 100%. You can unplug the mouse.", icons["charging-full"].path)
			}
			systray.SetIcon(icons["charging-full"].data)
		} else if (batteryLevel.level >= 80) {
			systray.SetIcon(icons["charging-full"].data)
		} else if (batteryLevel.level >= 40) {
			systray.SetIcon(icons["charging-34"].data)
		} else if (batteryLevel.level >= 20) {
			systray.SetIcon(icons["charging-12"].data)
		} else {
			systray.SetIcon(icons["charging-14"].data)
		}
	} else {
		if (batteryLevel.level <= 5) {
			if !alreadyNotified[NOTIFY_CRIT] {
				alreadyNotified[NOTIFY_CRIT] = true
				sendCriticalNotification(fmt.Sprintf("Battery Level critical (%d%%)", batteryLevel.level), fmt.Sprintf("The Battery Level is decreasing (currently at %d%%). Please connect it soon.", batteryLevel.level), icons["14"].path)
			}
			systray.SetIcon(icons["14"].data)
		} else if (batteryLevel.level <= 10) {
			if !alreadyNotified[NOTIFY_LOW] {
				alreadyNotified[NOTIFY_LOW] = true
				sendNotification(fmt.Sprintf("Battery Level low (%d%%)", batteryLevel.level), fmt.Sprintf("The Battery Level is decreasing (currently at %d%%). Please connect it soon.", batteryLevel.level), icons["14"].path)
			}
			systray.SetIcon(icons["14"].data)
		} else if (batteryLevel.level <= 30) {
			systray.SetIcon(icons["12"].data)
		} else if (batteryLevel.level <= 70) {
			systray.SetIcon(icons["34"].data)
		} else {
			systray.SetIcon(icons["full"].data)
		}

	}
	chargingStr := ""
	if batteryLevel.charging {
		chargingStr = " (Charging)"
	}
	infoBtn.SetTitle(fmt.Sprintf("%s | Current level: %d%s", APP_NAME, batteryLevel.level, chargingStr))
}

type batteryLevel struct {
	charging bool
	level int
}

func parseBatteryOutput(output string) (batteryLevel, error) {
	// Discharging [========  ] 85 %
	// Charging    [=         ] 5 %
	// Something with an error text
	bat := batteryLevel{}
	if !strings.EqualFold(output, strings.ReplaceAll(output, "Discharging", "")) {
		bat.charging = false
	} else if !strings.EqualFold(output, strings.ReplaceAll(output, "Charging", "")) {
		bat.charging = true
	} else {
		return bat, fmt.Errorf("Invalid battery string: %s\n", output)
	}
	split := strings.Split(output, "]")
	if len(split) == 1 {
		return bat, fmt.Errorf("Invalid battery string: %s\n", output)
	}
	split2 := strings.Split(split[1], "%")
	if len(split2) == 1 {
		return bat, fmt.Errorf("Invalid battery string: %s\n", output)
	}
	levelStr := strings.TrimSpace(split2[0])
	levelInt, err := strconv.Atoi(levelStr)
	if err != nil {
		return bat, fmt.Errorf("Invalid battery string: %s\n", output)
	}
	bat.level = levelInt
	return bat, nil
}

func readBatterylevel() (string, error) {
	cmd := exec.Command("/bin/sh", getBatteryLevelScript)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("Error executing bash to read battery level: %s\n", err)
	}
	return string(output), nil
}

func sendNotification(title string, body string, icon string) {
	cmd := exec.Command("/bin/sh", sendNotificationScript, title, body, icon)
	_, err := cmd.Output()
	if err != nil {
		log.Printf("Error executing bash to send a notification: %v\n", err)
	}
}

func sendCriticalNotification(title string, body string, icon string) {
	cmd := exec.Command("/bin/sh", sendNotificationScript, title, body, icon)
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

