// Package storage holds what AWS documents about EBS volume types: sizes and
// performance per type, and the Service Quotas quota that bounds each type's total
// capacity. EC2 has no API listing volume types, and every current-generation (Nitro)
// instance type can attach every current volume type, so the table is the catalog.
//
// Sources, last checked 2026-11-07:
//   - https://docs.aws.amazon.com/ebs/latest/userguide/ebs-volume-types.html
//     (volume size, max IOPS and throughput per type)
//   - Service Quotas, service code ebs: "Storage for <description> (<type>) volumes, in TiB"
package storage

import (
	"sort"
	"strings"

	"github.com/converged-computing/google-cloud-topology/aws/pkg/database"
)

// VolumeTypes are the current EBS volume types; sizes in GiB (the page's TiB × 1024).
var VolumeTypes = []database.DiskType{
	{Name: "gp3", Description: "General Purpose SSD (gp3)", MinGB: 1, MaxGB: 64 * 1024, MaxIOPS: 80000, MaxMiBps: 2000},
	{Name: "gp2", Description: "General Purpose SSD (gp2)", MinGB: 1, MaxGB: 16 * 1024, MaxIOPS: 16000, MaxMiBps: 250},
	{Name: "io2", Description: "Provisioned IOPS SSD (io2) Block Express", MinGB: 4, MaxGB: 64 * 1024, MaxIOPS: 256000, MaxMiBps: 4000},
	{Name: "io1", Description: "Provisioned IOPS SSD (io1)", MinGB: 4, MaxGB: 16 * 1024, MaxIOPS: 64000, MaxMiBps: 1000},
	{Name: "st1", Description: "Throughput Optimized HDD (st1)", MinGB: 125, MaxGB: 16 * 1024, MaxIOPS: 500, MaxMiBps: 500},
	{Name: "sc1", Description: "Cold HDD (sc1)", MinGB: 125, MaxGB: 16 * 1024, MaxIOPS: 250, MaxMiBps: 250},
}

// Attachable reports whether an instance type can attach a volume type: every current
// volume type attaches to every instance type that boots from EBS.
func Attachable(mt database.MachineType, diskType string) bool {
	for _, v := range VolumeTypes {
		if v.Name == diskType {
			return true
		}
	}
	return false
}

// Compatible returns the disk types (of those given) an instance type can attach.
func Compatible(mt database.MachineType, diskTypes []database.DiskType) []database.DiskType {
	var out []database.DiskType
	for _, dt := range diskTypes {
		if Attachable(mt, dt.Name) {
			out = append(out, dt)
		}
	}
	return out
}

// QuotaName returns the Service Quotas name bounding a volume type's total capacity, or
// "" when the type is unknown.
func QuotaName(diskType string) string {
	for _, v := range VolumeTypes {
		if v.Name == diskType {
			return "Storage for " + strings.TrimSuffix(v.Description, " Block Express") + " volumes, in TiB"
		}
	}
	return ""
}

// VertexType is the resource type a volume type becomes in a graph: gp3 is ebs_gp3.
func VertexType(diskType string) string {
	return "ebs_" + diskType
}

// Known lists the volume type names, sorted.
func Known() []string {
	var out []string
	for _, v := range VolumeTypes {
		out = append(out, v.Name)
	}
	sort.Strings(out)
	return out
}
