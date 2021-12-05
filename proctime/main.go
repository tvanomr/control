package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/shirou/gopsutil/process"
)

const timeout = 10 * time.Second

type userLimitsCfg struct {
	Limit     int32    `json:"limit"`
	Processes []string `json:"procs"`
}

type userLimits struct {
	limit     int32
	processes map[string]struct{}
}

type config map[string]userLimits

func readConf(confFile string) (config, error) {
	file, err := os.Open(confFile)
	defer file.Close()
	if err != nil {
		return nil, err
	}
	buffer, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var fileConf map[string]userLimitsCfg
	err = json.Unmarshal(buffer, &fileConf)
	if err != nil {
		return nil, err
	}
	conf := make(config, len(fileConf))
	for key, value := range fileConf {
		userConf := userLimits{limit: value.Limit}
		userConf.processes = make(map[string]struct{}, len(value.Processes))
		for _, name := range value.Processes {
			userConf.processes[name] = struct{}{}
		}
		conf[key] = userConf
	}
	return conf, nil
}

type stats struct {
	Day   int32 `json:"day"`
	Count int32 `json:"count"`
}

func (s *stats) Increase() {
	time.Now().Nanosecond()
}

type counts map[string]stats

func readCounts(countFile string) (counts, error) {
	file, err := os.Open(countFile)
	defer file.Close()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(counts), nil
		}
		return nil, err
	}
	buffer, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var appCounts counts
	err = json.Unmarshal(buffer, &appCounts)
	if err != nil {
		return nil, err
	}
	if appCounts == nil {
		appCounts = make(counts)
	}
	return appCounts, err
}

func main() {
	confFile := ""
	countFile := ""
	flag.StringVar(&confFile, "cfg", "", "config file")
	flag.StringVar(&countFile, "counts", "", "count file")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	pids, err := process.PidsWithContext(ctx)
	cancel()
	if err != nil {
		fmt.Println("failed to fetch process list", err)
		return
	}
	conf, err := readConf(confFile)
	if err != nil {
		fmt.Println("can't read config file", err)
		return
	}
	counts, err := readCounts(countFile)
	if err != nil {
		fmt.Println("can't read counts file", err)
		return
	}
	flaggedUsers := make(map[string]struct{})
	now := int32(time.Now().Sub(time.Unix(0, 0)) / time.Hour / 24)
	for _, pid := range pids {
		proc, err := process.NewProcess(pid)
		if err != nil {
			continue
		}
		procUser, err := proc.Username()
		if err != nil {
			continue
		}
		limit, ok := conf[procUser]
		if !ok {
			continue
		}
		exe, err := proc.Exe()
		if err != nil {
			continue
		}
		name, err := proc.Name()
		if err != nil {
			continue
		}
		cmdline, err := proc.Cmdline()
		if err != nil {
			continue
		}
		_, ok = limit.processes[exe]
		if !ok {
			continue
		}
		fmt.Println(exe, name, cmdline)
		_, ok = flaggedUsers[procUser]
		if ok {
			continue
		}
		flaggedUsers[procUser] = struct{}{}
		count := counts[procUser]
		if now > count.Day {
			count.Day = now
			count.Count = 1
		} else {
			count.Count++
		}
		counts[procUser] = count
		if count.Count > limit.limit {
			proc.Kill()
			fmt.Println(exe, "killed, quota exceeded")
		}
	}
	out, err := os.Create(countFile)
	defer out.Close()
	if err != nil {
		fmt.Println("failed to open count file for writing", err)
		return
	}
	encoder := json.NewEncoder(out)
	encoder.Encode(counts)
}
