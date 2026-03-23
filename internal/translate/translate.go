package translate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SysInfo struct {
	Alias      string `json:"alias"`
	Model      string `json:"model"`
	Mac        string `json:"mac"`
	DevType    string `json:"type"`
	MicType    string `json:"mic_type"`
	RelayState int    `json:"relay_state"`
}

type Response struct {
	System struct {
		SysInfo SysInfo `json:"get_sysinfo"`
	} `json:"system"`
}

func XOREncrypt(plaintext string) []byte {
	key := byte(171)
	encrypted := make([]byte, len(plaintext))
	for i := 0; i < len(plaintext); i++ {
		encrypted[i] = plaintext[i] ^ key
		key = encrypted[i]
	}
	return encrypted
}

func XORDecrypt(data []byte) string {
	key := byte(171)
	result := make([]byte, len(data))
	for i := 0; i < len(data); i++ {
		result[i] = data[i] ^ key
		key = data[i]
	}
	return string(result)
}

func EncryptWithHeader(plaintext string) []byte {
	payload := XOREncrypt(plaintext)
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.BigEndian, uint32(len(payload)))
	buf.Write(payload)
	return buf.Bytes()
}

func DiscoverDevices(subnet string, timeout time.Duration) ([]SysInfo, error) {
	parts := strings.Split(subnet, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("subnet must be like 192.168.88")
	}

	base := strings.Join(parts, ".") + "."

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(timeout))

	cmd := `{"system":{"get_sysinfo":null}}`
	probe := XOREncrypt(cmd)

	dest := &net.UDPAddr{IP: net.IPv4bcast, Port: 9999}

	var devices []SysInfo
	var mu sync.Mutex
	found := make(map[string]bool)
	done := make(chan bool)

	go func() {
		buf := make([]byte, 4096)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-done:
					return
				default:
					continue
				}
			}

			payload := XORDecrypt(buf[:n])
			var resp Response
			if err := json.Unmarshal([]byte(payload), &resp); err == nil && resp.System.SysInfo.Mac != "" {
				ip := remoteAddr.IP.String()
				mu.Lock()
				if !found[ip] {
					found[ip] = true
					devices = append(devices, resp.System.SysInfo)
				}
				mu.Unlock()
			}
		}
	}()

	for i := 1; i <= 254; i++ {
		targetIP := base + strconv.Itoa(i)
		target := &net.UDPAddr{IP: net.ParseIP(targetIP), Port: 9999}
		_, _ = conn.WriteToUDP(probe, target)
	}

	_, _ = conn.WriteToUDP(probe, dest)

	time.Sleep(timeout)
	close(done)

	return devices, nil
}

func GetDeviceInfo(ip string) (*SysInfo, error) {
	cmd := `{"system":{"get_sysinfo":null}}`
	payload := EncryptWithHeader(cmd)

	conn, err := net.DialTimeout("tcp", ip+":9999", 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header)
	if length > 16384 {
		return nil, fmt.Errorf("response too large: %d", length)
	}

	dataBuf := make([]byte, length)
	if _, err := io.ReadFull(conn, dataBuf); err != nil {
		return nil, err
	}

	data := XORDecrypt(dataBuf)
	var resp Response
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, err
	}

	return &resp.System.SysInfo, nil
}

func SetPower(ip string, state int) error {
	cmd := fmt.Sprintf(`{"system":{"set_relay_state":{"state":%d}}}`, state)
	payload := EncryptWithHeader(cmd)

	conn, err := net.DialTimeout("tcp", ip+":9999", 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(payload)
	return err
}

func MakeDeviceID(mac string) string {
	cleaned := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", ""))
	if cleaned == "" {
		cleaned = "unknown"
	}
	return "kasa-" + cleaned
}
