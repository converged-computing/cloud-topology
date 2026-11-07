package topology

import (
	"encoding/xml"
	"os"
	"strconv"
)

// Object is one hwloc object, compact for the browser: the type, OS index, a name, a
// size in bytes for caches and NUMA memory, attributes worth showing, and children.
// Bridges are skipped so PCI devices hang off the package or machine they belong to.
type Object struct {
	Type     string            `json:"t"`
	Index    int               `json:"i,omitempty"`
	Name     string            `json:"n,omitempty"`
	Size     int64             `json:"s,omitempty"`
	Attrs    map[string]string `json:"a,omitempty"`
	Children []*Object         `json:"c,omitempty"`
}

type xmlObject struct {
	Type        string `xml:"type,attr"`
	OSIndex     string `xml:"os_index,attr"`
	Name        string `xml:"name,attr"`
	CacheSize   string `xml:"cache_size,attr"`
	CacheType   string `xml:"cache_type,attr"`
	Depth       string `xml:"depth,attr"`
	LocalMemory string `xml:"local_memory,attr"`
	PCIBusID    string `xml:"pci_busid,attr"`
	PCIType     string `xml:"pci_type,attr"`
	LinkSpeed   string `xml:"pci_link_speed,attr"`
	OSDevType   string `xml:"osdev_type,attr"`
	Infos       []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"info"`
	Children []xmlObject `xml:"object"`
}

var keepInfo = map[string]bool{"PCIVendor": true, "PCIDevice": true, "CPUModel": true, "CPUVendor": true, "Vendor": true, "Model": true,
	"hwlocVersion": true, "Architecture": true, "Backend": true, "OSName": true, "OSRelease": true, "Address": true, "Port": true, "NVIDIAUUID": true}

// ReadObjects reads an hwloc XML file into an Object tree.
func ReadObjects(path string) (*Object, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Objects []xmlObject `xml:"object"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Objects) == 0 {
		return nil, nil
	}
	return convert(doc.Objects[0]), nil
}

func convert(x xmlObject) *Object {
	o := &Object{Type: x.Type, Name: x.Name}
	o.Index, _ = strconv.Atoi(x.OSIndex)
	if x.CacheSize != "" {
		o.Size, _ = strconv.ParseInt(x.CacheSize, 10, 64)
	}
	if x.LocalMemory != "" {
		o.Size, _ = strconv.ParseInt(x.LocalMemory, 10, 64)
	}
	attrs := map[string]string{}
	if x.CacheType != "" {
		attrs["cache_type"] = map[string]string{"0": "unified", "1": "data", "2": "instruction"}[x.CacheType]
	}
	if x.PCIBusID != "" {
		attrs["bus_id"] = x.PCIBusID
	}
	if x.PCIType != "" {
		attrs["pci_type"] = x.PCIType
	}
	if x.LinkSpeed != "" && x.LinkSpeed != "0" {
		attrs["link_gbs"] = x.LinkSpeed
	}
	if x.OSDevType != "" {
		attrs["osdev_type"] = map[string]string{"0": "block", "1": "gpu", "2": "network", "3": "openfabrics", "4": "dma", "5": "coprocessor", "6": "memory"}[x.OSDevType]
	}
	for _, in := range x.Infos {
		if keepInfo[in.Name] {
			attrs[in.Name] = in.Value
		}
	}
	if len(attrs) > 0 {
		o.Attrs = attrs
	}
	for _, c := range x.Children {
		if c.Type == "Bridge" {
			// Hoist the bridge's devices.
			for _, d := range flattenBridge(c) {
				o.Children = append(o.Children, convert(d))
			}
			continue
		}
		o.Children = append(o.Children, convert(c))
	}
	return o
}

func flattenBridge(b xmlObject) []xmlObject {
	var out []xmlObject
	for _, c := range b.Children {
		if c.Type == "Bridge" {
			out = append(out, flattenBridge(c)...)
		} else {
			out = append(out, c)
		}
	}
	return out
}
