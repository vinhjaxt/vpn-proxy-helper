package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
)

type DialFunc func(network, addr string) (net.Conn, error)

var localInterfaceAddr = flag.String(`i`, ``, `Out going Local Network Interface Address or Interface Name`)
var listen = flag.String(`l`, `127.0.0.1:1080`, `Bind address. Eg: unix:/tmp/.unix-socket/socks5.sock`)

var dialFuncAtomic = &atomic.Value{}
var dialFunc6Atomic = &atomic.Value{}
var lastLocalAddr = &atomic.Value{}
var lastLocal6Addr = &atomic.Value{}

func loadDialFunc() {
	var lastIpv4 net.IP
	var lastIpv6 net.IP

	var tempIpv4 net.IP
	var tempIpv6 net.IP

	if v := lastLocalAddr.Load(); v != nil {
		lastIpv4 = lastLocalAddr.Load().(net.IP)
	}
	if v := lastLocal6Addr.Load(); v != nil {
		lastIpv6 = lastLocal6Addr.Load().(net.IP)
	}

	tempIpv4 = lastIpv4
	tempIpv6 = lastIpv6

	tcpDialer := &net.Dialer{
		DualStack: false,
		Timeout:   7 * time.Second,
	}
	tcpDialerV6 := &net.Dialer{
		DualStack: false,
		Timeout:   7 * time.Second,
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		log.Println("get localAddresses:", err)
		return
	}

	for _, i := range ifaces {
		if tcpDialer.LocalAddr != nil {
			break
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		// log.Println(`Interface:`, i.Name)
		if i.Name == *localInterfaceAddr {
			for _, a := range addrs {
				// log.Println(`IP:`, a.String())
				// log.Println(`Choose interface:`, i.Name, `, IP:`, a.String())
				var ip net.IP
				switch v := a.(type) {
				case *net.IPAddr:
					{
						ip = v.IP
					}

				case *net.IPNet:
					{
						ip = v.IP
					}
				}
				if ip.IsLinkLocalUnicast() {
					continue
				}
				if ip.To4() != nil {
					if !tempIpv4.Equal(ip) {
						tempIpv4 = ip
						tcpDialer.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				} else {
					if !tempIpv6.Equal(ip) {
						tempIpv6 = ip
						tcpDialerV6.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				}
				if tcpDialer.LocalAddr != nil && tcpDialerV6.LocalAddr != nil {
					break
				}
			}
			continue
		}
		for _, a := range addrs {
			// log.Println(`IP:`, a.String())
			var ip net.IP
			switch v := a.(type) {
			case *net.IPAddr:
				{
					ip = v.IP
				}

			case *net.IPNet:
				{
					ip = v.IP
				}
			}

			if ip.Equal(net.ParseIP(*localInterfaceAddr)) {
				// log.Println(`Choose interface:`, i.Name, `, IP:`, a.String())
				if ip.To4() != nil {
					if !tempIpv4.Equal(ip) {
						tempIpv4 = ip
						tcpDialer.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				} else {
					if !tempIpv6.Equal(ip) {
						tempIpv6 = ip
						tcpDialerV6.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				}
				break
			}
		}
	}

	if !tempIpv4.Equal(lastIpv4) {
		lastLocalAddr.Store(tempIpv4)
		dialFuncAtomic.Store(DialFunc(tcpDialer.Dial))
		log.Println(`change local ipv4`, tempIpv4)
	}

	if !tempIpv6.Equal(lastIpv6) {
		lastLocal6Addr.Store(tempIpv6)
		dialFunc6Atomic.Store(DialFunc(tcpDialerV6.Dial))
		log.Println(`change local ipv6`, tempIpv6)
	}

}

func main() {
	flag.Parse()

	var err error
	var ln net.Listener
	if strings.HasPrefix(*listen, `unix:`) {
		unixFile := (*listen)[5:]
		os.Remove(unixFile)
		ln, err = net.Listen(`unix`, unixFile)
		os.Chmod(unixFile, os.ModePerm)
		log.Println(`Listening:`, unixFile)
	} else {
		ln, err = net.Listen(`tcp`, *listen)
		if ln != nil {
			log.Println(`Listening:`, ln.Addr().String())
		}
	}
	if err != nil {
		log.Panicln(err)
	}
	if ln == nil {
		log.Panicln(`Error listening:`, *listen)
	}

	log.Println("Socks5 server is running at:", *listen)
	log.Println("Usage: socks5://" + *listen)
	log.Println("Eg: curl -x socks5://" + *listen + " https://1.1.1.1/cdn-cgi/trace")

	go func() {
		for {
			loadDialFunc()
			time.Sleep(2 * time.Second)
		}
	}()

	server := socks5.NewServer(
		socks5.WithLogger(socks5.NewLogger(log.New(os.Stdout, "socks5: ", log.LstdFlags))),
		socks5.WithDialAndRequest(func(ctx context.Context, network, addr string, request *socks5.Request) (net.Conn, error) {

			log.Println(`=>`, addr)

			var dialFunc DialFunc

			if request.DestAddr.AddrType == statute.ATYPIPv6 {
				if v := dialFunc6Atomic.Load(); v == nil {
					return nil, errors.New(`no dial6 func`)
				} else {
					dialFunc = v.(DialFunc)
				}
			} else {
				if v := dialFuncAtomic.Load(); v == nil {
					return nil, errors.New(`no dial func`)
				} else {
					dialFunc = v.(DialFunc)
				}
			}

			return dialFunc(network, addr)
		}),
	)

	log.Panicln(server.Serve(ln))
}
