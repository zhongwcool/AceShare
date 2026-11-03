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

func TestSortLANIPv4sPrefersHomeLAN(t *testing.T) {
	ips := []string{"172.31.64.1", "10.0.0.2", "198.18.0.1", "192.168.3.9", "172.16.0.1"}
	sortLANIPv4s(ips)
	want := []string{"192.168.3.9", "10.0.0.2", "172.16.0.1", "172.31.64.1", "198.18.0.1"}
	for i := range want {
		if ips[i] != want[i] {
			t.Fatalf("排序结果 %v，期望 %v", ips, want)
		}
	}
	if got := preferredOpenHost(ips); got != "192.168.3.9" {
		t.Fatalf("应打开 %s，实际 %s", "192.168.3.9", got)
	}
	if formatURL("192.168.3.9", 80) != "http://192.168.3.9" {
		t.Fatal("80 端口不应出现在地址里")
	}
	if formatURL("192.168.3.9", 8001) != "http://192.168.3.9:8001" {
		t.Fatal("非 80 端口应写在地址里")
	}
}

func TestPreferredOpenPortUsesBoundPort(t *testing.T) {
	if got := preferredOpenPort([]int{80, 8000}); got != 80 {
		t.Fatalf("同时绑上 80 与 8000 时应使用 80，实际 %d", got)
	}
	if got := preferredOpenPort([]int{8000}); got != 8000 {
		t.Fatalf("80 失败时应使用 8000，实际 %d", got)
	}
	if got := preferredOpenPort([]int{8003}); got != 8003 {
		t.Fatalf("80 与 8000 都失败时应使用实际端口，实际 %d", got)
	}
	if formatPorts([]int{8003}) != "8003" {
		t.Fatal("端口行应显示实际绑定端口")
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
