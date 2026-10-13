package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		flags := flag.NewFlagSet("healthcheck", flag.ExitOnError)
		url := flags.String("url", "http://127.0.0.1:8765", "service URL")
		flags.Parse(os.Args[2:])
		client := http.Client{Timeout: 2 * time.Second}
		response, err := client.Get(*url + "/healthz")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "Brainiac server implementation in progress")
}
