package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

// DNS-SD / Bonjour 服务类型。客户端应浏览此类型：
//
//	Android: NsdManager（serviceType = "_aceshare._tcp"）
//	Apple:   NWBrowser / Bonjour（"_aceshare._tcp."）
const (
	mdnsServiceType = "_aceshare._tcp"
	mdnsDomain      = "local."
)

// registerMDNS 在局域网宣告 AceShare HTTP 服务，便于客户端免 IP 发现。
// port 为对外主端口（与 preferredOpenPort 一致）；allPorts 写入 TXT 供多端口场景参考。
func registerMDNS(port int, allPorts []int) (*zeroconf.Server, error) {
	name := mdnsInstanceName()
	txt := []string{
		"txtvers=1",
		"version=" + version,
		"path=/",
	}
	if len(allPorts) > 0 {
		parts := make([]string, 0, len(allPorts))
		for _, p := range allPorts {
			parts = append(parts, strconv.Itoa(p))
		}
		txt = append(txt, "ports="+strings.Join(parts, ","))
	}

	ifaces := selectLANInterfaces()
	var regIfaces []net.Interface
	if len(ifaces) > 0 {
		regIfaces = ifaces
	}
	server, err := zeroconf.Register(name, mdnsServiceType, mdnsDomain, port, txt, regIfaces)
	if err != nil {
		return nil, err
	}

	log.Printf("mDNS 已宣告：%s.%s%s（端口 %d）", name, mdnsServiceType, mdnsDomain, port)
	return server, nil
}

// mdnsInstanceName 生成局域网内可区分的实例名，例如「AceShare (DESKTOP-ABC)」。
func mdnsInstanceName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "AceShare"
	}
	host = strings.TrimSuffix(host, ".local")
	host = strings.TrimSuffix(host, ".LOCAL")
	return fmt.Sprintf("AceShare (%s)", host)
}

// selectLANInterfaces 返回适合向局域网宣告的网卡。
// Hyper-V / WSL 虚拟交换机和代理 TUN 上的地址，手机所在的局域网路由不到。
func selectLANInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	selected := make([]net.Interface, 0, len(ifaces))
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualAdapterName(ifi.Name) {
			continue
		}
		if !hasShareableIPv4(ifi) {
			continue
		}
		selected = append(selected, ifi)
	}
	return selected
}

func isVirtualAdapterName(name string) bool {
	n := strings.ToLower(name)
	for _, part := range []string{
		"vethernet",
		"hyper-v",
		"wsl",
		"docker",
		"vmware",
		"virtualbox",
		"vbox",
		"bluetooth",
		"蓝牙",
		"wintun",
		"flclash",
		"clash",
		"tailscale",
		"zerotier",
		"tunnel",
		"vpn",
	} {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

func hasShareableIPv4(ifi net.Interface) bool {
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if ok && shareableIPv4(ipnet.IP) {
			return true
		}
	}
	return false
}

func shareableIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
		return false
	}
	// 198.18.0.0/15 是基准测试网段，代理 TUN 和 fake-ip 用它，不能当局域网地址。
	if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
		return false
	}
	return true
}
