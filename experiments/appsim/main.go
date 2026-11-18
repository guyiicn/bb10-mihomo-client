//go:build qnx

// Command appsim faithfully mimics the app's VPN launch over /bin/__root, for
// a pre-install on-device check. Run as the app uid (via `on -u 100:902`).
//
//	appsim HELPER CORE CONFIGDIR
//
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("usage: appsim HELPER CORE CONFIGDIR")
		os.Exit(2)
	}
	helper, core, cfgdir := os.Args[1], os.Args[2], os.Args[3]
	root := exec.Command("/bin/__root")
	root.Env = []string{
		"PATH=/proc/boot:/bin:/usr/bin:/base/bin:/base/usr/bin",
		"LD_LIBRARY_PATH=/base/usr/lib:/base/lib:/base/lib/dll:/radio/lib:/radio/lib/dll:/radio/usr/lib:/proc/boot",
		"LANG=C", "TERM=dumb",
	}
	in, _ := root.StdinPipe()
	out, _ := root.StdoutPipe()
	root.Stderr = os.Stderr
	if err := root.Start(); err != nil {
		fmt.Println("start __root:", err)
		os.Exit(1)
	}
	fmt.Fprintf(in, "exec %s vpnroot %d\n", helper, os.Getuid())
	var coreCmd *exec.Cmd
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		line := sc.Text()
		fmt.Println("helper>", line)
		switch {
		case line == "OWNED":
			coreCmd = exec.Command(core, "-d", cfgdir, "-f", cfgdir+"/config.yaml")
			coreCmd.Env = append(os.Environ(), "BB10_TUN_EXPERIMENT=1")
			lf, _ := os.Create(cfgdir + "/core.log")
			coreCmd.Stdout, coreCmd.Stderr = lf, lf
			if err := coreCmd.Start(); err != nil {
				fmt.Println("core start:", err)
				in.Close()
				os.Exit(1)
			}
			fmt.Println("core started pid", coreCmd.Process.Pid)
			time.Sleep(3 * time.Second)
			fmt.Fprint(in, "UP\n")
		case len(line) >= 5 && line[:5] == "READY":
			fmt.Println("=== VPN up; nameservers line:", line)
			// let it run briefly for the caller's curl, then tear down
			time.Sleep(8 * time.Second)
			in.Close() // EOF -> helper rolls back
		case len(line) >= 3 && line[:3] == "ERR":
			fmt.Println("=== helper error")
			in.Close()
		}
	}
	if coreCmd != nil {
		coreCmd.Process.Kill()
		coreCmd.Wait()
	}
	root.Wait()
	_ = strconv.Itoa
	fmt.Println("appsim done")
}
