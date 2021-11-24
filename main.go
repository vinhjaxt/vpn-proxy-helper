package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"strings"

	socks5 "github.com/haxii/socks5"
)

type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

var localInterfaceAddr = flag.String("i", "", "Out going Local Network Interface Address")
var bindAddr = flag.String("l", "127.0.0.1:1080", "Bind address")

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
		for _, addr := range addrs {
			ipAddr := addr.String()
			idx := strings.IndexRune(ipAddr, '/')
			if idx != -1 {
				ipAddr = ipAddr[0:idx]
			}
			ips += ", " + ipAddr
			if ipAddr == *localInterfaceAddr {
				dialer := &net.Dialer{
					LocalAddr: &net.TCPAddr{
						IP: addr.(*net.IPNet).IP,
					},
				}
				return dialer.DialContext, nil
			}
		}
	}
	return nil, errors.New(`interface for ip ` + *localInterfaceAddr + ` not found` + ips)
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
