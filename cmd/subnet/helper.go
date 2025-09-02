package subnet

import (
	"fmt"
	"io"
	"net"
)


type node struct {
	name     string
	cidr     string
	children []*node
}


// subnetSplit splits a CIDR into 2^levels subnets
func subnetSplit(cidr string, levels int) ([]*net.IPNet, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	subnets := []*net.IPNet{ipnet}

	// For each level, split each subnet in half
	for i := 0; i < levels; i++ {
		var next []*net.IPNet
		for _, sn := range subnets {
			// Get mask size
			ones, bits := sn.Mask.Size()
			if ones >= bits {
				return nil, fmt.Errorf("cannot split subnet %s further", sn.String())
			}

			// First subnet (same base IP, longer prefix)
			first := &net.IPNet{
				IP:   sn.IP.Mask(net.CIDRMask(ones+1, bits)),
				Mask: net.CIDRMask(ones+1, bits),
			}

			// Second subnet (base + offset)
			secondIP := make(net.IP, len(sn.IP))
			copy(secondIP, sn.IP)
			increment := 1 << (uint(bits-ones-1))
			for j := len(secondIP) - 1; j >= 0 && increment > 0; j-- {
				val := int(secondIP[j]) + increment
				secondIP[j] = byte(val % 256)
				increment = val / 256
			}
			second := &net.IPNet{
				IP:   secondIP.Mask(net.CIDRMask(ones+1, bits)),
				Mask: net.CIDRMask(ones+1, bits),
			}

			next = append(next, first, second)
		}
		subnets = next
	}

	return subnets, nil
}


func printTree(w io.Writer, n *node, prefix string, isLast bool) {
	branch := "├── "
	nextPrefix := prefix + "│   "
	if isLast {
		branch = "└── "
		nextPrefix = prefix + "    "
	}
	// Use tabwriter alignment between name (with tree branches) and CIDR
	fmt.Fprintf(w, "%s%s%s\t%s\n", prefix, branch, n.name, n.cidr)

	for i, c := range n.children {
		printTree(w, c, nextPrefix, i == len(n.children)-1)
	}
}
