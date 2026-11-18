// Command e2e-client is the application side of the minimal TUN steering
// test. It uses no proxy settings at all.
//
//	e2e-client resolve DNSADDR DOMAIN        prints the A record DNSADDR returns
//	e2e-client fetch IP DOMAIN               HTTPS GET https://DOMAIN/ dialing IP:443
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("usage: e2e-client resolve DNSADDR DOMAIN | fetch IP DOMAIN")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	switch os.Args[1] {
	case "resolve":
		// UDP first, then DNS over TCP to the same listener.
		var ips []net.IP
		var err error
		for _, network := range []string{"udp4", "tcp4"} {
			network := network
			r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, os.Args[2])
			}}
			c, stop := context.WithTimeout(ctx, 3*time.Second)
			ips, err = r.LookupIP(c, "ip4", os.Args[3])
			stop()
			fmt.Fprintf(os.Stderr, "dns over %s: %d answers, err=%v\n", network, len(ips), err)
			if err == nil && len(ips) > 0 {
				break
			}
		}
		if err != nil || len(ips) == 0 {
			os.Exit(1)
		}
		fmt.Println(ips[0].String())
	case "fetch":
		ip, domain := os.Args[2], os.Args[3]
		tr := &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				c, err := (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort(ip, "443"))
				if err == nil {
					fmt.Println("tcp connected:", c.LocalAddr(), "->", c.RemoteAddr())
				}
				return c, err
			},
			TLSClientConfig: &tls.Config{ServerName: domain},
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+domain+"/", nil)
		start := time.Now()
		resp, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			fmt.Println("fetch FAILED:", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		n, _ := io.Copy(io.Discard, resp.Body)
		fmt.Printf("fetch OK: HTTP %d, %d body bytes, TLS verified for %s, %v\n", resp.StatusCode, n, domain, time.Since(start).Round(time.Millisecond))
	default:
		os.Exit(2)
	}
}
