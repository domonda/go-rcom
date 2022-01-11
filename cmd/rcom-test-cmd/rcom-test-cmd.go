package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"
)

func main() {
	var (
		wait        = flag.Duration("wait", time.Second, "duration until program end")
		createFiles = flag.String("createFiles", "", "comma separated list of filenames to crete with random data")
		stdout      = flag.String("stdout", "", "write to stdout")
		stderr      = flag.String("stderr", "", "write to stderr")
		exitCode    = flag.Int("exitCode", 0, "status code on exit")
	)

	time.Sleep(*wait)

	for _, filename := range strings.Split(*createFiles, ",") {
		if filename == "" {
			continue
		}
		err := ioutil.WriteFile(filename, nil, 0600)
		if err != nil {
			panic(err)
		}
	}

	if *stdout != "" {
		fmt.Fprintln(os.Stdout, *stdout)
	}
	if *stderr != "" {
		fmt.Fprintln(os.Stderr, *stderr)
	}

	os.Exit(*exitCode)
}
