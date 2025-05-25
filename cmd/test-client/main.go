package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func main() {
	var url string
	flag.StringVar(&url, "url", "https://localhost:8443/health", "URL to test")
	flag.Parse()

	// Create HTTP/3 client
	client := &http.Client{
		Transport: &http3.RoundTripper{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		Timeout: 10 * time.Second,
	}

	fmt.Printf("Testing HTTP/3 endpoint: %s\n", url)

	// Make request
	resp, err := client.Get(url)
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		log.Printf("Failed to read response: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Status: %s\n", resp.Status)
	fmt.Printf("Protocol: %s\n", resp.Proto)
	fmt.Printf("Headers:\n")
	for k, v := range resp.Header {
		fmt.Printf("  %s: %v\n", k, v)
	}
	fmt.Printf("\nBody:\n%s\n", string(body))
}
