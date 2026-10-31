package topology

import "fmt"

// Derive returns the topology of a smaller machine in the same series as t, assuming
// it is a cut of t: cores are taken socket by socket from socket 0, GPUs come first
// from the sockets used, each socket used keeps its NICs (at least one NIC overall),
// and memory is split across the sockets used in proportion to cores taken.
func Derive(t *Topology, vcpus, gpus int, memoryBytes int64) (*Topology, error) {
	totalCores, totalPUs := 0, 0
	for _, s := range t.Sockets {
		totalCores += s.Cores
		totalPUs += s.PUs
	}
	if totalCores == 0 {
		return nil, fmt.Errorf("source topology has no cores")
	}
	pusPerCore := max(1, totalPUs/totalCores)
	need := (vcpus + pusPerCore - 1) / pusPerCore
	if need > totalCores {
		return nil, fmt.Errorf("need %d cores, source has %d", need, totalCores)
	}
	if gpus > t.GPUCount() {
		return nil, fmt.Errorf("need %d GPUs, source has %d", gpus, t.GPUCount())
	}

	out := &Topology{}
	remaining := need
	for _, s := range t.Sockets {
		if remaining == 0 {
			break
		}
		take := min(remaining, s.Cores)
		remaining -= take
		out.Sockets = append(out.Sockets, Socket{Index: len(out.Sockets), Cores: take, PUs: take * pusPerCore, NICs: s.NICs})
	}

	// GPUs: sockets in use first, then other sockets (which join with no cores), then node-level GPUs.
	left := gpus
	for i := range out.Sockets {
		n := min(left, len(t.Sockets[i].GPUs))
		out.Sockets[i].GPUs = t.Sockets[i].GPUs[:n]
		left -= n
	}
	for i := len(out.Sockets); i < len(t.Sockets) && left > 0; i++ {
		n := min(left, len(t.Sockets[i].GPUs))
		if n > 0 {
			out.Sockets = append(out.Sockets, Socket{Index: len(out.Sockets), GPUs: t.Sockets[i].GPUs[:n], NICs: t.Sockets[i].NICs})
			left -= n
		}
	}
	if left > 0 {
		out.GPUs = t.GPUs[:left]
	}

	if out.NICCount() == 0 {
		for _, s := range t.Sockets {
			if len(s.NICs) > 0 {
				out.NICs = s.NICs[:1]
				break
			}
		}
		if out.NICCount() == 0 && len(t.NICs) > 0 {
			out.NICs = t.NICs[:1]
		}
	}

	for i := range out.Sockets {
		if out.Sockets[i].Cores > 0 {
			out.Sockets[i].MemoryBytes = memoryBytes * int64(out.Sockets[i].Cores) / int64(need)
		}
	}
	return out, nil
}
