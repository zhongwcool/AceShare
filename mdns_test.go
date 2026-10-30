package main

import (
	"net"
	"testing"
)

func TestIsVirtualAdapterName(t *testing.T) {
	virtual := []string{
		"vEthernet (Default Switch)",
		"vEthernet (WSL (Hyper-V firewall))",
		"FlClash",
		"蓝牙网络连接",
	}
	for _, name := range virtual {
		if !isVirtualAdapterName(name) {
			t.Fatalf("应视为虚拟网卡：%s", name)
		}
	}
	physical := []string{"WLAN", "以太网", "Ethernet", "Wi-Fi"}
	for _, name := range physical {
		if isVirtualAdapterName(name) {
			t.Fatalf("应视为物理网卡：%s", name)
		}
	}
}

func TestShareableIPv4(t *testing.T) {
	if !shareableIPv4(net.ParseIP("192.168.3.9")) {
		t.Fatal("192.168.3.9 应可宣告")
	}
	if !shareableIPv4(net.ParseIP("172.31.64.1")) {
		t.Fatal("172.31.64.1 本身是私网地址，由网卡名排除，不按网段丢掉")
	}
	rejects := []string{"198.18.0.1", "198.19.0.1", "169.254.1.1", "127.0.0.1", "0.0.0.0"}
	for _, raw := range rejects {
		if shareableIPv4(net.ParseIP(raw)) {
			t.Fatalf("不应宣告：%s", raw)
		}
	}
}

func TestSelectLANInterfacesSkipsVirtualNics(t *testing.T) {
	selected := selectLANInterfaces()
	var ips []string
	for _, ifi := range selected {
		if isVirtualAdapterName(ifi.Name) {
			t.Fatalf("选中了虚拟网卡 %s", ifi.Name)
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			t.Fatal(err)
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || !shareableIPv4(ipnet.IP) {
				continue
			}
			ip := ipnet.IP.To4().String()
			ips = append(ips, ifi.Name+"="+ip)
			switch ip {
			case "172.31.64.1", "172.28.0.1", "198.18.0.1":
				t.Fatalf("选中了不可达地址 %s（%s）", ip, ifi.Name)
			}
		}
	}
	t.Logf("selected=%v", ips)
}
