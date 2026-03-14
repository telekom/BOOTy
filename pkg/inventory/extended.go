package inventory

// ExtendedInventory adds operational data to the base HardwareInventory.
type ExtendedInventory struct {
	Base               HardwareInventory `json:"base"`
	GPUs               []GPUInfo         `json:"gpus,omitempty"`
	StorageControllers []StorageCtrlInfo `json:"storageControllers,omitempty"`
	Thermal            *ThermalInfo      `json:"thermal,omitempty"`
	PowerSupplies      []PSUInfo         `json:"powerSupplies,omitempty"`
	USBDevices         []USBDeviceInfo   `json:"usbDevices,omitempty"`
	PCITopology        []PCIBridgeInfo   `json:"pciTopology,omitempty"`
	Transceivers       []TransceiverInfo `json:"transceivers,omitempty"`
	Chassis            *ChassisInfo      `json:"chassis,omitempty"`
}

// GPUInfo captures GPU/accelerator details.
type GPUInfo struct {
	Name          string `json:"name"`
	Vendor        string `json:"vendor"`
	PCIAddr       string `json:"pciAddr"`
	VRAM          uint64 `json:"vram"`
	Driver        string `json:"driver"`
	DriverVersion string `json:"driverVersion"`
	Architecture  string `json:"architecture"`
	NUMANode      int    `json:"numaNode"`
	SRIOVCapable  bool   `json:"sriovCapable"`
}

// StorageCtrlInfo captures RAID/HBA controller details.
type StorageCtrlInfo struct {
	Name       string `json:"name"`
	Vendor     string `json:"vendor"`
	Model      string `json:"model"`
	PCIAddr    string `json:"pciAddr"`
	FWVersion  string `json:"fwVersion"`
	Driver     string `json:"driver"`
	RAIDLevels string `json:"raidLevels"`
	Ports      int    `json:"ports"`
	CacheSize  uint64 `json:"cacheSize"`
	BBU        bool   `json:"bbu"`
}

// ThermalInfo captures temperature sensor data.
type ThermalInfo struct {
	CPUTemps    []SensorReading `json:"cpuTemps,omitempty"`
	InletTemp   *SensorReading  `json:"inletTemp,omitempty"`
	ExhaustTemp *SensorReading  `json:"exhaustTemp,omitempty"`
	Fans        []FanInfo       `json:"fans,omitempty"`
}

// SensorReading is a single temperature sensor reading.
type SensorReading struct {
	Name     string  `json:"name"`
	TempC    float64 `json:"tempC"`
	Warning  float64 `json:"warningC,omitempty"`
	Critical float64 `json:"criticalC,omitempty"`
}

// FanInfo captures fan status.
type FanInfo struct {
	Name   string `json:"name"`
	RPM    int    `json:"rpm"`
	Status string `json:"status"`
}

// PSUInfo captures power supply details.
type PSUInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Watts  int    `json:"watts"`
	Model  string `json:"model"`
	Serial string `json:"serial"`
}

// USBDeviceInfo captures USB device details.
type USBDeviceInfo struct {
	Bus       int    `json:"bus"`
	Device    int    `json:"device"`
	VendorID  string `json:"vendorId"`
	ProductID string `json:"productId"`
	Name      string `json:"name"`
	Class     string `json:"class"`
}

// PCIBridgeInfo captures PCI topology for NUMA affinity.
type PCIBridgeInfo struct {
	Bus      string          `json:"bus"`
	NUMANode int             `json:"numaNode"`
	Children []PCIDeviceInfo `json:"children"`
}

// PCIDeviceInfo captures a PCI device in the topology.
type PCIDeviceInfo struct {
	Addr     string `json:"addr"`
	Vendor   string `json:"vendor"`
	Device   string `json:"device"`
	Class    string `json:"class"`
	NUMANode int    `json:"numaNode"`
}

// TransceiverInfo captures SFP/QSFP module data.
type TransceiverInfo struct {
	Interface  string  `json:"interface"`
	Type       string  `json:"type"`
	Vendor     string  `json:"vendor"`
	PartNumber string  `json:"partNumber"`
	Serial     string  `json:"serial"`
	TempC      float64 `json:"tempC"`
	PowerDBm   float64 `json:"powerDbm"`
}

// ChassisInfo captures physical enclosure information.
type ChassisInfo struct {
	Manufacturer string `json:"manufacturer"`
	Type         string `json:"type"`
	SerialNumber string `json:"serialNumber"`
	AssetTag     string `json:"assetTag"`
	Height       int    `json:"height"`
}
