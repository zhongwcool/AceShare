package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/grandcat/zeroconf"
)

// DNS-SD / Bonjour 服务类型。客户端应浏览此类型：
//   Android: NsdManager（serviceType = "_aceshare._tcp"）
//   Apple:   NWBrowser / Bonjour（"_aceshare._tcp."）
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

	server, err := zeroconf.Register(name, mdnsServiceType, mdnsDomain, port, txt, nil)
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
