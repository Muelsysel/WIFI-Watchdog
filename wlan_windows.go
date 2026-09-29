//go:build windows

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

const (
	wlanClientVersionLonghorn       = 2
	wlanIntfOpcodeCurrentConnection = 7
	wlanInterfaceStateConnected     = 1
	wlanConnectionModeProfile       = 0
	dot11BSSTypeInfrastructure      = 1
	wlanMaxNameLength               = 256
)

type wlanInterfaceInfo struct {
	InterfaceGuid guid
	Description   [wlanMaxNameLength]uint16
	State         uint32
}

type dot11SSID struct {
	Length uint32
	SSID   [32]byte
}

type wlanAssociationAttributes struct {
	SSID          dot11SSID
	BSSType       uint32
	BSSID         [6]byte
	PhyType       uint32
	PhyIndex      uint32
	SignalQuality uint32
	RxRate        uint32
	TxRate        uint32
}

type wlanSecurityAttributes struct {
	SecurityEnabled int32
	OneXEnabled     int32
	AuthAlgorithm   uint32
	CipherAlgorithm uint32
}

type wlanConnectionAttributes struct {
	State       uint32
	Mode        uint32
	ProfileName [wlanMaxNameLength]uint16
	Association wlanAssociationAttributes
	Security    wlanSecurityAttributes
}

type wlanConnectionParameters struct {
	Mode             uint32
	Profile          *uint16
	Dot11SSID        uintptr
	DesiredBSSIDList uintptr
	BSSType          uint32
	Flags            uint32
}

type wlanProfileInfo struct {
	ProfileName [wlanMaxNameLength]uint16
	Flags       uint32
}

var (
	wlanapi                = syscall.NewLazyDLL("wlanapi.dll")
	procWlanOpenHandle     = wlanapi.NewProc("WlanOpenHandle")
	procWlanCloseHandle    = wlanapi.NewProc("WlanCloseHandle")
	procWlanEnumInterfaces = wlanapi.NewProc("WlanEnumInterfaces")
	procWlanQueryInterface = wlanapi.NewProc("WlanQueryInterface")
	procWlanConnect        = wlanapi.NewProc("WlanConnect")
	procWlanDisconnect     = wlanapi.NewProc("WlanDisconnect")
	procWlanScan           = wlanapi.NewProc("WlanScan")
	procWlanGetProfileList = wlanapi.NewProc("WlanGetProfileList")
	procWlanFreeMemory     = wlanapi.NewProc("WlanFreeMemory")
)

type nativeWlanClient struct {
	handle uintptr
}

