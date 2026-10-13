package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const DefaultIPAllowlist = ""

var localDestinationNetwork = netip.MustParsePrefix("127.0.0.0/24")

// IPAllowlist contains literal IP addresses and CIDR networks. Access decisions
// use actual socket peer/destination addresses, never forwarding headers.
type IPAllowlist struct {
	prefixes []netip.Prefix
}

func ParseIPAllowlist(value string) (*IPAllowlist, error) {
	list := &IPAllowlist{}
	if strings.TrimSpace(value) == "" {
		return list, nil
	}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("IP_ALLOWLIST must contain comma-separated IP addresses or CIDRs; empty entries between values are not allowed")
		}
		var prefix netip.Prefix
		if strings.Contains(entry, "/") {
			var err error
			prefix, err = netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid IP_ALLOWLIST entry %q: %w", entry, err)
			}
			if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
			}
		} else {
			address, err := netip.ParseAddr(entry)
			if err != nil || address.Zone() != "" {
				return nil, fmt.Errorf("invalid IP_ALLOWLIST address %q", entry)
			}
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		list.prefixes = append(list.prefixes, prefix.Masked())
	}
	return list, nil
}

func (list *IPAllowlist) Allows(remote string) bool {
	if list == nil {
		return false
	}
	peer, err := netip.ParseAddrPort(remote)
	if err != nil {
		return false
	}
	address := peer.Addr().Unmap().WithZone("")
	for _, prefix := range list.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// allowsRequest exempts only the actual accepted socket's local destination.
// Host and forwarding headers cannot opt a remote-interface request into it.
func (list *IPAllowlist) allowsRequest(r *http.Request) bool {
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if destination, err := netip.ParseAddrPort(local.String()); err == nil && localDestinationNetwork.Contains(destination.Addr().Unmap()) {
			return true
		}
	}
	return list.Allows(r.RemoteAddr)
}

// ConfigureIPAllowlist must be called before the server starts serving requests.
func (a *App) ConfigureIPAllowlist(value string) error {
	list, err := ParseIPAllowlist(value)
	if err != nil {
		return err
	}
	a.allowlist = list
	return nil
}
