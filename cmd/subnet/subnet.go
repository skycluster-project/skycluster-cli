package subnet

import (
	"fmt"
	"net"
	"os"
	"strings"
	"text/tabwriter"

	lo "github.com/samber/lo"

	"github.com/spf13/cobra"
)

var provider string

func init() {
	// subnetCmd.AddCommand(subnetCmd)
	subnetCmd.PersistentFlags().StringVarP(&provider, "provider", "p", "aws", "Cloud provider (openstack, aws, azure, gcp)")
}

var subnetCmd = &cobra.Command{
	Use:   "subnet <subnet-cidr>",
	Short: "Subnet calculates the subnet information for a given CIDR for you cluster.",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
		err := checkCIDR(args[0]); if err != nil {
			fmt.Println("This tool only supports CIDR in 10.0.0.0/8. Use other CIDRs at your own discretion.")
			return
		}
		switch provider {
		case "aws":
			calculateAWSSubnets(args[0])
		case "gcp":
			calculateGCPSubnets(args[0])	
			fmt.Printf("\n%s\t%s\n",
			"Note:", "For GCP GKE service, you need to specify a subnet range for nodes (XKube Nodes)")
		default:
			fmt.Println("Unsupported provider")
			return
		}
		
		fmt.Printf("\n%s\t%s\n",
			"Note:", "You can use any CIDR within the Subnet Ranges for your XProvider configuration.")
		// fmt.Printf("\n%s\t%s\n",
		// 	"Note:", "This tool provides a basic subnet calculation for SkyCluster environment.")

	},
}

func GetSubnetCmd() *cobra.Command {
	return subnetCmd
}

func checkCIDR(cidr string) error {
	// check if cidr starts with 10.
	// if it does not, return error
	if !strings.HasPrefix(cidr, "10.") {
		return fmt.Errorf("wrong cidr")
	}
	return nil
}

/*
 GCP Helper function
*/
func calculateGCPSubnets(cidr string) {

	vpcCIDR := cidr
	splitVPC, err := subnetSplit(vpcCIDR, 1)
	if err != nil {
		panic(err)
	}
	
	// Build hierarchy
	root := &node{
		name: "VPC",
		cidr: vpcCIDR,
		children: []*node{
			{
				name: "Subnet Range",
				cidr: splitVPC[0].String(),
				children: []*node{},
			},
			{
				name: "XKube Node Range (GKE)",
				cidr: splitVPC[1].String(),
				children: []*node{},
			},
		},
	}

	podCidr, err := buildSubnet(vpcCIDR, 172)
	if err != nil {
		panic(err)
	}
	podRoot := &node{
		name: "Pod/Service Range",
		cidr: podCidr.String(),
		children: nil,
	}

	// Render with alignment
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCIDR")
	printTree(tw, root, "", true)
	printTree(tw, podRoot, "", true)
	if err := tw.Flush(); err != nil {
		panic(err)
	}
}

/*
 AWS Subnet Calculation
*/
func calculateAWSSubnets(cidr string) {

	vpcCIDR := cidr
	splitVPC, err := subnetSplit(vpcCIDR, 1)
	if err != nil {
		panic(err)
	}

	podCIDRs, err := subnetSplit(splitVPC[1].String(), 1)
	if err != nil {
		panic(err)
	}

	// Build hierarchy
	root := &node{
		name: "VPC",
		cidr: vpcCIDR,
		children: []*node{{
				name: "Subnet Range",
				cidr: splitVPC[0].String(),
				children: []*node{},
			}, {
				name: "XKube Pod Range (EKS)",
				cidr: splitVPC[1].String(),
				children: []*node{
					{name: "Primary", cidr: podCIDRs[0].String()},
					{name: "Secondary", cidr: podCIDRs[1].String()},
				},
			},
		},
	}

	svcCidr, err := buildSubnet(vpcCIDR, 172)
	if err != nil {
		panic(err)
	}

	// svcCidr := "172.16.0.0/16"
	svcRoot := &node{
		name: "XKube Service Range (EKS)",
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
}

// Helper function
func buildSubnet(cidr string, octets ...int) (*net.IPNet, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	octetsBytes := lo.Map(octets, func(o int, _ int) byte {return byte(o)})

	// Construct new subnet <first>.<second>.<base>.0/24
	firstOctet  := lo.NthOr(octetsBytes, 0, ipnet.IP[0])
	secondOctet := lo.NthOr(octetsBytes, 1, ipnet.IP[1])
	baseOctet   := lo.NthOr(octetsBytes, 2, ipnet.IP[2])

	ones := 24
	switch len(octets) {
	case 1:
		ones = 16
	case 2:
		ones = 24
	case 3:
		ones = 32
	}

	newIP := net.IPv4(firstOctet, secondOctet, baseOctet, 0)
	newCIDR := &net.IPNet{
		IP:   newIP,
		Mask: net.CIDRMask(ones, 32), // fixed /24
	}
	return newCIDR, nil
}