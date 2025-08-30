package subnet

import (
	"fmt"
	"io"
	"net"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func init() {
	// subnetCmd.AddCommand(subnetCmd)
}

var subnetCmd = &cobra.Command{
	Use:   "subnet <subnet-cidr>",
	Short: "Subnet calculates the subnet information for a given CIDR for you cluster.",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
		calculateSubnets(args[0])
	},
}


func GetSubnetCmd() *cobra.Command {
	return subnetCmd
}

func calculateSubnets(cidr string) {

	vpcCIDR := cidr

	splitVPC, err := subnetSplit(vpcCIDR, 1)
	if err != nil {
		panic(err)
	}

	podCIDRs, err := subnetSplit(splitVPC[0].String(), 1)
	if err != nil {
		panic(err)
	}

	subnetCIDRs, err := subnetSplit(splitVPC[1].String(), 1)
	if err != nil {
		panic(err)
	}

	// Build hierarchy
	root := &node{
		name: "VPC",
		cidr: vpcCIDR,
		children: []*node{
			{
				name: "Pod Range",
				cidr: splitVPC[0].String(),
				children: []*node{
					{name: "Private Pod", cidr: podCIDRs[0].String()},
					{name: "Public Pod", cidr: podCIDRs[1].String()},
				},
			},
			{
				name: "Subnet Range",
				cidr: splitVPC[1].String(),
				children: []*node{
					{name: "Private Subnet", cidr: subnetCIDRs[0].String()},
					{name: "Public Subnet", cidr: subnetCIDRs[1].String()},
				},
			},
		},
	}

	svcCidr, err := svcSubnet(vpcCIDR)
	if err != nil {
		panic(err)
	}
	svcRoot := &node{
		name: "Service Range",
		cidr: svcCidr.String(),
		children: nil,
	}

	// Render with alignment
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCIDR")
	printTree(tw, root, "", true)
	printTree(tw, svcRoot, "", true)
	if err := tw.Flush(); err != nil {
		panic(err)
	}

	// writer := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)

	// fmt.Fprintln(writer, "NAME\tCIDR")
	// fmt.Fprintf(writer, "VPC\t%s\n", vpcCIDR)

	// fmt.Fprintf(writer, "Pod Range\t%s\n", splitVPC[0].String())
	// fmt.Fprintf(writer, "  Private Pod\t%s\n", podCIDRs[0].String())
	// fmt.Fprintf(writer, "  Public Pod\t%s\n", podCIDRs[1].String())

	// fmt.Fprintf(writer, "Subnet Range\t%s\n", splitVPC[1].String())
	// fmt.Fprintf(writer, "  Private Subnet\t%s\n", subnetCIDRs[0].String())
	// fmt.Fprintf(writer, "  Public Subnet\t%s\n", subnetCIDRs[1].String())

	// writer.Flush()
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

func svcSubnet(cidr string) (*net.IPNet, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	// Extract the 2nd octet
	secondOctet := ipnet.IP[1]

	// Construct new IP 172.<secondOctet>.0.0/16
	newIP := net.IPv4(172, secondOctet, 0, 0)
	newCIDR := &net.IPNet{
		IP:   newIP,
		Mask: net.CIDRMask(16, 32),
	}
	return newCIDR, nil
}

type node struct {
	name     string
	cidr     string
	children []*node
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
