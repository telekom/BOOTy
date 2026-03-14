package inventory

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CollectThermal reads thermal sensor data from sysfs.
func CollectThermal() ThermalInfo {
	return collectThermalFrom("/sys/class/thermal", "/sys/class/hwmon")
}

func collectThermalFrom(thermalPath, hwmonPath string) ThermalInfo {
	info := ThermalInfo{}

	// Read thermal zones for CPU temps.
	zones, err := os.ReadDir(thermalPath)
	if err == nil {
		for _, zone := range zones {
			if !strings.HasPrefix(zone.Name(), "thermal_zone") {
				continue
			}
			zonePath := filepath.Join(thermalPath, zone.Name())
			reading := readThermalZone(zonePath)
			if reading.Name != "" {
				info.CPUTemps = append(info.CPUTemps, reading)
			}
		}
	}

	// Read hwmon for fan data.
	hwmons, err := os.ReadDir(hwmonPath)
	if err == nil {
		for _, hw := range hwmons {
			hwPath := filepath.Join(hwmonPath, hw.Name())
			fans := readFans(hwPath)
			info.Fans = append(info.Fans, fans...)
		}
	}

	return info
}

func readThermalZone(zonePath string) SensorReading {
	typeName := readSysfs(zonePath, "type")
	tempStr := readSysfs(zonePath, "temp")
	if tempStr == "" {
		return SensorReading{}
	}
	tempMilliC, err := strconv.ParseInt(tempStr, 10, 64)
	if err != nil {
		return SensorReading{}
	}
	return SensorReading{
		Name:  typeName,
		TempC: float64(tempMilliC) / 1000.0,
	}
}

func readFans(hwPath string) []FanInfo {
	var fans []FanInfo
	entries, err := os.ReadDir(hwPath)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_input") || !strings.HasPrefix(name, "fan") {
			continue
		}
		rpmStr := readSysfs(hwPath, name)
		rpm, err := strconv.Atoi(rpmStr)
		if err != nil {
			continue
		}
		status := "ok"
		if rpm == 0 {
			status = "failed"
		}
		fans = append(fans, FanInfo{
			Name:   strings.TrimSuffix(name, "_input"),
			RPM:    rpm,
			Status: status,
		})
	}
	return fans
}
