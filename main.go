package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"strings"
	"time"

	socks5 "github.com/haxii/socks5"
)

type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

var localInterfaceAddr = flag.String("i", "", "Out going Local Network Interface Address or Interface Name")
var bindAddr = flag.String("l", "127.0.0.1:1080", "Bind address")

const dialTimeout = 20 * time.Second

func getDialFunc(localAddr string) (DialFunc, error) {
	netInterfaces, err := net.Interfaces()
	if err != nil {
		log.Panicln(err)
	}

	var ips string
	for _, netInterface := range netInterfaces {
		addrs, err := netInterface.Addrs()
		if err != nil {
			log.Println(netInterface.Name, err)
			continue
		}
		isIfn := netInterface.Name == *localInterfaceAddr
		for _, addr := range addrs {
			ipAddr := addr.String()
			idx := strings.IndexRune(ipAddr, '/')
			if idx != -1 {
				ipAddr = ipAddr[0:idx]
			}
			ips += ", " + ipAddr
			if isIfn || ipAddr == *localInterfaceAddr {
				dialer := &net.Dialer{
					LocalAddr: &net.TCPAddr{
						IP: addr.(*net.IPNet).IP,
					},
					Timeout: dialTimeout,
				}
				return dialer.DialContext, nil
			}
		}
	}
	return nil, errors.New(`interface for ` + *localInterfaceAddr + ` not found` + ips)
}

func main() {
	flag.Parse()

	var dialer DialFunc
	if len(*localInterfaceAddr) != 0 {
		var err error
		dialer, err = getDialFunc(*localInterfaceAddr)
		if err != nil {
			log.Panicln(err)
		}
		log.Println("Network going out interface:", *localInterfaceAddr)
	}
	if dialer == nil {
		dialer = (&net.Dialer{
			Timeout: dialTimeout,
		}).DialContext
	}

	conf := &socks5.Config{
		Dial: dialer,
	}
	server, err := socks5.New(conf)
	if err != nil {
		log.Panicln(err)
	}

	log.Println("Socks5 server is running at:", *bindAddr)
	log.Println("Usage: socks5://" + *bindAddr)
	log.Println("Eg: curl -x socks5://" + *bindAddr + " https://1.1.1.1/cdn-cgi/trace")
	if err := server.ListenAndServe("tcp", *bindAddr); err != nil {
		log.Panicln(err)
	}

}