func openNativeWlan() (*nativeWlanClient, error) {
	var negotiated uint32
	var handle uintptr
	r, _, _ := procWlanOpenHandle.Call(
		uintptr(wlanClientVersionLonghorn),
		0,
		uintptr(unsafe.Pointer(&negotiated)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if r != 0 {
		return nil, fmt.Errorf("WlanOpenHandle error=%d", r)
	}
	return &nativeWlanClient{handle: handle}, nil
}

func (c *nativeWlanClient) close() {
	if c != nil && c.handle != 0 {
		procWlanCloseHandle.Call(c.handle, 0)
		c.handle = 0
	}
}

func utf16FixedToString(v []uint16) string {
	n := 0
	for n < len(v) && v[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(v[:n])
}

func dot11SSIDToString(s dot11SSID) string {
	n := int(s.Length)
	if n < 0 {
		n = 0
	}
	if n > len(s.SSID) {
		n = len(s.SSID)
	}
	return string(s.SSID[:n])
}

func guidEqual(a, b guid) bool {
	return a.Data1 == b.Data1 && a.Data2 == b.Data2 && a.Data3 == b.Data3 && a.Data4 == b.Data4
}

func guidIsZero(g guid) bool {
	return g == (guid{})
}

func guidString(g guid) string {
	return fmt.Sprintf("{%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3], g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

func parseGUIDString(s string) (guid, bool) {
	var g guid
	s = strings.TrimSpace(strings.Trim(s, "{}"))
	var d4 [8]uint8
	n, err := fmt.Sscanf(s, "%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		&g.Data1, &g.Data2, &g.Data3,
		&d4[0], &d4[1], &d4[2], &d4[3], &d4[4], &d4[5], &d4[6], &d4[7])
	if err != nil || n != 11 {
		return guid{}, false
	}
	g.Data4 = d4
	return g, true
}

func (c *nativeWlanClient) enumInterfaces() ([]wlanInterfaceInfo, error) {
	var listPtr unsafe.Pointer
	r, _, _ := procWlanEnumInterfaces.Call(c.handle, 0, uintptr(unsafe.Pointer(&listPtr)))
	if r != 0 {
		return nil, fmt.Errorf("WlanEnumInterfaces error=%d", r)
	}
	if listPtr == nil {
		return nil, nil
	}
	defer procWlanFreeMemory.Call(uintptr(listPtr))

	count := *(*uint32)(listPtr)
	base := unsafe.Add(listPtr, 8)
	size := unsafe.Sizeof(wlanInterfaceInfo{})
	out := make([]wlanInterfaceInfo, 0, count)
	for i := uint32(0); i < count; i++ {
		p := unsafe.Add(base, uintptr(i)*size)
		out = append(out, *(*wlanInterfaceInfo)(p))
	}
	return out, nil
}

func (c *nativeWlanClient) currentConnection(iface guid) (wlanConnectionAttributes, error) {
	var dataSize uint32
	var dataPtr unsafe.Pointer
	var opType uint32
	r, _, _ := procWlanQueryInterface.Call(
		c.handle,
		uintptr(unsafe.Pointer(&iface)),
		uintptr(wlanIntfOpcodeCurrentConnection),
		0,
		uintptr(unsafe.Pointer(&dataSize)),
		uintptr(unsafe.Pointer(&dataPtr)),
		uintptr(unsafe.Pointer(&opType)),
	)
	if r != 0 {
		return wlanConnectionAttributes{}, fmt.Errorf("WlanQueryInterface(current_connection) error=%d", r)
	}
	if dataPtr == nil || dataSize < uint32(unsafe.Sizeof(wlanConnectionAttributes{})) {
		if dataPtr != nil {
			procWlanFreeMemory.Call(uintptr(dataPtr))
		}
		return wlanConnectionAttributes{}, fmt.Errorf("WlanQueryInterface returned short buffer: %d", dataSize)
	}
	defer procWlanFreeMemory.Call(uintptr(dataPtr))
	return *(*wlanConnectionAttributes)(dataPtr), nil
}

func (c *nativeWlanClient) profileExists(iface guid, profile string) bool {
	if strings.TrimSpace(profile) == "" {
		return false
	}
	var listPtr unsafe.Pointer
	r, _, _ := procWlanGetProfileList.Call(c.handle, uintptr(unsafe.Pointer(&iface)), 0, uintptr(unsafe.Pointer(&listPtr)))
	if r != 0 || listPtr == nil {
		return false
	}
	defer procWlanFreeMemory.Call(uintptr(listPtr))
	count := *(*uint32)(listPtr)
	base := unsafe.Add(listPtr, 8)
	size := unsafe.Sizeof(wlanProfileInfo{})
	for i := uint32(0); i < count; i++ {
		p := (*wlanProfileInfo)(unsafe.Add(base, uintptr(i)*size))
		if utf16FixedToString(p.ProfileName[:]) == profile {
			return true
		}
	}
	return false
}

func (c *nativeWlanClient) connectProfile(iface guid, profile string) error {
	p, err := syscall.UTF16PtrFromString(profile)
	if err != nil {
		return err
	}
	params := wlanConnectionParameters{
		Mode:             wlanConnectionModeProfile,
		Profile:          p,
		Dot11SSID:        0,
		DesiredBSSIDList: 0,
		BSSType:          dot11BSSTypeInfrastructure,
		Flags:            0,
	}
	r, _, _ := procWlanConnect.Call(c.handle, uintptr(unsafe.Pointer(&iface)), uintptr(unsafe.Pointer(&params)), 0)
	if r != 0 {
		return fmt.Errorf("WlanConnect error=%d", r)
	}
	return nil
}

func (c *nativeWlanClient) disconnect(iface guid) error {
	r, _, _ := procWlanDisconnect.Call(c.handle, uintptr(unsafe.Pointer(&iface)), 0)
	if r != 0 {
		return fmt.Errorf("WlanDisconnect error=%d", r)
	}
	return nil
}

func (c *nativeWlanClient) scan(iface guid) error {
	r, _, _ := procWlanScan.Call(c.handle, uintptr(unsafe.Pointer(&iface)), 0, 0, 0)
	if r != 0 {
		return fmt.Errorf("WlanScan error=%d", r)
	}
	return nil
}

func detectWifiNative() (wifiInfo, error) {
	c, err := openNativeWlan()
	if err != nil {
		return wifiInfo{}, err
	}
	defer c.close()
	ifaces, err := c.enumInterfaces()
	if err != nil {
		return wifiInfo{}, err
	}
	if len(ifaces) == 0 {
		return wifiInfo{}, fmt.Errorf("no enabled WLAN interfaces")
	}

	// Prefer connected interface, otherwise return first enabled interface as disconnected.
	var fallback wlanInterfaceInfo
	fallback = ifaces[0]
	for _, iface := range ifaces {
		if iface.State != wlanInterfaceStateConnected {
			continue
		}
		partial := wifiInfo{
			Connected:            true,
			InterfaceGUID:        guidString(iface.InterfaceGuid),
			InterfaceDescription: utf16FixedToString(iface.Description[:]),
		}
		conn, err := c.currentConnection(iface.InterfaceGuid)
		if err != nil {
			// Windows 11 can restrict SSID/BSSID details when location privacy
			// rules apply. Keep the reliable interface connected-state and let
			// the netsh compatibility layer fill any details it can expose.
			return partial, nil
		}
		partial.ProfileName = utf16FixedToString(conn.ProfileName[:])
		partial.SSID = dot11SSIDToString(conn.Association.SSID)
		partial.SignalQuality = int(conn.Association.SignalQuality)
		return partial, nil
	}
	return wifiInfo{
		Connected:            false,
		InterfaceGUID:        guidString(fallback.InterfaceGuid),
		InterfaceDescription: utf16FixedToString(fallback.Description[:]),
	}, nil
}

func nativeConnectProfile(interfaceGUID, profile string) error {
	c, err := openNativeWlan()
	if err != nil {
		return err
	}
	defer c.close()
	ifaces, err := c.enumInterfaces()
	if err != nil {
		return err
	}
	var preferred guid
	hasPreferred := false
	if g, ok := parseGUIDString(interfaceGUID); ok {
		preferred = g
		hasPreferred = true
	}

	// 1) preferred interface if it still exists and contains the profile.
	if hasPreferred {
		for _, iface := range ifaces {
			if guidEqual(iface.InterfaceGuid, preferred) && c.profileExists(iface.InterfaceGuid, profile) {
				_ = c.scan(iface.InterfaceGuid)
				return c.connectProfile(iface.InterfaceGuid, profile)
			}
		}
	}
	// 2) any enabled WLAN interface with that profile.
	for _, iface := range ifaces {
		if c.profileExists(iface.InterfaceGuid, profile) {
			_ = c.scan(iface.InterfaceGuid)
			return c.connectProfile(iface.InterfaceGuid, profile)
		}
	}
	return fmt.Errorf("profile %q not found on an enabled WLAN interface", profile)
}

func nativeDisconnect(interfaceGUID string) error {
	c, err := openNativeWlan()
	if err != nil {
		return err
	}
	defer c.close()
	g, ok := parseGUIDString(interfaceGUID)
	if !ok {
		return fmt.Errorf("invalid interface GUID")
	}
	return c.disconnect(g)
}

func nativeScan(interfaceGUID string) error {
	c, err := openNativeWlan()
	if err != nil {
		return err
	}
	defer c.close()
	g, ok := parseGUIDString(interfaceGUID)
	if !ok {
		return fmt.Errorf("invalid interface GUID")
	}
	return c.scan(g)
}
