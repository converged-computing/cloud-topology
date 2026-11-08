package quota

import (
	"strings"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
)

// Instance classes from https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html
// (last checked 2026-11-07): each On-Demand quota, and each Spot quota, is a vCPU count
// shared by every instance type in the class. The class is read from the letters that
// start the family name.
var classes = map[string]string{
	"hpc": "HPC", "inf": "Inf", "trn": "Trn", "dl": "DL", "u": "High Memory",
	"g": "G and VT", "gr": "G and VT", "vt": "G and VT", "p": "P", "f": "F", "x": "X",
}

const standard = "Standard (A, C, D, H, I, M, R, T, Z)"

// Class returns the quota class of an instance type, or "" with a reason when it has
// no vCPU quota (mac types run on Dedicated Hosts).
func Class(mt database.MachineType) (class, reason string) {
	prefix := strings.TrimRightFunc(strings.SplitN(mt.Family, "-", 2)[0], func(r rune) bool { return r >= '0' && r <= '9' })
	// The prefix is the letters before the generation digit: c7i → c, hpc7a → hpc.
	for i, r := range prefix {
		if r >= '0' && r <= '9' {
			prefix = prefix[:i]
			break
		}
	}
	if prefix == "mac" {
		return "", "mac instances run on Dedicated Hosts and have no vCPU quota"
	}
	if c, ok := classes[prefix]; ok {
		return c, ""
	}
	if prefix != "" && strings.Contains("acdhimrtz", prefix[:1]) {
		return standard, ""
	}
	return "", "no quota class for family " + mt.Family
}

// QuotaName returns the Service Quotas name bounding a class under a purchasing model
// (on-demand or spot).
func QuotaName(class, model string) string {
	if model == "spot" {
		return "All " + class + " Spot Instance Requests"
	}
	return "Running On-Demand " + class + " instances"
}
