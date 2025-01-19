package utils

import (
	"bufio"
	"os"
	"regexp"
	"runtime"
)

// CpuInfo CPU信息
type CpuInfo struct {
	Vendor  string `json:"vendor"`
	Model   string `json:"model"`
	Speed   string `json:"speed"`   // CPU clock rate in MHz
	Cache   uint   `json:"cache"`   // CPU cache size in KB
	Cpus    uint   `json:"cpus"`    // number of physical CPUs
	Cores   uint   `json:"cores"`   // number of physical CPU cores
	Threads uint   `json:"threads"` // number of logical (HT) CPU cores
}

func GetCpuInfo() *CpuInfo {
	var res = &CpuInfo{
		Vendor:  "",
		Model:   "",
		Speed:   "",
		Cache:   0,
		Cpus:    0,
		Cores:   0,
		Threads: 0,
	}

	res.Threads = uint(runtime.NumCPU())
	f, err := os.Open("/proc/cpuinfo")
	if err == nil {
		cpu := make(map[string]bool)
		core := make(map[string]bool)
		var cpuID string

		s := bufio.NewScanner(f)
		for s.Scan() {
			//if sl := cpuRegTwoColumns.Split(s.Text(), 2); sl != nil {
			//	switch sl[0] {
			//	case "physical id":
			//		cpuID = sl[1]
			//		cpu[cpuID] = true
			//	case "core id":
			//		coreID := fmt.Sprintf("%s/%s", cpuID, sl[1])
			//		core[coreID] = true
			//	case "vendor_id":
			//		if res.Vendor == "" {
			//			res.Vendor = sl[1]
			//		}
			//	case "model name":
			//		if res.Model == "" {
			//			// CPU model, as reported by /proc/cpuinfo, can be a bit ugly. Clean up...
			//			model := cpuRegExtraSpace.ReplaceAllLiteralString(sl[1], " ")
			//			res.Model = strings.Replace(model, "- ", "-", 1)
			//		}
			//	case "cpu MHz":
			//		if res.Speed == "" {
			//			res.Speed = sl[1]
			//		}
			//	case "cache size":
			//		if res.Cache == 0 {
			//			if m := cpuRegCacheSize.FindStringSubmatch(sl[1]); m != nil {
			//				if cache, err := strconv.ParseUint(m[1], 10, 64); err == nil {
			//					res.Cache = uint(cache)
			//				}
			//			}
			//		}
			//	}
			//}

			// 鲲鹏 920
			if sl := cpuRegTwoColumns.Split(s.Text(), 2); sl != nil {
				switch sl[0] {
				case "processor":
					cpuID = sl[1]
					core[cpuID] = true
				}
			}
		}
		res.Cpus = uint(len(cpu))
		res.Cores = uint(len(core))
	}
	defer func() {
		_ = f.Close()
	}()

	return res
}

var (
	cpuRegTwoColumns = regexp.MustCompile("\t+: ")
	cpuRegExtraSpace = regexp.MustCompile(" +")
	cpuRegCacheSize  = regexp.MustCompile(`^(\d+) KB$`)
)
